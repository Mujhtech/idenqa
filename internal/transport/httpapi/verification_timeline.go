package httpapi

import (
	"net/http"

	"github.com/Mujhtech/idenqa/internal/access"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/go-chi/chi/v5"
)

func (routes *VerificationRoutes) recordJourneyEvent(writer http.ResponseWriter, request *http.Request) {
	authority, ok := CaptureContext(request.Context())
	if !ok {
		writeCaptureProblem(writer, request, routes.logger, access.ErrInvalidCaptureToken, true)
		return
	}
	body, err := decodeJSONBody[openapiv1.CaptureJourneyEventCreate](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	event, err := routes.timeline.Record(request.Context(), authority, verification.JourneyEventInput{
		EventID: body.EventID, EventType: string(body.EventType), Screen: string(body.Screen),
		Action: enumStringValue(body.Action), RequirementKey: stringValue(body.RequirementKey),
		Artefact: stringValue(body.Artefact), AcquisitionMethod: stringValue(body.AcquisitionMethod),
		Sequence: body.Sequence, ClientOccurredAt: body.ClientOccurredAt,
	})
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeJSON(writer, request, http.StatusAccepted, openapiv1.CaptureJourneyEventReceipt{
		EventID: event.EventID, ReceivedAt: event.ReceivedAt,
	})
}

func (routes *VerificationRoutes) findTimeline(writer http.ResponseWriter, request *http.Request) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	identifier, err := id.ParseVerification(chi.URLParam(request, "verificationID"))
	if err != nil {
		routes.problem(writer, request, verification.ErrSessionNotFound)
		return
	}
	timeline, err := routes.timeline.Find(request.Context(), authority, identifier)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	response := openapiv1.VerificationTimeline{
		Events:    make([]openapiv1.VerificationTimelineEvent, 0, len(timeline.Events)),
		Truncated: timeline.Truncated,
	}
	for _, event := range timeline.Events {
		response.Events = append(response.Events, openapiv1.VerificationTimelineEvent{
			ID: event.ID, Category: openapiv1.VerificationTimelineEventCategory(event.Category),
			Source: openapiv1.VerificationTimelineEventSource(event.Source), Name: event.Name,
			Status: event.Status, Detail: event.Detail, OccurredAt: event.OccurredAt,
			Authoritative: event.Authoritative,
		})
	}
	routes.writeJSON(writer, request, http.StatusOK, response)
}

func enumStringValue[T ~string](value *T) string {
	if value == nil {
		return ""
	}
	return string(*value)
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
