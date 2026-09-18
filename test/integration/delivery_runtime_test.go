//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/delivery"
	deliverypostgres "github.com/Mujhtech/idenqa/internal/delivery/postgres"
	deliverytask "github.com/Mujhtech/idenqa/internal/delivery/task"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// The send cannot be rolled back; only acknowledged effects and successor intent
// become durable together. A replay therefore signs the same event again.
func testDeliveryRuntimeRollback(t *testing.T, runtime *platformpostgres.Pool, store *deliverypostgres.Store, scope tenant.Scope, ids *id.Generator, manager *delivery.Manager, endpointID id.WebhookEndpoint, now time.Time) {
	t.Helper()
	eventID, err := ids.NewEvent()
	if err != nil {
		t.Fatal(err)
	}
	intent, err := manager.CreateDelivery(t.Context(), scope, endpointID, eventID, "verification.completed.v1", []byte(`{"verification_id":"synthetic"}`))
	if err != nil {
		t.Fatal(err)
	}
	queue := &deliveryRuntimeQueue{failure: errors.New("injected enqueue failure")}
	sender := &deliveryRuntimeSender{}
	handler, err := deliverytask.NewHandler(store, integrationProtector{}, sender, ids, queue, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	taskIntent, err := deliverytask.NewIntent(ids, scope, intent.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	prepared, result := handler.Prepare(t.Context(), platformtask.Delivery{Intent: taskIntent, Attempt: 7, Fence: 1})
	if result.Outcome != platformtask.OutcomeComplete {
		t.Fatalf("prepare: %#v", result)
	}
	commit := func(effect platformtask.TransactionWork) error {
		return runtime.WithinTransaction(t.Context(), platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationSerializable}, func(ctx context.Context, tx platformpostgres.Transaction) error {
			result := effect(ctx, tx)
			if result.Outcome != platformtask.OutcomeComplete {
				return result.Err
			}
			return nil
		})
	}
	if err := commit(prepared); err == nil {
		t.Fatal("injected enqueue failure committed")
	}
	stored, err := store.FindDelivery(t.Context(), scope, intent.ID)
	if err != nil || stored.AttemptCount != 0 || stored.State != delivery.StatePending {
		t.Fatalf("rollback state: %#v, %v", stored, err)
	}
	var count int
	if err := runtime.WithinTransaction(t.Context(), platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, scope.ID().String()); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM idenqa.webhook_delivery_attempts WHERE tenant_id=$1 AND delivery_id=$2`, scope.ID().String(), intent.ID.String()).Scan(&count)
	}); err != nil || count != 0 {
		t.Fatalf("rolled back diagnostics=%d, %v", count, err)
	}
	queue.failure = nil
	prepared, result = handler.Prepare(t.Context(), platformtask.Delivery{Intent: taskIntent, Attempt: 8, Fence: 2})
	if result.Outcome != platformtask.OutcomeComplete {
		t.Fatalf("retry prepare: %#v", result)
	}
	if err := commit(prepared); err != nil {
		t.Fatal(err)
	}
	stored, err = store.FindDelivery(t.Context(), scope, intent.ID)
	if err != nil || stored.AttemptCount != 1 || stored.State != delivery.StatePending || len(queue.intents) != 1 {
		t.Fatalf("committed retry: %#v, tasks=%d, %v", stored, len(queue.intents), err)
	}
	if !queue.intents[0].ScheduledAt().Equal(now.Add(5*time.Minute)) || !queue.intents[0].Deadline().Equal(intent.CreatedAt.Add(deliverytask.MaximumDeliveryDuration)) {
		t.Fatal("Retry-After or fixed delivery deadline was lost")
	}
	prepared, result = handler.Prepare(t.Context(), platformtask.Delivery{Intent: taskIntent, Attempt: 1, Fence: 3})
	if result.Outcome != platformtask.OutcomeComplete {
		t.Fatalf("stale retry: %#v", result)
	}
	if err := commit(prepared); err != nil {
		t.Fatal(err)
	}
	if sender.calls != 2 {
		t.Fatalf("stale task resent: calls=%d", sender.calls)
	}
	targets, err := store.ListReadyDeliveries(t.Context(), now.Add(5*time.Minute), 100)
	if err != nil || len(targets) != 1 || targets[0].DeliveryID != intent.ID || targets[0].AttemptNumber != 2 {
		t.Fatalf("pending discovery: %#v, %v", targets, err)
	}
	secondEvent, err := ids.NewEvent()
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.CreateDelivery(t.Context(), scope, endpointID, secondEvent, "verification.completed.v1", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[id.Delivery]bool{}
	for offset := range 2 {
		batch, err := store.ListReadyDeliveries(t.Context(), now.Add(6*time.Minute+time.Duration(offset)*time.Second), 1)
		if err != nil || len(batch) != 1 {
			t.Fatalf("fair discovery: %#v, %v", batch, err)
		}
		seen[batch[0].DeliveryID] = true
	}
	if !seen[intent.ID] || !seen[second.ID] {
		t.Fatal("bounded discovery starved a pending delivery")
	}

}

type deliveryRuntimeQueue struct {
	failure error
	intents []platformtask.Intent
}

func (queue *deliveryRuntimeQueue) EnqueueTx(_ context.Context, _ platformpostgres.Transaction, intents ...platformtask.Intent) error {
	if queue.failure != nil {
		return queue.failure
	}
	queue.intents = append(queue.intents, intents...)
	return nil
}

type deliveryRuntimeSender struct{ calls int }

func (sender *deliveryRuntimeSender) Send(_ context.Context, _ string, _ delivery.Signature, _ []byte) (delivery.SafeDiagnostic, bool, bool, error) {
	sender.calls++
	diagnostic, err := delivery.NewSafeDiagnostic(503, "retryable_status", 5*time.Minute)
	return diagnostic, false, true, err
}
