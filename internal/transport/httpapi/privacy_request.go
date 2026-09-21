package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/Mujhtech/idenqa/internal/access"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/privacy"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/go-chi/chi/v5"
)

// PrivacyRequestService is the privacy-request administration boundary.
type PrivacyRequestService interface {
	Create(context.Context, tenant.Scope, privacy.Actor, privacy.CreateRequestInput) (privacy.Request, error)
	CreateSubject(context.Context, privacy.SubjectAuthority, privacy.CreateRequestInput) (privacy.SubjectRequest, error)
	Find(context.Context, tenant.Scope, privacy.Actor, id.PrivacyRequest) (privacy.Request, error)
	List(context.Context, tenant.Scope, privacy.Actor, privacy.RequestFilter, string, int) (privacy.RequestPage, error)
	SubjectList(context.Context, privacy.SubjectAuthority, string, int) (privacy.SubjectRequestPage, error)
	Decide(context.Context, tenant.Scope, privacy.Actor, id.PrivacyRequest, privacy.DecisionOutcome, privacy.ReasonCode, int64) (privacy.Request, error)
	Withdraw(context.Context, tenant.Scope, privacy.Actor, id.PrivacyRequest, int64) (privacy.Request, error)
	Execute(context.Context, tenant.Scope, privacy.Actor, id.PrivacyRequest, int64) (privacy.Request, error)
	Restrictions(context.Context, tenant.Scope, privacy.Actor, string, string, int) ([]privacy.Restriction, error)
	LiftRestriction(context.Context, tenant.Scope, privacy.Actor, id.PrivacyRestriction, privacy.RestrictionReason, int64) (privacy.Restriction, error)
	CreateDisclosure(context.Context, tenant.Scope, privacy.Actor, id.PrivacyRequest, string, string, privacy.DisclosureClass, string, string, string) (privacy.Disclosure, error)
	ListDisclosures(context.Context, tenant.Scope, privacy.Actor, id.PrivacyRequest, string, int) ([]privacy.Disclosure, error)
	PutProcessor(context.Context, tenant.Scope, privacy.Actor, id.Processor, int64, string, privacy.ProcessorRole, string, []privacy.DataClass, []string, string) (privacy.Processor, error)
	ListProcessors(context.Context, tenant.Scope, privacy.Actor, string, int) ([]privacy.Processor, error)
	FindProcessor(context.Context, tenant.Scope, privacy.Actor, id.Processor) (privacy.Processor, error)
}

func (routes *PrivacyRoutes) registerRequests(router chi.Router) {
	if routes.requests == nil {
		return
	}
	write := []func(http.Handler) http.Handler{routes.access.Authenticate, routes.access.Require(access.PermissionPrivacyRequestsWrite)}
	read := []func(http.Handler) http.Handler{routes.access.Authenticate, routes.access.Require(access.PermissionPrivacyRequestsRead)}
	approve := []func(http.Handler) http.Handler{routes.access.Authenticate, routes.access.Require(access.PermissionPrivacyRequestsApprove)}
	router.With(write...).Post("/privacy-requests", routes.createRequest)
	router.With(read...).Get("/privacy-requests", routes.listRequests)
	router.With(read...).Get("/privacy-requests/{requestID}", routes.getRequest)
	router.With(approve...).Post("/privacy-requests/{requestID}/approve", routes.decideRequest(true))
	router.With(approve...).Post("/privacy-requests/{requestID}/deny", routes.decideRequest(false))
	router.With(write...).Post("/privacy-requests/{requestID}/withdraw", routes.withdrawRequest)
	router.With(approve...).Post("/privacy-requests/{requestID}/execute", routes.executeRequest)
	router.With(read...).Get("/privacy-restrictions", routes.listRestrictions)
	router.With(approve...).Post("/privacy-restrictions/{restrictionID}/lift", routes.liftRestriction)
	router.With(read...).Get("/privacy-disclosures", routes.listDisclosures)
	router.With(write...).Post("/privacy-disclosures", routes.createDisclosure)
	router.With(read...).Get("/privacy-processors", routes.listProcessors)
	router.With(write...).Post("/privacy-processors", routes.createProcessor)
	router.With(read...).Get("/privacy-processors/{processorID}", routes.getProcessor)
	router.With(write...).Put("/privacy-processors/{processorID}", routes.putProcessor)
	if routes.outcome != nil {
		router.With(routes.outcome.Authenticate).Post("/capture/privacy-requests", routes.createSubjectRequest)
		router.With(routes.outcome.Authenticate).Get("/capture/privacy-requests", routes.listSubjectRequests)
	}
}

