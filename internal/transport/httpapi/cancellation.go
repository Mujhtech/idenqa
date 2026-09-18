package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/go-chi/chi/v5"
)

// CancellationService is the application-owned tenant and subject stop capability.
type CancellationService interface {
	CancelTenant(context.Context, access.Context, id.Verification, int64, string) (verification.CancellationResult, error)
	CancelSubject(context.Context, verification.CaptureContext, int64, string) (verification.CancellationResult, error)
}

// CancellationRoutes exposes only authorised cancellation, never arbitrary state changes.
type CancellationRoutes struct {
	access  *AccessMiddleware
	capture *CaptureAccessMiddleware
	service CancellationService
	logger  *slog.Logger
}

// NewCancellationRoutes constructs independently authenticated cancellation routes.
func NewCancellationRoutes(accessMiddleware *AccessMiddleware, captureMiddleware *CaptureAccessMiddleware, service CancellationService, logger *slog.Logger) (*CancellationRoutes, error) {
	if accessMiddleware == nil || captureMiddleware == nil || service == nil || logger == nil {
		return nil, errors.New("cancellation route dependencies are required")
	}
	return &CancellationRoutes{access: accessMiddleware, capture: captureMiddleware, service: service, logger: logger}, nil
}

// Register adds tenant and session-bound subject cancellation operations.
func (routes *CancellationRoutes) Register(router chi.Router) {
	router.With(routes.access.Authenticate, routes.access.Require(access.PermissionVerificationSessionsCancel)).Post("/verifications/{verificationID}/cancel", routes.cancel)
	router.With(routes.capture.Authenticate).Post("/capture/cancel", routes.cancel)
}

func (routes *CancellationRoutes) cancel(writer http.ResponseWriter, request *http.Request) {
	key, err := parseIdempotencyKey(request.Header.Values("Idempotency-Key"))
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	body, err := decodeJSONBody[struct {
		ExpectedVersion int64 `json:"expected_version"`
	}](request)
	if err != nil || body.ExpectedVersion < 1 {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	var result verification.CancellationResult
	if actor, ok := AccessContext(request.Context()); ok {
		identifier, parseErr := id.ParseVerification(chi.URLParam(request, "verificationID"))
		if parseErr != nil {
			routes.problem(writer, request, verification.ErrSessionNotFound)
			return
		}
		result, err = routes.service.CancelTenant(request.Context(), actor, identifier, body.ExpectedVersion, key)
	} else if actor, ok := CaptureContext(request.Context()); ok {
		result, err = routes.service.CancelSubject(request.Context(), actor, body.ExpectedVersion, key)
	} else {
		err = access.ErrInvalidCredential
	}
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	if err := respond.JSON(writer, request, http.StatusOK, result); err != nil {
		routes.logger.ErrorContext(request.Context(), "write cancellation response")
	}
}

func (routes *CancellationRoutes) problem(writer http.ResponseWriter, request *http.Request, err error) {
	if errors.Is(err, access.ErrInvalidCaptureToken) {
		err = captureUnauthenticated(err)
	}
	if err := respond.WriteProblem(writer, request, err, requestIDString(request.Context())); err != nil {
		routes.logger.ErrorContext(request.Context(), "write cancellation problem")
	}
}
