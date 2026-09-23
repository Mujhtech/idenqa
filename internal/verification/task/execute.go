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
	"github.com/Mujhtech/idenqa/internal/provider"
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

// SemanticRetryIdentifiers supplies fresh identities for a new semantic
// attempt. Headgate delivery retries deliberately keep the original attempt;
// these identities are used only after an authoritative provider failure.
type SemanticRetryIdentifiers interface {
	ResultIdentifiers
	IdentifierGenerator
	NewAttempt() (id.Attempt, error)
}

// SemanticRetryEnqueuer keeps the successor attempt and its task atomic.
type SemanticRetryEnqueuer interface {
	EnqueueTx(context.Context, postgres.Transaction, ...platformtask.Intent) error
}

type verificationDeadlineStore interface {
	VerificationDeadlineWithin(context.Context, tenant.Scope, postgres.Transaction, id.Verification) (time.Time, error)
}

type routeGate interface {
	RouteAdmission(context.Context, tenant.Scope, verification.Check) (verification.RouteAdmission, error)
	RouteAdmissionWithin(context.Context, tenant.Scope, postgres.Transaction, verification.Check) (verification.RouteAdmission, error)
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

// ExternalWait projects the parent session into awaiting_external when a durable
// external dispatch remains pending, and back to processing inside the fenced
// transaction that accepts the authoritative result.
type ExternalWait interface {
	Enter(context.Context, tenant.Scope, id.Verification, id.Check, string) error
	Leave(context.Context, postgres.Transaction, tenant.Scope, id.Verification, string) error
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
	retryIDs      SemanticRetryIdentifiers
	retryEnqueuer SemanticRetryEnqueuer
	externalWait  ExternalWait
	support       verification.DocumentSupportResolver
	routes        routeGate
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

// WithDocumentSupport attaches the active pack registry so a document the
// registry explicitly marks unsupported is classified provisionally. It never
// changes provider signal meaning or assurance semantics.
func (handler *ExecuteHandler) WithDocumentSupport(resolver verification.DocumentSupportResolver) error {
	if handler == nil || resolver == nil {
		return errors.New("verification task: document support resolver is required")
	}
	handler.support = resolver
	return nil
}

// WithSemanticRetries enables bounded provider semantic attempts. The setting
// is explicit so lightweight contract tests and alternative stores cannot
// accidentally claim atomic retry support.
func (handler *ExecuteHandler) WithSemanticRetries(identifiers SemanticRetryIdentifiers, enqueuer SemanticRetryEnqueuer) error {
	if handler == nil || identifiers == nil || enqueuer == nil {
		return errors.New("verification task: semantic retry dependencies are required")
	}
	if _, ok := handler.store.(verificationDeadlineStore); !ok {
		return errors.New("verification task: semantic retry store must expose verification deadline")
	}
	handler.retryIDs = identifiers
	handler.retryEnqueuer = enqueuer
	return nil
}

// WithExternalWait enables explicit parent-session projection around durable
// asynchronous provider dispatches. The dependency stays optional so contract
// tests and synchronous-only compositions keep their prior behaviour.
func (handler *ExecuteHandler) WithExternalWait(wait ExternalWait) error {
	if handler == nil || wait == nil {
		return errors.New("verification task: external wait dependency is required")
	}
	handler.externalWait = wait
	return nil
}

// WithRouteGate enables persisted dependency ordering and exact fallback
// admission immediately before external execution.
func (handler *ExecuteHandler) WithRouteGate(gate routeGate) error {
	if handler == nil || gate == nil {
		return errors.New("verification task: route gate is required")
	}
	handler.routes = gate
	return nil
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
	actor := delivery.Intent.ID().String()
	if handler.routes != nil {
		admission, admissionErr := handler.routes.RouteAdmission(ctx, scope, check)
		if admissionErr != nil {
			return nil, prepareStoreResult(admissionErr)
		}
		switch admission {
		case verification.RouteWait:
			return nil, platformtask.Retry(platformtask.RetryClassTransient, errors.New("verification dependency is not terminal"))
		case verification.RouteSkip:
			return handler.skipRouteWork(scope, payload), platformtask.Complete()
		case verification.RouteRun:
		default:
			return nil, platformtask.Quarantine(verification.ErrInvalidCheck)
		}
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
		return handler.providerWork(scope, payload, actor, result, fingerprint), platformtask.Complete()
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
		if errors.Is(executeErr, provider.ErrDispatchPending) && handler.externalWait != nil {
			if err := handler.externalWait.Enter(ctx, scope, check.VerificationID, check.ID, actor); err != nil {
				return nil, platformtask.Retry(platformtask.RetryClassUnavailable, fmt.Errorf("project external wait: %w", err))
			}
			return nil, executionError(executeErr)
		}
		if executeErr != nil {
			return nil, executionError(executeErr)
		}
		// The observation is transient: consume it into bounded signals before
		// anything is fingerprinted, persisted, audited, or logged.
		consumed, consumeErr := verification.ConsumeProviderDocument(result)
		if consumeErr != nil {
			return nil, platformtask.Quarantine(consumeErr)
		}
		result = consumed
		fingerprint, fingerprintErr := verification.ProviderResultFingerprint(result)
		if fingerprintErr != nil {
			return nil, platformtask.Quarantine(fingerprintErr)
		}
		return handler.providerWork(scope, payload, actor, result, fingerprint), platformtask.Complete()
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
		return handler.modelWork(scope, payload, actor, result, fingerprint), platformtask.Complete()
	default:
		return nil, platformtask.Quarantine(verification.ErrInvalidCheck)
	}
}

func (handler *ExecuteHandler) skipRouteWork(scope tenant.Scope, payload ExecutePayload) platformtask.TransactionWork {
	return func(ctx context.Context, transaction postgres.Transaction) platformtask.Result {
		check, err := handler.store.FindCheckWithin(ctx, scope, transaction, payload.CheckID)
		if err != nil {
			return commitStoreResult(err)
		}
		admission, err := handler.routes.RouteAdmissionWithin(ctx, scope, transaction, check)
		if err != nil {
			return commitStoreResult(err)
		}
		if admission == verification.RouteWait {
			return platformtask.Retry(platformtask.RetryClassTransient, errors.New("verification dependency is not terminal"))
		}
		if admission == verification.RouteRun {
			return platformtask.Retry(platformtask.RetryClassConflict, errors.New("verification route became runnable"))
		}
		expected := check.Version
		at := time.Now().UTC().Truncate(time.Microsecond)
		if err := check.SkipRunning(at, "route_not_selected"); err != nil {
			return platformtask.Quarantine(err)
		}
		eventID, err := handler.identifiers.NewEvent()
		if err != nil {
			return platformtask.Retry(platformtask.RetryClassUnavailable, fmt.Errorf("generate route event: %w", err))
		}
		_, err = handler.store.SaveCheckWithin(ctx, scope, transaction, verification.CheckCommit{Check: check, ExpectedVersion: expected, EventID: eventID})
		if err != nil {
			return commitStoreResult(err)
		}
		return platformtask.Complete()
	}
}

func (handler *ExecuteHandler) providerWork(
	scope tenant.Scope,
	payload ExecutePayload,
	actor string,
	result providerv1.Result,
	fingerprint string,
) platformtask.TransactionWork {
	return func(ctx context.Context, transaction postgres.Transaction) platformtask.Result {
		return handler.commit(ctx, scope, transaction, payload, actor, fingerprint, result.CompletedAt,
			func(check *verification.Check, attempt verification.Attempt) (string, error) {
				return verification.ApplyProviderResult(check, result, handler.identifiers, attempt.Fence, handler.resultOptions()...)
			})
	}
}

func (handler *ExecuteHandler) modelWork(
	scope tenant.Scope,
	payload ExecutePayload,
	actor string,
	result modelv1.Result,
	fingerprint string,
) platformtask.TransactionWork {
	return func(ctx context.Context, transaction postgres.Transaction) platformtask.Result {
		return handler.commit(ctx, scope, transaction, payload, actor, fingerprint, result.CompletedAt,
			func(check *verification.Check, attempt verification.Attempt) (string, error) {
				return verification.ApplyModelResult(check, result, handler.identifiers, attempt.Fence, handler.resultOptions()...)
			})
	}
}

func (handler *ExecuteHandler) resultOptions() []verification.ResultOption {
	if handler.support == nil {
		return nil
	}
	return []verification.ResultOption{verification.WithDocumentSupport(handler.support)}
}

func (handler *ExecuteHandler) commit(
	ctx context.Context,
	scope tenant.Scope,
	transaction postgres.Transaction,
	payload ExecutePayload,
	actor string,
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
	disposition, mutationErr := mutate(&check, attempt)
	if mutationErr != nil && !errors.Is(mutationErr, verification.ErrStaleAttempt) &&
		!errors.Is(mutationErr, verification.ErrAttemptConflict) {
		return platformtask.Quarantine(mutationErr)
	}
	var retryIntent *platformtask.Intent
	if mutationErr == nil && disposition == "applied" && handler.retryIDs != nil {
		intent, retryErr := handler.semanticRetry(ctx, scope, transaction, &check, attempt, receivedAt)
		if retryErr != nil {
			return platformtask.Retry(platformtask.RetryClassUnavailable, retryErr)
		}
		retryIntent = intent
	}
	receipt, err := verification.NewResultReceipt(payload.AttemptID, fingerprint, receivedAt)
	if err != nil {
		return platformtask.Quarantine(err)
	}
	eventID, err := handler.identifiers.NewEvent()
	if err != nil {
		return platformtask.Retry(platformtask.RetryClassUnavailable, fmt.Errorf("generate check progress event: %w", err))
	}
	duplicate, err := handler.store.SaveCheckWithin(ctx, scope, transaction, verification.CheckCommit{
		Check: check, ExpectedVersion: expected, EventID: eventID, Receipt: &receipt,
	})
	if err != nil {
		return commitStoreResult(err)
	}
	if !duplicate && mutationErr == nil && disposition == "applied" && handler.externalWait != nil {
		if err := handler.externalWait.Leave(ctx, transaction, scope, check.VerificationID, actor); err != nil {
			return platformtask.Retry(platformtask.RetryClassUnavailable, fmt.Errorf("project external result: %w", err))
		}
	}
	if !duplicate && retryIntent != nil {
		if err := handler.retryEnqueuer.EnqueueTx(ctx, transaction, *retryIntent); err != nil {
			return platformtask.Retry(platformtask.RetryClassUnavailable, fmt.Errorf("enqueue semantic retry: %w", err))
		}
	}
	return platformtask.Complete()
}

func (handler *ExecuteHandler) semanticRetry(
	ctx context.Context,
	scope tenant.Scope,
	transaction postgres.Transaction,
	check *verification.Check,
	completed verification.Attempt,
	receivedAt time.Time,
) (*platformtask.Intent, error) {
	attempts := check.Attempts()
	if len(attempts) == 0 || len(attempts) >= 3 {
		return nil, nil
	}
	failed := attempts[len(attempts)-1]
	if failed.ID.String() != completed.ID.String() || failed.Failure == nil ||
		(failed.Failure.Retry != verification.RetryBackoff && failed.Failure.Retry != verification.RetryReconcile) {
		return nil, nil
	}
	deadlineStore := handler.store.(verificationDeadlineStore)
	verificationDeadline, err := deadlineStore.VerificationDeadlineWithin(ctx, scope, transaction, check.VerificationID)
	if err != nil {
		return nil, fmt.Errorf("load verification deadline: %w", err)
	}
	scheduledAt := receivedAt.Add(failed.Failure.RetryAfter)
	if !scheduledAt.Before(verificationDeadline) {
		return nil, nil
	}
	attemptDeadline := scheduledAt.Add(MaximumExecuteDuration)
	if verificationDeadline.Before(attemptDeadline) {
		attemptDeadline = verificationDeadline
	}
	attemptID, err := handler.retryIDs.NewAttempt()
	if err != nil {
		return nil, fmt.Errorf("generate semantic attempt: %w", err)
	}
	next := verification.Attempt{
		ID:         attemptID,
		Number:     failed.Number + 1,
		Fence:      failed.Fence + 1,
		RunnerKind: failed.RunnerKind,
		Provenance: failed.Provenance,
		State:      verification.AttemptRunning,
		StartedAt:  scheduledAt,
		Deadline:   attemptDeadline,
	}
	if err := check.BeginAttempt(next); err != nil {
		return nil, err
	}
	factory := NewExecuteIntent
	if failed.Failure.Retry == verification.RetryReconcile {
		factory = NewAsyncExecuteIntent
	}
	intent, err := factory(handler.retryIDs, scope, ExecutePayload{CheckID: check.ID, AttemptID: next.ID}, IntentMetadata{
		ScheduledAt: scheduledAt,
		Deadline:    attemptDeadline,
	})
	if err != nil {
		return nil, err
	}
	return &intent, nil
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
