package task

import (
	"context"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/postgres"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type expiryRepository struct {
	targets []ExpiryTarget
	calls   int
}

func (repository *expiryRepository) ListDueExpirations(context.Context, time.Time, int) ([]ExpiryTarget, error) {
	return repository.targets, nil
}
func (repository *expiryRepository) ExpireWithin(context.Context, tenant.Scope, postgres.Transaction, id.Verification, id.Task) error {
	repository.calls++
	return nil
}

func TestExpiryTaskRecoveryWindowAndFence(t *testing.T) {
	tenantID, _ := id.ParseTenant("ten_" + taskTestULID)
	verificationID, _ := id.ParseVerification("ver_" + taskTestULID)
	taskID, _ := id.ParseTask("tsk_" + taskTestULID)
	repository := &expiryRepository{targets: []ExpiryTarget{{TenantID: tenantID, VerificationID: verificationID}}}
	queue := &intentCollector{}
	source := coordinationClock{now: time.Date(2026, 9, 6, 12, 30, 0, 0, time.UTC)}
	coordinator, err := NewExpiryCoordinator(repository, fixedTaskIDs{taskID}, queue, source)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := coordinator.ScheduleExpired(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if queue.intents[0].IdempotencyKey() != queue.intents[1].IdempotencyKey() || !queue.intents[0].Deadline().After(source.now) {
		t.Fatal("unstable expiry retry identity or elapsed task deadline")
	}
	coordinator.clock = coordinationClock{now: source.now.Add(time.Hour)}
	if _, err := coordinator.ScheduleExpired(t.Context()); err != nil {
		t.Fatal(err)
	}
	if queue.intents[2].IdempotencyKey() == queue.intents[0].IdempotencyKey() {
		t.Fatal("exhausted expiry task cannot recover in a later window")
	}
	handler, err := NewExpiryHandler(repository)
	if err != nil {
		t.Fatal(err)
	}
	if result := handler.Handle(t.Context(), platformtask.Delivery{Intent: queue.intents[0]}); result.Outcome != platformtask.OutcomeQuarantine || repository.calls != 0 {
		t.Fatal("unfenced expiry executed")
	}
	work, result := handler.Prepare(t.Context(), platformtask.Delivery{Intent: queue.intents[0]})
	if result.Outcome != platformtask.OutcomeComplete || work == nil || repository.calls != 0 {
		t.Fatal("expiry read before fenced effect")
	}
}
