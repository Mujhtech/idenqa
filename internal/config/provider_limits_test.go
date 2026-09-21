package config_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/provider"
)

func TestProviderDispatchLimitConfigurationDefaultsAndBounds(t *testing.T) {
	t.Parallel()

	baseline, err := config.ProviderLimitConfiguration{}.DispatchLimit()
	if err != nil || baseline != provider.DefaultLimit() {
		t.Fatalf("unset DispatchLimit() = %+v, %v", baseline, err)
	}
	configured, err := config.ProviderLimitConfiguration{
		ProviderMaxConcurrent: 2, ProviderRateLimit: 30, ProviderRatePeriod: time.Minute,
		ProviderRateBurst: 5, ProviderLeaseTTL: 5 * time.Minute,
	}.DispatchLimit()
	if err != nil {
		t.Fatalf("configured DispatchLimit() error = %v", err)
	}
	if configured.MaximumConcurrent != 2 || configured.RateLimit != 30 || configured.RatePeriod != time.Minute ||
		configured.RateBurst != 5 || configured.LeaseTTL != 5*time.Minute {
		t.Fatalf("configured DispatchLimit() = %+v", configured)
	}
	if _, err := (config.ProviderLimitConfiguration{ProviderMaxConcurrent: -1}).DispatchLimit(); !errors.Is(err, provider.ErrLimitInvalid) {
		t.Fatalf("invalid DispatchLimit() error = %v, want ErrLimitInvalid", err)
	}
}

func TestProviderHealthPolicyDefaultsAndBounds(t *testing.T) {
	t.Parallel()

	baseline, err := config.ProviderHealthConfiguration{}.ProviderHealthPolicy()
	if err != nil || baseline != provider.DefaultHealthPolicy() {
		t.Fatalf("unset ProviderHealthPolicy() = %+v, %v", baseline, err)
	}
	configured, err := config.ProviderHealthConfiguration{
		ProviderHealthWindow: time.Minute, ProviderHealthMinimumSamples: 3,
		ProviderHealthDegradedRatio: 0.25, ProviderHealthNotReadyRatio: 0.75,
		ProviderHealthAsyncBacklog: 8, ProviderHealthStaleAfter: 10 * time.Minute,
		ProviderHealthCacheTTL: 5 * time.Second, ProviderHealthProbeTimeout: time.Second,
		ProviderBreakerWindow: time.Minute, ProviderBreakerMinimumSamples: 3,
		ProviderBreakerFailureRatio: 0.5, ProviderBreakerOpenDuration: 20 * time.Second,
		ProviderBreakerHalfOpenProbes: 2,
	}.ProviderHealthPolicy()
	if err != nil {
		t.Fatalf("configured ProviderHealthPolicy() error = %v", err)
	}
	if configured.MinimumSamples != 3 || configured.NotReadyFailureRatio != 0.75 || configured.Breaker.HalfOpenProbes != 2 {
		t.Fatalf("configured ProviderHealthPolicy() = %+v", configured)
	}
	if _, err := (config.ProviderHealthConfiguration{ProviderHealthWindow: time.Second}).ProviderHealthPolicy(); !errors.Is(err, provider.ErrHealthInvalid) {
		t.Fatalf("invalid ProviderHealthPolicy() error = %v, want ErrHealthInvalid", err)
	}
}
