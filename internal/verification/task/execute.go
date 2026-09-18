package task

import (
	"context"
	"errors"
	"fmt"
	"time"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/authority"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/postgres"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

// CheckStore is the tenant-scoped persistence consumed by the handler.
type CheckStore interface {
	FindCheck(context.Context, tenant.Scope, id.Check) (verification.Check, error)
	FindCheckWithin(context.Context, tenant.Scope, postgres.Transaction, id.Check) (verification.Check, error)
	SaveCheckWithin(context.Context, tenant.Scope, postgres.Transaction, verification.CheckCommit) (bool, error)
}

// ResultIdentifiers supplies durable progress and observation identifiers.
type ResultIdentifiers interface {
	NewEvent() (id.Event, error)
	NewObservation() (id.Observation, error)
}

// ProviderRequests reconstructs the exact persisted request and checks current authority.
type ProviderRequests interface {
	Load(context.Context, tenant.Scope, verification.Check, verification.Attempt) (providerv1.Request, error)
}

// ModelRequests reconstructs the exact persisted model envelope.
type ModelRequests interface {
	Load(context.Context, tenant.Scope, verification.Check, verification.Attempt) (modelv1.Request, error)
}

// ExpiredProviderRequests recovers a saved receipt or records an operational timeout.
type ExpiredProviderRequests interface {
	Expire(context.Context, tenant.Scope, verification.Check, verification.Attempt) (providerv1.Result, error)
}

// ExecuteHandler runs a provider or model outside a transaction and commits its
// bounded normalized result through Headgate's fenced transactional completion.
type ExecuteHandler struct {
	store         CheckStore
	identifiers   ResultIdentifiers
	provider      providerv1.Executor
	model         modelv1.Executor
	requests      ProviderRequests
	modelRequests ModelRequests
}

// NewExecuteHandler constructs the exact version-1 verification task handler.
func NewExecuteHandler(
	store CheckStore,
	identifiers ResultIdentifiers,
	provider providerv1.Executor,
	model modelv1.Executor,
) (*ExecuteHandler, error) {
	if store == nil || identifiers == nil || provider == nil || model == nil {
		return nil, errors.New("verification task: execute dependencies are required")
	}
	return &ExecuteHandler{store: store, identifiers: identifiers, provider: provider, model: model}, nil
}

// NewExecuteHandlerWithRequests requires authoritative request loading for real providers.
func NewExecuteHandlerWithRequests(store CheckStore, identifiers ResultIdentifiers, provider providerv1.Executor, model modelv1.Executor, requests ProviderRequests) (*ExecuteHandler, error) {
	if requests == nil {
		return nil, errors.New("verification task: provider request loader is required")
	}
	handler, err := NewExecuteHandler(store, identifiers, provider, model)
	if err != nil {
		return nil, err
	}
	handler.requests = requests
	return handler, nil
}

// Handle fails closed when a driver cannot provide transactional task effects.
func (handler *ExecuteHandler) Handle(context.Context, platformtask.Delivery) platformtask.Result {
	return platformtask.Quarantine(errors.New("verification task requires transactional completion"))
}

// Prepare performs external execution before returning a bounded transaction effect.
func (handler *ExecuteHandler) Prepare(
	ctx context.Context,
	delivery platformtask.Delivery,
) (platformtask.TransactionWork, platformtask.Result) {
	payload, err := DecodeExecute(delivery.Intent.Payload())
	if err != nil {
		return nil, platformtask.Quarantine(err)
	}
	scope, err := tenant.NewScope(delivery.Intent.TenantID())
	if err != nil {
		return nil, platformtask.Quarantine(err)
	}
	check, err := handler.store.FindCheck(ctx, scope, payload.CheckID)
	if err != nil {
		return nil, prepareStoreResult(err)
	}
	attempt, err := exactAttempt(check, payload.AttemptID)
	if err != nil {
		return nil, platformtask.Quarantine(err)
	}

	if delivery.Intent.Key() == AsyncExecuteKey && !time.Now().Before(attempt.Deadline) {
		result := providerv1.Result{Contract: providerv1.CurrentVersion, AttemptID: attempt.ID.String(), Outcome: providerv1.ResultOutcomeFailed, Failure: &providerv1.Failure{Class: providerv1.FailureDeadline, Code: "provider_job_unresolved", Retry: providerv1.RetryReconcile}, CompletedAt: attempt.Deadline}
		if expired, ok := handler.requests.(ExpiredProviderRequests); ok {
			result, err = expired.Expire(ctx, scope, check, attempt)
			if err != nil {
				return nil, prepareStoreResult(err)
			}
		}
		fingerprint, err := verification.ProviderResultFingerprint(result)
		if err != nil {
			return nil, platformtask.Quarantine(err)
		}
		return handler.providerWork(scope, payload, result, fingerprint), platformtask.Complete()
	}
	switch attempt.RunnerKind {
	case verification.RunnerProvider:
		request := providerv1.Request{Contract: providerv1.CurrentVersion, AttemptID: attempt.ID.String()}
		if handler.requests != nil {
			request, err = handler.requests.Load(ctx, scope, check, attempt)
			if err != nil {
				return nil, prepareStoreResult(err)
			}
		}
		result, executeErr := handler.provider.Execute(ctx, request)
		if executeErr != nil {
			return nil, executionError(executeErr)
		}
		fingerprint, fingerprintErr := verification.ProviderResultFingerprint(result)
		if fingerprintErr != nil {
			return nil, platformtask.Quarantine(fingerprintErr)
		}
		return handler.providerWork(scope, payload, result, fingerprint), platformtask.Complete()
	case verification.RunnerModel:
		request := modelv1.Request{Contract: modelv1.CurrentVersion, AttemptID: attempt.ID.String()}
		if handler.modelRequests != nil {
			request, err = handler.modelRequests.Load(ctx, scope, check, attempt)
			if err != nil {
				return nil, prepareStoreResult(err)
			}
		}
		result, executeErr := handler.model.Execute(ctx, request)
		if executeErr != nil {
			return nil, executionError(executeErr)
		}
		fingerprint, fingerprintErr := verification.ModelResultFingerprint(result)
		if fingerprintErr != nil {
			return nil, platformtask.Quarantine(fingerprintErr)
		}
		return handler.modelWork(scope, payload, result, fingerprint), platformtask.Complete()
	default:
		return nil, platformtask.Quarantine(verification.ErrInvalidCheck)
	}
}

func (handler *ExecuteHandler) providerWork(
	scope tenant.Scope,
	payload ExecutePayload,
	result providerv1.Result,
	fingerprint string,
) platformtask.TransactionWork {
	return func(ctx context.Context, transaction postgres.Transaction) platformtask.Result {
		return handler.commit(ctx, scope, transaction, payload, fingerprint, result.CompletedAt,
			func(check *verification.Check, attempt verification.Attempt) (string, error) {
				return verification.ApplyProviderResult(check, result, handler.identifiers, attempt.Fence)
			})
	}
}

func (handler *ExecuteHandler) modelWork(
	scope tenant.Scope,
	payload ExecutePayload,
	result modelv1.Result,
	fingerprint string,
) platformtask.TransactionWork {
	return func(ctx context.Context, transaction postgres.Transaction) platformtask.Result {
		return handler.commit(ctx, scope, transaction, payload, fingerprint, result.CompletedAt,
			func(check *verification.Check, attempt verification.Attempt) (string, error) {
				return verification.ApplyModelResult(check, result, handler.identifiers, attempt.Fence)
			})
	}
}

func (handler *ExecuteHandler) commit(
	ctx context.Context,
	scope tenant.Scope,
	transaction postgres.Transaction,
	payload ExecutePayload,
	fingerprint string,
	receivedAt time.Time,
	mutate func(*verification.Check, verification.Attempt) (string, error),
) platformtask.Result {
	check, err := handler.store.FindCheckWithin(ctx, scope, transaction, payload.CheckID)
	if err != nil {
		return commitStoreResult(err)
	}
	attempt, err := exactAttempt(check, payload.AttemptID)
	if err != nil {
		return platformtask.Quarantine(err)
	}
	expected := check.Version
	_, mutationErr := mutate(&check, attempt)
	if mutationErr != nil && !errors.Is(mutationErr, verification.ErrStaleAttempt) &&
		!errors.Is(mutationErr, verification.ErrAttemptConflict) {
		return platformtask.Quarantine(mutationErr)
	}
	receipt, err := verification.NewResultReceipt(payload.AttemptID, fingerprint, receivedAt)
	if err != nil {
		return platformtask.Quarantine(err)
	}
	eventID, err := handler.identifiers.NewEvent()
	if err != nil {
		return platformtask.Retry(platformtask.RetryClassUnavailable, fmt.Errorf("generate check progress event: %w", err))
	}
	_, err = handler.store.SaveCheckWithin(ctx, scope, transaction, verification.CheckCommit{
		Check: check, ExpectedVersion: expected, EventID: eventID, Receipt: &receipt,
	})
	if err != nil {
		return commitStoreResult(err)
	}
	return platformtask.Complete()
}

func exactAttempt(check verification.Check, attemptID id.Attempt) (verification.Attempt, error) {
	attempts := check.Attempts()
	if attemptID.IsZero() || len(attempts) == 0 {
		return verification.Attempt{}, verification.ErrInvalidCheck
	}
	for _, attempt := range attempts {
		if attempt.ID.String() == attemptID.String() {
			return attempt, nil
		}
	}
	return verification.Attempt{}, verification.ErrInvalidCheck
}

func executionError(err error) platformtask.Result {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return platformtask.Retry(platformtask.RetryClassTransient, err)
	}
	return platformtask.Retry(platformtask.RetryClassUnavailable, err)
}

