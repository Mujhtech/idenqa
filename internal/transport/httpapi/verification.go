package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/evidence"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/apierror"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/go-chi/chi/v5"
)

// VerificationSessionService is the tenant application capability consumed by routes.
type VerificationSessionService interface {
	Create(
		context.Context,
		access.Context,
		string,
		verification.SessionCreateInput,
	) (verification.CreatedSession, error)
	Find(context.Context, access.Context, id.Verification) (verification.Session, error)
}

// VerificationRoutes adapts tenant verification and capture bootstrap to HTTP.
type VerificationRoutes struct {
	access  *AccessMiddleware
	capture *CaptureAccessMiddleware
	service VerificationSessionService
	catalog evidence.Catalog
	logger  *slog.Logger
}

// NewVerificationRoutes constructs verification and capture routes.
func NewVerificationRoutes(
	accessMiddleware *AccessMiddleware,
	captureMiddleware *CaptureAccessMiddleware,
	service VerificationSessionService,
	catalog evidence.Catalog,
	logger *slog.Logger,
) (*VerificationRoutes, error) {
	if accessMiddleware == nil || captureMiddleware == nil || service == nil ||
		catalog.IsZero() || logger == nil {
		return nil, errors.New("verification route dependencies are required")
	}

	return &VerificationRoutes{
		access: accessMiddleware, capture: captureMiddleware,
		service: service, catalog: catalog, logger: logger,
	}, nil
}

// Register adds tenant session and capture-token bootstrap routes.
func (routes *VerificationRoutes) Register(router chi.Router) {
	router.With(
		routes.access.Authenticate,
		routes.access.Require(access.PermissionVerificationSessionsCreate),
	).Post("/v1/verifications", routes.create)
	router.With(
		routes.access.Authenticate,
		routes.access.Require(access.PermissionVerificationSessionsRead),
	).Get("/v1/verifications/{verificationID}", routes.find)
	router.With(routes.capture.Authenticate).Get("/v1/capture/session", routes.captureSession)
}

func (routes *VerificationRoutes) create(writer http.ResponseWriter, request *http.Request) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)

		return
	}
	key, err := parseIdempotencyKey(request.Header.Values("Idempotency-Key"))
	if err != nil {
		routes.problem(writer, request, err)

		return
	}
	body, err := decodeJSONBody[openapiv1.VerificationCreate](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))

		return
	}
	profileID, err := id.ParseProfile(body.CaptureProfileID)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))

		return
	}
	policyID, err := id.ParsePolicy(body.PolicyID)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))

		return
	}
	verificationTTL, err := optionalSeconds(body.VerificationTTLSeconds)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))

		return
	}
	captureTTL, err := optionalSeconds(body.CaptureTokenTTLSeconds)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))

		return
	}
	created, err := routes.service.Create(request.Context(), authority, key, verification.SessionCreateInput{
		ProfileID: profileID, PolicyID: policyID, VerificationTTL: verificationTTL, CaptureTokenTTL: captureTTL,
	})
	if err != nil {
		routes.problem(writer, request, err)

		return
	}
	response, err := routes.createdResponse(created)
	if err != nil {
		routes.problem(writer, request, err)

		return
	}
	writer.Header().Set("Location", "/v1/verifications/"+created.Session.ID().String())
	routes.writeJSON(writer, request, http.StatusCreated, response)
}

func (routes *VerificationRoutes) find(writer http.ResponseWriter, request *http.Request) {
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
	session, err := routes.service.Find(request.Context(), authority, identifier)
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

func (routes *VerificationRoutes) captureSession(writer http.ResponseWriter, request *http.Request) {
	captureContext, ok := CaptureContext(request.Context())
	if !ok {
		writeCaptureProblem(writer, request, routes.logger, access.ErrInvalidCaptureToken, true)

		return
	}
	response, err := routes.sessionResponse(captureContext.Session())
	if err != nil {
		routes.problem(writer, request, err)

		return
	}
	routes.writeJSON(writer, request, http.StatusOK, response)
}

func (routes *VerificationRoutes) createdResponse(
	created verification.CreatedSession,
) (openapiv1.VerificationCreated, error) {
	session, err := routes.sessionResponse(created.Session)
	if err != nil {
		return openapiv1.VerificationCreated{}, err
	}
	encoded := created.CaptureToken.Reveal()

	return openapiv1.VerificationCreated{Session: session, CaptureToken: &encoded}, nil
}

func (routes *VerificationRoutes) sessionResponse(
	session verification.Session,
) (openapiv1.VerificationSession, error) {
	return verificationSessionResponse(routes.catalog, session)
}

func verificationSessionResponse(catalog evidence.Catalog, session verification.Session) (openapiv1.VerificationSession, error) {
	registry, err := catalog.Resolve(session.Requirements().Registry)
	if err != nil {
		return openapiv1.VerificationSession{}, err
	}
	requirements, err := verification.CanonicalJSON(session.Requirements(), registry)
	if err != nil {
		return openapiv1.VerificationSession{}, err
	}

	return openapiv1.VerificationSession{
		ID: session.ID().String(), State: openapiv1.VerificationSessionState(session.State()),
		Version: session.Version(), ProfileID: session.ProfileID().String(),
		PolicyID:        session.PolicyID().String(),
		Region:          session.Region(),
		ProfileRevision: int(session.ProfileRevision()), ProfileDigest: session.ProfileDigest(),
		Requirements: json.RawMessage(requirements), CreatedAt: session.CreatedAt(),
		UpdatedAt: session.UpdatedAt(), ExpiresAt: session.ExpiresAt(),
	}, nil
}

func optionalSeconds(value *int64) (*time.Duration, error) {
	if value == nil {
		return nil, nil
	}
	if *value <= 0 || *value > math.MaxInt64/int64(time.Second) {
		return nil, errors.New("lifetime seconds must be a positive duration")
	}
	duration := time.Duration(*value) * time.Second

	return &duration, nil
}

func captureUnauthenticated(cause error) error {
	return apierror.New(
		http.StatusUnauthorized,
		apierror.CodeUnauthenticated,
		"Unauthenticated",
		"Authentication is required.",
		cause,
	).WithChallenge(`Bearer realm="idenqa-capture"`)
}

func (routes *VerificationRoutes) problem(writer http.ResponseWriter, request *http.Request, err error) {
	if writeErr := respond.WriteProblem(writer, request, err, requestIDString(request.Context())); writeErr != nil {
		routes.logger.ErrorContext(request.Context(), "write verification problem response")
	}
}

func (routes *VerificationRoutes) writeJSON(
	writer http.ResponseWriter,
	request *http.Request,
	status int,
	value any,
) {
	if err := respond.JSON(writer, request, status, value); err != nil {
		routes.logger.ErrorContext(request.Context(), "write verification response")
	}
}
