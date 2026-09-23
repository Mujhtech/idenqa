package provider

import (
	"context"
	"errors"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	platformbreaker "github.com/Mujhtech/idenqa/internal/platform/breaker"
	"github.com/Mujhtech/idenqa/internal/platform/observability"
)

// ErrBreakerInvalid identifies an invalid bounded circuit breaker policy.
var ErrBreakerInvalid = errors.New("provider: invalid circuit breaker configuration")

// BreakerState is the shared circuit state used by provider health contracts.
type BreakerState = platformbreaker.State

// Bounded circuit breaker states.
const (
	BreakerUnknown  = platformbreaker.StateUnknown
	BreakerClosed   = platformbreaker.StateClosed
	BreakerOpen     = platformbreaker.StateOpen
	BreakerHalfOpen = platformbreaker.StateHalfOpen
)

// BreakerPolicy bounds one per-registration circuit breaker. The window and
// minimum sample count gate the failure ratio; an open breaker admits at most
// HalfOpenProbes in-flight probes after OpenDuration before closing or
// reopening.
type BreakerPolicy platformbreaker.Policy

// DefaultBreakerPolicy returns the selected bounded baseline policy.
func DefaultBreakerPolicy() BreakerPolicy {
	return BreakerPolicy(platformbreaker.DefaultPolicy())
}

// Validate checks the bounded policy bounds.
func (policy BreakerPolicy) Validate() error {
	if platformbreaker.Policy(policy).Validate() != nil {
		return ErrBreakerInvalid
	}
	return nil
}

// BreakerKey identifies one tenant provider configuration boundary. The
// dispatch envelope carries no registration identifier, so the deployment
// adapter and resolved provider configuration are the stable owned key.
type BreakerKey struct {
	TenantID   string
	AdapterID  string
	ProviderID string
}

// Valid reports whether the key can identify one bounded breaker.
func (key BreakerKey) Valid() bool {
	return key.TenantID != "" && (key.AdapterID != "" || key.ProviderID != "")
}

// BreakerKeyFromRequest derives the bounded breaker key from one dispatch
// envelope without importing tenant registration state.
func BreakerKeyFromRequest(request providerv1.Request) BreakerKey {
	return BreakerKey{TenantID: request.TenantID, AdapterID: request.Adapter.AdapterID, ProviderID: request.ProviderID}
}

// BreakerTransition is one shared bounded state change.
type BreakerTransition = platformbreaker.Transition

// Breaker is the shared circuit used by the provider-specific executor.
type Breaker = platformbreaker.Circuit

// BreakerRegistry is a shared registry keyed by one provider configuration.
type BreakerRegistry = platformbreaker.Registry[BreakerKey]

// BreakerOutcome classifies one terminal dispatch result for the breaker.
type BreakerOutcome int

// Bounded breaker outcome classifications.
const (
	BreakerOutcomeIgnored BreakerOutcome = iota
	BreakerOutcomeSuccess
	BreakerOutcomeFailure
)

// ClassifyBreakerOutcome reports whether one terminal result is an
// availability success or failure. Business and credential rejections never
// open the breaker, and a pending or ambiguous dispatch is never counted.
func ClassifyBreakerOutcome(result providerv1.Result) BreakerOutcome {
	if result.Outcome == providerv1.ResultOutcomeCompleted {
		return BreakerOutcomeSuccess
	}
	if result.Failure == nil {
		return BreakerOutcomeIgnored
	}
	switch result.Failure.Class {
	case providerv1.FailureUnavailable, providerv1.FailureRateLimited, providerv1.FailureDeadline, providerv1.FailureInternal:
		return BreakerOutcomeFailure
	default:
		return BreakerOutcomeIgnored
	}
}

// HealthObserver receives one bounded breaker transition so the owning health
// service can persist and announce it exactly once. A persistence failure is
// reported to the executor, which never fails the dispatch because of it.
type HealthObserver interface {
	ObserveBreaker(context.Context, BreakerKey, BreakerTransition) error
}

