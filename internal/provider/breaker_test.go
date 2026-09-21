package provider_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/platform/observability"
	"github.com/Mujhtech/idenqa/internal/provider"
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

func breakerPolicyFixture() provider.BreakerPolicy {
	return provider.BreakerPolicy{Window: time.Minute, MinimumSamples: 3, FailureRatio: 0.5, OpenDuration: 30 * time.Second, HalfOpenProbes: 1}
}

func TestBreakerTransitionsClosedOpenHalfOpen(t *testing.T) {
	t.Parallel()
	clock := &stepClock{at: time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)}
	registry, err := provider.NewBreakerRegistry(breakerPolicyFixture(), clock.Now, 8)
	if err != nil {
		t.Fatal(err)
	}
	key := provider.BreakerKey{TenantID: "ten_01K4AR9V8FQ2G7ZXCPNM5T6JWH", AdapterID: "dojah", ProviderID: "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWH"}
	breaker := registry.Breaker(key)
	if breaker.State() != provider.BreakerClosed || !breaker.Allow() {
		t.Fatalf("initial state = %s", breaker.State())
	}
	// Below the minimum sample count the breaker never opens.
	for range 2 {
		if transition := breaker.Record(false); transition.Changed {
			t.Fatalf("breaker opened below minimum samples: %+v", transition)
		}
	}
	if breaker.State() != provider.BreakerClosed {
		t.Fatalf("state below minimum = %s", breaker.State())
	}
	transition := breaker.Record(false)
	if !transition.Changed || transition.From != provider.BreakerClosed || transition.To != provider.BreakerOpen {
		t.Fatalf("open transition = %+v", transition)
	}
	state, exists := registry.State(key)
	if breaker.Allow() || !exists || state != provider.BreakerOpen {
		t.Fatal("open breaker admitted a dispatch")
	}
	// After the open duration one bounded probe is admitted.
	clock.Advance(30 * time.Second)
	if !breaker.Allow() || breaker.Allow() {
		t.Fatal("half-open probe bound is wrong")
	}
	if breaker.State() != provider.BreakerHalfOpen {
		t.Fatalf("state after open duration = %s", breaker.State())
	}
	// A successful probe closes the breaker.
	closed := breaker.Record(true)
	if !closed.Changed || closed.To != provider.BreakerClosed || !breaker.Allow() {
		t.Fatalf("closing transition = %+v state=%s", closed, breaker.State())
	}
}

func TestBreakerHalfOpenFailureReopens(t *testing.T) {
	t.Parallel()
	clock := &stepClock{at: time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)}
	registry, err := provider.NewBreakerRegistry(breakerPolicyFixture(), clock.Now, 8)
	if err != nil {
		t.Fatal(err)
	}
	key := provider.BreakerKey{TenantID: "ten_01K4AR9V8FQ2G7ZXCPNM5T6JWH", AdapterID: "dojah", ProviderID: "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWH"}
	breaker := registry.Breaker(key)
	for range 3 {
		breaker.Record(false)
	}
	clock.Advance(30 * time.Second)
	if !breaker.Allow() {
		t.Fatal("half-open probe refused")
	}
	reopened := breaker.Record(false)
	if !reopened.Changed || reopened.To != provider.BreakerOpen || breaker.Allow() {
		t.Fatalf("reopen transition = %+v state=%s", reopened, breaker.State())
	}
	// The window is pruned: old failures cannot reopen a closed breaker.
	clock.Advance(time.Hour)
	if breaker.State() != provider.BreakerHalfOpen {
		t.Fatalf("stale open state = %s", breaker.State())
	}
	breaker.Record(true)
	if breaker.State() != provider.BreakerClosed {
		t.Fatalf("recovery state = %s", breaker.State())
	}
	breaker.Record(false)
	if transition := breaker.Record(false); transition.Changed {
		t.Fatalf("pruned window reopened early: %+v", transition)
	}
}

