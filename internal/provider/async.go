package provider

import (
	"context"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/platform/observability"
	"github.com/Mujhtech/idenqa/internal/verification"
)

// AsyncClaim fences one initial submission or status-only operation.
type AsyncClaim struct {
	Initial, Acquired bool
	Fence             int64
	Result            *providerv1.Result
}

// AsyncRepository atomically claims dispatch and bounded polling coordination.
type AsyncRepository interface {
	ClaimAsync(context.Context, providerv1.Request) (AsyncClaim, error)
	SaveProgress(context.Context, providerv1.Request, AsyncClaim, providerv1.Progress) error
}

// AsyncExecutor keeps pending progress outside terminal verification results.
type AsyncExecutor struct {
	repository AsyncRepository
	remote     providerv1.Advancer
	now        func() time.Time
	metrics    Metrics
}

// NewAsyncExecutor composes durable asynchronous submission and recovery.
func NewAsyncExecutor(repository AsyncRepository, remote providerv1.Advancer, now func() time.Time) (*AsyncExecutor, error) {
	if repository == nil || remote == nil || now == nil {
		return nil, ErrRequestUnavailable
	}
	return &AsyncExecutor{repository, remote, now, nil}, nil
}

// WithMetrics attaches the bounded provider dispatch metric receiver.
func (executor *AsyncExecutor) WithMetrics(metrics Metrics) *AsyncExecutor {
	if executor != nil && metrics != nil {
		executor.metrics = metrics
	}
	return executor
}

// Execute submits at most once, then uses status-only recovery on every continuation.
func (executor *AsyncExecutor) Execute(ctx context.Context, request providerv1.Request) (providerv1.Result, error) {
	if request.Validate() != nil || !executor.now().Before(request.Deadline) {
		return providerv1.Result{}, ErrRequestUnavailable
	}
	claim, err := executor.repository.ClaimAsync(ctx, request)
	if err != nil {
		return providerv1.Result{}, err
	}
	if claim.Result != nil {
		executor.observe(request, observability.DispatchRecovered, observability.FailureNone)
		return *claim.Result, nil
	}
	if !claim.Acquired {
		executor.observe(request, observability.DispatchFailed, observability.FailureUnavailable)
		return providerv1.Result{}, ErrDispatchPending
	}
	progress, err := executor.remote.Advance(ctx, request, !claim.Initial)
	if err != nil || progress.ValidateForRequest(request) != nil {
		progress = providerv1.Progress{}
	}
	if progress.Result != nil {
		// The document observation is transient: consume it into bounded Core
		// signals before SaveProgress persists the terminal result.
		consumed, consumeErr := verification.ConsumeProviderDocument(*progress.Result)
		if consumeErr != nil {
			progress = providerv1.Progress{}
		} else {
			progress.Result = &consumed
		}
	}
	// A cancelled RPC may have submitted successfully. Release coordination under
	// a bounded independent context; retain the immutable claim for status recovery.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := executor.repository.SaveProgress(saveCtx, request, claim, progress); err != nil {
		return providerv1.Result{}, err
	}
	if progress.Result == nil {
		return providerv1.Result{}, ErrDispatchPending
	}
	executor.observe(request, providerDispatchOutcome(*progress.Result), providerFailureClass(*progress.Result))
	return *progress.Result, nil
}

func (executor *AsyncExecutor) observe(request providerv1.Request, outcome observability.DispatchOutcome, class observability.FailureClass) {
	if executor.metrics == nil {
		return
	}
	executor.metrics.RecordProviderDispatch(observability.ProviderDispatch{
		Provider:     providerLabel(request),
		Outcome:      outcome,
		FailureClass: class,
	})
}
