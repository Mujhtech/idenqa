package provider

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/platform/observability"
)

// ErrBreakerInvalid identifies an invalid bounded circuit breaker policy.
var ErrBreakerInvalid = errors.New("provider: invalid circuit breaker configuration")

// BreakerState is a bounded circuit breaker state label.
type BreakerState string

// Bounded circuit breaker states.
const (
	BreakerUnknown  BreakerState = "unknown"
	BreakerClosed   BreakerState = "closed"
	BreakerOpen     BreakerState = "open"
	BreakerHalfOpen BreakerState = "half_open"
)

// Safe returns the canonical label, collapsing unknown states.
func (state BreakerState) Safe() string {
	switch state {
	case BreakerClosed, BreakerOpen, BreakerHalfOpen:
		return string(state)
	default:
		return string(BreakerUnknown)
	}
}

// BreakerPolicy bounds one per-registration circuit breaker. The window and
// minimum sample count gate the failure ratio; an open breaker admits at most
// HalfOpenProbes in-flight probes after OpenDuration before closing or
// reopening.
type BreakerPolicy struct {
	Window         time.Duration
	MinimumSamples int64
	FailureRatio   float64
	OpenDuration   time.Duration
	HalfOpenProbes int64
}

// DefaultBreakerPolicy returns the selected bounded baseline policy.
func DefaultBreakerPolicy() BreakerPolicy {
	return BreakerPolicy{Window: time.Minute, MinimumSamples: 4, FailureRatio: 0.5, OpenDuration: 30 * time.Second, HalfOpenProbes: 1}
}

