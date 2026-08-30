package realtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

const maximumReplayCleanupBatch = 1000

// ReplayCleanupRepository deletes only events whose exclusive retention
// boundary has elapsed and advances each affected stream floor.
type ReplayCleanupRepository interface {
	DeleteExpiredEvents(context.Context, tenant.Scope, time.Time, int32) (int, error)
}

// ReplayCleanupService exposes bounded durable-event retention work through an
// owned boundary suitable for later Headgate scheduling.
type ReplayCleanupService struct {
	repository ReplayCleanupRepository
	clock      clock.Clock
}

// NewReplayCleanupService constructs the replay-retention workflow.
func NewReplayCleanupService(
	repository ReplayCleanupRepository,
	source clock.Clock,
) (*ReplayCleanupService, error) {
	if repository == nil || source == nil {
		return nil, errors.New("realtime: replay cleanup dependencies are required")
	}

	return &ReplayCleanupService{repository: repository, clock: source}, nil
}

// CleanupExpiredEvents deletes one tenant-scoped bounded batch.
func (service *ReplayCleanupService) CleanupExpiredEvents(
	ctx context.Context,
	scope tenant.Scope,
	batchSize int32,
) (int, error) {
	if service == nil || service.repository == nil || service.clock == nil || scope.ID().IsZero() ||
		batchSize < 1 || batchSize > maximumReplayCleanupBatch {
		return 0, errors.New("realtime: replay cleanup scope and batch are invalid")
	}
	deleted, err := service.repository.DeleteExpiredEvents(
		ctx,
		scope,
		service.clock.Now().UTC(),
		batchSize,
	)
	if err != nil {
		return 0, fmt.Errorf("realtime: delete expired durable events: %w", err)
	}

	return deleted, nil
}
