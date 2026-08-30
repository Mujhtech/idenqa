package realtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

const maximumTicketCleanupBatch = 1000

// TicketCleanupRepository deletes only expired, never-redeemed credentials.
type TicketCleanupRepository interface {
	DeleteExpiredUnredeemed(context.Context, tenant.Scope, time.Time, int32) (int, error)
}

// CleanupService exposes bounded ticket cleanup for the owned task boundary.
// Scheduling remains outside this package and may be supplied by Headgate.
type CleanupService struct {
	repository TicketCleanupRepository
	clock      clock.Clock
}

// NewCleanupService constructs bounded expired-ticket cleanup.
func NewCleanupService(repository TicketCleanupRepository, source clock.Clock) (*CleanupService, error) {
	if repository == nil || source == nil {
		return nil, errors.New("realtime: cleanup service dependencies are required")
	}

	return &CleanupService{repository: repository, clock: source}, nil
}

// CleanupExpired deletes up to batchSize expired, unredeemed tickets for one tenant.
func (service *CleanupService) CleanupExpired(
	ctx context.Context,
	scope tenant.Scope,
	batchSize int32,
) (int, error) {
	if service == nil || service.repository == nil || service.clock == nil || scope.ID().IsZero() ||
		batchSize < 1 || batchSize > maximumTicketCleanupBatch {
		return 0, errors.New("realtime: cleanup scope and batch size are invalid")
	}
	deleted, err := service.repository.DeleteExpiredUnredeemed(
		ctx,
		scope,
		service.clock.Now().UTC(),
		batchSize,
	)
	if err != nil {
		return 0, fmt.Errorf("realtime: delete expired connection tickets: %w", err)
	}

	return deleted, nil
}