// Validate checks the bounded policy bounds.
func (policy BreakerPolicy) Validate() error {
	if policy.Window < time.Second || policy.Window > time.Hour ||
		policy.MinimumSamples < 1 || policy.MinimumSamples > 1000 ||
		policy.FailureRatio <= 0 || policy.FailureRatio > 1 ||
		policy.OpenDuration < time.Second || policy.OpenDuration > time.Hour ||
		policy.HalfOpenProbes < 1 || policy.HalfOpenProbes > 32 {
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

// BreakerTransition is one bounded state change.
type BreakerTransition struct {
	Key     BreakerKey
	From    BreakerState
	To      BreakerState
	At      time.Time
	Changed bool
}

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

// breakerOutcome is one bounded in-window observation.
type breakerOutcome struct {
	at      time.Time
	success bool
}

// Breaker is one bounded in-process circuit breaker with deterministic clock
// injection. Its window counters are single-instance; the state is exported to
// the persisted health snapshot for cross-process continuity.
type Breaker struct {
	mu               sync.Mutex
	policy           BreakerPolicy
	now              func() time.Time
	state            BreakerState
	openedAt         time.Time
	halfOpenInFlight int64
	outcomes         []breakerOutcome
}

// State returns the current bounded state.
func (breaker *Breaker) State() BreakerState {
	if breaker == nil {
		return BreakerUnknown
	}
	breaker.mu.Lock()
	defer breaker.mu.Unlock()
	breaker.refresh(breaker.now())
	return breaker.state
}

// Allow reports whether one dispatch may proceed. An open breaker admits a
// bounded probe once its open duration has elapsed; every other request fails
// fast until the probe resolves.
func (breaker *Breaker) Allow() bool {
	if breaker == nil {
		return true
	}
	breaker.mu.Lock()
	defer breaker.mu.Unlock()
	now := breaker.now()
	breaker.refresh(now)
	switch breaker.state {
	case BreakerOpen:
		return false
	case BreakerHalfOpen:
		if breaker.halfOpenInFlight >= breaker.policy.HalfOpenProbes {
			return false
		}
		breaker.halfOpenInFlight++
		return true
	default:
		return true
	}
}

// Record applies one classified outcome and returns the bounded transition.
func (breaker *Breaker) Record(success bool) BreakerTransition {
	if breaker == nil {
		return BreakerTransition{}
	}
	breaker.mu.Lock()
	defer breaker.mu.Unlock()
	now := breaker.now()
	breaker.refresh(now)
	transition := BreakerTransition{From: breaker.state, To: breaker.state, At: now.UTC()}
	if breaker.state == BreakerOpen {
		return transition
	}
	if breaker.state == BreakerHalfOpen {
		if breaker.halfOpenInFlight > 0 {
			breaker.halfOpenInFlight--
		}
		if success {
			breaker.state = BreakerClosed
			breaker.openedAt = time.Time{}
			breaker.outcomes = nil
		} else {
			breaker.state = BreakerOpen
			breaker.openedAt = now
		}
		transition.To, transition.Changed = breaker.state, breaker.state != transition.From
		return transition
	}
	breaker.outcomes = append(breaker.outcomes, breakerOutcome{at: now, success: success})
	breaker.prune(now)
	if breaker.shouldOpen() {
		breaker.state = BreakerOpen
		breaker.openedAt = now
		transition.To, transition.Changed = BreakerOpen, true
	}
	return transition
}

// Seed adopts one persisted breaker state when this instance has no stronger
// in-process evidence. It never closes an open breaker earlier than its own
// open duration allows.
func (breaker *Breaker) Seed(state BreakerState, at time.Time) {
	if breaker == nil {
		return
	}
	breaker.mu.Lock()
	defer breaker.mu.Unlock()
	if state != BreakerOpen && state != BreakerHalfOpen {
		return
	}
	now := breaker.now()
	breaker.refresh(now)
	if breaker.state != BreakerOpen && breaker.state != BreakerHalfOpen {
		breaker.state = state
		breaker.openedAt = at
	}
}

func (breaker *Breaker) refresh(now time.Time) {
	breaker.prune(now)
	if breaker.state == BreakerOpen && !breaker.openedAt.IsZero() && now.Sub(breaker.openedAt) >= breaker.policy.OpenDuration {
		breaker.state = BreakerHalfOpen
		breaker.halfOpenInFlight = 0
	}
}

func (breaker *Breaker) prune(now time.Time) {
	cutoff := now.Add(-breaker.policy.Window)
	breaker.outcomes = slices.DeleteFunc(breaker.outcomes, func(outcome breakerOutcome) bool {
		return outcome.at.Before(cutoff)
	})
}

func (breaker *Breaker) shouldOpen() bool {
	samples := int64(len(breaker.outcomes))
	if samples < breaker.policy.MinimumSamples {
		return false
	}
	failures := int64(0)
	for _, outcome := range breaker.outcomes {
		if !outcome.success {
			failures++
		}
	}
	return float64(failures)/float64(samples) >= breaker.policy.FailureRatio
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

// BreakerRegistry owns the bounded set of in-process registration breakers.
type BreakerRegistry struct {
	mu       sync.Mutex
	policy   BreakerPolicy
	now      func() time.Time
	limit    int
	breakers map[BreakerKey]*Breaker
}

// NewBreakerRegistry constructs the bounded breaker set with an explicit limit.
func NewBreakerRegistry(policy BreakerPolicy, now func() time.Time, limit int) (*BreakerRegistry, error) {
	if policy.Validate() != nil || now == nil || limit < 1 || limit > 1_000_000 {
		return nil, ErrBreakerInvalid
	}
	return &BreakerRegistry{policy: policy, now: now, limit: limit, breakers: map[BreakerKey]*Breaker{}}, nil
}

// Breaker returns the breaker for one key, creating it on first use. A full
// registry evicts the least recently observed head. Invalid keys return nil so
// callers fail closed or bypass explicitly.
func (registry *BreakerRegistry) Breaker(key BreakerKey) *Breaker {
	if registry == nil || !key.Valid() {
		return nil
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	now := registry.now()
	if breaker, exists := registry.breakers[key]; exists {
		breaker.mu.Lock()
		breaker.refresh(now)
		breaker.mu.Unlock()
		return breaker
	}
	if len(registry.breakers) >= registry.limit {
		registry.evictOldest(now)
	}
	breaker := &Breaker{policy: registry.policy, now: registry.now, state: BreakerClosed}
	registry.breakers[key] = breaker
	return breaker
}

// State returns the current state for one key without creating a breaker.
func (registry *BreakerRegistry) State(key BreakerKey) (BreakerState, bool) {
	if registry == nil || !key.Valid() {
		return BreakerUnknown, false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	breaker, exists := registry.breakers[key]
	if !exists {
		return BreakerUnknown, false
	}
	breaker.mu.Lock()
	defer breaker.mu.Unlock()
	breaker.refresh(registry.now())
	return breaker.state, true
}

// Seed adopts one persisted breaker state for a key without opening or closing
// any other breaker.
func (registry *BreakerRegistry) Seed(key BreakerKey, state BreakerState, at time.Time) {
	if registry == nil || !key.Valid() || at.IsZero() {
		return
	}
	breaker := registry.Breaker(key)
	if breaker != nil {
		breaker.Seed(state, at)
	}
}

func (registry *BreakerRegistry) evictOldest(now time.Time) {
	var oldestKey BreakerKey
	var oldestAt time.Time
	first := true
	for key, breaker := range registry.breakers {
		breaker.mu.Lock()
		breaker.refresh(now)
		opened := breaker.openedAt
		if opened.IsZero() {
			opened = now.Add(-breaker.policy.Window)
		}
		breaker.mu.Unlock()
		if first || opened.Before(oldestAt) {
			oldestKey, oldestAt, first = key, opened, false
		}
	}
	delete(registry.breakers, oldestKey)
}
