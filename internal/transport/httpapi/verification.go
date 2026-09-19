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
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
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
	Resume(context.Context, access.Context, id.Verification, int64, string) (verification.ResumedSession, error)
}

// VerificationDecisionReader is the optional current-decision projection capability.
type VerificationDecisionReader interface {
	FindLatest(context.Context, access.Context, id.Verification) (policy.ReproductionReport, error)
}

// VerificationCaseReader is the optional current-case projection capability.
type VerificationCaseReader interface {
	FindCaseForVerification(context.Context, tenant.Scope, review.Actor, id.Verification) (review.Case, error)
}

// VerificationRoutes adapts tenant verification and capture bootstrap to HTTP.
type VerificationRoutes struct {
	access    *AccessMiddleware
	capture   *CaptureAccessMiddleware
	service   VerificationSessionService
	catalog   evidence.Catalog
	decisions VerificationDecisionReader
	cases     VerificationCaseReader
	logger    *slog.Logger
}

// NewVerificationRoutes constructs verification and capture routes. Nil decision
// or case readers skip their optional projections on tenant-authenticated reads.
func NewVerificationRoutes(
	accessMiddleware *AccessMiddleware,
	captureMiddleware *CaptureAccessMiddleware,
	service VerificationSessionService,
	catalog evidence.Catalog,
	decisions VerificationDecisionReader,
	cases VerificationCaseReader,
	logger *slog.Logger,
) (*VerificationRoutes, error) {
	if accessMiddleware == nil || captureMiddleware == nil || service == nil ||
		catalog.IsZero() || logger == nil {
		return nil, errors.New("verification route dependencies are required")
	}

	return &VerificationRoutes{
		access: accessMiddleware, capture: captureMiddleware,
		service: service, catalog: catalog, decisions: decisions, cases: cases, logger: logger,
	}, nil
}

// Register adds tenant session and capture-token bootstrap routes.
func (routes *VerificationRoutes) Register(router chi.Router) {
	router.With(
		routes.access.Authenticate,
		routes.access.Require(access.PermissionVerificationSessionsCreate),
	).Post("/verifications", routes.create)
	router.With(
		routes.access.Authenticate,
		routes.access.Require(access.PermissionVerificationSessionsRead),
	).Get("/verifications/{verificationID}", routes.find)
	router.With(
		routes.access.Authenticate,
		routes.access.Require(access.PermissionVerificationSessionsResume),
	).Post("/verifications/{verificationID}/resume", routes.resume)
	router.With(routes.capture.Authenticate).Get("/capture/session", routes.captureSession)
}

func (routes *VerificationRoutes) resume(writer http.ResponseWriter, request *http.Request) {
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
	key, err := parseIdempotencyKey(request.Header.Values("Idempotency-Key"))
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	body, err := decodeJSONBody[openapiv1.VerificationResume](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	resumed, err := routes.service.Resume(request.Context(), authority, identifier, body.ExpectedVersion, key)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	session, err := routes.projectedSessionResponse(request.Context(), authority, resumed.Session)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	response := openapiv1.VerificationResumed{
		Session: session, CaptureTokenID: resumed.Credential.ID().String(),
		CaptureTokenExpiresAt: resumed.Credential.ExpiresAt(), Replaced: resumed.Replaced,
		Replayed: resumed.Replayed,
	}
	if resumed.Replaced && !resumed.Replayed {
		encoded := resumed.CaptureToken.Reveal()
		response.CaptureToken = &encoded
	}
	routes.writeJSON(writer, request, http.StatusOK, response)
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
	outcomePostTTL, err := optionalSeconds(body.OutcomeTokenPostExpiryTTLSeconds)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))

		return
	}
	created, err := routes.service.Create(request.Context(), authority, key, verification.SessionCreateInput{
		ProfileID: profileID, PolicyID: policyID, VerificationTTL: verificationTTL,
		CaptureTokenTTL: captureTTL, OutcomePostTTL: outcomePostTTL,
	})
	if err != nil {
		routes.problem(writer, request, err)

		return
	}
	session, err := routes.projectedSessionResponse(request.Context(), authority, created.Session)
	if err != nil {
		routes.problem(writer, request, err)

		return
	}
	response := routes.createdResponse(created, session)
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
	response, err := routes.projectedSessionResponse(request.Context(), authority, session)
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
	session openapiv1.VerificationSession,
) openapiv1.VerificationCreated {
	encoded := created.CaptureToken.Reveal()
	outcomeToken := created.OutcomeToken.Reveal()

	return openapiv1.VerificationCreated{
		Session: session, CaptureToken: &encoded, OutcomeToken: &outcomeToken,
		OutcomeTokenExpiresAt: created.OutcomeCredential.ExpiresAt(),
	}
}

