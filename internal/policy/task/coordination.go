package task

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// CoordinationBatch bounds installation-wide authorship discovery.
const CoordinationBatch = 100

// AuthorshipTarget is identifier-only installation-wide discovery output.
type AuthorshipTarget struct {
	TenantID       id.Tenant
	VerificationID id.Verification
	DecisionID     id.Decision
	ReadyAt        time.Time
}

// CoordinationRepository discovers bounded ready decision work.
type CoordinationRepository interface {
	ListReadyAuthorships(context.Context, time.Time, int) ([]AuthorshipTarget, error)
}

// Coordinator schedules stable, idempotent policy.author intents.
type Coordinator struct {
	repository  CoordinationRepository
	identifiers IdentifierGenerator
	enqueuer    platformtask.Enqueuer
	clock       clock.Clock
	batchSize   int
}

// NewCoordinator constructs the bounded policy authorship scheduler.
func NewCoordinator(repository CoordinationRepository, identifiers IdentifierGenerator, enqueuer platformtask.Enqueuer, source clock.Clock, batchSize int) (*Coordinator, error) {
	if repository == nil || identifiers == nil || enqueuer == nil || source == nil || batchSize < 1 || batchSize > CoordinationBatch {
		return nil, errors.New("policy task: coordination dependencies are required")
	}
	return &Coordinator{repository: repository, identifiers: identifiers, enqueuer: enqueuer, clock: source, batchSize: batchSize}, nil
}

// ScheduleReadyAuthorships performs bounded discovery and independent enqueue.
func (coordinator *Coordinator) ScheduleReadyAuthorships(ctx context.Context) (int, error) {
	if coordinator == nil {
		return 0, policy.ErrInvalid
	}
	now := coordinator.clock.Now().UTC()
	targets, err := coordinator.repository.ListReadyAuthorships(ctx, now, coordinator.batchSize)
	if err != nil {
		return 0, fmt.Errorf("list ready policy authorships: %w", err)
	}
	if len(targets) > coordinator.batchSize {
		return 0, policy.ErrInvalid
	}
	enqueued := 0
	for _, target := range targets {
		if target.TenantID.IsZero() || target.VerificationID.IsZero() || target.DecisionID.IsZero() || target.ReadyAt.IsZero() || target.ReadyAt.Location() != time.UTC || target.ReadyAt.After(now) {
			return enqueued, policy.ErrInvalid
		}
		scope, scopeErr := tenant.NewScope(target.TenantID)
		if scopeErr != nil {
			return enqueued, policy.ErrInvalid
		}
		intent, intentErr := NewAuthorIntent(coordinator.identifiers, scope, policy.AuthorRequest{
			DecisionID: target.DecisionID, VerificationID: target.VerificationID,
			EvaluatedAt: target.ReadyAt, DecidedAt: target.ReadyAt,
		}, IntentMetadata{ScheduledAt: now, Deadline: now.Add(MaximumAuthorDuration)})
		if intentErr != nil {
			return enqueued, intentErr
		}
		if enqueueErr := coordinator.enqueuer.Enqueue(ctx, intent); enqueueErr != nil {
			return enqueued, fmt.Errorf("enqueue ready policy authorship: %w", enqueueErr)
		}
		enqueued++
	}
	return enqueued, nil
}
