// Package breaker provides bounded in-process circuit breakers for external
// dependencies. Consumers own their keys, failure classification, persistence,
// metrics, and domain-specific fallback behavior.
package breaker

import (
	"errors"
	"slices"
	"sync"
	"time"
)

// ErrInvalidConfiguration identifies invalid circuit or registry bounds.
var ErrInvalidConfiguration = errors.New("breaker: invalid configuration")

// State is a circuit breaker state label.
type State string

// Circuit breaker states.
const (
	StateUnknown  State = "unknown"
	StateClosed   State = "closed"
	StateOpen     State = "open"
	StateHalfOpen State = "half_open"
)

// Safe returns the canonical label, collapsing unknown states.
func (state State) Safe() string {
	switch state {
	case StateClosed, StateOpen, StateHalfOpen:
		return string(state)
	default:
		return string(StateUnknown)
	}
}

// Policy bounds one circuit. The window and minimum sample count gate the
// failure ratio. Once the open duration elapses, at most HalfOpenProbes may be
// in flight until an admitted probe records its outcome.
type Policy struct {
	Window         time.Duration
	MinimumSamples int64
	FailureRatio   float64
	OpenDuration   time.Duration
	HalfOpenProbes int64
}

// DefaultPolicy returns the bounded baseline policy.
func DefaultPolicy() Policy {
	return Policy{
		Window:         time.Minute,
		MinimumSamples: 4,
		FailureRatio:   0.5,
		OpenDuration:   30 * time.Second,
		HalfOpenProbes: 1,
	}
}

// Validate checks the policy bounds.
func (policy Policy) Validate() error {
	if policy.Window < time.Second || policy.Window > time.Hour ||
		policy.MinimumSamples < 1 || policy.MinimumSamples > 1000 ||
		policy.FailureRatio <= 0 || policy.FailureRatio > 1 ||
		policy.OpenDuration < time.Second || policy.OpenDuration > time.Hour ||
		policy.HalfOpenProbes < 1 || policy.HalfOpenProbes > 32 {
		return ErrInvalidConfiguration
	}
	return nil
}

// Transition describes one circuit state change.
type Transition struct {
	From    State
	To      State
	At      time.Time
	Changed bool
}

type outcome struct {
	at      time.Time
	success bool
}

// Circuit is one bounded, concurrent in-process circuit breaker with an
// injected clock. Its owner decides which completed operations count as
// successes or failures.
type Circuit struct {
	mu               sync.Mutex
	policy           Policy
	now              func() time.Time
	state            State
	openedAt         time.Time
	halfOpenInFlight int64
	outcomes         []outcome
}

// State returns the current state.
func (circuit *Circuit) State() State {
	if circuit == nil {
		return StateUnknown
	}
	circuit.mu.Lock()
	defer circuit.mu.Unlock()
	circuit.refresh(circuit.now())
	return circuit.state
}

// Allow reports whether one operation may proceed. An open circuit admits a
// bounded probe once its open duration has elapsed; other operations fail fast
// until an admitted probe records its outcome.
func (circuit *Circuit) Allow() bool {
	if circuit == nil {
		return true
	}
	circuit.mu.Lock()
	defer circuit.mu.Unlock()
	now := circuit.now()
	circuit.refresh(now)
	switch circuit.state {
	case StateOpen:
		return false
	case StateHalfOpen:
		if circuit.halfOpenInFlight >= circuit.policy.HalfOpenProbes {
			return false
		}
		circuit.halfOpenInFlight++
		return true
	default:
		return true
	}
}

// Record applies one classified outcome and returns any state transition.
func (circuit *Circuit) Record(success bool) Transition {
	if circuit == nil {
		return Transition{}
	}
	circuit.mu.Lock()
	defer circuit.mu.Unlock()
	now := circuit.now()
	circuit.refresh(now)
	transition := Transition{From: circuit.state, To: circuit.state, At: now.UTC()}
	if circuit.state == StateOpen {
		return transition
	}
	if circuit.state == StateHalfOpen {
		if circuit.halfOpenInFlight > 0 {
			circuit.halfOpenInFlight--
		}
		if success {
			circuit.state = StateClosed
			circuit.openedAt = time.Time{}
			circuit.outcomes = nil
		} else {
			circuit.state = StateOpen
			circuit.openedAt = now
		}
		transition.To = circuit.state
		transition.Changed = circuit.state != transition.From
		return transition
	}
	circuit.outcomes = append(circuit.outcomes, outcome{at: now, success: success})
	circuit.prune(now)
	if circuit.shouldOpen() {
		circuit.state = StateOpen
		circuit.openedAt = now
		transition.To = StateOpen
		transition.Changed = true
	}
	return transition
}

