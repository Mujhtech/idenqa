package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/provider"
)

// ProviderHealthConfiguration is the additive bounded provider health and
// circuit breaker policy shared by the API and worker processes.
type ProviderHealthConfiguration struct {
	ProviderHealthWindow          time.Duration `envconfig:"PROVIDER_HEALTH_WINDOW" default:"5m"`
	ProviderHealthMinimumSamples  int64         `envconfig:"PROVIDER_HEALTH_MINIMUM_SAMPLES" default:"5"`
	ProviderHealthDegradedRatio   float64       `envconfig:"PROVIDER_HEALTH_DEGRADED_FAILURE_RATIO" default:"0.2"`
	ProviderHealthNotReadyRatio   float64       `envconfig:"PROVIDER_HEALTH_NOT_READY_FAILURE_RATIO" default:"0.5"`
	ProviderHealthAsyncBacklog    int64         `envconfig:"PROVIDER_HEALTH_ASYNC_BACKLOG" default:"16"`
	ProviderHealthStaleAfter      time.Duration `envconfig:"PROVIDER_HEALTH_STALE_AFTER" default:"15m"`
	ProviderHealthCacheTTL        time.Duration `envconfig:"PROVIDER_HEALTH_CACHE_TTL" default:"10s"`
	ProviderHealthProbeTimeout    time.Duration `envconfig:"PROVIDER_HEALTH_PROBE_TIMEOUT" default:"2s"`
	ProviderBreakerWindow         time.Duration `envconfig:"PROVIDER_BREAKER_WINDOW" default:"1m"`
	ProviderBreakerMinimumSamples int64         `envconfig:"PROVIDER_BREAKER_MINIMUM_SAMPLES" default:"4"`
	ProviderBreakerFailureRatio   float64       `envconfig:"PROVIDER_BREAKER_FAILURE_RATIO" default:"0.5"`
	ProviderBreakerOpenDuration   time.Duration `envconfig:"PROVIDER_BREAKER_OPEN_DURATION" default:"30s"`
	ProviderBreakerHalfOpenProbes int64         `envconfig:"PROVIDER_BREAKER_HALF_OPEN_PROBES" default:"1"`
}

// ProviderHealthPolicy validates and projects the configured additive policy.
// An entirely unset configuration (programmatic construction without the
// documented defaults) yields the selected baseline policy; a partially set
// configuration is still validated strictly.
func (configuration ProviderHealthConfiguration) ProviderHealthPolicy() (provider.HealthPolicy, error) {
	if configuration == (ProviderHealthConfiguration{}) {
		return provider.DefaultHealthPolicy(), nil
	}
	policy := provider.HealthPolicy{
		Window:               configuration.ProviderHealthWindow,
		MinimumSamples:       configuration.ProviderHealthMinimumSamples,
		DegradedFailureRatio: configuration.ProviderHealthDegradedRatio,
		NotReadyFailureRatio: configuration.ProviderHealthNotReadyRatio,
		AsyncBacklog:         configuration.ProviderHealthAsyncBacklog,
		StaleAfter:           configuration.ProviderHealthStaleAfter,
		CacheTTL:             configuration.ProviderHealthCacheTTL,
		ProbeTimeout:         configuration.ProviderHealthProbeTimeout,
		Breaker: provider.BreakerPolicy{
			Window:         configuration.ProviderBreakerWindow,
			MinimumSamples: configuration.ProviderBreakerMinimumSamples,
			FailureRatio:   configuration.ProviderBreakerFailureRatio,
			OpenDuration:   configuration.ProviderBreakerOpenDuration,
			HalfOpenProbes: configuration.ProviderBreakerHalfOpenProbes,
		},
	}
	if policy.Validate() != nil || policy.Breaker.Validate() != nil {
		return provider.HealthPolicy{}, provider.ErrHealthInvalid
	}
	return policy, nil
}

