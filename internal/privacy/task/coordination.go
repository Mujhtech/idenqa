package task

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/privacy"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// DeletionTarget is bounded identifier-only installation discovery output.
type DeletionTarget struct {
	TenantID   id.Tenant
	DeletionID id.Deletion
	Version    int64
	DueAt      time.Time
}

// CoordinationRepository discovers workflows ready for one Headgate intent.
type CoordinationRepository interface {
	ListDueDeletions(context.Context, time.Time, int) ([]DeletionTarget, error)
}

// Coordinator schedules stable privacy deletion intents.
type Coordinator struct {
	repository  CoordinationRepository
	identifiers IdentifierGenerator
	enqueuer    platformtask.Enqueuer
	clock       clock.Clock
	batchSize   int
}

// NewCoordinator constructs bounded deletion coordination.
func NewCoordinator(repository CoordinationRepository, identifiers IdentifierGenerator, enqueuer platformtask.Enqueuer, source clock.Clock, batchSize int) (*Coordinator, error) {
	if repository == nil || identifiers == nil || enqueuer == nil || source == nil || batchSize < 1 || batchSize > CoordinationBatch {
		return nil, errors.New("privacy task: coordination dependencies are invalid")
	}
	return &Coordinator{repository: repository, identifiers: identifiers, enqueuer: enqueuer, clock: source, batchSize: batchSize}, nil
}

// ScheduleDueDeletions discovers and independently enqueues one bounded page.
func (coordinator *Coordinator) ScheduleDueDeletions(ctx context.Context) (int, error) {
	if coordinator == nil {
		return 0, privacy.ErrInvalid
	}
	now := coordinator.clock.Now().UTC()
	targets, err := coordinator.repository.ListDueDeletions(ctx, now, coordinator.batchSize)
	if err != nil {
		return 0, fmt.Errorf("list due privacy deletions: %w", err)
	}
	if len(targets) > coordinator.batchSize {
		return 0, privacy.ErrInvalid
	}
	enqueued := 0
	for _, target := range targets {
		if target.TenantID.IsZero() || target.DeletionID.IsZero() || target.Version < 1 || target.DueAt.IsZero() || target.DueAt.Location() != time.UTC || target.DueAt.After(now) {
			return enqueued, privacy.ErrInvalid
		}
		scope, err := tenant.NewScope(target.TenantID)
		if err != nil {
			return enqueued, privacy.ErrInvalid
		}
		intent, err := NewDeleteIntent(coordinator.identifiers, scope, DeletePayload{DeletionID: target.DeletionID}, IntentMetadata{ScheduledAt: now, WorkflowVersion: target.Version, CausationID: target.DeletionID.String()})
		if err != nil {
			return enqueued, err
		}
		if err := coordinator.enqueuer.Enqueue(ctx, intent); err != nil {
			return enqueued, fmt.Errorf("enqueue privacy deletion: %w", err)
		}
		enqueued++
	}
	return enqueued, nil
}