// Seed adopts a persisted open or half-open state when the circuit has no
// stronger in-process evidence. It never closes an open circuit early.
func (circuit *Circuit) Seed(state State, at time.Time) {
	if circuit == nil {
		return
	}
	circuit.mu.Lock()
	defer circuit.mu.Unlock()
	if state != StateOpen && state != StateHalfOpen {
		return
	}
	now := circuit.now()
	circuit.refresh(now)
	if circuit.state != StateOpen && circuit.state != StateHalfOpen {
		circuit.state = state
		circuit.openedAt = at
	}
}

func (circuit *Circuit) refresh(now time.Time) {
	circuit.prune(now)
	if circuit.state == StateOpen &&
		!circuit.openedAt.IsZero() &&
		now.Sub(circuit.openedAt) >= circuit.policy.OpenDuration {
		circuit.state = StateHalfOpen
		circuit.halfOpenInFlight = 0
	}
}

func (circuit *Circuit) prune(now time.Time) {
	cutoff := now.Add(-circuit.policy.Window)
	circuit.outcomes = slices.DeleteFunc(circuit.outcomes, func(outcome outcome) bool {
		return outcome.at.Before(cutoff)
	})
}

func (circuit *Circuit) shouldOpen() bool {
	samples := int64(len(circuit.outcomes))
	if samples < circuit.policy.MinimumSamples {
		return false
	}
	failures := int64(0)
	for _, outcome := range circuit.outcomes {
		if !outcome.success {
			failures++
		}
	}
	return float64(failures)/float64(samples) >= circuit.policy.FailureRatio
}

// Registry owns a bounded set of keyed in-process circuits. Key validity is
// supplied by the consuming feature so this package remains domain-neutral.
type Registry[K comparable] struct {
	mu       sync.Mutex
	policy   Policy
	now      func() time.Time
	validKey func(K) bool
	limit    int
	circuits map[K]*Circuit
}

// NewRegistry constructs a bounded keyed circuit registry.
func NewRegistry[K comparable](
	policy Policy,
	now func() time.Time,
	limit int,
	validKey func(K) bool,
) (*Registry[K], error) {
	if policy.Validate() != nil || now == nil || validKey == nil || limit < 1 || limit > 1_000_000 {
		return nil, ErrInvalidConfiguration
	}
	return &Registry[K]{
		policy:   policy,
		now:      now,
		validKey: validKey,
		limit:    limit,
		circuits: map[K]*Circuit{},
	}, nil
}

// Breaker returns the circuit for one key, creating it on first use. A full
// registry evicts one oldest circuit. Invalid keys return nil so the consumer
// can choose explicitly whether to fail closed or bypass.
func (registry *Registry[K]) Breaker(key K) *Circuit {
	if registry == nil || !registry.validKey(key) {
		return nil
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	now := registry.now()
	if circuit, exists := registry.circuits[key]; exists {
		circuit.mu.Lock()
		circuit.refresh(now)
		circuit.mu.Unlock()
		return circuit
	}
	if len(registry.circuits) >= registry.limit {
		registry.evictOldest(now)
	}
	circuit := &Circuit{policy: registry.policy, now: registry.now, state: StateClosed}
	registry.circuits[key] = circuit
	return circuit
}

// State returns the current state for one key without creating a circuit.
func (registry *Registry[K]) State(key K) (State, bool) {
	if registry == nil || !registry.validKey(key) {
		return StateUnknown, false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	circuit, exists := registry.circuits[key]
	if !exists {
		return StateUnknown, false
	}
	circuit.mu.Lock()
	defer circuit.mu.Unlock()
	circuit.refresh(registry.now())
	return circuit.state, true
}

// Seed adopts one persisted state for a key without affecting other circuits.
func (registry *Registry[K]) Seed(key K, state State, at time.Time) {
	if registry == nil || !registry.validKey(key) || at.IsZero() {
		return
	}
	circuit := registry.Breaker(key)
	if circuit != nil {
		circuit.Seed(state, at)
	}
}

func (registry *Registry[K]) evictOldest(now time.Time) {
	var oldestKey K
	var oldestAt time.Time
	isFirst := true
	for key, circuit := range registry.circuits {
		circuit.mu.Lock()
		circuit.refresh(now)
		opened := circuit.openedAt
		if opened.IsZero() {
			opened = now.Add(-circuit.policy.Window)
		}
		circuit.mu.Unlock()
		if isFirst || opened.Before(oldestAt) {
			oldestKey = key
			oldestAt = opened
			isFirst = false
		}
	}
	delete(registry.circuits, oldestKey)
}
