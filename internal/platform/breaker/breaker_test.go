package breaker_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/breaker"
)

type stepClock struct {
	mu sync.Mutex
	at time.Time
}

func (clock *stepClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.at
}

func (clock *stepClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.at = clock.at.Add(duration)
}

func policyFixture() breaker.Policy {
	return breaker.Policy{
		Window:         time.Minute,
		MinimumSamples: 3,
		FailureRatio:   0.5,
		OpenDuration:   30 * time.Second,
		HalfOpenProbes: 1,
	}
}

func TestCircuitTransitionsClosedOpenHalfOpen(t *testing.T) {
	t.Parallel()
	clock := &stepClock{at: time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)}
	registry, err := breaker.NewRegistry(policyFixture(), clock.Now, 8, func(key string) bool { return key != "" })
	if err != nil {
		t.Fatal(err)
	}
	circuit := registry.Breaker("provider:one")
	if circuit.State() != breaker.StateClosed || !circuit.Allow() {
		t.Fatalf("initial state = %s", circuit.State())
	}
	for range 2 {
		if transition := circuit.Record(false); transition.Changed {
			t.Fatalf("circuit opened below minimum samples: %+v", transition)
		}
	}
	transition := circuit.Record(false)
	if !transition.Changed || transition.From != breaker.StateClosed || transition.To != breaker.StateOpen {
		t.Fatalf("open transition = %+v", transition)
	}
	if circuit.Allow() {
		t.Fatal("open circuit admitted an operation")
	}
	clock.Advance(30 * time.Second)
	if !circuit.Allow() || circuit.Allow() {
		t.Fatal("half-open probe bound is wrong")
	}
	closed := circuit.Record(true)
	if !closed.Changed || closed.To != breaker.StateClosed || !circuit.Allow() {
		t.Fatalf("closing transition = %+v state=%s", closed, circuit.State())
	}
}

func TestCircuitHalfOpenFailureReopens(t *testing.T) {
	t.Parallel()
	clock := &stepClock{at: time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)}
	registry, err := breaker.NewRegistry(policyFixture(), clock.Now, 8, func(key string) bool { return key != "" })
	if err != nil {
		t.Fatal(err)
	}
	circuit := registry.Breaker("provider:one")
	for range 3 {
		circuit.Record(false)
	}
	clock.Advance(30 * time.Second)
	if !circuit.Allow() {
		t.Fatal("half-open probe refused")
	}
	reopened := circuit.Record(false)
	if !reopened.Changed || reopened.To != breaker.StateOpen || circuit.Allow() {
		t.Fatalf("reopen transition = %+v state=%s", reopened, circuit.State())
	}
	clock.Advance(time.Hour)
	if circuit.State() != breaker.StateHalfOpen {
		t.Fatalf("stale open state = %s", circuit.State())
	}
	circuit.Record(true)
	circuit.Record(false)
	if transition := circuit.Record(false); transition.Changed {
		t.Fatalf("pruned window reopened early: %+v", transition)
	}
}

func TestRegistryIsBoundedValidatedAndSeeded(t *testing.T) {
	t.Parallel()
	clock := &stepClock{at: time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)}
	registry, err := breaker.NewRegistry(policyFixture(), clock.Now, 1, func(key string) bool { return key != "" })
	if err != nil {
		t.Fatal(err)
	}
	if registry.Breaker("") != nil {
		t.Fatal("invalid key created a circuit")
	}
	if registry.Breaker("first") == nil || registry.Breaker("second") == nil {
		t.Fatal("bounded registry did not create circuits")
	}
	if _, exists := registry.State("first"); exists {
		t.Fatal("old circuit was not evicted")
	}
	at := clock.Now()
	registry.Seed("second", breaker.StateOpen, at)
	if state, exists := registry.State("second"); !exists || state != breaker.StateOpen {
		t.Fatalf("seeded state = %s, exists=%t", state, exists)
	}
}

func TestNewRegistryRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		policy   breaker.Policy
		now      func() time.Time
		limit    int
		validKey func(string) bool
	}{
		{name: "invalid policy", policy: breaker.Policy{}, now: time.Now, limit: 1, validKey: func(string) bool { return true }},
		{name: "missing clock", policy: policyFixture(), limit: 1, validKey: func(string) bool { return true }},
		{name: "invalid limit", policy: policyFixture(), now: time.Now, validKey: func(string) bool { return true }},
		{name: "missing key validator", policy: policyFixture(), now: time.Now, limit: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := breaker.NewRegistry(test.policy, test.now, test.limit, test.validKey)
			if !errors.Is(err, breaker.ErrInvalidConfiguration) {
				t.Fatalf("NewRegistry() error = %v", err)
			}
		})
	}
}
