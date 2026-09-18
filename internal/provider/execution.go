package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

// ErrRequestUnavailable includes absent and cross-tenant provider request state.
var ErrRequestUnavailable = errors.New("provider: request unavailable")

// ErrDispatchPending requires recovery without repeating the external call.
var ErrDispatchPending = errors.New("provider: dispatch pending reconciliation")

// RequestRepository owns immutable request lookup and durable dispatch receipts.
type RequestRepository interface {
	Load(context.Context, tenant.Scope, verification.Check, verification.Attempt) (providerv1.Request, error)
	Claim(context.Context, providerv1.Request) (bool, *providerv1.Result, error)
	Complete(context.Context, providerv1.Request, providerv1.Result) error
}

// DurableExecutor prevents blind resubmission after an ambiguous external call.
// A pending claim is reconciliation evidence, not permission to charge again.
type DurableExecutor struct {
	repository RequestRepository
	executor   providerv1.Executor
	now        func() time.Time
}

// NewDurableExecutor composes a single provider dispatch with durable recovery.
func NewDurableExecutor(repository RequestRepository, executor providerv1.Executor, now func() time.Time) (*DurableExecutor, error) {
	if repository == nil || executor == nil || now == nil {
		return nil, ErrRequestUnavailable
	}
	return &DurableExecutor{repository, executor, now}, nil
}

// Execute sends one exact request, or recovers its stored result without a new call.
func (executor *DurableExecutor) Execute(ctx context.Context, request providerv1.Request) (providerv1.Result, error) {
	if err := request.Validate(); err != nil {
		return providerv1.Result{}, ErrRequestUnavailable
	}
	claimed, result, err := executor.repository.Claim(ctx, request)
	if err != nil {
		return providerv1.Result{}, err
	}
	if result != nil {
		return *result, nil
	}
	if !claimed {
		return providerv1.Result{}, ErrDispatchPending
	}
	value, err := executor.executor.Execute(ctx, request)
	if err != nil || value.ValidateForRequest(request) != nil {
		// The remote process may have submitted the request before losing its reply.
		// Persist a stable ambiguity outcome; a failed commit leaves the claim pending.
		value = executor.uncertain(request)
	}
	if err := executor.repository.Complete(ctx, request, value); err != nil {
		return providerv1.Result{}, err
	}
	return value, nil
}

func (executor *DurableExecutor) uncertain(request providerv1.Request) providerv1.Result {
	return providerv1.Result{Contract: request.Contract, AttemptID: request.AttemptID, Outcome: providerv1.ResultOutcomeFailed,
		CompletedAt: executor.now().UTC(), Failure: &providerv1.Failure{Class: providerv1.FailureUnavailable, Code: "dispatch_requires_reconciliation", Retry: providerv1.RetryReconcile}}
}

// RequestDigest identifies the complete reference-only attempt envelope.
func RequestDigest(request providerv1.Request) (string, error) {
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
func RequestScope(request providerv1.Request) (tenant.Scope, id.Attempt, error) {
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
