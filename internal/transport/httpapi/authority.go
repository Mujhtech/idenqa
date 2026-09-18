package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/authority"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/go-chi/chi/v5"
)

// ProcessingAuthorityService is the application capability consumed by authority routes.
type ProcessingAuthorityService interface {
	CreateNotice(context.Context, access.Context, string, authority.NoticeInput) (authority.Notice, error)
	FindNotice(context.Context, access.Context, id.Notice) (authority.Notice, error)
	Declare(context.Context, access.Context, string, authority.DeclarationInput) (authority.Authority, error)
	FindByVerification(context.Context, access.Context, id.Verification) (authority.Authority, error)
	Transition(context.Context, access.Context, id.Verification, string, int64, authority.State) (authority.Authority, error)
	CaptureSnapshot(context.Context, verification.CaptureContext) (authority.Snapshot, error)
	Respond(context.Context, verification.CaptureContext, string, authority.ResponseInput) (authority.Response, error)
}

// AuthorityRoutes adapts notice and processing-authority use cases to HTTP.
type AuthorityRoutes struct {
	access  *AccessMiddleware
	capture *CaptureAccessMiddleware
	service ProcessingAuthorityService
	logger  *slog.Logger
}

// NewAuthorityRoutes constructs tenant and capture authority routes.
func NewAuthorityRoutes(
	accessMiddleware *AccessMiddleware,
	captureMiddleware *CaptureAccessMiddleware,
	service ProcessingAuthorityService,
	logger *slog.Logger,
) (*AuthorityRoutes, error) {
	if accessMiddleware == nil || captureMiddleware == nil || service == nil || logger == nil {
		return nil, errors.New("authority route dependencies are required")
	}
	return &AuthorityRoutes{
		access: accessMiddleware, capture: captureMiddleware, service: service, logger: logger,
	}, nil
}

// Register adds tenant declaration and subject-facing capture routes.
func (routes *AuthorityRoutes) Register(router chi.Router) {
	router.With(routes.access.Authenticate, routes.access.Require(access.PermissionNoticesWrite)).
		Post("/notices", routes.createNotice)
	router.With(routes.access.Authenticate, routes.access.Require(access.PermissionNoticesRead)).
		Get("/notices/{noticeID}", routes.findNotice)
	router.With(routes.access.Authenticate, routes.access.Require(access.PermissionAuthoritiesWrite)).
		Post("/verifications/{verificationID}/authority", routes.declare)
	router.With(routes.access.Authenticate, routes.access.Require(access.PermissionAuthoritiesRead)).
		Get("/verifications/{verificationID}/authority", routes.find)
	router.With(routes.access.Authenticate, routes.access.Require(access.PermissionAuthoritiesWrite)).
		Post("/verifications/{verificationID}/authority/restrict", routes.transition(authority.StateRestricted))
	router.With(routes.access.Authenticate, routes.access.Require(access.PermissionAuthoritiesWrite)).
		Post("/verifications/{verificationID}/authority/withdraw", routes.transition(authority.StateWithdrawn))
	router.With(routes.access.Authenticate, routes.access.Require(access.PermissionAuthoritiesWrite)).
		Post("/verifications/{verificationID}/authority/supersede", routes.transition(authority.StateSuperseded))
	router.With(routes.capture.Authenticate).Get("/capture/authority", routes.captureSnapshot)
	router.With(routes.capture.Authenticate).Post("/capture/authority/responses", routes.respond)
}

