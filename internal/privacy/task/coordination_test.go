package task_test

import (
	"context"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	privacytask "github.com/Mujhtech/idenqa/internal/privacy/task"
)

func TestCoordinatorSchedulesOneVersionedIntent(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	identifiers, _ := id.NewSystemGenerator()
	tenantID, _ := identifiers.NewTenant()
	deletionID, _ := identifiers.NewDeletion()
	repository := coordinationRepositoryStub{targets: []privacytask.DeletionTarget{{TenantID: tenantID, DeletionID: deletionID, Version: 4, DueAt: now.Add(-time.Minute)}}}
	enqueuer := &enqueuerStub{}
	coordinator, err := privacytask.NewCoordinator(repository, identifiers, enqueuer, fixedClock{now}, 10)
	if err != nil {
		t.Fatal(err)
	}
	count, err := coordinator.ScheduleDueDeletions(t.Context())
	if err != nil || count != 1 || len(enqueuer.intents) != 1 {
		t.Fatalf("ScheduleDueDeletions() = %d, %v, intents=%d", count, err, len(enqueuer.intents))
	}
	if enqueuer.intents[0].IdempotencyKey() != "privacy.delete:"+deletionID.String()+":4" {
		t.Fatalf("idempotency key = %s", enqueuer.intents[0].IdempotencyKey())
	}
}

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

type coordinationRepositoryStub struct{ targets []privacytask.DeletionTarget }

func (stub coordinationRepositoryStub) ListDueDeletions(context.Context, time.Time, int) ([]privacytask.DeletionTarget, error) {
	return append([]privacytask.DeletionTarget(nil), stub.targets...), nil
}

type enqueuerStub struct{ intents []platformtask.Intent }

func (stub *enqueuerStub) Enqueue(_ context.Context, intents ...platformtask.Intent) error {
	stub.intents = append(stub.intents, intents...)
	return nil
}
