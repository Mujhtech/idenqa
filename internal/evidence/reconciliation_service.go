package evidence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// ReconciliationService recovers one exact staged object from authoritative
// PostgreSQL state. It never guesses an acceptance outcome from object presence.
type ReconciliationService struct {
	queue         ObjectReconciliationQueue
	uploads       UploadFinder
	assets        AssetFinder
	objects       ObjectDeleter
	clock         clock.Clock
	claimTimeout  time.Duration
	retryDelay    time.Duration
	deleteTimeout time.Duration
}

// NewReconciliationService constructs the bounded, fenced recovery workflow.
func NewReconciliationService(
	queue ObjectReconciliationQueue,
	uploads UploadFinder,
	assets AssetFinder,
	objects ObjectDeleter,
	source clock.Clock,
	claimTimeout time.Duration,
	retryDelay time.Duration,
	deleteTimeout time.Duration,
) (*ReconciliationService, error) {
	if queue == nil || uploads == nil || assets == nil || objects == nil || source == nil ||
		claimTimeout <= 0 || claimTimeout > 15*time.Minute || retryDelay <= 0 || deleteTimeout <= 0 {
		return nil, errors.New("evidence: reconciliation dependencies and bounds are required")
	}

	return &ReconciliationService{
		queue: queue, uploads: uploads, assets: assets, objects: objects, clock: source,
		claimTimeout: claimTimeout, retryDelay: retryDelay, deleteTimeout: deleteTimeout,
	}, nil
}

// ReconcileNext claims and resolves at most one tenant-scoped obligation.
func (service *ReconciliationService) ReconcileNext(
	ctx context.Context,
	scope tenant.Scope,
) (ReconciliationState, error) {
	if service == nil || ctx == nil || scope.ID().IsZero() {
		return "", errors.New("evidence: reconciliation request is invalid")
	}
	now := service.clock.Now().UTC().Truncate(time.Second)
	claimed, err := service.queue.ClaimObjectReconciliation(ctx, scope, now, service.claimTimeout)
	if err != nil {
		return "", err
	}
	state, err := service.determineAndApply(ctx, scope, claimed)
	if err != nil {
		return "", service.retry(ctx, scope, claimed, err)
	}
	completedAt := service.clock.Now().UTC().Truncate(time.Second)
	if err := service.queue.CompleteObjectReconciliation(ctx, scope, claimed, state, completedAt); err != nil {
		return "", fmt.Errorf("complete evidence object reconciliation: %w", err)
	}

	return state, nil
}

func (service *ReconciliationService) determineAndApply(
	ctx context.Context,
	scope tenant.Scope,
	claimed ObjectReconciliation,
) (ReconciliationState, error) {
	record := claimed.Record()
	upload, err := service.uploads.FindUpload(ctx, scope, record.UploadID)
	if err != nil {
		return "", fmt.Errorf("find reconciliation upload: %w", err)
	}
	if upload.Record().EvidenceID != record.EvidenceID {
		return "", ErrReconciliationConflict
	}
	asset, assetErr := service.assets.Find(ctx, scope, record.EvidenceID)
	if upload.State() == UploadStateAccepted {
		if assetErr != nil {
			return "", fmt.Errorf("find accepted reconciliation evidence: %w", assetErr)
		}
		if upload.Attempt() != record.UploadAttempt || asset.Content().Object() != claimed.Object() {
			return "", ErrReconciliationConflict
		}

		return ReconciliationRetained, nil
	}
	if assetErr == nil {
		return "", ErrReconciliationConflict
	}
	if !errors.Is(assetErr, ErrNotFound) {
		return "", fmt.Errorf("check reconciliation evidence absence: %w", assetErr)
	}
	deleteContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), service.deleteTimeout)
	defer cancel()
	if err := service.objects.Delete(deleteContext, claimed.Object()); err != nil {
		return "", fmt.Errorf("delete reconciled evidence object: %w", err)
	}

	return ReconciliationDeleted, nil
}

func (service *ReconciliationService) retry(
	ctx context.Context,
	scope tenant.Scope,
	claimed ObjectReconciliation,
	cause error,
) error {
	retryContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), service.deleteTimeout)
	defer cancel()
	now := service.clock.Now().UTC().Truncate(time.Second)
	if err := service.queue.RetryObjectReconciliation(
		retryContext, scope, claimed, now, service.retryDelay,
	); err != nil {
		return errors.Join(cause, fmt.Errorf("release evidence reconciliation claim: %w", err))
	}

	return cause
}
