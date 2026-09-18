package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/Mujhtech/idenqa/internal/access"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/go-chi/chi/v5"
)

// CaptureOutcomeService is the read-only subject-outcome capability consumed by HTTP.
type CaptureOutcomeService interface {
	Find(context.Context, verification.OutcomeContext) (verification.CaptureOutcome, error)
}

// CaptureOutcomeRoutes exposes the safe authoritative result to Capture Web.
type CaptureOutcomeRoutes struct {
	outcome *OutcomeAccessMiddleware
	service CaptureOutcomeService
	logger  *slog.Logger
}

// NewCaptureOutcomeRoutes constructs the read-only subject-outcome route.
func NewCaptureOutcomeRoutes(
	outcome *OutcomeAccessMiddleware,
	service CaptureOutcomeService,
	logger *slog.Logger,
) (*CaptureOutcomeRoutes, error) {
	if outcome == nil || service == nil || logger == nil {
		return nil, errors.New("capture outcome route dependencies are required")
	}
	return &CaptureOutcomeRoutes{outcome: outcome, service: service, logger: logger}, nil
}

// Register adds the outcome-token-scoped authoritative outcome read.
func (routes *CaptureOutcomeRoutes) Register(router chi.Router) {
	router.With(routes.outcome.Authenticate).Get("/capture/outcome", routes.find)
}

func (routes *CaptureOutcomeRoutes) find(writer http.ResponseWriter, request *http.Request) {
	authority, ok := OutcomeContext(request.Context())
	if !ok {
		writeOutcomeProblem(writer, request, routes.logger, access.ErrInvalidOutcomeToken, true)
		return
	}
	outcome, err := routes.service.Find(request.Context(), authority)
	if err != nil {
		writeOutcomeProblem(writer, request, routes.logger, err, false)
		return
	}
	routes.writeJSON(writer, request, http.StatusOK, openapiv1.CaptureOutcome{
		VerificationID: outcome.VerificationID.String(),
		State:          openapiv1.CaptureOutcomeState(outcome.State),
		SessionVersion: outcome.SessionVersion,
		UpdatedAt:      outcome.UpdatedAt,
	})
}

func (routes *CaptureOutcomeRoutes) writeJSON(
	writer http.ResponseWriter,
	request *http.Request,
	status int,
	value any,
) {
	if err := respond.JSON(writer, request, status, value); err != nil {
		requestID, _ := RequestID(request.Context())
		routes.logger.ErrorContext(
			request.Context(),
			"write capture outcome response",
			"request_id", requestID.String(),
			"error", err,
		)
	}
}
