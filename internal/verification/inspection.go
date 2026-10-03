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

// EvidenceInspection is safe evidence metadata for tenant operators. It never
// contains object-store references, encryption material, or evidence bytes.
type EvidenceInspection struct {
	ID, RequirementKey, EvidenceType, Artefact, AcquisitionMethod string
	Assurances                                                    []string
	State, Integrity, RetentionClass                              string
	CreatedAt, UpdatedAt                                          time.Time
}

// CheckInspection is the bounded operational state of one verification check.
type CheckInspection struct {
	ID, Name, State string
	Outcome         *string
	AttemptCount    int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// AttemptInspection is reference-only execution history. It excludes provider
// payloads, observations, evidence bytes, credentials, and diagnostic bodies.
type AttemptInspection struct {
	ID, CheckID                                       string
	Number                                            int
	RunnerKind, RunnerID, RunnerVersion               string
	PackageDigest, RequestDigest, ConfigurationDigest string
	ContractMajor, ContractMinor                      int
	State                                             string
	StartedAt, Deadline                               time.Time
	FinishedAt                                        *time.Time
	FailureClass, FailureCode, RetryDisposition       *string
	RetryAfterMilliseconds                            *int64
	ResultDigest                                      *string
}

// DecisionInspection exposes immutable decision lineage without canonical
// policy inputs or subject data.
type DecisionInspection struct {
	ID, DecisionDigest, Selected, Outcome, Actor string
	SupersedesID                                 *string
	DecidedAt                                    time.Time
}

// RetentionInspection describes one immutable retention binding.
type RetentionInspection struct {
	DataClass, Region, PolicyDigest string
	ExpiresAt                       time.Time
}

// WebhookInspection is a safe reference to a webhook event and its delivery.
type WebhookInspection struct {
	EventID, EventType, EventState string
	DeliveryID, DeliveryState      *string
	CreatedAt                      time.Time
}

// Inspection is the tenant-safe operational projection for one verification.
type Inspection struct {
	Evidence  []EvidenceInspection
	Checks    []CheckInspection
	Attempts  []AttemptInspection
	Decisions []DecisionInspection
	Retention []RetentionInspection
	Webhooks  []WebhookInspection
	LegalHold bool
}

// InspectionRepository owns the tenant-scoped operational projection read.
type InspectionRepository interface {
	Inspect(context.Context, tenant.Scope, id.Verification) (Inspection, error)
}

// InspectionService authorises the cross-capability operational projection.
type InspectionService struct {
	tracer     observability.Tracer
	repository InspectionRepository
}

func NewInspectionService(repository InspectionRepository) (*InspectionService, error) {
	if repository == nil {
		return nil, errors.New("verification: inspection repository is required")
	}
	return &InspectionService{repository: repository}, nil
}

// Inspect returns only metadata explicitly safe for tenant operations.
func (service *InspectionService) Inspect(ctx context.Context, authority access.Context, identifier id.Verification) (spanResult0 Inspection, spanErr error) {
	ctx, completeSpan := observability.StartSpan(ctx, service.operationTracer(), "verification.InspectionService.Inspect")
	defer observability.EndSpan(completeSpan, &spanErr)

	for _, permission := range []access.Permission{
		access.PermissionVerificationSessionsRead,
		access.PermissionEvidenceRead,
		access.PermissionWebhooksRead,
	} {
		if err := authority.Require(permission); err != nil {
			return Inspection{}, err
		}
	}
	if identifier.IsZero() {
		return Inspection{}, ErrSessionNotFound
	}
	return service.repository.Inspect(ctx, authority.TenantScope(), identifier)
}

// WithTracer injects operation tracing during composition, before concurrent use.
func (service *InspectionService) WithTracer(tracer observability.Tracer) *InspectionService {
	if service != nil {
		service.tracer = tracer
	}
	return service
}

func (service *InspectionService) operationTracer() observability.Tracer {
	if service == nil {
		return nil
	}
	return service.tracer
}
