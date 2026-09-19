//go:build integration

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	webhookv1 "github.com/Mujhtech/idenqa/contracts/webhook/v1"
	"github.com/Mujhtech/idenqa/internal/delivery"
	deliverypostgres "github.com/Mujhtech/idenqa/internal/delivery/postgres"
	deliverytask "github.com/Mujhtech/idenqa/internal/delivery/task"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	localkms "github.com/Mujhtech/idenqa/internal/platform/kms/local"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	taskheadgate "github.com/Mujhtech/idenqa/internal/platform/task/headgate"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// TestWebhookFanoutResumesAcrossBatchesAfterWorkerReplacement proves a
// committed 256-endpoint batch leaves one durable successor that a replacement
// worker can finish without duplicating any endpoint/event delivery.
func TestWebhookFanoutResumesAcrossBatchesAfterWorkerReplacement(t *testing.T) {
	database := createIsolatedDatabase(t)
	ctx := t.Context()
	migrator, err := platformpostgres.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatal(err)
	}
	headgateMigrator, err := taskheadgate.OpenMigrator(ctx, database.url, "headgate", 5*time.Second, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := headgateMigrator.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := headgateMigrator.Close(ctx); err != nil {
		t.Fatal(err)
	}
	admin, err := platformpostgres.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	generator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	tenantID, err := generator.NewTenant()
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.tenants (id,state,version,created_at,updated_at) VALUES ($1,'active',1,$2,$2)`, tenantID.String(), now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	role := database.createRuntimeRole(t)
	database.grantHeadgateRuntime(t, role)
	runtimeConfig := poolConfig(database.url)
	runtimeConfig.Role = role
	runtime, err := platformpostgres.Open(ctx, runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := localkms.Create(filepath.Join(t.TempDir(), "keyring.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = keyring.Close() }()
	store, err := deliverypostgres.New(runtime)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := delivery.NewManager(store, generator, keyring, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < deliverytask.FanoutBatch+1; index++ {
		_, secret, err := manager.CreateEndpoint(ctx, scope, fmt.Sprintf("https://batch-%03d.example.test/webhook", index))
		if err != nil {
			t.Fatalf("create endpoint %d: %v", index, err)
		}
		clear(secret)
	}
	seed := "verification.completed:multi-batch-worker-replacement"
	if err := runtime.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, scope.ID().String()); err != nil {
			return err
		}
		return deliverypostgres.EmitCatalogueEvent(ctx, tx, keyring, scope.ID().String(), "ng-lagos", webhookv1.VerificationCompleted, seed, now, map[string]any{
			"verification_id": "ver_01ARZ3NDEKTSV4RRFFQ69G5FAV",
			"subject_id":      "sub_01ARZ3NDEKTSV4RRFFQ69G5FAV",
			"decision_id":     "dec_01ARZ3NDEKTSV4RRFFQ69G5FAV",
			"verification": map[string]any{
				"id": "ver_01ARZ3NDEKTSV4RRFFQ69G5FAV", "type": "verification.session", "status": "completed",
			},
		})
	}); err != nil {
		t.Fatal(err)
	}
	eventID := mustEventID(t, webhookv1.DeterministicEventID(seed))
	adapter, err := taskheadgate.NewPostgres(runtime.Native(), taskheadgate.DefaultConfig("idenqa-fanout-recovery"))
	if err != nil {
		t.Fatal(err)
	}
	initialIntent, err := deliverytask.NewFanoutIntent(generator, scope, eventID, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Enqueue(ctx, initialIntent); err != nil {
		t.Fatal(err)
	}

	firstRegistry := platformtask.NewRegistry()
	firstHandler, err := deliverytask.NewFanoutHandler(store, generator, adapter, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err := firstRegistry.Register(deliverytask.FanoutKey, firstHandler); err != nil {
		t.Fatal(err)
	}
	firstWorker, err := adapter.NewWorker(firstRegistry, taskheadgate.DefaultWorkerConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := firstWorker.Drain(ctx, 1)
	if err != nil || len(completed) != 1 {
		t.Fatalf("first fanout drain=%v err=%v", completed, err)
	}
	assertFanoutState(t, admin, scope, eventID, delivery.EventPending, deliverytask.FanoutBatch, deliverytask.FanoutBatch)

	replacementRegistry := platformtask.NewRegistry()
	replacementHandler, err := deliverytask.NewFanoutHandler(store, generator, adapter, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err := replacementRegistry.Register(deliverytask.FanoutKey, replacementHandler); err != nil {
		t.Fatal(err)
	}
	if err := replacementRegistry.Register(deliverytask.DeliverKey, platformtask.HandlerFunc(func(context.Context, platformtask.Delivery) platformtask.Result {
		return platformtask.Complete()
	})); err != nil {
		t.Fatal(err)
	}
	replacementWorker, err := adapter.NewWorker(replacementRegistry, taskheadgate.DefaultWorkerConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	replacementContext, stopReplacement := context.WithCancel(ctx)
	replacementDone := make(chan error, 1)
	go func() { replacementDone <- replacementWorker.Run(replacementContext) }()
	deadline := time.NewTimer(15 * time.Second)
	ticker := time.NewTicker(20 * time.Millisecond)
	completedFanout := false
	for !completedFanout {
		select {
		case <-deadline.C:
			stopReplacement()
			t.Fatal("replacement worker did not complete the successor fanout")
		case <-ticker.C:
			var state string
			if err := admin.Native().QueryRow(ctx, `SELECT state FROM idenqa.webhook_events WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), eventID.String()).Scan(&state); err != nil {
				stopReplacement()
				t.Fatal(err)
			}
			completedFanout = state == string(delivery.EventCompleted)
		}
	}
	deadline.Stop()
	ticker.Stop()
	stopReplacement()
	if err := <-replacementDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	assertFanoutState(t, admin, scope, eventID, delivery.EventCompleted, deliverytask.FanoutBatch+1, deliverytask.FanoutBatch+1)

	work, result := replacementHandler.Prepare(ctx, platformtask.Delivery{Intent: initialIntent, Attempt: 2, Fence: 2})
	if result.Outcome != platformtask.OutcomeComplete || work == nil {
		t.Fatalf("completed replay prepare=%#v", result)
	}
	if err := runtime.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		returned := work(ctx, tx)
		if returned.Outcome != platformtask.OutcomeComplete {
			return returned.Err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	assertFanoutState(t, admin, scope, eventID, delivery.EventCompleted, deliverytask.FanoutBatch+1, deliverytask.FanoutBatch+1)
}

func assertFanoutState(t *testing.T, admin *platformpostgres.Pool, scope tenant.Scope, eventID id.Event, wantState delivery.EventState, wantDelivered, wantRows int) {
	t.Helper()
	var state string
	var delivered, rows, uniqueRows int
	if err := admin.Native().QueryRow(t.Context(), `SELECT state,delivered_count,
		(SELECT count(*) FROM idenqa.webhook_deliveries WHERE tenant_id=$1 AND event_id=$2),
		(SELECT count(*) FROM (SELECT endpoint_id,event_id FROM idenqa.webhook_deliveries WHERE tenant_id=$1 AND event_id=$2 GROUP BY endpoint_id,event_id) AS unique_deliveries)
		FROM idenqa.webhook_events WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), eventID.String()).Scan(&state, &delivered, &rows, &uniqueRows); err != nil {
		t.Fatal(err)
	}
	if state != string(wantState) || delivered != wantDelivered || rows != wantRows || uniqueRows != wantRows {
		t.Fatalf("fanout state=%s delivered=%d rows=%d unique=%d want=%s/%d/%d", state, delivered, rows, uniqueRows, wantState, wantDelivered, wantRows)
	}
}
