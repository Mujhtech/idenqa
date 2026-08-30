package headgate

import (
	"context"
	"fmt"

	"github.com/Mujhtech/idenqa/internal/platform/task"
	libheadgate "github.com/mujhtech/headgate/go"
)

type policyStore interface {
	UpsertRateClass(context.Context, libheadgate.RateClassConfig) error
	UpsertConcurrencyLimit(context.Context, libheadgate.ConcurrencyLimit) error
}

// ApplyPolicies idempotently reconciles fleet-wide admission policy before a
// worker may claim work. Every worker in an installation must use the same
// deployment configuration.
func (adapter *Adapter) ApplyPolicies(ctx context.Context, configuration WorkerConfig) error {
	if adapter == nil || adapter.store == nil || configuration.Validate() != nil {
		return fmt.Errorf("%w: headgate admission policy", task.ErrInvalid)
	}
	store, ok := adapter.store.(policyStore)
	if !ok {
		return fmt.Errorf("%w: headgate store lacks policy control", task.ErrInvalid)
	}
	for _, queue := range queues {
		policy := configuration.QueuePolicies[queue]
		if err := store.UpsertRateClass(ctx, libheadgate.RateClassConfig{
			Name: queue, Limit: policy.RateLimit,
			WindowMs: policy.RatePeriod.Milliseconds(), Burst: policy.RateBurst,
		}); err != nil {
			return fmt.Errorf("apply %s Headgate rate policy: %w", queue, classify(err))
		}
		if err := store.UpsertConcurrencyLimit(ctx, libheadgate.ConcurrencyLimit{
			Name: "idenqa-" + queue + "-tenant", Queue: queue,
			PartitionBy: "partition_key", MaxConcurrent: policy.TenantConcurrency,
			OnSaturated: libheadgate.SaturateQueue,
		}); err != nil {
			return fmt.Errorf("apply %s Headgate concurrency policy: %w", queue, classify(err))
		}
	}
	return nil
}