func (routes *AuthorityRoutes) createNotice(writer http.ResponseWriter, request *http.Request) {
	accessContext, ok := routes.accessContext(writer, request)
	if !ok {
		return
	}
	key, err := parseIdempotencyKey(request.Header.Values("Idempotency-Key"))
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	body, err := decodeJSONBody[openapiv1.NoticeVersionCreate](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	notice, err := routes.service.CreateNotice(request.Context(), accessContext, key, authority.NoticeInput{
		Key: body.Key, Locale: body.Locale, Controller: body.Controller, Recipient: body.Recipient,
		Copy: authority.NoticeCopy{
			Title: body.Copy.Title, Summary: body.Copy.Summary,
			Purpose: body.Copy.Purpose, Consequences: body.Copy.Consequences,
		},
		EffectiveAt: body.EffectiveAt,
	})
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	writer.Header().Set("Location", "/v1/notices/"+notice.ID().String())
	routes.writeJSON(writer, request, http.StatusCreated, noticeResponse(notice))
}

func (routes *AuthorityRoutes) findNotice(writer http.ResponseWriter, request *http.Request) {
	accessContext, ok := routes.accessContext(writer, request)
	if !ok {
		return
	}
	identifier, err := id.ParseNotice(chi.URLParam(request, "noticeID"))
	if err != nil {
		routes.problem(writer, request, authority.ErrNotFound)
		return
	}
	notice, err := routes.service.FindNotice(request.Context(), accessContext, identifier)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeJSON(writer, request, http.StatusOK, noticeResponse(notice))
}

func (routes *AuthorityRoutes) declare(writer http.ResponseWriter, request *http.Request) {
	accessContext, verificationID, ok := routes.accessAndVerification(writer, request)
	if !ok {
		return
	}
	key, err := parseIdempotencyKey(request.Header.Values("Idempotency-Key"))
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	body, err := decodeJSONBody[openapiv1.ProcessingAuthorityDeclare](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	noticeID, err := id.ParseNotice(body.NoticeID)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	declaration, err := routes.service.Declare(request.Context(), accessContext, key, authority.DeclarationInput{
		VerificationID: verificationID, NoticeID: noticeID, Category: body.Category,
		Purpose: body.Purpose, Jurisdiction: body.Jurisdiction, PolicyPack: body.PolicyPack,
		IsConsentRequired: body.ConsentRequired, RecipientReference: body.RecipientReference,
		RecipientDisplayName: body.RecipientDisplayName, Regions: body.Regions,
		RetentionReference: body.RetentionReference, ValidFrom: body.ValidFrom, ExpiresAt: body.ExpiresAt,
	})
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	writer.Header().Set("ETag", entityTag(declaration.Record().Version))
	routes.writeJSON(writer, request, http.StatusCreated, processingAuthorityResponse(declaration))
}

func (routes *AuthorityRoutes) find(writer http.ResponseWriter, request *http.Request) {
	accessContext, verificationID, ok := routes.accessAndVerification(writer, request)
	if !ok {
		return
	}
	declaration, err := routes.service.FindByVerification(request.Context(), accessContext, verificationID)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	writer.Header().Set("ETag", entityTag(declaration.Record().Version))
	routes.writeJSON(writer, request, http.StatusOK, processingAuthorityResponse(declaration))
}

func (routes *AuthorityRoutes) transition(state authority.State) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		accessContext, verificationID, ok := routes.accessAndVerification(writer, request)
		if !ok {
			return
		}
		expected, err := parseIfMatch(request.Header.Values("If-Match"))
		if err != nil {
			routes.problem(writer, request, err)
			return
		}
		key, err := parseIdempotencyKey(request.Header.Values("Idempotency-Key"))
		if err != nil {
			routes.problem(writer, request, err)
			return
		}
		declaration, err := routes.service.Transition(
			request.Context(), accessContext, verificationID, key, expected, state,
		)
		if err != nil {
			routes.problem(writer, request, err)
			return
		}
		writer.Header().Set("ETag", entityTag(declaration.Record().Version))
		routes.writeJSON(writer, request, http.StatusOK, processingAuthorityResponse(declaration))
	}
}

func (routes *AuthorityRoutes) captureSnapshot(writer http.ResponseWriter, request *http.Request) {
	captureContext, ok := CaptureContext(request.Context())
	if !ok {
		writeCaptureProblem(writer, request, routes.logger, access.ErrInvalidCaptureToken, true)
		return
	}
	snapshot, err := routes.service.CaptureSnapshot(request.Context(), captureContext)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	response := openapiv1.CaptureAuthoritySnapshot{
		Authority: processingAuthorityResponse(snapshot.Authority), Notice: noticeResponse(snapshot.Notice),
	}
	if snapshot.Response != nil {
		converted := subjectResponse(*snapshot.Response)
		response.LatestResponse = &converted
	}
	routes.writeJSON(writer, request, http.StatusOK, response)
}

