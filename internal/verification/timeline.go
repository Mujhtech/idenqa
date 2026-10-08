package verification

import (
	"github.com/Mujhtech/idenqa/internal/platform/observability"

	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

var journeyEventIDPattern = regexp.MustCompile(`^journey_[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

var journeyEventTypes = []string{
	"screen_viewed", "action_selected", "navigation_back", "capture_started",
	"capture_retried", "capture_accepted", "capture_retake", "recovery_started",
	"processing_started", "completion_shown", "error_shown",
}

var journeyScreens = []string{
	"intro", "country", "notice", "method", "preparation", "capture", "review",
	"recovery", "processing", "completion", "error",
}

var journeyActions = []string{
	"continue", "back", "select_country", "accept_notice", "refuse_notice",
	"select_document", "select_method", "start_capture", "retake",
	"accept_capture", "retry", "submit",
}

// JourneyEventInput is a privacy-safe client interaction. It cannot carry
// arbitrary metadata, subject input, extracted values, or evidence content.
type JourneyEventInput struct {
	EventID, EventType, Screen, Action          string
	RequirementKey, Artefact, AcquisitionMethod string
	Sequence                                    int
	ClientOccurredAt                            time.Time
}

// JourneyEvent is the accepted immutable client event receipt.
type JourneyEvent struct {
	JourneyEventInput
	ReceivedAt time.Time
	Digest     string
}

// TimelineEvent is one normalized event from either Core or the capture client.
type TimelineEvent struct {
	ID, Category, Source, Name string
	Status, Detail             *string
	OccurredAt                 time.Time
	Authoritative              bool
}

// Timeline is a bounded chronological projection.
type Timeline struct {
	Events    []TimelineEvent
	Truncated bool
}

// TimelineRepository records capture journey events and reads verification timelines.
type TimelineRepository interface {
	RecordJourneyEvent(context.Context, CaptureContext, JourneyEvent) (JourneyEvent, error)
	FindTimeline(context.Context, tenant.Scope, id.Verification) (Timeline, error)
}

// TimelineService validates and authorises capture journey timeline operations.
type TimelineService struct {
	tracer observability.Tracer

	repository TimelineRepository
	now        func() time.Time
}

// NewTimelineService constructs a timeline service with an explicit clock.
func NewTimelineService(repository TimelineRepository, now func() time.Time) (*TimelineService, error) {
	if repository == nil || now == nil {
		return nil, errors.New("verification: timeline dependencies are required")
	}
	return &TimelineService{repository: repository, now: now}, nil
}

// Record validates and persists one capture journey event.
func (service *TimelineService) Record(ctx context.Context, authority CaptureContext, input JourneyEventInput) (spanResult0 JourneyEvent, spanErr error) {
	ctx, completeSpan := observability.StartSpan(ctx, service.operationTracer(), "verification.TimelineService.Record")
	defer observability.EndSpan(completeSpan, &spanErr)

	now := service.now().UTC().Truncate(time.Microsecond)
	input.ClientOccurredAt = input.ClientOccurredAt.UTC().Truncate(time.Microsecond)
	if authority.TenantScope().ID().IsZero() || authority.Session().ID().IsZero() || authority.TokenID().IsZero() ||
		!validJourneyEvent(input, now) {
		return JourneyEvent{}, ErrSessionConflict
	}
	canonical, err := json.Marshal(input)
	if err != nil {
		return JourneyEvent{}, err
	}
	sum := sha256.Sum256(canonical)
	event := JourneyEvent{JourneyEventInput: input, ReceivedAt: now, Digest: hex.EncodeToString(sum[:])}
	return service.repository.RecordJourneyEvent(ctx, authority, event)
}

// Find returns a verification timeline after checking read permission.
func (service *TimelineService) Find(ctx context.Context, authority access.Context, identifier id.Verification) (spanResult0 Timeline, spanErr error) {
	ctx, completeSpan := observability.StartSpan(ctx, service.operationTracer(), "verification.TimelineService.Find")
	defer observability.EndSpan(completeSpan, &spanErr)

	if err := authority.Require(access.PermissionVerificationSessionsRead); err != nil {
		return Timeline{}, err
	}
	if identifier.IsZero() {
		return Timeline{}, ErrSessionNotFound
	}
	return service.repository.FindTimeline(ctx, authority.TenantScope(), identifier)
}

func validJourneyEvent(input JourneyEventInput, now time.Time) bool {
	if !journeyEventIDPattern.MatchString(input.EventID) || input.Sequence < 1 || input.Sequence > 10000 ||
		!slices.Contains(journeyEventTypes, input.EventType) || !slices.Contains(journeyScreens, input.Screen) ||
		(input.Action != "" && !slices.Contains(journeyActions, input.Action)) ||
		!utcNonZero(input.ClientOccurredAt) || input.ClientOccurredAt.Before(now.Add(-24*time.Hour)) ||
		input.ClientOccurredAt.After(now.Add(5*time.Minute)) {
		return false
	}
	return (input.RequirementKey == "" || safeExecutionToken(input.RequirementKey, 64)) &&
		(input.Artefact == "" || safeExecutionToken(input.Artefact, 128)) &&
		(input.AcquisitionMethod == "" || safeExecutionToken(input.AcquisitionMethod, 128))
}

// WithTracer injects operation tracing during composition, before concurrent use.
func (service *TimelineService) WithTracer(tracer observability.Tracer) *TimelineService {
	if service != nil {
		service.tracer = tracer
	}
	return service
}

func (service *TimelineService) operationTracer() observability.Tracer {
	if service == nil {
		return nil
	}
	return service.tracer
}
