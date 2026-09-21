package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/platform/observability"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// ErrThrottled identifies bounded provider admission backpressure. It is an
// operational retry condition: the same attempt is retried unchanged and no
// identity outcome is produced.
var ErrThrottled = errors.New("provider: dispatch throttled")

// ErrLimitInvalid identifies an unbounded or inconsistent limiter configuration.
var ErrLimitInvalid = errors.New("provider: invalid dispatch limit")

// Limit bounds concurrent and periodic dispatches for one tenant provider
// configuration boundary. All bounds are conservative deployment settings; a
// registration cannot widen them.
type Limit struct {
	MaximumConcurrent int
	RateLimit         int64
	RatePeriod        time.Duration
	RateBurst         int64
	LeaseTTL          time.Duration
}

// DefaultLimit returns the conservative selected baseline. It admits four
// concurrent dispatches per tenant provider and sixty per minute with a small
// burst while bounding a leaked lease to ten minutes.
func DefaultLimit() Limit {
	return Limit{MaximumConcurrent: 4, RateLimit: 60, RatePeriod: time.Minute, RateBurst: 10, LeaseTTL: 10 * time.Minute}
}

// Validate checks the bounded limit.
func (limit Limit) Validate() error {
	if limit.MaximumConcurrent < 1 || limit.MaximumConcurrent > 1024 ||
		limit.RateLimit < 1 || limit.RateLimit > 1_000_000_000 ||
		limit.RatePeriod < time.Millisecond || limit.RatePeriod > 24*time.Hour ||
		limit.RateBurst < 0 || limit.RateBurst > 1_000_000_000 ||
		limit.LeaseTTL < time.Second || limit.LeaseTTL > 30*time.Minute {
		return ErrLimitInvalid
	}
	return nil
}

// Lease is one admitted dispatch. Release must be idempotent so a failed
// completion path cannot leak admission capacity.
type Lease interface {
	Release(context.Context) error
}

// Limiter admits one dispatch for a bounded key and returns its lease. A
// saturated boundary returns ErrThrottled before any external call.
type Limiter interface {
	Acquire(context.Context, tenant.Scope, string, Limit) (Lease, error)
}

// LimitKey derives the bounded tenant provider boundary key. The deployment
// adapter and resolved provider configuration identify the registration
// boundary without exposing a registration identifier to metrics.
func LimitKey(request providerv1.Request) (string, bool) {
	if request.TenantID == "" || (request.Adapter.AdapterID == "" && request.ProviderID == "") {
		return "", false
	}
	digest := sha256.Sum256([]byte("idenqa.provider.limit\x00" + request.TenantID + "\x00" + request.Adapter.AdapterID + "\x00" + request.ProviderID))
	return hex.EncodeToString(digest[:]), true
}

// LimitedExecutor bounds synchronous dispatch and asynchronous polling through
// one owned limiter. A refusal returns ErrThrottled before the wrapped executor
// performs any external call, so evidence meaning and attempt state are
// unchanged.
type LimitedExecutor struct {
	inner   providerv1.Executor
	limiter Limiter
	limit   Limit
	metrics Metrics
}

// NewLimitedExecutor composes a bounded provider executor.
func NewLimitedExecutor(inner providerv1.Executor, limiter Limiter, limit Limit) (*LimitedExecutor, error) {
	if inner == nil || limiter == nil || limit.Validate() != nil {
		return nil, ErrLimitInvalid
	}
	return &LimitedExecutor{inner: inner, limiter: limiter, limit: limit}, nil
}

// WithLimit overrides the admission limit after validation.
func (executor *LimitedExecutor) WithLimit(limit Limit) *LimitedExecutor {
	if executor != nil && limit.Validate() == nil {
		executor.limit = limit
	}
	return executor
}

// WithMetrics attaches the bounded provider metric receiver.
func (executor *LimitedExecutor) WithMetrics(metrics Metrics) *LimitedExecutor {
	if executor != nil && metrics != nil {
		executor.metrics = metrics
	}
	return executor
}

// Execute admits one dispatch, delegates it, and releases the lease afterwards.
func (executor *LimitedExecutor) Execute(ctx context.Context, request providerv1.Request) (providerv1.Result, error) {
	if executor == nil || executor.inner == nil || executor.limiter == nil {
		return providerv1.Result{}, ErrRequestUnavailable
	}
	key, ok := LimitKey(request)
	if !ok {
		return providerv1.Result{}, ErrRequestUnavailable
	}
	scope, _, err := RequestScope(request)
	if err != nil {
		return providerv1.Result{}, err
	}
	lease, err := executor.limiter.Acquire(ctx, scope, key, executor.limit)
	if err != nil {
		if errors.Is(err, ErrThrottled) {
			executor.recordThrottle(request)
		}
		return providerv1.Result{}, err
	}
	defer func() { _ = lease.Release(context.WithoutCancel(ctx)) }()
	return executor.inner.Execute(ctx, request)
}

func (executor *LimitedExecutor) recordThrottle(request providerv1.Request) {
	if executor.metrics == nil {
		return
	}
	executor.metrics.RecordProviderThrottle(observability.ProviderThrottle{Provider: providerLabel(request)})
}