func (routes *AuthorityRoutes) respond(writer http.ResponseWriter, request *http.Request) {
	captureContext, ok := CaptureContext(request.Context())
	if !ok {
		writeCaptureProblem(writer, request, routes.logger, access.ErrInvalidCaptureToken, true)
		return
	}
	key, err := parseIdempotencyKey(request.Header.Values("Idempotency-Key"))
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	body, err := decodeJSONBody[openapiv1.SubjectResponseCreate](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	rendered := ""
	if body.RenderedExperienceVersion != nil {
		rendered = *body.RenderedExperienceVersion
	}
	receipt, err := routes.service.Respond(request.Context(), captureContext, key, authority.ResponseInput{
		Action: authority.ResponseAction(body.Action), Locale: body.Locale, RenderedExperienceVersion: rendered,
	})
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeJSON(writer, request, http.StatusCreated, subjectResponse(receipt))
}

func (routes *AuthorityRoutes) accessContext(
	writer http.ResponseWriter,
	request *http.Request,
) (access.Context, bool) {
	accessContext, ok := AccessContext(request.Context())
	if !ok {
		writeAccessProblem(writer, request, routes.logger, access.ErrInvalidCredential, true)
		return access.Context{}, false
	}
	return accessContext, true
}

func (routes *AuthorityRoutes) accessAndVerification(
	writer http.ResponseWriter,
	request *http.Request,
) (access.Context, id.Verification, bool) {
	accessContext, ok := routes.accessContext(writer, request)
	if !ok {
		return access.Context{}, id.Verification{}, false
	}
	identifier, err := id.ParseVerification(chi.URLParam(request, "verificationID"))
	if err != nil {
		routes.problem(writer, request, authority.ErrNotFound)
		return access.Context{}, id.Verification{}, false
	}
	return accessContext, identifier, true
}

func noticeResponse(notice authority.Notice) openapiv1.NoticeVersion {
	noticeCopy := notice.Copy()
	return openapiv1.NoticeVersion{
		ID: notice.ID().String(), Key: notice.Key(), Locale: notice.Locale(),
		Controller: notice.Controller(), Recipient: notice.Recipient(),
		Copy: openapiv1.NoticeCopy{
			Title: noticeCopy.Title, Summary: noticeCopy.Summary,
			Purpose: noticeCopy.Purpose, Consequences: noticeCopy.Consequences,
		},
		EffectiveAt: notice.EffectiveAt(), CreatedAt: notice.CreatedAt(), Digest: notice.Digest(),
	}
}

func processingAuthorityResponse(declaration authority.Authority) openapiv1.ProcessingAuthority {
	record := declaration.Record()
	return openapiv1.ProcessingAuthority{
		ID: record.ID.String(), SubjectID: record.SubjectID.String(), VerificationID: record.VerificationID.String(),
		NoticeID: record.NoticeID.String(), Category: record.Category, Purpose: record.Purpose,
		Jurisdiction: record.Jurisdiction, PolicyPack: record.PolicyPack,
		ConsentRequired: record.IsConsentRequired, RequirementPurposes: record.RequirementPurposes,
		EvidenceTypes: record.EvidenceTypes, RecipientReference: record.RecipientReference,
		RecipientDisplayName: record.RecipientDisplayName, Regions: record.Regions,
		RetentionReference: record.RetentionReference, State: openapiv1.ProcessingAuthorityState(record.State),
		Version: record.Version, ValidFrom: record.ValidFrom, ExpiresAt: record.ExpiresAt,
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
}

func subjectResponse(receipt authority.Response) openapiv1.SubjectResponse {
	record := receipt.Record()
	var rendered *string
	if record.RenderedExperienceVersion != "" {
		value := record.RenderedExperienceVersion
		rendered = &value
	}
	return openapiv1.SubjectResponse{
		ID: record.ID.String(), AuthorityID: record.AuthorityID.String(), NoticeID: record.NoticeID.String(),
		SubjectID: record.SubjectID.String(), VerificationID: record.VerificationID.String(),
		Action: openapiv1.SubjectResponseAction(record.Action), Locale: record.Locale,
		RenderedExperienceVersion: rendered, RecordedAt: record.RecordedAt,
	}
}

func (routes *AuthorityRoutes) problem(writer http.ResponseWriter, request *http.Request, err error) {
	if writeErr := respond.WriteProblem(writer, request, err, requestIDString(request.Context())); writeErr != nil {
		routes.logger.ErrorContext(request.Context(), "write authority problem response")
	}
}

func (routes *AuthorityRoutes) writeJSON(writer http.ResponseWriter, request *http.Request, status int, value any) {
	if err := respond.JSON(writer, request, status, value); err != nil {
		routes.logger.ErrorContext(request.Context(), "write authority response")
	}
}