// ProviderLimitConfiguration is the additive bounded per-provider dispatch
// admission policy shared by the API and worker processes. Defaults are
// conservative; registrations cannot widen them.
type ProviderLimitConfiguration struct {
	ProviderMaxConcurrent int           `envconfig:"PROVIDER_MAX_CONCURRENT" default:"4"`
	ProviderRateLimit     int64         `envconfig:"PROVIDER_RATE_LIMIT" default:"60"`
	ProviderRatePeriod    time.Duration `envconfig:"PROVIDER_RATE_PERIOD" default:"1m"`
	ProviderRateBurst     int64         `envconfig:"PROVIDER_RATE_BURST" default:"10"`
	ProviderLeaseTTL      time.Duration `envconfig:"PROVIDER_LEASE_TTL" default:"10m"`
}

// DispatchLimit validates and projects the configured admission limit. An
// entirely unset configuration (programmatic construction without the
// documented defaults) yields the selected baseline limit.
func (configuration ProviderLimitConfiguration) DispatchLimit() (provider.Limit, error) {
	if configuration == (ProviderLimitConfiguration{}) {
		return provider.DefaultLimit(), nil
	}
	limit := provider.Limit{
		MaximumConcurrent: configuration.ProviderMaxConcurrent,
		RateLimit:         configuration.ProviderRateLimit,
		RatePeriod:        configuration.ProviderRatePeriod,
		RateBurst:         configuration.ProviderRateBurst,
		LeaseTTL:          configuration.ProviderLeaseTTL,
	}
	if limit.Validate() != nil {
		return provider.Limit{}, provider.ErrLimitInvalid
	}
	return limit, nil
}

// ProviderRuntime is an explicitly mounted first-provider route and private transport.
type ProviderRuntime struct {
	Adapter               string           `json:"adapter,omitempty"`
	Binding               provider.Binding `json:"binding"`
	RunnerAddress         string           `json:"runner_address"`
	RunnerCAFile          string           `json:"runner_ca_file"`
	RunnerServerName      string           `json:"runner_server_name"`
	RunnerCredentialFile  string           `json:"runner_credential_file"`
	GatewayCredentialFile string           `json:"gateway_credential_file"`
}

// LoadProviderRuntime reads closed bounded reference-only operator configuration.
func LoadProviderRuntime(path string) (ProviderRuntime, error) {
	var value ProviderRuntime
	if err := ReadClosedFile(path, &value, 64<<10); err != nil {
		return value, err
	}
	if value.Adapter != "" && value.Adapter != providerv1.AdapterDojah && value.Adapter != providerv1.AdapterSmileID {
		return value, errors.New("unsupported provider runtime adapter")
	}
	if value.RunnerAddress == "" || value.RunnerCAFile == "" || value.RunnerServerName == "" || value.RunnerCredentialFile == "" || value.GatewayCredentialFile == "" {
		return value, errors.New("provider runtime configuration is incomplete")
	}
	return value, nil
}

// ReadClosedFile reads bounded JSON without disclosing its contents in errors.
func ReadClosedFile(path string, target any, limit int64) error {
	file, err := os.Open(path) // #nosec G304 -- operator-mounted configuration or credential path, never request input.
	if err != nil {
		return errors.New("open runtime configuration")
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return errors.New("read bounded runtime configuration")
	}
	defer clear(raw)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return errors.New("invalid runtime configuration")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing runtime configuration")
	}
	return nil
}

// ReadCredentialFile reads a bounded owner-only mounted secret, without environment exposure.
func ReadCredentialFile(path string) (string, error) {
	file, err := os.Open(path) // #nosec G304 -- operator-mounted configuration or credential path, never request input.
	if err != nil {
		return "", errors.New("open mounted credential")
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return "", errors.New("mounted credential must be an owner-only regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(raw) > 4096 {
		return "", errors.New("read mounted credential")
	}
	defer clear(raw)
	value := strings.TrimSpace(string(raw))
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return "", errors.New("invalid mounted credential")
	}
	return value, nil
}