func (routes *PrivacyRoutes) requestAuthority(request *http.Request, permissions ...privacy.Permission) (tenant.Scope, privacy.Actor, bool) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		return tenant.Scope{}, privacy.Actor{}, false
	}
	return authority.TenantScope(), privacy.Actor{ID: authority.Principal().KeyID().String(), Permissions: permissions}, true
}

func (routes *PrivacyRoutes) createRequest(writer http.ResponseWriter, request *http.Request) {
	scope, actor, ok := routes.requestAuthority(request, privacy.PermissionWritePrivacyRequests)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	body, err := decodeJSONBody[openapiv1.PrivacyRequestCreate](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	input, err := privacySubjectInput(privacy.RequestType(body.Type), body.Region, derefString(body.SubjectID), derefString(body.Purpose), derefString(body.RecordID), derefString(body.DecisionID), derefString(body.Name), derefString(body.Value), derefTenantKind(body.Kind), derefTenantNormalization(body.Normalization))
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	value, err := routes.requests.Create(request.Context(), scope, actor, input)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	writer.Header().Set("Location", "/v1/privacy-requests/"+value.ID.String())
	routes.write(writer, request, http.StatusAccepted, privacyRequestSummary(value))
}

func (routes *PrivacyRoutes) createSubjectRequest(writer http.ResponseWriter, request *http.Request) {
	authority, ok := OutcomeContext(request.Context())
	if !ok {
		routes.problem(writer, request, access.ErrInvalidOutcomeToken)
		return
	}
	body, err := decodeJSONBody[openapiv1.SubjectPrivacyRequestCreate](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	input, err := privacySubjectInput(privacy.RequestType(body.Type), body.Region, derefString(body.SubjectID), derefString(body.Purpose), derefString(body.RecordID), derefString(body.DecisionID), derefString(body.Name), derefString(body.Value), derefKind(body.Kind), derefNormalization(body.Normalization))
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	projection, err := routes.requests.CreateSubject(request.Context(), privacy.SubjectAuthority{Scope: authority.TenantScope(), VerificationID: authority.VerificationID()}, input)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.write(writer, request, http.StatusAccepted, subjectPrivacyRequest(projection))
}

func (routes *PrivacyRoutes) listRequests(writer http.ResponseWriter, request *http.Request) {
	scope, actor, ok := routes.requestAuthority(request, privacy.PermissionReadPrivacyRequests)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	query := request.URL.Query()
	for name, values := range query {
		switch name {
		case "state", "type", "subject_id", "cursor", "limit":
		default:
			routes.problem(writer, request, invalidRequest(errors.New("unknown query parameter "+strconv.Quote(name))))
			return
		}
		if len(values) != 1 {
			routes.problem(writer, request, invalidRequest(errors.New("query parameter "+strconv.Quote(name)+" must appear once")))
			return
		}
	}
	filter := privacy.RequestFilter{State: privacy.RequestState(query.Get("state")), Type: privacy.RequestType(query.Get("type")), SubjectID: query.Get("subject_id")}
	limit, position, err := routes.privacyPage(request, scope, "privacy:requests;state="+string(filter.State)+";type="+string(filter.Type)+";subject_id="+filter.SubjectID)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	page, err := routes.requests.List(request.Context(), scope, actor, filter, position, limit)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	data := make([]openapiv1.PrivacyRequestSummary, 0, len(page.Requests))
	for _, value := range page.Requests {
		data = append(data, privacyRequestSummary(value))
	}
	result := openapiv1.PrivacyRequestList{Data: data, Page: openapiv1.Page{HasMore: page.HasMore}}
	if page.HasMore {
		next, err := routes.encodePrivacyCursor(scope, "privacy:requests;state="+string(filter.State)+";type="+string(filter.Type)+";subject_id="+filter.SubjectID, page.Requests[len(page.Requests)-1].ID.String())
		if err != nil {
			routes.problem(writer, request, err)
			return
		}
		result.Page.NextCursor = &next
	}
	routes.write(writer, request, http.StatusOK, result)
}

func (routes *PrivacyRoutes) listSubjectRequests(writer http.ResponseWriter, request *http.Request) {
	authority, ok := OutcomeContext(request.Context())
	if !ok {
		routes.problem(writer, request, access.ErrInvalidOutcomeToken)
		return
	}
	limit, position, err := routes.privacyPage(request, authority.TenantScope(), "privacy:subject;verification="+authority.VerificationID().String())
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	page, err := routes.requests.SubjectList(request.Context(), privacy.SubjectAuthority{Scope: authority.TenantScope(), VerificationID: authority.VerificationID()}, position, limit)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	data := make([]openapiv1.SubjectPrivacyRequest, 0, len(page.Requests))
	for _, value := range page.Requests {
		data = append(data, subjectPrivacyRequest(value))
	}
	result := openapiv1.SubjectPrivacyRequestList{Data: data, Page: openapiv1.Page{HasMore: page.HasMore}}
	if page.HasMore {
		next, err := routes.encodePrivacyCursor(authority.TenantScope(), "privacy:subject;verification="+authority.VerificationID().String(), page.NextPosition)
		if err != nil {
			routes.problem(writer, request, err)
			return
		}
		result.Page.NextCursor = &next
	}
	routes.write(writer, request, http.StatusOK, result)
}

func (routes *PrivacyRoutes) getRequest(writer http.ResponseWriter, request *http.Request) {
	scope, actor, ok := routes.requestAuthority(request, privacy.PermissionReadPrivacyRequests)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	identifier, err := id.ParsePrivacyRequest(chi.URLParam(request, "requestID"))
	if err != nil {
		routes.problem(writer, request, privacy.ErrInvalid)
		return
	}
	value, err := routes.requests.Find(request.Context(), scope, actor, identifier)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.write(writer, request, http.StatusOK, privacyRequestStatus(value))
}

func (routes *PrivacyRoutes) decideRequest(approve bool) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		scope, actor, ok := routes.requestAuthority(request, privacy.PermissionApprovePrivacyRequests)
		if !ok {
			routes.problem(writer, request, access.ErrInvalidCredential)
			return
		}
		identifier, err := id.ParsePrivacyRequest(chi.URLParam(request, "requestID"))
		if err != nil {
			routes.problem(writer, request, privacy.ErrInvalid)
			return
		}
		body, err := decodeJSONBody[openapiv1.PrivacyRequestDecision](request)
		if err != nil || body.ExpectedVersion < 1 {
			routes.problem(writer, request, invalidRequest(errors.New("expected_version and reason_code are required")))
			return
		}
		outcome := privacy.OutcomeDenied
		if approve {
			outcome = privacy.OutcomeApproved
			if body.Outcome != nil && *body.Outcome == openapiv1.PrivacyDecisionOutcome("partially_approved") {
				outcome = privacy.OutcomePartiallyApproved
			}
		}
		value, err := routes.requests.Decide(request.Context(), scope, actor, identifier, outcome, privacy.ReasonCode(body.ReasonCode), body.ExpectedVersion)
		if err != nil {
			routes.problem(writer, request, err)
			return
		}
		routes.write(writer, request, http.StatusOK, privacyRequestStatus(value))
	}
}

