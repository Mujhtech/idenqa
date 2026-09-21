// Package model owns durable, reference-only model execution.
package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/observability"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

// ErrRequestUnavailable includes absent and cross-tenant model request state.
var ErrRequestUnavailable = errors.New("model: request unavailable")

// ErrDispatchPending requires recovery without repeating the external call.
var ErrDispatchPending = errors.New("model: dispatch pending reconciliation")

// RequestRepository owns immutable request lookup and durable dispatch receipts.
type RequestRepository interface {
	Load(context.Context, tenant.Scope, verification.Check, verification.Attempt) (modelv1.Request, error)
	Claim(context.Context, modelv1.Request) (bool, *modelv1.Result, error)
	Complete(context.Context, modelv1.Request, modelv1.Result) error
}

// DurableExecutor prevents blind resubmission after an ambiguous external call.
// A pending claim is reconciliation evidence, not permission to redeem evidence again.
type DurableExecutor struct {
	repository RequestRepository
	executor   modelv1.Executor
	now        func() time.Time
	metrics    Metrics
}

// NewDurableExecutor composes a single model dispatch with durable recovery.
func NewDurableExecutor(repository RequestRepository, executor modelv1.Executor, now func() time.Time) (*DurableExecutor, error) {
	if repository == nil || executor == nil || now == nil {
		return nil, ErrRequestUnavailable
	}
	return &DurableExecutor{repository, executor, now, nil}, nil
}

// WithMetrics attaches the bounded model metric receiver.
func (executor *DurableExecutor) WithMetrics(metrics Metrics) *DurableExecutor {
	if executor != nil && metrics != nil {
		executor.metrics = metrics
	}
	return executor
}

// Execute sends one exact request, or recovers its stored result without a new call.
func (executor *DurableExecutor) Execute(ctx context.Context, request modelv1.Request) (modelv1.Result, error) {
	if err := request.Validate(); err != nil {
		return modelv1.Result{}, ErrRequestUnavailable
	}
	started := executor.now().UTC()
	claimed, result, err := executor.repository.Claim(ctx, request)
	if err != nil {
		return modelv1.Result{}, err
	}
	if result != nil {
		executor.observe(request, observability.DispatchRecovered, observability.FailureNone, 0)
		return *result, nil
	}
	if !claimed {
		executor.observe(request, observability.DispatchFailed, observability.FailureUnavailable, 0)
		return modelv1.Result{}, ErrDispatchPending
	}
	value, err := executor.executor.Execute(ctx, request)
	if err != nil || value.ValidateForRequest(request) != nil {
		// The remote process may have submitted the request before losing its reply.
		// Persist a stable ambiguity outcome; a failed commit leaves the claim pending.
		value = executor.uncertain(request)
	}
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := executor.repository.Complete(saveCtx, request, value); err != nil {
		return modelv1.Result{}, err
	}
	executor.observe(request, dispatchOutcome(value), modelFailureClass(value), modelDuration(started, value, executor.now().UTC()))
	return value, nil
}

func (executor *DurableExecutor) observe(request modelv1.Request, outcome observability.DispatchOutcome, class observability.FailureClass, duration time.Duration) {
	if executor.metrics == nil {
		return
	}
	executor.metrics.RecordModelDispatch(observability.ModelDispatch{
		Model:        observability.Model(request.Provenance.ModelID),
		Outcome:      outcome,
		FailureClass: class,
		Duration:     duration,
	})
}

func dispatchOutcome(value modelv1.Result) observability.DispatchOutcome {
	switch {
	case value.Outcome == modelv1.ResultOutcomeCompleted:
		return observability.DispatchCompleted
	case value.Failure != nil && value.Failure.Code == "dispatch_requires_reconciliation":
		return observability.DispatchUncertain
	case value.Outcome == modelv1.ResultOutcomeFailed:
		return observability.DispatchFailed
	default:
		return observability.DispatchOther
	}
}

func modelFailureClass(value modelv1.Result) observability.FailureClass {
	if value.Failure == nil {
		return observability.FailureNone
	}
	switch value.Failure.Class {
	case modelv1.FailureInvalidInput:
		return observability.FailureValidation
	case modelv1.FailureUnauthenticated, modelv1.FailureUnauthorized:
		return observability.FailureAuthority
	case modelv1.FailureUnsupported:
		return observability.FailureValidation
	case modelv1.FailureUnavailable:
		return observability.FailureUnavailable
	case modelv1.FailureResourceExhausted:
		return observability.FailureUnavailable
	case modelv1.FailureDeadline:
		return observability.FailureTimeout
	case modelv1.FailureCancelled:
		return observability.FailureTimeout
	case modelv1.FailureInternal:
		return observability.FailureInternal
	default:
		return observability.FailureOther
	}
}

func modelDuration(started time.Time, value modelv1.Result, now time.Time) time.Duration {
	completed := value.CompletedAt
	if completed.IsZero() {
		completed = now
	}
	duration := completed.Sub(started)
	if duration < 0 {
		return 0
	}
	return duration
}

func (executor *DurableExecutor) uncertain(request modelv1.Request) modelv1.Result {
	at := executor.now().UTC()
	if at.After(request.Deadline) {
		at = request.Deadline
	}
	return modelv1.Result{Contract: request.Contract, AttemptID: request.AttemptID, Outcome: modelv1.ResultOutcomeFailed,
		CompletedAt: at, Failure: &modelv1.Failure{Class: modelv1.FailureUnavailable, Code: "dispatch_requires_reconciliation", Retry: modelv1.RetryReconcile}}
}

// RequestDigest identifies the complete reference-only attempt envelope.
func RequestDigest(request modelv1.Request) (string, error) {
	if err := request.Validate(); err != nil {
		return "", ErrRequestUnavailable
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

// RequestScope validates the request's durable tenant and attempt identity.
func RequestScope(request modelv1.Request) (tenant.Scope, id.Attempt, error) {
	tenantID, err := id.ParseTenant(request.TenantID)
	if err != nil {
		return tenant.Scope{}, id.Attempt{}, ErrRequestUnavailable
	}
	attempt, err := id.ParseAttempt(request.AttemptID)
	if err != nil {
		return tenant.Scope{}, id.Attempt{}, ErrRequestUnavailable
	}
	scope, err := tenant.NewScope(tenantID)
	return scope, attempt, err
}
