package task

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/postgres"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

// ReconciliationStore owns exact application leasing and fenced resolution.
type ReconciliationStore interface {
	FindCheckWithin(context.Context, tenant.Scope, postgres.Transaction, id.Check) (verification.Check, error)
	ClaimReconciliationForAttempt(
		context.Context,
		tenant.Scope,
		id.Check,
		id.Attempt,
		id.Task,
		time.Time,
		time.Duration,
	) (verification.ReconciliationClaim, error)
	ResolveReconciliationWithin(
		context.Context,
		tenant.Scope,
		postgres.Transaction,
		verification.ReconciliationClaim,
		time.Time,
	) error
}

// ReconcileHandler closes one exact diagnostic reconciliation without making
// a policy decision or changing the verification check conclusion.
type ReconcileHandler struct {
	store       ReconciliationStore
	identifiers IdentifierGenerator
	clock       clock.Clock
	lease       time.Duration
}

// NewReconcileHandler constructs the exact version-1 reconciliation handler.
func NewReconcileHandler(
	store ReconciliationStore,
	identifiers IdentifierGenerator,
	source clock.Clock,
	lease time.Duration,
) (*ReconcileHandler, error) {
	if store == nil || identifiers == nil || source == nil || lease <= 0 || lease > 10*time.Minute {
		return nil, errors.New("verification task: reconcile dependencies are invalid")
	}
	return &ReconcileHandler{store: store, identifiers: identifiers, clock: source, lease: lease}, nil
}

// Handle fails closed when the task driver cannot fence application effects.
func (handler *ReconcileHandler) Handle(context.Context, platformtask.Delivery) platformtask.Result {
	return platformtask.Quarantine(errors.New("verification reconciliation requires transactional completion"))
}

// Prepare claims only the exact payload target and returns a short PostgreSQL
// effect for atomic application resolution and Headgate completion.
func (handler *ReconcileHandler) Prepare(
	ctx context.Context,
	delivery platformtask.Delivery,
) (platformtask.TransactionWork, platformtask.Result) {
	payload, err := DecodeReconcile(delivery.Intent.Payload())
	if err != nil {
		return nil, platformtask.Quarantine(err)
	}
	scope, err := tenant.NewScope(delivery.Intent.TenantID())
	if err != nil {
		return nil, platformtask.Quarantine(err)
	}
	claimToken, err := handler.identifiers.NewTask()
	if err != nil {
		return nil, platformtask.Retry(
			platformtask.RetryClassUnavailable,
			fmt.Errorf("generate reconciliation claim token: %w", err),
		)
	}
	claimedAt := handler.clock.Now().UTC()
	claim, err := handler.store.ClaimReconciliationForAttempt(
		ctx, scope, payload.CheckID, payload.AttemptID, claimToken, claimedAt, handler.lease,
	)
	if errors.Is(err, verification.ErrCheckNotFound) {
		return func(context.Context, postgres.Transaction) platformtask.Result {
			return platformtask.Complete()
		}, platformtask.Complete()
	}
	if errors.Is(err, verification.ErrStaleAttempt) {
		return nil, platformtask.Retry(platformtask.RetryClassConflict, err)
	}
	if err != nil {
		return nil, platformtask.Retry(platformtask.RetryClassUnavailable, err)
	}
	return handler.resolveWork(scope, payload, claim), platformtask.Complete()
}

func (handler *ReconcileHandler) resolveWork(
	scope tenant.Scope,
	payload ReconcilePayload,
	claim verification.ReconciliationClaim,
) platformtask.TransactionWork {
	return func(ctx context.Context, transaction postgres.Transaction) platformtask.Result {
		check, err := handler.store.FindCheckWithin(ctx, scope, transaction, payload.CheckID)
		if err != nil {
			return commitStoreResult(err)
		}
		if _, err := exactAttempt(check, payload.AttemptID); err != nil ||
			claim.CheckID.String() != payload.CheckID.String() ||
			claim.AttemptID.String() != payload.AttemptID.String() ||
			claim.VerificationID.String() != check.VerificationID.String() {
			return platformtask.Quarantine(verification.ErrInvalidCheck)
		}
		if err := handler.store.ResolveReconciliationWithin(
			ctx, scope, transaction, claim, handler.clock.Now().UTC(),
		); err != nil {
			return commitStoreResult(err)
		}
		return platformtask.Complete()
	}
}

var _ platformtask.TransactionalHandler = (*ReconcileHandler)(nil)