func (routes *PrivacyRoutes) withdrawRequest(writer http.ResponseWriter, request *http.Request) {
	scope, actor, ok := routes.requestAuthority(request, privacy.PermissionWritePrivacyRequests)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	identifier, err := id.ParsePrivacyRequest(chi.URLParam(request, "requestID"))
	if err != nil {
		routes.problem(writer, request, privacy.ErrInvalid)
		return
	}
	body, err := decodeJSONBody[openapiv1.PrivacyRequestExpectedVersion](request)
	if err != nil || body.ExpectedVersion < 1 {
		routes.problem(writer, request, invalidRequest(errors.New("expected_version is required")))
		return
	}
	value, err := routes.requests.Withdraw(request.Context(), scope, actor, identifier, body.ExpectedVersion)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.write(writer, request, http.StatusOK, privacyRequestStatus(value))
}

func (routes *PrivacyRoutes) executeRequest(writer http.ResponseWriter, request *http.Request) {
	scope, actor, ok := routes.requestAuthority(request, privacy.PermissionApprovePrivacyRequests)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	identifier, err := id.ParsePrivacyRequest(chi.URLParam(request, "requestID"))
	if err != nil {
		routes.problem(writer, request, privacy.ErrInvalid)
		return
	}
	body, err := decodeJSONBody[openapiv1.PrivacyRequestExpectedVersion](request)
	if err != nil || body.ExpectedVersion < 1 {
		routes.problem(writer, request, invalidRequest(errors.New("expected_version is required")))
		return
	}
	value, err := routes.requests.Execute(request.Context(), scope, actor, identifier, body.ExpectedVersion)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.write(writer, request, http.StatusOK, privacyRequestStatus(value))
}

