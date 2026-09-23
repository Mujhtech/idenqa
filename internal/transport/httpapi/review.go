package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/go-chi/chi/v5"
)

// ReviewService is the non-disclosing application capability used by HTTP.
type ReviewService interface {
	FindCase(context.Context, tenant.Scope, review.Actor, id.ReviewCase) (review.Case, error)
	Claim(context.Context, tenant.Scope, review.Actor, id.ReviewCase, int64) (review.Case, error)
	SubmitFinding(context.Context, tenant.Scope, review.Actor, id.ReviewCase, review.Resolution, string, []id.Grant, int64) (review.Case, error)
	Correct(context.Context, tenant.Scope, review.Actor, id.ReviewCase, id.Decision, int64) (review.Case, error)
	RequestAppeal(context.Context, tenant.Scope, review.Actor, id.ReviewCase, time.Time) (review.Appeal, error)
	AssignAppeal(context.Context, tenant.Scope, review.Actor, id.Appeal, int64) (review.Appeal, error)
	ResolveAppeal(context.Context, tenant.Scope, review.Actor, id.Appeal, review.AppealOutcome, string, id.Decision, int64) (review.Appeal, error)
}

// ReviewRoutes exposes safe tenant administration without evidence bytes.
type ReviewRoutes struct {
	handlerBase
	queue     ReviewQueue
	cursors   ProfileCursor
	recapture RecaptureService
	access    *AccessMiddleware
	service   ReviewService
}

// NewReviewRoutes constructs the review HTTP boundary.
func NewReviewRoutes(accessMiddleware *AccessMiddleware, service ReviewService, logger *slog.Logger) (*ReviewRoutes, error) {
	if accessMiddleware == nil || service == nil || logger == nil {
		return nil, errors.New("review route dependencies are required")
	}
	return &ReviewRoutes{
		access:      accessMiddleware,
		service:     service,
		handlerBase: newHandlerBase(logger, "review"),
	}, nil
}

// Register adds authenticated review and appeal routes.
func (routes *ReviewRoutes) Register(router chi.Router) {
	routes.registerAppealLifecycle(router)
	if routes.queue != nil {
		routes.RegisterQueue(router, routes.queue, routes.cursors)
	}
	read := []func(http.Handler) http.Handler{routes.access.Authorize(access.PermissionReviewsRead)}
	write := []func(http.Handler) http.Handler{routes.access.Authorize(access.PermissionReviewsWrite)}
	appeal := []func(http.Handler) http.Handler{routes.access.Authorize(access.PermissionAppealsWrite)}
	if routes.recapture != nil {
		if _, ok := routes.recapture.(recaptureReevaluator); ok {
			router.With(write...).Post("/review-cases/{caseID}/recaptures/reevaluations", routes.reevaluateRecapture)
		}

		if _, ok := routes.recapture.(recaptureAcknowledger); ok {
			router.With(write...).Post("/review-cases/{caseID}/recaptures/acknowledgements", routes.acknowledgeRecapture)
		}
		if _, ok := routes.recapture.(recaptureReader); ok {
			router.With(read...).Get("/review-cases/{caseID}/recaptures", routes.listRecaptures)
		}
		router.With(routes.access.Authorize(access.PermissionReviewsWrite), routes.access.Require(access.PermissionVerificationSessionsCreate)).Post("/review-cases/{caseID}/recaptures", routes.createRecapture)
		if _, ok := routes.recapture.(recaptureRenewer); ok {
			router.With(routes.access.Authorize(access.PermissionReviewsWrite), routes.access.Require(access.PermissionVerificationSessionsCreate)).Post("/review-cases/{caseID}/recaptures/renew", routes.renewRecapture)
		}
	}
	router.With(read...).Get("/review-cases/{caseID}", routes.find)
	router.With(write...).Post("/review-cases/{caseID}/claim", routes.claim)
	router.With(write...).Post("/review-cases/{caseID}/findings", routes.finding)
	router.With(write...).Post("/review-cases/{caseID}/corrections", routes.correct)
	router.With(appeal...).Post("/review-cases/{caseID}/appeals", routes.requestAppeal)
	router.With(appeal...).Post("/appeals/{appealID}/assign", routes.assignAppeal)
	router.With(appeal...).Post("/appeals/{appealID}/resolve", routes.resolveAppeal)
}

type reviewerRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
}

type findingRequest struct {
	Resolution      review.Resolution `json:"resolution"`
	ReasonCode      string            `json:"reason_code"`
	GrantIDs        []string          `json:"grant_ids"`
	ExpectedVersion int64             `json:"expected_version"`
}