func TestBreakerClassifiesOnlyAvailabilityFailures(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		result providerv1.Result
		want   provider.BreakerOutcome
	}{
		{name: "completed", result: providerv1.Result{Outcome: providerv1.ResultOutcomeCompleted}, want: provider.BreakerOutcomeSuccess},
		{name: "unavailable", result: failureResult(providerv1.FailureUnavailable), want: provider.BreakerOutcomeFailure},
		{name: "rate limited", result: failureResult(providerv1.FailureRateLimited), want: provider.BreakerOutcomeFailure},
		{name: "deadline", result: failureResult(providerv1.FailureDeadline), want: provider.BreakerOutcomeFailure},
		{name: "internal", result: failureResult(providerv1.FailureInternal), want: provider.BreakerOutcomeFailure},
		{name: "invalid request", result: failureResult(providerv1.FailureInvalidRequest), want: provider.BreakerOutcomeIgnored},
		{name: "unauthenticated", result: failureResult(providerv1.FailureUnauthenticated), want: provider.BreakerOutcomeIgnored},
		{name: "provider rejected", result: failureResult(providerv1.FailureProviderRejected), want: provider.BreakerOutcomeIgnored},
		{name: "cancelled", result: failureResult(providerv1.FailureCancelled), want: provider.BreakerOutcomeIgnored},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := provider.ClassifyBreakerOutcome(test.result); got != test.want {
				t.Fatalf("ClassifyBreakerOutcome() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestBreakerExecutorFailsFastWithoutIdentityOutcome(t *testing.T) {
	t.Parallel()
	request := runtimeRequest(t)
	clock := &stepClock{at: time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)}
	registry, err := provider.NewBreakerRegistry(breakerPolicyFixture(), clock.Now, 8)
	if err != nil {
		t.Fatal(err)
	}
	inner := &countingExecutor{result: failureResult(providerv1.FailureUnavailable)}
	observer := &recordingObserver{}
	metrics := &recordingProviderMetrics{}
	executor, err := provider.NewBreakerExecutor(inner, registry, observer, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	executor.WithMetrics(metrics)
	for range 3 {
		if _, err := executor.Execute(t.Context(), request); err != nil {
			t.Fatal(err)
		}
	}
	if inner.calls != 3 {
		t.Fatalf("inner calls = %d, want 3", inner.calls)
	}
	result, err := executor.Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if inner.calls != 3 {
		t.Fatalf("open breaker dispatched: calls=%d", inner.calls)
	}
	if result.Outcome != providerv1.ResultOutcomeFailed || result.Failure == nil || result.Failure.Code != "provider_circuit_open" ||
		result.Failure.Class != providerv1.FailureUnavailable || len(result.Signals) != 0 {
		t.Fatalf("fast-fail result = %+v", result)
	}
	if result.ValidateForRequest(request) != nil {
		t.Fatalf("fast-fail result is invalid: %+v", result)
	}
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	if len(metrics.dispatches) != 1 || metrics.dispatches[0].FailureClass != observability.FailureUnavailable {
		t.Fatalf("fast-fail metrics = %+v", metrics.dispatches)
	}
	if len(observer.transitions) != 1 || observer.transitions[0].To != provider.BreakerOpen {
		t.Fatalf("observer transitions = %+v", observer.transitions)
	}
}

func TestBreakerRegistryIsBoundedAndSeeded(t *testing.T) {
	t.Parallel()
	clock := &stepClock{at: time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)}
	registry, err := provider.NewBreakerRegistry(breakerPolicyFixture(), clock.Now, 1)
	if err != nil {
		t.Fatal(err)
	}
	first := provider.BreakerKey{TenantID: "ten_01K4AR9V8FQ2G7ZXCPNM5T6JWH", AdapterID: "dojah", ProviderID: "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWH"}
	second := provider.BreakerKey{TenantID: "ten_01K4AR9V8FQ2G7ZXCPNM5T6JWH", AdapterID: "smileid", ProviderID: "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWK"}
	registry.Breaker(first)
	if _, exists := registry.State(second); exists {
		t.Fatal("unexpected second breaker")
	}
	registry.Breaker(second)
	if _, exists := registry.State(first); exists {
		t.Fatal("bounded registry did not evict")
	}
	at := clock.Now()
	registry.Seed(second, provider.BreakerOpen, at)
	state, exists := registry.State(second)
	if !exists || state != provider.BreakerOpen {
		t.Fatalf("seeded state = %s, %v", state, exists)
	}
	if registry.Breaker(provider.BreakerKey{}) != nil {
		t.Fatal("invalid breaker key created a breaker")
	}
}

type countingExecutor struct {
	mu     sync.Mutex
	calls  int
	result providerv1.Result
}

func (executor *countingExecutor) Execute(context.Context, providerv1.Request) (providerv1.Result, error) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	executor.calls++
	return executor.result, nil
}

type recordingObserver struct {
	transitions []provider.BreakerTransition
}

func (observer *recordingObserver) ObserveBreaker(_ context.Context, _ provider.BreakerKey, transition provider.BreakerTransition) error {
	observer.transitions = append(observer.transitions, transition)
	return nil
}

func failureResult(class providerv1.FailureClass) providerv1.Result {
	return providerv1.Result{Outcome: providerv1.ResultOutcomeFailed, Failure: &providerv1.Failure{Class: class, Code: strings.ToLower(string(class)), Retry: providerv1.RetryNever}}
}