func (routes *PrivacyRoutes) listRestrictions(writer http.ResponseWriter, request *http.Request) {
	scope, actor, ok := routes.requestAuthority(request, privacy.PermissionReadPrivacyRequests)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	query := request.URL.Query()
	for name, values := range query {
		if name != "subject_id" && name != "cursor" && name != "limit" {
			routes.problem(writer, request, invalidRequest(errors.New("unknown query parameter "+strconv.Quote(name))))
			return
		}
		if len(values) != 1 {
			routes.problem(writer, request, invalidRequest(errors.New("query parameter "+strconv.Quote(name)+" must appear once")))
			return
		}
	}
	subjectID := query.Get("subject_id")
	limit, position, err := routes.privacyPage(request, scope, "privacy:restrictions;subject_id="+subjectID)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	values, err := routes.requests.Restrictions(request.Context(), scope, actor, subjectID, position, limit)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	data := make([]openapiv1.PrivacyRestriction, 0, len(values))
	for _, value := range values {
		data = append(data, privacyRestriction(value))
	}
	result := openapiv1.PrivacyRestrictionList{Data: data, Page: openapiv1.Page{}}
	if len(values) == limit {
		next, err := routes.encodePrivacyCursor(scope, "privacy:restrictions;subject_id="+subjectID, values[len(values)-1].ID.String())
		if err != nil {
			routes.problem(writer, request, err)
			return
		}
		result.Page.HasMore, result.Page.NextCursor = true, &next
	}
	routes.write(writer, request, http.StatusOK, result)
}

func (routes *PrivacyRoutes) liftRestriction(writer http.ResponseWriter, request *http.Request) {
	scope, actor, ok := routes.requestAuthority(request, privacy.PermissionApprovePrivacyRequests)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	identifier, err := id.ParsePrivacyRestriction(chi.URLParam(request, "restrictionID"))
	if err != nil {
		routes.problem(writer, request, privacy.ErrInvalid)
		return
	}
	body, err := decodeJSONBody[openapiv1.PrivacyRestrictionLift](request)
	if err != nil || body.ExpectedVersion < 1 {
		routes.problem(writer, request, invalidRequest(errors.New("expected_version and reason_code are required")))
		return
	}
	value, err := routes.requests.LiftRestriction(request.Context(), scope, actor, identifier, privacy.RestrictionReason(body.ReasonCode), body.ExpectedVersion)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.write(writer, request, http.StatusOK, privacyRestriction(value))
}

