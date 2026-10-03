package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/experience"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/apierror"
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

// VerificationSessionLister is the bounded collection capability consumed by
// the optional list route.
type VerificationSessionLister interface {
	List(context.Context, access.Context, *verification.SessionListPosition, int) (verification.SessionPage, error)
}

// VerificationInspector is the safe operational read capability consumed by
// the optional inspection route.
type VerificationInspector interface {
	Inspect(context.Context, access.Context, id.Verification) (verification.Inspection, error)
}

// VerificationHistoryReader is the authoritative lifecycle-history capability
// consumed by the optional history route.
type VerificationHistoryReader interface {
	Find(context.Context, access.Context, id.Verification) (verification.LifecycleHistory, error)
}

// VerificationSignalReader is the provider-independent observation capability.
type VerificationSignalReader interface {
	Find(context.Context, access.Context, id.Verification) (verification.SignalPage, error)
}

// VerificationTimelineService records closed capture interactions and reads the
// normalized tenant timeline without exposing provider or evidence payloads.
type VerificationTimelineService interface {
	Record(context.Context, verification.CaptureContext, verification.JourneyEventInput) (verification.JourneyEvent, error)
	Find(context.Context, access.Context, id.Verification) (verification.Timeline, error)
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
	handlerBase
	selection *verification.DocumentSelectionService
	access    *AccessMiddleware
	capture   *CaptureAccessMiddleware
	service   VerificationSessionService
	catalog   evidence.Catalog
	decisions VerificationDecisionReader
	cases     VerificationCaseReader
	lister    VerificationSessionLister
	inspector VerificationInspector
	history   VerificationHistoryReader
	signals   VerificationSignalReader
	timeline  VerificationTimelineService
	cursors   ProfileCursor
}

// WithInspection enables the metadata-only verification inspection route.
func (routes *VerificationRoutes) WithInspection(inspector VerificationInspector) *VerificationRoutes {
	routes.inspector = inspector
	return routes
}

// WithHistory enables the authoritative verification lifecycle-history route.
func (routes *VerificationRoutes) WithHistory(history VerificationHistoryReader) *VerificationRoutes {
	routes.history = history
	return routes
}

// WithSignals enables the provider-independent verification signal route.
func (routes *VerificationRoutes) WithSignals(signals VerificationSignalReader) *VerificationRoutes {
	routes.signals = signals
	return routes
}

// WithTimeline enables capture interaction ingestion and unified tenant reads.
func (routes *VerificationRoutes) WithTimeline(timeline VerificationTimelineService) *VerificationRoutes {
	routes.timeline = timeline
	return routes
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
		service: service, catalog: catalog, decisions: decisions, cases: cases, handlerBase: newHandlerBase(logger, "verification"),
	}, nil
}

// WithList enables the bounded verification collection route.
func (routes *VerificationRoutes) WithList(lister VerificationSessionLister, cursors ProfileCursor) *VerificationRoutes {
	routes.lister = lister
	routes.cursors = cursors
	return routes
}

// Register adds tenant session and capture-token bootstrap routes.
func (routes *VerificationRoutes) Register(router chi.Router) {
	if routes.selection != nil {
		router.With(routes.capture.Authenticate).Post("/capture/document-selection", routes.selectDocument)
	}
	router.With(routes.access.Authorize(access.PermissionVerificationSessionsCreate)).Post("/verifications", routes.create)
	if routes.lister != nil && routes.cursors != nil {
		router.With(routes.access.Authorize(access.PermissionVerificationSessionsRead)).Get("/verifications", routes.list)
	}
	router.With(routes.access.Authorize(access.PermissionVerificationSessionsRead)).Get("/verifications/{verificationID}", routes.find)
	if routes.inspector != nil {
		router.With(routes.access.Authorize(access.PermissionVerificationSessionsRead)).Get("/verifications/{verificationID}/inspection", routes.inspect)
	}
	if routes.history != nil {
		router.With(routes.access.Authorize(access.PermissionVerificationSessionsRead)).Get("/verifications/{verificationID}/history", routes.findHistory)
	}
	if routes.signals != nil {
		router.With(routes.access.Authorize(access.PermissionVerificationSessionsRead)).Get("/verifications/{verificationID}/signals", routes.findSignals)
	}
	if routes.timeline != nil {
		router.With(routes.access.Authorize(access.PermissionVerificationSessionsRead)).Get("/verifications/{verificationID}/timeline", routes.findTimeline)
		router.With(routes.capture.Authenticate).Post("/capture/journey-events", routes.recordJourneyEvent)
	}
	router.With(routes.access.Authorize(access.PermissionVerificationSessionsResume)).Post("/verifications/{verificationID}/resume", routes.resume)
	router.With(routes.capture.Authenticate).Get("/capture/session", routes.captureSession)
}

