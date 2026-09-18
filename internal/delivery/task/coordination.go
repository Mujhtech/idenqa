package task

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Mujhtech/idenqa/internal/delivery"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// CoordinationBatch bounds identifier-only installation-wide discovery.
const CoordinationBatch = 100

// DeliveryTarget describes pending durable work without payloads or secrets.
type DeliveryTarget struct {
	TenantID      id.Tenant
	DeliveryID    id.Delivery
	AttemptNumber int32
	DueAt         time.Time
	CreatedAt     time.Time
}

// CoordinationRepository discovers pending deliveries independently of notifications.
type CoordinationRepository interface {
	ListReadyDeliveries(context.Context, time.Time, int) ([]DeliveryTarget, error)
}

// Coordinator recovers initial, replayed, and interrupted delivery scheduling.
type Coordinator struct {
	repository  CoordinationRepository
	identifiers IdentifierGenerator
	enqueuer    platformtask.Enqueuer
	clock       clock.Clock
	batchSize   int
}

// NewCoordinator constructs bounded durable callback discovery.
func NewCoordinator(repository CoordinationRepository, identifiers IdentifierGenerator, enqueuer platformtask.Enqueuer, source clock.Clock, batchSize int) (*Coordinator, error) {
	if repository == nil || identifiers == nil || enqueuer == nil || source == nil || batchSize < 1 || batchSize > CoordinationBatch {
		return nil, delivery.ErrInvalid
	}
	return &Coordinator{repository: repository, identifiers: identifiers, enqueuer: enqueuer, clock: source, batchSize: batchSize}, nil
}

// ScheduleReadyDeliveries enqueues stable identities for each logical attempt.
func (coordinator *Coordinator) ScheduleReadyDeliveries(ctx context.Context) (int, error) {
	if coordinator == nil {
		return 0, delivery.ErrInvalid
	}
	now := coordinator.clock.Now().UTC().Truncate(time.Microsecond)
	targets, err := coordinator.repository.ListReadyDeliveries(ctx, now, coordinator.batchSize)
	if err != nil {
		return 0, fmt.Errorf("list ready deliveries: %w", err)
	}
	if len(targets) > coordinator.batchSize {
		return 0, delivery.ErrInvalid
	}
	count := 0
	for _, target := range targets {
		scope, err := tenant.NewScope(target.TenantID)
		if err != nil || target.DeliveryID.IsZero() || target.DueAt.IsZero() || target.CreatedAt.IsZero() || target.DueAt.After(now) || target.CreatedAt.After(now) {
			return count, delivery.ErrInvalid
		}
		deadline := target.CreatedAt.Add(MaximumDeliveryDuration)
		scheduledAt := target.DueAt.UTC()
		suffix := ""
		if !now.Before(deadline) {
			// This separate task can close an intent whose original task deadline
			// elapsed. The handler still enforces the original delivery window.
			window := now.Truncate(time.Hour)
			scheduledAt, deadline = now, window.Add(2*time.Hour)
			suffix = ":expiry:" + strconv.FormatInt(window.Unix(), 10)
		}
		intent, err := newAttemptIntent(coordinator.identifiers, scope, target.DeliveryID, target.AttemptNumber, scheduledAt, deadline, suffix)
		if err != nil {
			return count, err
		}
		if err := coordinator.enqueuer.Enqueue(ctx, intent); err != nil {
			return count, fmt.Errorf("enqueue ready delivery: %w", err)
		}
		count++
	}
	return count, nil
}