func prepareStoreResult(err error) platformtask.Result {
	if errors.Is(err, authority.ErrProcessingNotPermitted) || errors.Is(err, authority.ErrSubjectResponseRequired) || errors.Is(err, verification.ErrCheckNotFound) {
		return platformtask.Quarantine(err)
	}
	return platformtask.Retry(platformtask.RetryClassUnavailable, err)
}

func commitStoreResult(err error) platformtask.Result {
	if errors.Is(err, verification.ErrCheckVersion) || errors.Is(err, verification.ErrStaleAttempt) {
		return platformtask.Retry(platformtask.RetryClassConflict, err)
	}
	if errors.Is(err, authority.ErrProcessingNotPermitted) || errors.Is(err, authority.ErrSubjectResponseRequired) || errors.Is(err, verification.ErrCheckNotFound) {
		return platformtask.Quarantine(err)
	}
	if errors.Is(err, verification.ErrInvalidCheck) {
		return platformtask.Quarantine(err)
	}
	return platformtask.Retry(platformtask.RetryClassUnavailable, err)
}

var _ platformtask.TransactionalHandler = (*ExecuteHandler)(nil)

// WithModelRequests enables authoritative request loading for model execution.
func (handler *ExecuteHandler) WithModelRequests(requests ModelRequests) error {
	if requests == nil {
		return errors.New("model request loader required")
	}
	handler.modelRequests = requests
	return nil
}