func (routes *VerificationRoutes) findSignals(writer http.ResponseWriter, request *http.Request) {
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
	page, err := routes.signals.Find(request.Context(), authority, identifier)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	response := openapiv1.VerificationSignalPage{Items: make([]openapiv1.VerificationSignal, 0, len(page.Items)), Truncated: page.Truncated}
	for _, item := range page.Items {
		response.Items = append(response.Items, openapiv1.VerificationSignal{
			ID: item.ID, CheckID: item.CheckID, AttemptID: item.AttemptID,
			RunnerKind: openapiv1.VerificationSignalRunnerKind(item.RunnerKind), RunnerID: item.RunnerID,
			RunnerVersion: item.RunnerVersion, ContractMajor: item.ContractMajor, ContractMinor: item.ContractMinor,
			Name: item.Name, Outcome: openapiv1.VerificationSignalOutcome(item.Outcome),
			ReasonCodes: item.ReasonCodes, RecordedAt: item.RecordedAt,
		})
	}
	routes.writeJSON(writer, request, http.StatusOK, response)
}

func (routes *VerificationRoutes) findHistory(writer http.ResponseWriter, request *http.Request) {
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
	history, err := routes.history.Find(request.Context(), authority, identifier)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	response := openapiv1.VerificationLifecycleHistory{
		Origin: openapiv1.VerificationLifecycleOrigin{
			State:   openapiv1.VerificationLifecycleOriginState(history.Origin.State),
			Version: openapiv1.VerificationLifecycleOriginVersion(history.Origin.Version), OccurredAt: history.Origin.OccurredAt,
		},
		Transitions: make([]openapiv1.VerificationLifecycleTransition, 0, len(history.Transitions)),
		Truncated:   history.Truncated,
	}
	for _, transition := range history.Transitions {
		var decisionID *string
		if !transition.DecisionID.IsZero() {
			value := transition.DecisionID.String()
			decisionID = &value
		}
		response.Transitions = append(response.Transitions, openapiv1.VerificationLifecycleTransition{
			EventID:   transition.EventID.String(),
			FromState: openapiv1.VerificationLifecycleTransitionFromState(transition.From),
			ToState:   openapiv1.VerificationLifecycleTransitionToState(transition.To),
			Version:   transition.Version, DecisionID: decisionID, OccurredAt: transition.OccurredAt,
		})
	}
	routes.writeJSON(writer, request, http.StatusOK, response)
}

func (routes *VerificationRoutes) inspect(writer http.ResponseWriter, request *http.Request) {
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
	inspection, err := routes.inspector.Inspect(request.Context(), authority, identifier)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	response := openapiv1.VerificationInspection{
		Evidence:  make([]openapiv1.VerificationInspectionEvidence, 0, len(inspection.Evidence)),
		Checks:    make([]openapiv1.VerificationInspectionCheck, 0, len(inspection.Checks)),
		Attempts:  make([]openapiv1.VerificationInspectionAttempt, 0, len(inspection.Attempts)),
		Decisions: make([]openapiv1.VerificationInspectionDecision, 0, len(inspection.Decisions)),
		Retention: make([]openapiv1.VerificationInspectionRetention, 0, len(inspection.Retention)),
		Webhooks:  make([]openapiv1.VerificationInspectionWebhook, 0, len(inspection.Webhooks)),
		LegalHold: inspection.LegalHold,
	}
	for _, item := range inspection.Evidence {
		response.Evidence = append(response.Evidence, openapiv1.VerificationInspectionEvidence{
			ID: item.ID, RequirementKey: item.RequirementKey, EvidenceType: item.EvidenceType,
			Artefact: item.Artefact, AcquisitionMethod: item.AcquisitionMethod, Assurances: item.Assurances,
			State: openapiv1.VerificationInspectionEvidenceState(item.State), Integrity: openapiv1.VerificationInspectionEvidenceIntegrity(item.Integrity),
			RetentionClass: item.RetentionClass, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		})
	}
	for _, item := range inspection.Checks {
		var outcome *openapiv1.VerificationInspectionCheckOutcome
		if item.Outcome != nil {
			value := openapiv1.VerificationInspectionCheckOutcome(*item.Outcome)
			outcome = &value
		}
		response.Checks = append(response.Checks, openapiv1.VerificationInspectionCheck{ID: item.ID, Name: item.Name, State: item.State, Outcome: outcome, AttemptCount: item.AttemptCount, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt})
	}
	for _, item := range inspection.Attempts {
		var retryDisposition *openapiv1.VerificationInspectionAttemptRetryDisposition
		if item.RetryDisposition != nil {
			value := openapiv1.VerificationInspectionAttemptRetryDisposition(*item.RetryDisposition)
			retryDisposition = &value
		}
		response.Attempts = append(response.Attempts, openapiv1.VerificationInspectionAttempt{
			ID: item.ID, CheckID: item.CheckID, AttemptNumber: item.Number,
			RunnerKind: openapiv1.VerificationInspectionAttemptRunnerKind(item.RunnerKind), RunnerID: item.RunnerID, RunnerVersion: item.RunnerVersion,
			PackageDigest: item.PackageDigest, ContractMajor: item.ContractMajor, ContractMinor: item.ContractMinor,
			RequestDigest: item.RequestDigest, ConfigurationDigest: item.ConfigurationDigest,
			State: openapiv1.VerificationInspectionAttemptState(item.State), StartedAt: item.StartedAt, Deadline: item.Deadline,
			FinishedAt: item.FinishedAt, FailureClass: item.FailureClass, FailureCode: item.FailureCode,
			RetryDisposition: retryDisposition, RetryAfterMilliseconds: item.RetryAfterMilliseconds, ResultDigest: item.ResultDigest,
		})
	}
	for _, item := range inspection.Decisions {
		response.Decisions = append(response.Decisions, openapiv1.VerificationInspectionDecision{
			ID: item.ID, DecisionDigest: item.DecisionDigest, Selected: openapiv1.VerificationInspectionDecisionSelected(item.Selected),
			Outcome: openapiv1.VerificationInspectionDecisionOutcome(item.Outcome), Actor: openapiv1.VerificationInspectionDecisionActor(item.Actor),
			SupersedesID: item.SupersedesID, DecidedAt: item.DecidedAt,
		})
	}
	for _, item := range inspection.Retention {
		response.Retention = append(response.Retention, openapiv1.VerificationInspectionRetention{DataClass: item.DataClass, Region: item.Region, PolicyDigest: item.PolicyDigest, ExpiresAt: item.ExpiresAt})
	}
	for _, item := range inspection.Webhooks {
		var deliveryState *openapiv1.VerificationInspectionWebhookDeliveryState
		if item.DeliveryState != nil {
			value := openapiv1.VerificationInspectionWebhookDeliveryState(*item.DeliveryState)
			deliveryState = &value
		}
		response.Webhooks = append(response.Webhooks, openapiv1.VerificationInspectionWebhook{EventID: item.EventID, EventType: item.EventType, EventState: openapiv1.VerificationInspectionWebhookEventState(item.EventState), DeliveryID: item.DeliveryID, DeliveryState: deliveryState, CreatedAt: item.CreatedAt})
	}
	routes.writeJSON(writer, request, http.StatusOK, response)
}

