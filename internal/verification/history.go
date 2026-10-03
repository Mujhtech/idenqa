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

// LifecycleOrigin is the authoritative version-one state recorded when a
// verification session is created. Creation predates lifecycle transition
// receipts and is therefore represented separately from Transitions.
type LifecycleOrigin struct {
	State      SessionState
	Version    int64
	OccurredAt time.Time
}

// LifecycleTransition is one immutable, aggregate-ordered state transition.
// Actor identifiers and command digests remain internal operational data.
type LifecycleTransition struct {
	EventID    id.Event
	From       SessionState
	To         SessionState
	Version    int64
	DecisionID id.Decision
	OccurredAt time.Time
}

// LifecycleHistory contains the authoritative creation state and every
// retained post-creation transition in aggregate-version order.
type LifecycleHistory struct {
	Origin      LifecycleOrigin
	Transitions []LifecycleTransition
	Truncated   bool
}

// HistoryRepository owns the tenant-scoped lifecycle-history read.
type HistoryRepository interface {
	FindHistory(context.Context, tenant.Scope, id.Verification) (LifecycleHistory, error)
}

// HistoryService authorises lifecycle-history inspection independently from
// evidence, webhook, and other operational projections.
type HistoryService struct {
	tracer     observability.Tracer
	repository HistoryRepository
}

// NewHistoryService constructs the lifecycle-history application service.
func NewHistoryService(repository HistoryRepository) (*HistoryService, error) {
	if repository == nil {
		return nil, errors.New("verification: history repository is required")
	}
	return &HistoryService{repository: repository}, nil
}

// Find returns the authoritative lifecycle history for one verification.
func (service *HistoryService) Find(ctx context.Context, authority access.Context, identifier id.Verification) (spanResult0 LifecycleHistory, spanErr error) {
	ctx, completeSpan := observability.StartSpan(ctx, service.operationTracer(), "verification.HistoryService.Find")
	defer observability.EndSpan(completeSpan, &spanErr)

	if err := authority.Require(access.PermissionVerificationSessionsRead); err != nil {
		return LifecycleHistory{}, err
	}
	if identifier.IsZero() {
		return LifecycleHistory{}, ErrSessionNotFound
	}
	return service.repository.FindHistory(ctx, authority.TenantScope(), identifier)
}

// WithTracer injects operation tracing during composition, before concurrent use.
func (service *HistoryService) WithTracer(tracer observability.Tracer) *HistoryService {
	if service != nil {
		service.tracer = tracer
	}
	return service
}

func (service *HistoryService) operationTracer() observability.Tracer {
	if service == nil {
		return nil
	}
	return service.tracer
}
