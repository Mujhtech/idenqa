package headgate

import (
	"context"
	"testing"

	libheadgate "github.com/mujhtech/headgate/go"
)

type policyStoreStub struct {
	libheadgate.Store
	rates       []libheadgate.RateClassConfig
	concurrency []libheadgate.ConcurrencyLimit
}

func (store *policyStoreStub) UpsertRateClass(
	_ context.Context,
	configuration libheadgate.RateClassConfig,
) error {
	store.rates = append(store.rates, configuration)
	return nil
}

func (store *policyStoreStub) UpsertConcurrencyLimit(
	_ context.Context,
	configuration libheadgate.ConcurrencyLimit,
) error {
	store.concurrency = append(store.concurrency, configuration)
	return nil
}

func TestApplyPoliciesReconcilesEveryQueue(t *testing.T) {
	t.Parallel()
	store := &policyStoreStub{}
	adapter, err := New(store, DefaultConfig("idenqa-test"))
	if err != nil {
		t.Fatal(err)
	}
	configuration := DefaultWorkerConfig()
	if err := adapter.ApplyPolicies(t.Context(), configuration); err != nil {
		t.Fatal(err)
	}
	if len(store.rates) != len(queues) || len(store.concurrency) != len(queues) {
		t.Fatalf("policy writes = %d rates, %d concurrency", len(store.rates), len(store.concurrency))
	}
	for index, queue := range queues {
		policy := configuration.QueuePolicies[queue]
		rate := store.rates[index]
		limit := store.concurrency[index]
		if rate.Name != queue || rate.Limit != policy.RateLimit ||
			rate.WindowMs != policy.RatePeriod.Milliseconds() || rate.Burst != policy.RateBurst {
			t.Fatalf("rate policy[%s] = %+v", queue, rate)
		}
		if limit.Queue != queue || limit.MaxConcurrent != policy.TenantConcurrency ||
			limit.OnSaturated != libheadgate.SaturateQueue {
			t.Fatalf("concurrency policy[%s] = %+v", queue, limit)
		}
	}
}