func (routes *VerificationRoutes) list(writer http.ResponseWriter, request *http.Request) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	limit, encodedCursor, err := parseListQuery(request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	bound := "verification_sessions:list;limit=" + strconv.Itoa(limit)
	var after *verification.SessionListPosition
	if encodedCursor != "" {
		claims, decodeErr := routes.cursors.Decode(encodedCursor, authority.TenantScope().ID(), bound)
		if decodeErr != nil {
			routes.problem(writer, request, invalidRequest(decodeErr))
			return
		}
		var position verification.SessionListPosition
		if decodeErr = decodeStrictJSON(claims.Position, &position); decodeErr != nil {
			routes.problem(writer, request, invalidRequest(decodeErr))
			return
		}
		after = &position
	}
	page, err := routes.lister.List(request.Context(), authority, after, limit)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	response := openapiv1.VerificationSessionList{
		Data: make([]openapiv1.VerificationSession, 0, len(page.Sessions)),
		Page: openapiv1.Page{HasMore: page.Next != nil},
	}
	for _, session := range page.Sessions {
		projected, projectErr := routes.projectedSessionResponse(request.Context(), authority, session)
		if projectErr != nil {
			routes.problem(writer, request, projectErr)
			return
		}
		response.Data = append(response.Data, projected)
	}
	if page.Next != nil {
		position, encodeErr := json.Marshal(page.Next)
		if encodeErr != nil {
			routes.problem(writer, request, encodeErr)
			return
		}
		next, encodeErr := routes.cursors.Encode(authority.TenantScope().ID(), bound, position)
		if encodeErr != nil {
			routes.problem(writer, request, encodeErr)
			return
		}
		response.Page.NextCursor = &next
	}
	routes.writeJSON(writer, request, http.StatusOK, response)
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
		Locale: optionalStringValue(body.Locale), Experience: experienceResolutionRequest(body.Experience),
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
	if selections := session.DocumentSelections(); len(selections) > 0 {
		response.DocumentSelections = &selections
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

func optionalStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func experienceResolutionRequest(value *struct {
	ApplicationID *string `json:"application_id,omitempty"`
	Country       *string `json:"country,omitempty"`
	Origin        *string `json:"origin,omitempty"`
	SdkVersion    *string `json:"sdk_version,omitempty"`
	Workflow      *string `json:"workflow,omitempty"`
}) *experience.ResolutionRequest {
	if value == nil {
		return nil
	}
	return &experience.ResolutionRequest{
		Workflow:      optionalStringValue(value.Workflow),
		Country:       optionalStringValue(value.Country),
		ApplicationID: optionalStringValue(value.ApplicationID),
		Origin:        optionalStringValue(value.Origin),
		SDKVersion:    optionalStringValue(value.SdkVersion),
	}
}
