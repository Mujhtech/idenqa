package task

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/postgres"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	taskheadgate "github.com/Mujhtech/idenqa/internal/platform/task/headgate"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// ExpireKey identifies the reference-only verification.expire v1 task.
var ExpireKey = mustKey("verification.expire", 1)

// ExpiryTarget is a bounded installation-wide routing hint, never transition authority.
type ExpiryTarget struct {
	TenantID       id.Tenant
	VerificationID id.Verification
}

// ExpiryRepository reloads current state in the fenced transaction.
type ExpiryRepository interface {
	ListDueExpirations(context.Context, time.Time, int) ([]ExpiryTarget, error)
	ExpireWithin(context.Context, tenant.Scope, postgres.Transaction, id.Verification, id.Task) error
}

// ExpiryHandler runs only with the worker's transactional completion fence.
type ExpiryHandler struct{ repository ExpiryRepository }

// NewExpiryHandler constructs durable expiry execution.
func NewExpiryHandler(repository ExpiryRepository) (*ExpiryHandler, error) {
	if repository == nil {
		return nil, errors.New("verification task: expiry repository is required")
	}
	return &ExpiryHandler{repository: repository}, nil
}

// Handle refuses execution without transactional fencing.
func (handler *ExpiryHandler) Handle(context.Context, platformtask.Delivery) platformtask.Result {
	return platformtask.Quarantine(errors.New("verification expiry requires transactional completion"))
}

// Prepare decodes references; all state reads and time observations happen at commit.
func (handler *ExpiryHandler) Prepare(_ context.Context, work platformtask.Delivery) (platformtask.TransactionWork, platformtask.Result) {
	var wire struct {
		VerificationID string `json:"verification_id"`
	}
	if err := decodeStrict(work.Intent.Payload(), &wire); err != nil {
		return nil, platformtask.Quarantine(err)
	}
	identifier, err := id.ParseVerification(wire.VerificationID)
	if err != nil || work.Intent.Key() != ExpireKey {
		return nil, platformtask.Quarantine(platformtask.ErrInvalid)
	}
	scope, err := tenant.NewScope(work.Intent.TenantID())
	if err != nil {
		return nil, platformtask.Quarantine(err)
	}
	return func(ctx context.Context, tx postgres.Transaction) platformtask.Result {
		if err := handler.repository.ExpireWithin(ctx, scope, tx, identifier, work.Intent.ID()); err != nil {
			return platformtask.Retry(platformtask.RetryClassTransient, err)
		}
		return platformtask.Complete()
	}, platformtask.Complete()
}

// ExpiryCoordinator recovers elapsed deadlines from durable session state.
type ExpiryCoordinator struct {
	repository  ExpiryRepository
	identifiers IdentifierGenerator
	enqueuer    platformtask.Enqueuer
	clock       clock.Clock
}

// NewExpiryCoordinator constructs bounded expiry discovery.
func NewExpiryCoordinator(repository ExpiryRepository, identifiers IdentifierGenerator, enqueuer platformtask.Enqueuer, source clock.Clock) (*ExpiryCoordinator, error) {
	if repository == nil || identifiers == nil || enqueuer == nil || source == nil {
		return nil, errors.New("verification task: expiry dependencies are required")
	}
	return &ExpiryCoordinator{repository: repository, identifiers: identifiers, enqueuer: enqueuer, clock: source}, nil
}

// ScheduleExpired discovers one fair batch. Expired queue work can be recovered in
// a new hourly window; this never changes the authoritative session deadline.
func (coordinator *ExpiryCoordinator) ScheduleExpired(ctx context.Context) (int, error) {
	now := coordinator.clock.Now().UTC().Truncate(time.Microsecond)
	targets, err := coordinator.repository.ListDueExpirations(ctx, now, CoordinationBatch)
	if err != nil {
		return 0, err
	}
	if len(targets) > CoordinationBatch {
		return 0, platformtask.ErrInvalid
	}
	count := 0
	var failures error
	for _, target := range targets {
		scope, err := tenant.NewScope(target.TenantID)
		if err != nil || target.VerificationID.IsZero() {
			return count, platformtask.ErrInvalid
		}
		window := now.Truncate(time.Hour)
		intent, err := newIntent(coordinator.identifiers, scope, ExpireKey, taskheadgate.QueueMaintenance,
			"verification.expire:"+target.VerificationID.String()+":"+strconv.FormatInt(window.Unix(), 10),
			struct {
				VerificationID string `json:"verification_id"`
			}{target.VerificationID.String()},
			IntentMetadata{ScheduledAt: now}, window.Add(2*time.Hour), ReconcileRetry)
		if err == nil {
			err = coordinator.enqueuer.Enqueue(ctx, intent)
		}
		if err != nil {
			failures = errors.Join(failures, fmt.Errorf("enqueue verification expiry: %w", err))
			continue
		}
		count++
	}
	return count, failures
}
