package task

import (
	"context"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type coordinationClock struct{ now time.Time }

func (source coordinationClock) Now() time.Time { return source.now }

type coordinationRepository struct {
	targets    []ReconciliationTarget
	tenants    []id.Tenant
	projected  int
	listedAt   time.Time
	batchSizes []int
}

func (repository *coordinationRepository) ListDueReconciliations(
	_ context.Context,
	observedAt time.Time,
	batchSize int,
) ([]ReconciliationTarget, error) {
	repository.listedAt = observedAt
	repository.batchSizes = append(repository.batchSizes, batchSize)
	return repository.targets, nil
}

func (repository *coordinationRepository) ListPendingProgressTenants(
	context.Context,
	int,
) ([]id.Tenant, error) {
	return repository.tenants, nil
}

func (repository *coordinationRepository) ProjectCheckProgress(
	_ context.Context,
	_ tenant.Scope,
	_ time.Time,
	batchSize int,
) (int, error) {
	repository.batchSizes = append(repository.batchSizes, batchSize)
	return min(repository.projected, batchSize), nil
}

type intentCollector struct {
	intents   []platformtask.Intent
	callSizes []int
}

func (collector *intentCollector) Enqueue(_ context.Context, intents ...platformtask.Intent) error {
	collector.callSizes = append(collector.callSizes, len(intents))
	collector.intents = append(collector.intents, intents...)
	return nil
}

func TestCoordinatorSchedulesExactBoundedReconciliationIntent(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	tenantID, _ := id.ParseTenant("ten_" + taskTestULID)
	checkID, _ := id.ParseCheck("chk_" + taskTestULID)
	attemptID, _ := id.ParseAttempt("atm_" + taskTestULID)
	taskID, _ := id.ParseTask("tsk_" + taskTestULID)
	repository := &coordinationRepository{targets: []ReconciliationTarget{{
		TenantID: tenantID, CheckID: checkID, AttemptID: attemptID,
	}}}
	collector := &intentCollector{}
	coordinator, err := NewCoordinator(
		repository, fixedTaskIDs{taskID}, collector, coordinationClock{now}, 7,
	)
	if err != nil {
		t.Fatal(err)
	}
	count, err := coordinator.ScheduleReconciliations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || len(collector.intents) != 1 || repository.listedAt != now ||
		repository.batchSizes[0] != 7 {
		t.Fatalf("schedule count=%d intents=%d batch=%v", count, len(collector.intents), repository.batchSizes)
	}
	intent := collector.intents[0]
	if intent.Key() != ReconcileKey || intent.TenantID() != tenantID ||
		intent.IdempotencyKey() != "verification.reconcile:"+attemptID.String() {
		t.Fatalf("scheduled intent = %+v", intent)
	}
}

func TestCoordinatorEnqueuesDiscoveredReconciliationsIndependently(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	tenantID, _ := id.ParseTenant("ten_" + taskTestULID)
	checkID, _ := id.ParseCheck("chk_" + taskTestULID)
	attemptID, _ := id.ParseAttempt("atm_" + taskTestULID)
	taskID, _ := id.ParseTask("tsk_" + taskTestULID)
	repository := &coordinationRepository{targets: []ReconciliationTarget{
		{TenantID: tenantID, CheckID: checkID, AttemptID: attemptID},
		{TenantID: tenantID, CheckID: checkID, AttemptID: attemptID},
	}}
	collector := &intentCollector{}
	coordinator, err := NewCoordinator(
		repository, fixedTaskIDs{taskID}, collector, coordinationClock{now}, 7,
	)
	if err != nil {
		t.Fatal(err)
	}
	count, err := coordinator.ScheduleReconciliations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || len(collector.callSizes) != 2 ||
		collector.callSizes[0] != 1 || collector.callSizes[1] != 1 {
		t.Fatalf("schedule count=%d enqueue call sizes=%v", count, collector.callSizes)
	}
}

func TestCoordinatorProjectsTenantScopedProgressWithinSharedBatch(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	tenantID, _ := id.ParseTenant("ten_" + taskTestULID)
	taskID, _ := id.ParseTask("tsk_" + taskTestULID)
	repository := &coordinationRepository{tenants: []id.Tenant{tenantID}, projected: 5}
	coordinator, err := NewCoordinator(
		repository, fixedTaskIDs{taskID}, &intentCollector{}, coordinationClock{now}, 5,
	)
	if err != nil {
		t.Fatal(err)
	}
	projected, err := coordinator.ProjectPendingProgress(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if projected != 5 || len(repository.batchSizes) != 1 || repository.batchSizes[0] != 5 {
		t.Fatalf("projected=%d batches=%v", projected, repository.batchSizes)
	}
}
