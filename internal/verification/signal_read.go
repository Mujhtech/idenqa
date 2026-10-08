package verification

import (
	"github.com/Mujhtech/idenqa/internal/platform/observability"

	"context"
	"errors"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// SignalRead is a provider-independent observation. It contains no provider
// payload, extracted identity value, evidence byte, or assurance assertion.
type SignalRead struct {
	ID, CheckID, AttemptID, RunnerID, RunnerVersion string
	RunnerKind, Name, Outcome                       string
	ContractMajor, ContractMinor                    int
	ReasonCodes                                     []string
	RecordedAt                                      time.Time
}

// SignalPage is a bounded chronological signal projection.
type SignalPage struct {
	Items     []SignalRead
	Truncated bool
}

// SignalRepository owns the tenant-scoped observation read.
type SignalRepository interface {
	Signals(context.Context, tenant.Scope, id.Verification) (SignalPage, error)
}

// SignalService authorises provider-independent observation reads.
type SignalService struct {
	tracer     observability.Tracer
	repository SignalRepository
}

// NewSignalService constructs a service for provider-independent signal reads.
func NewSignalService(repository SignalRepository) (*SignalService, error) {
	if repository == nil {
		return nil, errors.New("verification: signal repository is required")
	}
	return &SignalService{repository: repository}, nil
}

// Find returns a page of verification signals after checking read permission.
func (service *SignalService) Find(ctx context.Context, authority access.Context, identifier id.Verification) (spanResult0 SignalPage, spanErr error) {
	ctx, completeSpan := observability.StartSpan(ctx, service.operationTracer(), "verification.SignalService.Find")
	defer observability.EndSpan(completeSpan, &spanErr)

	if err := authority.Require(access.PermissionVerificationSessionsRead); err != nil {
		return SignalPage{}, err
	}
	if identifier.IsZero() {
		return SignalPage{}, ErrSessionNotFound
	}
	return service.repository.Signals(ctx, authority.TenantScope(), identifier)
}

// WithTracer injects operation tracing during composition, before concurrent use.
func (service *SignalService) WithTracer(tracer observability.Tracer) *SignalService {
	if service != nil {
		service.tracer = tracer
	}
	return service
}

func (service *SignalService) operationTracer() observability.Tracer {
	if service == nil {
		return nil
	}
	return service.tracer
}
