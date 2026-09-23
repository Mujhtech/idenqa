package httpapi

import (
	"net/http"

	"github.com/Mujhtech/idenqa/internal/verification"
)

// WithDocumentSelection attaches the capture-token document-choice command.
func (routes *VerificationRoutes) WithDocumentSelection(service *verification.DocumentSelectionService) *VerificationRoutes {
	routes.selection = service
	return routes
}

func (routes *VerificationRoutes) selectDocument(writer http.ResponseWriter, request *http.Request) {
	capture, ok := CaptureContext(request.Context())
	if !ok {
		routes.problem(writer, request, accessFailure())
		return
	}
	key, err := parseIdempotencyKey(request.Header.Values("Idempotency-Key"))
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	input, err := decodeJSONBody[verification.DocumentSelectionInput](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	session, err := routes.selection.Select(request.Context(), capture, key, input)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	response, err := routes.sessionResponse(session)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeJSON(writer, request, http.StatusOK, response)
}