func (routes *PrivacyRoutes) listDisclosures(writer http.ResponseWriter, request *http.Request) {
	scope, actor, ok := routes.requestAuthority(request, privacy.PermissionReadPrivacyRequests)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	var requestID id.PrivacyRequest
	if encoded := request.URL.Query().Get("request_id"); encoded != "" {
		parsed, err := id.ParsePrivacyRequest(encoded)
		if err != nil {
			routes.problem(writer, request, invalidRequest(errors.New("request_id is invalid")))
			return
		}
		requestID = parsed
	}
	limit, position, err := routes.privacyPage(request, scope, "privacy:disclosures;request_id="+requestID.String())
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	values, err := routes.requests.ListDisclosures(request.Context(), scope, actor, requestID, position, limit)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	data := make([]openapiv1.PrivacyDisclosure, 0, len(values))
	for _, value := range values {
		data = append(data, privacyDisclosure(value))
	}
	result := openapiv1.PrivacyDisclosureList{Data: data, Page: openapiv1.Page{}}
	if len(values) == limit {
		next, err := routes.encodePrivacyCursor(scope, "privacy:disclosures;request_id="+requestID.String(), values[len(values)-1].ID.String())
		if err != nil {
			routes.problem(writer, request, err)
			return
		}
		result.Page.HasMore, result.Page.NextCursor = true, &next
	}
	routes.write(writer, request, http.StatusOK, result)
}

