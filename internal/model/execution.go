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
}

// NewDurableExecutor composes a single model dispatch with durable recovery.
func NewDurableExecutor(repository RequestRepository, executor modelv1.Executor, now func() time.Time) (*DurableExecutor, error) {
	if repository == nil || executor == nil || now == nil {
		return nil, ErrRequestUnavailable
	}
	return &DurableExecutor{repository, executor, now}, nil
}

// Execute sends one exact request, or recovers its stored result without a new call.
func (executor *DurableExecutor) Execute(ctx context.Context, request modelv1.Request) (modelv1.Result, error) {
	if err := request.Validate(); err != nil {
		return modelv1.Result{}, ErrRequestUnavailable
	}
	claimed, result, err := executor.repository.Claim(ctx, request)
	if err != nil {
		return modelv1.Result{}, err
	}
	if result != nil {
		return *result, nil
	}
	if !claimed {
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
	return value, nil
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