func (routes *VerificationRoutes) sessionResponse(
	session verification.Session,
) (openapiv1.VerificationSession, error) {
	return verificationSessionResponse(routes.catalog, session)
}

// projectedSessionResponse adds permission-gated decision and case projections
// to a tenant-authenticated read. Capture-token reads never call it.
func (routes *VerificationRoutes) projectedSessionResponse(
	ctx context.Context,
	authority access.Context,
	session verification.Session,
) (openapiv1.VerificationSession, error) {
	response, err := routes.sessionResponse(session)
	if err != nil {
		return openapiv1.VerificationSession{}, err
	}
	// The input-request projection is added only on the tenant read path. The
	// capture-token read never loads it, so the shared builder stays safe.
	if request, present := session.InputRequest(); present {
		projected := &openapiv1.VerificationInputRequest{
			ReasonCodes: append([]string(nil), request.ReasonCodes()...),
			RequestedAt: request.RequestedAt(),
		}
		if !request.CaseID().IsZero() {
			encoded := request.CaseID().String()
			projected.CaseID = &encoded
		}
		response.RequestedInput = projected
	}
	if routes.decisions != nil && authority.Require(access.PermissionDecisionsRead) == nil {
		report, err := routes.decisions.FindLatest(ctx, authority, session.ID())
		switch {
		case err == nil:
			response.CurrentDecision = &openapiv1.VerificationDecisionReference{
				DecisionID: report.DecisionID,
				Outcome:    openapiv1.PolicyOutcome(report.Outcome),
				Directive:  openapiv1.PolicyDirective(report.Directive),
				DecidedAt:  report.DecidedAt,
			}
		case errors.Is(err, policy.ErrDecisionNotFound):
		default:
			return openapiv1.VerificationSession{}, err
		}
	}
	if routes.cases != nil && authority.Require(access.PermissionReviewsRead) == nil {
		value, err := routes.cases.FindCaseForVerification(
			ctx, authority.TenantScope(), review.Actor{ID: authority.Principal().KeyID().String()}, session.ID(),
		)
		switch {
		case err == nil:
			response.CurrentCase = &openapiv1.VerificationCaseReference{
				CaseID: value.ID.String(), State: string(value.State), Version: value.Version,
			}
		case errors.Is(err, review.ErrCaseNotFound):
		default:
			return openapiv1.VerificationSession{}, err
		}
	}
	return response, nil
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

	response := openapiv1.VerificationSession{
		ID: session.ID().String(), State: openapiv1.VerificationSessionState(session.State()),
		Version: session.Version(), ProfileID: session.ProfileID().String(),
		PolicyID:        session.PolicyID().String(),
		Region:          session.Region(),
		ProfileRevision: int(session.ProfileRevision()), ProfileDigest: session.ProfileDigest(),
		Requirements: json.RawMessage(requirements), CreatedAt: session.CreatedAt(),
		UpdatedAt: session.UpdatedAt(), ExpiresAt: session.ExpiresAt(),
	}
	// The failure projection is carried only when the session store loaded it.
	// Subject-safe capture reads never load it, so the same builder cannot leak
	// operational failure detail through the capture-token projection.
	if failure := session.Failure(); session.State() == verification.SessionStateFailed && failure.Validate() == nil {
		response.Failure = &openapiv1.VerificationFailure{Class: failure.Class, Code: failure.Code}
	}

	return response, nil
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
