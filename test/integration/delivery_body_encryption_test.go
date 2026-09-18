//go:build integration

package integration_test

import (
	"bytes"
	"context"
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
	"github.com/Mujhtech/idenqa/internal/tenant"
	sdk "github.com/Mujhtech/idenqa/sdk/go"
)

// TestDeliveryBodiesAreWrappedAtRest proves catalogue event and delivery bodies
// never rest in plaintext, that fanout reuses one wrapped body for every
// endpoint, that the send boundary signs the decrypted exact bytes, and that
// replay preserves the original event identity and wrapping.
func TestDeliveryBodiesAreWrappedAtRest(t *testing.T) {
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
	runtimeConfig := poolConfig(database.url)
	runtimeConfig.Role = database.createRuntimeRole(t)
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
	_, secret, err := manager.CreateEndpoint(ctx, scope, "https://body.example.test/webhook")
	if err != nil {
		t.Fatal(err)
	}
	defer clear(secret)
	if _, otherSecret, err := manager.CreateEndpoint(ctx, scope, "https://second.example.test/webhook"); err != nil {
		t.Fatal(err)
	} else {
		clear(otherSecret)
	}
	region := "ng-lagos"
	seed := "verification.completed:synthetic-body-encryption"
	fields := map[string]any{
		"verification_id": "ver_01ARZ3NDEKTSV4RRFFQ69G5FAV",
		"subject_id":      "sub_01ARZ3NDEKTSV4RRFFQ69G5FAV",
		"decision_id":     "dec_01ARZ3NDEKTSV4RRFFQ69G5FAV",
		"outcome":         "verified",
		"decided_at":      now.Format(time.RFC3339),
		"verification": map[string]any{
			"id":      "ver_01ARZ3NDEKTSV4RRFFQ69G5FAV",
			"type":    "verification.session",
			"status":  "completed",
			"outcome": "verified",
			"checks":  []map[string]any{},
		},
	}
	if err := runtime.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, scope.ID().String()); err != nil {
			return err
		}
		return deliverypostgres.EmitCatalogueEvent(ctx, tx, keyring, scope.ID().String(), region, webhookv1.VerificationCompleted, seed, now, fields)
	}); err != nil {
		t.Fatal(err)
	}
	assertBodyWrapped(t, admin, `SELECT count(*) FROM idenqa.webhook_events WHERE tenant_id=$1 AND position(encode('verification.completed'::bytea,'hex') in encode(body,'hex')) > 0`, scope.ID().String())
	eventID := webhookv1.DeterministicEventID(seed)
	queue := &bodyProofQueue{}
	fanout, err := deliverytask.NewFanoutHandler(store, generator, queue, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	fanoutIntent, err := deliverytask.NewFanoutIntent(generator, scope, mustEventID(t, eventID), now)
	if err != nil {
		t.Fatal(err)
	}
	work, result := fanout.Prepare(ctx, platformtask.Delivery{Intent: fanoutIntent, Attempt: 1, Fence: 1})
	if result.Outcome != platformtask.OutcomeComplete || work == nil {
		t.Fatalf("fanout prepare: %#v", result)
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
	if len(queue.intents) != 2 {
		t.Fatalf("fanout attempts=%d want=2", len(queue.intents))
	}
	assertBodyWrapped(t, admin, `SELECT count(*) FROM idenqa.webhook_deliveries WHERE tenant_id=$1 AND position(encode('verification.completed'::bytea,'hex') in encode(body,'hex')) > 0`, scope.ID().String())
	firstBody, secondBody, originalID := deliveryBodies(t, admin, scope.ID().String())
	if !bytes.Equal(firstBody, secondBody) {
		t.Fatal("fanout did not reuse one wrapped body for both endpoints")
	}
	verifier, err := sdk.NewWebhookVerifier([][]byte{secret}, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	sender := &bodyProofSender{verifier: verifier}
	handler, err := deliverytask.NewHandler(store, keyring, sender, generator, queue, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	taskIntent, err := deliverytask.NewIntent(generator, scope, originalID, now)
	if err != nil {
		t.Fatal(err)
	}
	prepared, result := handler.Prepare(ctx, platformtask.Delivery{Intent: taskIntent, Attempt: 1, Fence: 1})
	if result.Outcome != platformtask.OutcomeComplete || prepared == nil {
		t.Fatalf("delivery prepare: %#v", result)
	}
	if err := runtime.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		returned := prepared(ctx, tx)
		if returned.Outcome != platformtask.OutcomeComplete {
			return returned.Err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if sender.err != nil {
		t.Fatalf("receiver rejected signed body: %v", sender.err)
	}
	data, err := delivery.EventData(fields)
	if err != nil {
		t.Fatal(err)
	}
	event, err := webhookv1.NewEvent(eventID, scope.ID().String(), region, webhookv1.VerificationCompleted, webhookv1.SchemaVersion, now, data)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := event.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sender.body, canonical) {
		t.Fatal("receiver did not receive the exact canonical plaintext body")
	}
	original, err := store.FindDelivery(ctx, scope, originalID)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := manager.Replay(ctx, scope, originalID, mustEventID(t, eventID))
	if err != nil {
		t.Fatal(err)
	}
	if replay.EventID != original.EventID || replay.ReplayOf != original.ID || replay.BodyWrapping == nil || !bytes.Equal(replay.Body, original.Body) {
		t.Fatalf("replay changed event identity or wrapping: %#v", replay)
	}
	stored, err := store.FindDelivery(ctx, scope, replay.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.BodyWrapping == nil || stored.EventID != original.EventID || !bytes.Equal(stored.Body, original.Body) {
		t.Fatalf("stored replay lost wrapping: %#v", stored)
	}
}

func mustEventID(t *testing.T, value string) id.Event {
	t.Helper()
	parsed, err := id.ParseEvent(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func assertBodyWrapped(t *testing.T, admin *platformpostgres.Pool, query, tenantID string) {
	t.Helper()
	var leaked int
	if err := admin.Native().QueryRow(t.Context(), query, tenantID).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Fatalf("%d rows store plaintext event bodies", leaked)
	}
}

func deliveryBodies(t *testing.T, admin *platformpostgres.Pool, tenantID string) ([]byte, []byte, id.Delivery) {
	t.Helper()
	rows, err := admin.Native().Query(t.Context(), `SELECT body,body_provider,body_algorithm FROM idenqa.webhook_deliveries WHERE tenant_id=$1 ORDER BY id`, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var bodies [][]byte
	for rows.Next() {
		var body []byte
		var provider, algorithm *string
		if err := rows.Scan(&body, &provider, &algorithm); err != nil {
			t.Fatal(err)
		}
		if provider == nil || algorithm == nil {
			t.Fatal("delivery row lacks wrapping metadata")
		}
		bodies = append(bodies, body)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 {
		t.Fatalf("deliveries=%d want=2", len(bodies))
	}
	var encoded string
	if err := admin.Native().QueryRow(t.Context(), `SELECT id FROM idenqa.webhook_deliveries WHERE tenant_id=$1 ORDER BY id LIMIT 1`, tenantID).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	identifier, err := id.ParseDelivery(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return bodies[0], bodies[1], identifier
}

type bodyProofQueue struct{ intents []platformtask.Intent }

func (queue *bodyProofQueue) EnqueueTx(_ context.Context, _ platformpostgres.Transaction, intents ...platformtask.Intent) error {
	queue.intents = append(queue.intents, intents...)
	return nil
}

type bodyProofSender struct {
	verifier *sdk.WebhookVerifier
	body     []byte
	err      error
}

func (sender *bodyProofSender) Send(_ context.Context, _ string, signature delivery.Signature, body []byte) (delivery.SafeDiagnostic, bool, bool, error) {
	if err := sender.verifier.Verify(signature.Timestamp, signature.EventID, signature.Value, body); err != nil {
		sender.err = err
		return delivery.SafeDiagnostic{}, false, false, err
	}
	sender.body = append([]byte(nil), body...)
	diagnostic, err := delivery.NewSafeDiagnostic(204, "delivered", 0)
	return diagnostic, true, false, err
}
