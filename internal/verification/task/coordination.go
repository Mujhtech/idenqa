package task

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

const (
	// CoordinationBatch is the selected installation-wide discovery bound.
	CoordinationBatch = 100
	// ReconciliationItemLease is the selected application reconciliation lease.
	ReconciliationItemLease = 2 * time.Minute
	// ProgressPollInterval is the correctness fallback when notifications are absent.
	ProgressPollInterval = time.Second
	// ReconciliationSweepInterval is the selected periodic repair interval.
	ReconciliationSweepInterval = time.Minute
)

// ReconciliationTarget contains only routing identifiers discovered by the
// narrow installation-wide coordination function.
type ReconciliationTarget struct {
	TenantID  id.Tenant
	CheckID   id.Check
	AttemptID id.Attempt
}

// CoordinationRepository exposes bounded, identifier-only system discovery.
// Every consequential read or mutation after discovery re-enters tenant scope.
type CoordinationRepository interface {
	ListDueReconciliations(context.Context, time.Time, int) ([]ReconciliationTarget, error)
	ListPendingProgressTenants(context.Context, int) ([]id.Tenant, error)
	ProjectCheckProgress(context.Context, tenant.Scope, time.Time, int) (int, error)
}

// Coordinator schedules reconciliation and projects safe durable progress.
type Coordinator struct {
	repository  CoordinationRepository
	identifiers IdentifierGenerator
	enqueuer    platformtask.Enqueuer
	clock       clock.Clock
	batchSize   int
}

// NewCoordinator constructs the bounded worker-owned coordination service.
func NewCoordinator(
	repository CoordinationRepository,
	identifiers IdentifierGenerator,
	enqueuer platformtask.Enqueuer,
	source clock.Clock,
	batchSize int,
) (*Coordinator, error) {
	if repository == nil || identifiers == nil || enqueuer == nil || source == nil ||
		batchSize < 1 || batchSize > CoordinationBatch {
		return nil, errors.New("verification task: coordination dependencies are required")
	}
	return &Coordinator{
		repository: repository, identifiers: identifiers, enqueuer: enqueuer,
		clock: source, batchSize: batchSize,
	}, nil
}

// ScheduleReconciliations discovers one bounded installation-wide batch and
// enqueues each semantic reconciliation independently. Keeping enqueue
// transactions independent prevents one expected uniqueness replay from
// suppressing unrelated targets in the same discovery result.
func (coordinator *Coordinator) ScheduleReconciliations(ctx context.Context) (int, error) {
	if coordinator == nil {
		return 0, verification.ErrInvalidCheck
	}
	now := coordinator.clock.Now().UTC()
	targets, err := coordinator.repository.ListDueReconciliations(ctx, now, coordinator.batchSize)
	if err != nil {
		return 0, fmt.Errorf("list due verification reconciliations: %w", err)
	}
	if len(targets) > coordinator.batchSize {
		return 0, verification.ErrInvalidCheck
	}
	enqueued := 0
	for _, target := range targets {
		if target.TenantID.IsZero() || target.CheckID.IsZero() || target.AttemptID.IsZero() {
			return 0, verification.ErrInvalidCheck
		}
		scope, err := tenant.NewScope(target.TenantID)
		if err != nil {
			return 0, verification.ErrInvalidCheck
		}
		intent, err := NewReconcileIntent(
			coordinator.identifiers,
			scope,
			ReconcilePayload{CheckID: target.CheckID, AttemptID: target.AttemptID},
			IntentMetadata{ScheduledAt: now, Deadline: now.Add(MaximumReconcileDuration)},
		)
		if err != nil {
			return 0, err
		}
		if err := coordinator.enqueuer.Enqueue(ctx, intent); err != nil {
			return enqueued, fmt.Errorf("enqueue verification reconciliation: %w", err)
		}
		enqueued++
	}
	return enqueued, nil
}

// ProjectPendingProgress runs the authoritative one-second fallback sweep.
func (coordinator *Coordinator) ProjectPendingProgress(ctx context.Context) (int, error) {
	if coordinator == nil {
		return 0, verification.ErrInvalidCheck
	}
	tenants, err := coordinator.repository.ListPendingProgressTenants(ctx, coordinator.batchSize)
	if err != nil {
		return 0, fmt.Errorf("list pending progress tenants: %w", err)
	}
	if len(tenants) > coordinator.batchSize {
		return 0, verification.ErrInvalidCheck
	}
	total := 0
	for _, tenantID := range tenants {
		if total >= coordinator.batchSize {
			break
		}
		scope, err := tenant.NewScope(tenantID)
		if err != nil {
			return total, verification.ErrInvalidCheck
		}
		projected, err := coordinator.repository.ProjectCheckProgress(
			ctx, scope, coordinator.clock.Now().UTC(), coordinator.batchSize-total,
		)
		if err != nil {
			return total, fmt.Errorf("project tenant check progress: %w", err)
		}
		if projected < 0 || projected > coordinator.batchSize-total {
			return total, verification.ErrInvalidCheck
		}
		total += projected
	}
	return total, nil
}

// ProjectTenantProgress handles a lossy PostgreSQL routing hint. The database
// remains authoritative and the same bounded operation is safe to repeat.
func (coordinator *Coordinator) ProjectTenantProgress(ctx context.Context, tenantID id.Tenant) (int, error) {
	if coordinator == nil || tenantID.IsZero() {
		return 0, verification.ErrInvalidCheck
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		return 0, verification.ErrInvalidCheck
	}
	projected, err := coordinator.repository.ProjectCheckProgress(
		ctx, scope, coordinator.clock.Now().UTC(), coordinator.batchSize,
	)
	if err != nil {
		return 0, fmt.Errorf("project notified check progress: %w", err)
	}
	return projected, nil
}