type correctionRequest struct {
	SupersedingDecisionID string `json:"superseding_decision_id"`
	ExpectedVersion       int64  `json:"expected_version"`
}
type appealRequest struct {
	Deadline time.Time `json:"deadline"`
}
type appealResolutionRequest struct {
	Outcome               review.AppealOutcome `json:"outcome"`
	ReasonCode            string               `json:"reason_code"`
	SupersedingDecisionID string               `json:"superseding_decision_id"`
	ExpectedVersion       int64                `json:"expected_version"`
}

type reviewResource struct {
	ID                    string           `json:"id"`
	VerificationID        string           `json:"verification_id"`
	ChallengedDecisionID  string           `json:"challenged_decision_id,omitempty"`
	RoutingRequestID      string           `json:"routing_request_id,omitempty"`
	SupersedingDecisionID string           `json:"superseding_decision_id,omitempty"`
	Region                string           `json:"region"`
	Oversight             review.Oversight `json:"oversight"`
	State                 review.CaseState `json:"state"`
	AssignedReviewer      string           `json:"assigned_reviewer,omitempty"`
	FindingCount          int              `json:"finding_count"`
	Version               int64            `json:"version"`
}
type appealResource struct {
	Deadline              time.Time            `json:"deadline"`
	ReasonCode            string               `json:"reason_code,omitempty"`
	SupersedingDecisionID string               `json:"superseding_decision_id,omitempty"`
	ID                    string               `json:"id"`
	CaseID                string               `json:"case_id"`
	State                 review.AppealState   `json:"state"`
	Outcome               review.AppealOutcome `json:"outcome,omitempty"`
	AssignedReviewer      string               `json:"assigned_reviewer,omitempty"`
	Version               int64                `json:"version"`
}

func (routes *ReviewRoutes) authority(request *http.Request) (access.Context, review.Actor, bool) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		return access.Context{}, review.Actor{}, false
	}
	return authority, review.Actor{ID: authority.Principal().KeyID().String()}, true
}
func (routes *ReviewRoutes) caseID(request *http.Request) (id.ReviewCase, error) {
	return id.ParseReviewCase(chi.URLParam(request, "caseID"))
}
func (routes *ReviewRoutes) appealID(request *http.Request) (id.Appeal, error) {
	return id.ParseAppeal(chi.URLParam(request, "appealID"))
}