func (routes *PrivacyRoutes) createDisclosure(writer http.ResponseWriter, request *http.Request) {
	scope, actor, ok := routes.requestAuthority(request, privacy.PermissionWritePrivacyRequests)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	body, err := decodeJSONBody[openapiv1.PrivacyDisclosureCreate](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	requestID, err := id.ParsePrivacyRequest(body.RequestID)
	if err != nil {
		routes.problem(writer, request, invalidRequest(errors.New("request_id is invalid")))
		return
	}
	value, err := routes.requests.CreateDisclosure(request.Context(), scope, actor, requestID, body.Recipient, body.Purpose,
		privacy.DisclosureClass(body.DataClass), body.LegalBasis, body.Region, body.Reference)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	writer.Header().Set("Location", "/v1/privacy-disclosures/"+value.ID.String())
	routes.write(writer, request, http.StatusCreated, privacyDisclosure(value))
}

func (routes *PrivacyRoutes) listProcessors(writer http.ResponseWriter, request *http.Request) {
	scope, actor, ok := routes.requestAuthority(request, privacy.PermissionReadPrivacyRequests)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	limit, position, err := routes.privacyPage(request, scope, "privacy:processors")
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	values, err := routes.requests.ListProcessors(request.Context(), scope, actor, position, limit)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	data := make([]openapiv1.PrivacyProcessor, 0, len(values))
	for _, value := range values {
		data = append(data, privacyProcessor(value))
	}
	result := openapiv1.PrivacyProcessorList{Data: data, Page: openapiv1.Page{}}
	if len(values) == limit {
		next, err := routes.encodePrivacyCursor(scope, "privacy:processors", values[len(values)-1].ID.String())
		if err != nil {
			routes.problem(writer, request, err)
			return
		}
		result.Page.HasMore, result.Page.NextCursor = true, &next
	}
	routes.write(writer, request, http.StatusOK, result)
}

func (routes *PrivacyRoutes) createProcessor(writer http.ResponseWriter, request *http.Request) {
	scope, actor, ok := routes.requestAuthority(request, privacy.PermissionWritePrivacyRequests)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	body, err := decodeJSONBody[openapiv1.PrivacyProcessorPut](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	if body.ExpectedVersion != 0 {
		routes.problem(writer, request, invalidRequest(errors.New("expected_version must be zero on create")))
		return
	}
	value, err := privacyPutProcessor(request, routes, scope, actor, id.Processor{}, body)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	writer.Header().Set("Location", "/v1/privacy-processors/"+value.ID.String())
	routes.write(writer, request, http.StatusCreated, privacyProcessor(value))
}

func (routes *PrivacyRoutes) putProcessor(writer http.ResponseWriter, request *http.Request) {
	scope, actor, ok := routes.requestAuthority(request, privacy.PermissionWritePrivacyRequests)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	identifier, err := id.ParseProcessor(chi.URLParam(request, "processorID"))
	if err != nil {
		routes.problem(writer, request, privacy.ErrInvalid)
		return
	}
	body, err := decodeJSONBody[openapiv1.PrivacyProcessorPut](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	value, err := privacyPutProcessor(request, routes, scope, actor, identifier, body)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.write(writer, request, http.StatusOK, privacyProcessor(value))
}

func (routes *PrivacyRoutes) getProcessor(writer http.ResponseWriter, request *http.Request) {
	scope, actor, ok := routes.requestAuthority(request, privacy.PermissionReadPrivacyRequests)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	identifier, err := id.ParseProcessor(chi.URLParam(request, "processorID"))
	if err != nil {
		routes.problem(writer, request, privacy.ErrInvalid)
		return
	}
	value, err := routes.requests.FindProcessor(request.Context(), scope, actor, identifier)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.write(writer, request, http.StatusOK, privacyProcessor(value))
}

func privacyPutProcessor(request *http.Request, routes *PrivacyRoutes, scope tenant.Scope, actor privacy.Actor, identifier id.Processor, body openapiv1.PrivacyProcessorPut) (privacy.Processor, error) {
	classes := make([]privacy.DataClass, 0, len(body.DataClasses))
	for _, class := range body.DataClasses {
		classes = append(classes, privacy.DataClass(class))
	}
	return routes.requests.PutProcessor(request.Context(), scope, actor, identifier, body.ExpectedVersion, body.Name,
		privacy.ProcessorRole(body.Role), body.Purpose, classes, body.Regions, body.TransferMechanism)
}

func (routes *PrivacyRoutes) privacyPage(request *http.Request, scope tenant.Scope, bound string) (int, string, error) {
	query := request.URL.Query()
	limit := 25
	if encoded := query.Get("limit"); encoded != "" {
		parsed, err := strconv.Atoi(encoded)
		if err != nil || parsed < 1 || parsed > 100 {
			return 0, "", errors.New("limit must be from 1 to 100")
		}
		limit = parsed
	}
	cursor := query.Get("cursor")
	if cursor == "" {
		return limit, "", nil
	}
	claims, err := routes.cursors.Decode(cursor, scope.ID(), bound)
	if err != nil {
		return 0, "", err
	}
	var position string
	if err := json.Unmarshal(claims.Position, &position); err != nil || !privacyToken(position, 64) {
		return 0, "", errors.New("cursor position is invalid")
	}
	return limit, position, nil
}

func (routes *PrivacyRoutes) encodePrivacyCursor(scope tenant.Scope, bound, position string) (string, error) {
	encoded, err := json.Marshal(position)
	if err != nil {
		return "", err
	}
	return routes.cursors.Encode(scope.ID(), bound, encoded)
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func derefKind(value *openapiv1.SubjectPrivacyRequestCreateKind) string {
	if value == nil {
		return ""
	}
	return string(*value)
}

func derefNormalization(value *openapiv1.SubjectPrivacyRequestCreateNormalization) string {
	if value == nil {
		return ""
	}
	return string(*value)
}

func derefTenantKind(value *openapiv1.PrivacyRequestCreateKind) string {
	if value == nil {
		return ""
	}
	return string(*value)
}

func derefTenantNormalization(value *openapiv1.PrivacyRequestCreateNormalization) string {
	if value == nil {
		return ""
	}
	return string(*value)
}

func privacySubjectInput(requestType privacy.RequestType, region, subjectID, purpose, recordID, decisionID, name, value, kind, normalization string) (privacy.CreateRequestInput, error) {
	if !requestType.Valid() {
		return privacy.CreateRequestInput{}, errors.New("type is not a selected privacy-request type")
	}
	if region == "" {
		return privacy.CreateRequestInput{}, errors.New("region is required")
	}
	payload := privacy.RequestPayload{
		Purpose: purpose, RecordID: recordID, DecisionID: decisionID, Name: name,
		Value: value, Kind: kind, Normalization: normalization,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return privacy.CreateRequestInput{}, err
	}
	return privacy.CreateRequestInput{Type: requestType, SubjectID: subjectID, Region: region, Payload: encoded}, nil
}

func privacyRequestSummary(value privacy.Request) openapiv1.PrivacyRequestSummary {
	return openapiv1.PrivacyRequestSummary{
		ID: value.ID.String(), Type: openapiv1.PrivacyRequestType(value.Type), State: openapiv1.PrivacyRequestState(value.State),
		Channel: openapiv1.PrivacyRequestChannel(value.Channel), SubjectID: optionalString(value.SubjectID),
		VerificationID: optionalString(value.VerificationID), Region: value.Region, ReasonCode: optionalString(string(value.ReasonCode)),
		FailureClass: optionalString(value.FailureClass), EffectKind: optionalString(value.EffectKind),
		EffectReference: optionalString(value.EffectRef), EffectDigest: optionalString(value.EffectDigest),
		ExpiresAt: value.ExpiresAt, RequestedAt: value.RequestedAt, UpdatedAt: value.UpdatedAt, Version: value.Version,
	}
}

func privacyRequestStatus(value privacy.Request) openapiv1.PrivacyRequestStatus {
	decisions := make([]openapiv1.PrivacyRequestDecisionSummary, 0, len(value.Decisions))
	for _, decision := range value.Decisions {
		decisions = append(decisions, openapiv1.PrivacyRequestDecisionSummary{
			ID: decision.ID.String(), Outcome: openapiv1.PrivacyDecisionOutcome(decision.Outcome),
			ReasonCode: string(decision.ReasonCode), DecidedAt: decision.DecidedAt, Version: decision.Version,
		})
	}
	summary := privacyRequestSummary(value)
	return openapiv1.PrivacyRequestStatus{
		ID: summary.ID, Type: summary.Type, State: summary.State, Channel: summary.Channel, SubjectID: summary.SubjectID,
		VerificationID: summary.VerificationID, Region: summary.Region, ReasonCode: summary.ReasonCode,
		FailureClass: summary.FailureClass, EffectKind: summary.EffectKind, EffectReference: summary.EffectReference,
		EffectDigest: summary.EffectDigest, ExpiresAt: summary.ExpiresAt, RequestedAt: summary.RequestedAt,
		UpdatedAt: summary.UpdatedAt, Version: summary.Version, Decisions: decisions,
	}
}

func privacyRestriction(value privacy.Restriction) openapiv1.PrivacyRestriction {
	return openapiv1.PrivacyRestriction{
		ID: value.ID.String(), RequestID: value.RequestID.String(), SubjectID: value.SubjectID,
		Scope: openapiv1.PrivacyRestrictionScope(value.Scope), Purpose: optionalString(value.Purpose),
		ReasonCode: string(value.ReasonCode), Region: value.Region, State: openapiv1.PrivacyRestrictionState(value.State),
		StartsAt: value.StartsAt, LiftedAt: optionalTime(value.LiftedAt), LiftReasonCode: optionalString(string(value.LiftReasonCode)),
		Version: value.Version,
	}
}

func privacyDisclosure(value privacy.Disclosure) openapiv1.PrivacyDisclosure {
	return openapiv1.PrivacyDisclosure{
		ID: value.ID.String(), RequestID: value.RequestID.String(), Recipient: value.Recipient, Purpose: value.Purpose,
		DataClass: openapiv1.PrivacyDisclosureClass(value.DataClass), LegalBasis: value.LegalBasis, Region: value.Region,
		Reference: value.Reference, DisclosedAt: value.DisclosedAt, Version: value.Version,
	}
}

func privacyProcessor(value privacy.Processor) openapiv1.PrivacyProcessor {
	classes := make([]openapiv1.PrivacyProcessorDataClasses, 0, len(value.DataClasses))
	for _, class := range value.DataClasses {
		classes = append(classes, openapiv1.PrivacyProcessorDataClasses(class))
	}
	return openapiv1.PrivacyProcessor{
		ID: value.ID.String(), Name: value.Name, Role: openapiv1.PrivacyProcessorRole(value.Role), Purpose: value.Purpose,
		DataClasses: classes, Regions: append([]string(nil), value.Regions...), TransferMechanism: value.TransferMechanism,
		Version: value.Version, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func subjectPrivacyRequest(value privacy.SubjectRequest) openapiv1.SubjectPrivacyRequest {
	return openapiv1.SubjectPrivacyRequest{
		Type: openapiv1.PrivacyRequestType(value.Type), Status: openapiv1.SubjectPrivacyStatus(value.Status),
		RequestedAt: value.RequestedAt, UpdatedAt: value.UpdatedAt,
	}
}
