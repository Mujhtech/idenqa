package provider_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/platform/observability"
	"github.com/Mujhtech/idenqa/internal/provider"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type recordingLease struct {
	mutex    sync.Mutex
	released int
}

func (lease *recordingLease) Release(context.Context) error {
	lease.mutex.Lock()
	defer lease.mutex.Unlock()
	lease.released++
	return nil
}

type recordingLimiter struct {
	mutex    sync.Mutex
	acquires []string
	leases   []*recordingLease
	err      error
}

func (limiter *recordingLimiter) Acquire(_ context.Context, _ tenant.Scope, key string, _ provider.Limit) (provider.Lease, error) {
	limiter.mutex.Lock()
	defer limiter.mutex.Unlock()
	limiter.acquires = append(limiter.acquires, key)
	if limiter.err != nil {
		return nil, limiter.err
	}
	lease := &recordingLease{}
	limiter.leases = append(limiter.leases, lease)
	return lease, nil
}

func TestLimitedExecutorReleasesOnSuccessAndFailure(t *testing.T) {
	t.Parallel()

	request := runtimeRequest(t)
	limiter := &recordingLimiter{}
	var calls int
	var executeErr error
	inner := executorFunc(func(context.Context, providerv1.Request) (providerv1.Result, error) {
		calls++
		return providerv1.Result{}, executeErr
	})
	executor, err := provider.NewLimitedExecutor(inner, limiter, provider.DefaultLimit())
	if err != nil {
		t.Fatalf("NewLimitedExecutor() error = %v", err)
	}
	if _, err := executor.Execute(t.Context(), request); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	executeErr = errors.New("provider transport failed")
	if _, err := executor.Execute(t.Context(), request); !errors.Is(err, executeErr) {
		t.Fatalf("Execute(failing) error = %v", err)
	}
	limiter.mutex.Lock()
	defer limiter.mutex.Unlock()
	if calls != 2 || len(limiter.leases) != 2 {
		t.Fatalf("calls=%d leases=%d", calls, len(limiter.leases))
	}
	for index, lease := range limiter.leases {
		lease.mutex.Lock()
		released := lease.released
		lease.mutex.Unlock()
		if released != 1 {
			t.Fatalf("lease[%d] released = %d, want 1", index, released)
		}
	}
	if key, ok := provider.LimitKey(request); !ok || limiter.acquires[0] != key {
		t.Fatalf("limit key = %q, %v", limiter.acquires[0], ok)
	}
}

func TestLimitedExecutorThrottlesBeforeDispatch(t *testing.T) {
	t.Parallel()

	request := runtimeRequest(t)
	limiter := &recordingLimiter{err: provider.ErrThrottled}
	metrics := &recordingProviderMetrics{}
	inner := executorFunc(func(context.Context, providerv1.Request) (providerv1.Result, error) {
		t.Fatal("inner executor called after throttle")
		return providerv1.Result{}, nil
	})
	executor, err := provider.NewLimitedExecutor(inner, limiter, provider.DefaultLimit())
	if err != nil {
		t.Fatalf("NewLimitedExecutor() error = %v", err)
	}
	executor.WithMetrics(metrics)
	if _, err := executor.Execute(t.Context(), request); !errors.Is(err, provider.ErrThrottled) {
		t.Fatalf("Execute(throttled) error = %v, want ErrThrottled", err)
	}
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	if len(metrics.throttles) != 1 || metrics.throttles[0].Provider != observability.Provider(request.Adapter.AdapterID) {
		t.Fatalf("throttle metrics = %+v", metrics.throttles)
	}
}

func TestLimitValidationIsBounded(t *testing.T) {
	t.Parallel()

	if provider.DefaultLimit().Validate() != nil {
		t.Fatal("DefaultLimit() is invalid")
	}
	for _, test := range []struct {
		name  string
		limit provider.Limit
	}{
		{name: "zero concurrency", limit: provider.Limit{MaximumConcurrent: 0, RateLimit: 60, RatePeriod: time.Minute, LeaseTTL: time.Minute}},
		{name: "zero rate", limit: provider.Limit{MaximumConcurrent: 4, RateLimit: 0, RatePeriod: time.Minute, LeaseTTL: time.Minute}},
		{name: "rate period too large", limit: provider.Limit{MaximumConcurrent: 4, RateLimit: 60, RatePeriod: 48 * time.Hour, LeaseTTL: time.Minute}},
		{name: "lease too large", limit: provider.Limit{MaximumConcurrent: 4, RateLimit: 60, RatePeriod: time.Minute, LeaseTTL: time.Hour}},
		{name: "concurrency too large", limit: provider.Limit{MaximumConcurrent: 4096, RateLimit: 60, RatePeriod: time.Minute, LeaseTTL: time.Minute}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if !errors.Is(test.limit.Validate(), provider.ErrLimitInvalid) {
				t.Fatalf("Validate() = %v, want ErrLimitInvalid", test.limit.Validate())
			}
		})
	}
	if _, err := provider.NewLimitedExecutor(nil, &recordingLimiter{}, provider.DefaultLimit()); !errors.Is(err, provider.ErrLimitInvalid) {
		t.Fatalf("NewLimitedExecutor(nil inner) error = %v", err)
	}
	if _, err := provider.NewLimitedExecutor(executorFunc(nil), nil, provider.DefaultLimit()); !errors.Is(err, provider.ErrLimitInvalid) {
		t.Fatalf("NewLimitedExecutor(nil limiter) error = %v", err)
	}
}