func (routes *ReviewRoutes) find(writer http.ResponseWriter, request *http.Request) {
	authority, actor, ok := routes.authority(request)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	identifier, err := routes.caseID(request)
	if err != nil {
		routes.problem(writer, request, review.ErrInvalid)
		return
	}
	value, err := routes.service.FindCase(request.Context(), authority.TenantScope(), actor, identifier)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeCase(writer, request, value)
}
func (routes *ReviewRoutes) claim(writer http.ResponseWriter, request *http.Request) {
	authority, actor, ok := routes.authority(request)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	identifier, err := routes.caseID(request)
	if err != nil {
		routes.problem(writer, request, review.ErrInvalid)
		return
	}
	body, err := decodeJSONBody[reviewerRequest](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	value, err := routes.service.Claim(request.Context(), authority.TenantScope(), actor, identifier, body.ExpectedVersion)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeCase(writer, request, value)
}
func (routes *ReviewRoutes) finding(writer http.ResponseWriter, request *http.Request) {
	authority, actor, ok := routes.authority(request)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	identifier, err := routes.caseID(request)
	if err != nil {
		routes.problem(writer, request, review.ErrInvalid)
		return
	}
	body, err := decodeJSONBody[findingRequest](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	grants := make([]id.Grant, 0, len(body.GrantIDs))
	for _, encoded := range body.GrantIDs {
		parsed, parseErr := id.ParseGrant(encoded)
		if parseErr != nil {
			routes.problem(writer, request, review.ErrForbidden)
			return
		}
		grants = append(grants, parsed)
	}
	value, err := routes.service.SubmitFinding(request.Context(), authority.TenantScope(), actor, identifier, body.Resolution, body.ReasonCode, grants, body.ExpectedVersion)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeCase(writer, request, value)
}
func (routes *ReviewRoutes) correct(writer http.ResponseWriter, request *http.Request) {
	authority, actor, ok := routes.authority(request)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	identifier, err := routes.caseID(request)
	if err != nil {
		routes.problem(writer, request, review.ErrInvalid)
		return
	}
	body, err := decodeJSONBody[correctionRequest](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	decision, err := id.ParseDecision(body.SupersedingDecisionID)
	if err != nil {
		routes.problem(writer, request, review.ErrInvalid)
		return
	}
	value, err := routes.service.Correct(request.Context(), authority.TenantScope(), actor, identifier, decision, body.ExpectedVersion)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeCase(writer, request, value)
}
func (routes *ReviewRoutes) requestAppeal(writer http.ResponseWriter, request *http.Request) {
	authority, actor, ok := routes.authority(request)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	identifier, err := routes.caseID(request)
	if err != nil {
		routes.problem(writer, request, review.ErrInvalid)
		return
	}
	body, err := decodeJSONBody[appealRequest](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	var value review.Appeal
	if enhanced, ok := routes.service.(interface {
		RequestAppealWithKey(context.Context, access.Context, id.ReviewCase, time.Time, string) (review.Appeal, error)
	}); ok {
		key, keyErr := parseIdempotencyKey(request.Header.Values("Idempotency-Key"))
		if keyErr != nil {
			routes.problem(writer, request, keyErr)
			return
		}
		value, err = enhanced.RequestAppealWithKey(request.Context(), authority, identifier, body.Deadline, key)
	} else {
		value, err = routes.service.RequestAppeal(request.Context(), authority.TenantScope(), actor, identifier, body.Deadline)
	}
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeAppeal(writer, request, http.StatusCreated, value)
}
func (routes *ReviewRoutes) assignAppeal(writer http.ResponseWriter, request *http.Request) {
	authority, actor, ok := routes.authority(request)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	identifier, err := routes.appealID(request)
	if err != nil {
		routes.problem(writer, request, review.ErrInvalid)
		return
	}
	body, err := decodeJSONBody[reviewerRequest](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	value, err := routes.service.AssignAppeal(request.Context(), authority.TenantScope(), actor, identifier, body.ExpectedVersion)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeAppeal(writer, request, http.StatusOK, value)
}
func (routes *ReviewRoutes) resolveAppeal(writer http.ResponseWriter, request *http.Request) {
	authority, actor, ok := routes.authority(request)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	identifier, err := routes.appealID(request)
	if err != nil {
		routes.problem(writer, request, review.ErrInvalid)
		return
	}
	body, err := decodeJSONBody[appealResolutionRequest](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	var successor id.Decision
	if body.SupersedingDecisionID != "" {
		successor, err = id.ParseDecision(body.SupersedingDecisionID)
		if err != nil {
			routes.problem(writer, request, review.ErrInvalid)
			return
		}
	}
	value, err := routes.service.ResolveAppeal(request.Context(), authority.TenantScope(), actor, identifier, body.Outcome, body.ReasonCode, successor, body.ExpectedVersion)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeAppeal(writer, request, http.StatusOK, value)
}
func (routes *ReviewRoutes) writeCase(writer http.ResponseWriter, request *http.Request, value review.Case) {
	routes.write(writer, request, http.StatusOK, reviewResource{ID: value.ID.String(), VerificationID: value.VerificationID.String(), ChallengedDecisionID: value.ChallengedDecision.String(), RoutingRequestID: value.RoutingRequest.String(), SupersedingDecisionID: value.SupersedesDecision.String(), Region: value.Region, Oversight: value.Oversight, State: value.State, AssignedReviewer: value.AssignedReviewer, FindingCount: len(value.Findings), Version: value.Version})
}
func (routes *ReviewRoutes) writeAppeal(writer http.ResponseWriter, request *http.Request, status int, value review.Appeal) {
	routes.write(writer, request, status, appealResource{Deadline: value.Deadline, ReasonCode: value.ReasonCode, SupersedingDecisionID: nullableDecisionString(value.SupersedingDecision), ID: value.ID.String(), CaseID: value.CaseID.String(), State: value.State, Outcome: value.Outcome, AssignedReviewer: value.AssignedReviewer, Version: value.Version})
}
func (routes *ReviewRoutes) write(writer http.ResponseWriter, request *http.Request, status int, value any) {
	writer.Header().Set("Cache-Control", "no-store")
	if err := respond.JSON(writer, request, status, value); err != nil {
		routes.logger.ErrorContext(request.Context(), "write review response")
	}
}

// WithQueue attaches the bounded review queue reader.
func (routes *ReviewRoutes) WithQueue(queue ReviewQueue, cursors ProfileCursor) *ReviewRoutes {
	result := *routes
	result.queue = queue
	result.cursors = cursors
	return &result
}

func nullableDecisionString(value id.Decision) string {
	if value.IsZero() {
		return ""
	}
	return value.String()
}
