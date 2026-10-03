package verification

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type timelineRepositoryStub struct {
	recorded JourneyEvent
	calls    int
}

func (repository *timelineRepositoryStub) RecordJourneyEvent(_ context.Context, _ CaptureContext, event JourneyEvent) (JourneyEvent, error) {
	repository.calls++
	repository.recorded = event
	return event, nil
}

func (repository *timelineRepositoryStub) FindTimeline(context.Context, tenant.Scope, id.Verification) (Timeline, error) {
	repository.calls++
	return Timeline{Events: []TimelineEvent{}}, nil
}

func TestTimelineServiceRecordsClosedNormalizedEvent(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 2, 12, 0, 0, 123456789, time.UTC)
	repository := &timelineRepositoryStub{}
	service, err := NewTimelineService(repository, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	tenantID, err := id.ParseTenant("ten_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}
	verificationID, err := id.ParseVerification("ver_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}
	tokenID, err := id.ParseCaptureToken("ctk_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatal(err)
	}
	authority := CaptureContext{scope: scope, session: Session{id: verificationID}, tokenID: tokenID}
	clientTime := now.Add(-time.Second).In(time.FixedZone("WAT", 60*60))

	event, err := service.Record(t.Context(), authority, JourneyEventInput{
		EventID: "journey_71d7207f-6935-4d75-8f30-5bd35c8ee231", EventType: "navigation_back",
		Screen: "preparation", Action: "back", RequirementKey: "document",
		Artefact: "idenqa.artefact.document_front", AcquisitionMethod: "idenqa.method.camera",
		Sequence: 4, ClientOccurredAt: clientTime,
	})
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if repository.calls != 1 || event.ClientOccurredAt.Location() != time.UTC || event.ReceivedAt.Nanosecond() != 123456000 || len(event.Digest) != 64 {
		t.Fatalf("recorded event = %+v, calls = %d", event, repository.calls)
	}
}

func TestTimelineServiceRejectsUnsafeOrUnboundedEvents(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	repository := &timelineRepositoryStub{}
	service, _ := NewTimelineService(repository, func() time.Time { return now })
	tenantID, _ := id.ParseTenant("ten_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	verificationID, _ := id.ParseVerification("ver_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	tokenID, _ := id.ParseCaptureToken("ctk_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	scope, _ := tenant.NewScope(tenantID)
	authority := CaptureContext{scope: scope, session: Session{id: verificationID}, tokenID: tokenID}
	valid := JourneyEventInput{EventID: "journey_71d7207f-6935-4d75-8f30-5bd35c8ee231", EventType: "screen_viewed", Screen: "intro", Sequence: 1, ClientOccurredAt: now}

	for name, mutate := range map[string]func(*JourneyEventInput){
		"malformed id":     func(input *JourneyEventInput) { input.EventID = "journey_------------------------------------" },
		"unknown event":    func(input *JourneyEventInput) { input.EventType = "clicked_dom_selector" },
		"unknown action":   func(input *JourneyEventInput) { input.Action = "typed_name" },
		"future timestamp": func(input *JourneyEventInput) { input.ClientOccurredAt = now.Add(6 * time.Minute) },
		"subject value":    func(input *JourneyEventInput) { input.RequirementKey = "document value" },
	} {
		t.Run(name, func(t *testing.T) {
			input := valid
			mutate(&input)
			if _, err := service.Record(t.Context(), authority, input); !errors.Is(err, ErrSessionConflict) {
				t.Fatalf("Record() error = %v, want ErrSessionConflict", err)
			}
		})
	}
	if repository.calls != 0 {
		t.Fatal("invalid event reached persistence")
	}
}

func TestTimelineServiceRequiresVerificationRead(t *testing.T) {
	t.Parallel()
	repository := &timelineRepositoryStub{}
	service, _ := NewTimelineService(repository, time.Now)
	identifier, _ := id.ParseVerification("ver_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if _, err := service.Find(t.Context(), access.Context{}, identifier); !errors.Is(err, access.ErrInsufficientScope) {
		t.Fatalf("Find() error = %v, want ErrInsufficientScope", err)
	}
	if _, err := service.Find(t.Context(), newProfileServiceAuthority(t, "verification_sessions:read"), identifier); err != nil {
		t.Fatalf("Find() error = %v", err)
	}
}