// BreakerExecutor fails fast on an open registration breaker and feeds
// classified outcomes back into the bounded breaker. It never converts a
// breaker decision into an identity outcome.
type BreakerExecutor struct {
	inner    providerv1.Executor
	breakers *BreakerRegistry
	observer HealthObserver
	now      func() time.Time
	metrics  Metrics
}

// NewBreakerExecutor composes one fail-fast provider executor.
func NewBreakerExecutor(inner providerv1.Executor, breakers *BreakerRegistry, observer HealthObserver, now func() time.Time) (*BreakerExecutor, error) {
	if inner == nil || breakers == nil || now == nil {
		return nil, ErrBreakerInvalid
	}
	return &BreakerExecutor{inner: inner, breakers: breakers, observer: observer, now: now}, nil
}

// WithMetrics attaches the bounded provider metric receiver.
func (executor *BreakerExecutor) WithMetrics(metrics Metrics) *BreakerExecutor {
	if executor != nil && metrics != nil {
		executor.metrics = metrics
	}
	return executor
}

// Execute dispatches at most once unless the registration breaker is open. A
// fast-fail result is an operational failure with no identity signals.
func (executor *BreakerExecutor) Execute(ctx context.Context, request providerv1.Request) (providerv1.Result, error) {
	if request.Validate() != nil {
		return providerv1.Result{}, ErrRequestUnavailable
	}
	key := BreakerKeyFromRequest(request)
	breaker := executor.breakers.Breaker(key)
	if breaker == nil || !breaker.Allow() {
		executor.observe(request, observability.DispatchFailed, observability.FailureUnavailable)
		return BreakerFailureResult(request, executor.now()), nil
	}
	result, err := executor.inner.Execute(ctx, request)
	if err != nil {
		return result, err
	}
	switch ClassifyBreakerOutcome(result) {
	case BreakerOutcomeSuccess:
		executor.record(key, breaker.Record(true))
	case BreakerOutcomeFailure:
		executor.record(key, breaker.Record(false))
	}
	return result, nil
}

// BreakerFailureResult is the bounded operational result for a refused
// dispatch: an unavailable failure that never carries an identity conclusion.
func BreakerFailureResult(request providerv1.Request, at time.Time) providerv1.Result {
	return providerv1.Result{Contract: request.Contract, AttemptID: request.AttemptID, Outcome: providerv1.ResultOutcomeFailed,
		CompletedAt: at.UTC(), Failure: &providerv1.Failure{Class: providerv1.FailureUnavailable, Code: "provider_circuit_open", Retry: providerv1.RetryBackoff}}
}

func (executor *BreakerExecutor) record(key BreakerKey, transition BreakerTransition) {
	if !transition.Changed || executor.observer == nil {
		return
	}
	// Health persistence is best effort: the dispatch result is already
	// terminal and must not change because continuity storage failed. The
	// bounded health cache still carries the local transition.
	_ = executor.observer.ObserveBreaker(context.Background(), key, transition)
}

func (executor *BreakerExecutor) observe(request providerv1.Request, outcome observability.DispatchOutcome, class observability.FailureClass) {
	if executor.metrics == nil {
		return
	}
	executor.metrics.RecordProviderDispatch(observability.ProviderDispatch{Provider: providerLabel(request), Outcome: outcome, FailureClass: class})
}

// NewBreakerRegistry constructs the bounded breaker set with an explicit limit.
func NewBreakerRegistry(policy BreakerPolicy, now func() time.Time, limit int) (*BreakerRegistry, error) {
	if policy.Validate() != nil {
		return nil, ErrBreakerInvalid
	}
	registry, err := platformbreaker.NewRegistry(
		platformbreaker.Policy(policy),
		now,
		limit,
		func(key BreakerKey) bool { return key.Valid() },
	)
	if err != nil {
		return nil, ErrBreakerInvalid
	}
	return registry, nil
}
