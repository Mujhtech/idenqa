package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/Mujhtech/idenqa/internal/authority"
	"github.com/Mujhtech/idenqa/internal/evidence"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/apierror"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/go-chi/chi/v5"
)

// EvidenceUploadIssuer is the body-free upload-intent capability consumed by HTTP.
type EvidenceUploadIssuer interface {
	Issue(context.Context, verification.CaptureContext, string, authority.UploadRequest) (evidence.Upload, error)
}

// EvidenceUploadFinder is the authenticated upload-snapshot capability consumed by HTTP.
type EvidenceUploadFinder interface {
	Find(context.Context, evidence.UploadPrincipal, id.Upload) (evidence.Upload, error)
}

// EvidenceUploadAccepter is the streaming evidence-ingress capability consumed by HTTP.
type EvidenceUploadAccepter interface {
	Accept(
		context.Context,
		verification.CaptureContext,
		id.Upload,
		evidence.UploadMetadata,
		io.Reader,
	) (evidence.Upload, error)
}

// EvidenceUploadRoutes adapts requirement-bound upload issuance and ingress to HTTP.
type EvidenceUploadRoutes struct {
	handlerBase
	capture *CaptureAccessMiddleware
	issuer  EvidenceUploadIssuer
	finder  EvidenceUploadFinder
	accept  EvidenceUploadAccepter
	policy  evidence.UploadPolicy
}

// NewEvidenceUploadRoutes constructs the capture-token evidence-upload surface.
func NewEvidenceUploadRoutes(
	capture *CaptureAccessMiddleware,
	issuer EvidenceUploadIssuer,
	finder EvidenceUploadFinder,
	accept EvidenceUploadAccepter,
	policy evidence.UploadPolicy,
	logger *slog.Logger,
) (*EvidenceUploadRoutes, error) {
	if capture == nil || issuer == nil || finder == nil || accept == nil || policy.MaximumBytes() == 0 ||
		policy.AttemptTimeout() == 0 || logger == nil {
		return nil, errors.New("evidence upload route dependencies are required")
	}

	return &EvidenceUploadRoutes{
		capture:     capture,
		issuer:      issuer,
		finder:      finder,
		accept:      accept,
		policy:      policy,
		handlerBase: newHandlerBase(logger, "evidence upload"),
	}, nil
}

// Register adds upload intent issuance and whole-body evidence ingress.
func (routes *EvidenceUploadRoutes) Register(router chi.Router) {
	router.With(routes.capture.Authenticate).Post("/evidence-uploads", routes.issue)
	router.With(routes.capture.Authenticate).Get("/evidence-uploads/{uploadID}", routes.find)
	router.With(routes.capture.Authenticate).Put("/evidence-uploads/{uploadID}", routes.upload)
}

func (routes *EvidenceUploadRoutes) find(writer http.ResponseWriter, request *http.Request) {
	captureContext, ok := CaptureContext(request.Context())
	if !ok {
		routes.problem(writer, request, accessFailure())

		return
	}
	uploadID, err := id.ParseUpload(chi.URLParam(request, "uploadID"))
	if err != nil {
		routes.problem(writer, request, evidence.ErrUploadNotFound)

		return
	}
	upload, err := routes.finder.Find(request.Context(), evidence.UploadPrincipal{
		Scope: captureContext.TenantScope(), CaptureTokenID: captureContext.TokenID(),
		VerificationID: captureContext.Session().ID(),
	}, uploadID)
	if err != nil {
		routes.problem(writer, request, err)

		return
	}
	writer.Header().Set("ETag", entityTag(upload.Version()))
	routes.writeJSON(writer, request, http.StatusOK, evidenceUploadResponse(upload))
}

func (routes *EvidenceUploadRoutes) issue(writer http.ResponseWriter, request *http.Request) {
	captureContext, ok := CaptureContext(request.Context())
	if !ok {
		routes.problem(writer, request, accessFailure())

		return
	}
	key, err := parseIdempotencyKey(request.Header.Values("Idempotency-Key"))
	if err != nil {
		routes.problem(writer, request, err)

		return
	}
	body, err := decodeJSONBody[openapiv1.EvidenceUploadCreate](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))

		return
	}
	artefact, err := evidence.ParseName(evidence.KindArtefact, body.Artefact)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))

		return
	}
	method, err := evidence.ParseName(evidence.KindMethod, body.AcquisitionMethod)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))

		return
	}
	if !body.MediaType.Valid() {
		routes.problem(writer, request, invalidRequest(errors.New("unsupported upload media type")))

		return
	}
	if body.ExpectedBytes > routes.policy.MaximumBytes() {
		routes.problem(writer, request, requestTooLarge())

		return
	}
	if _, err := platformcrypto.NewDigest(body.ExpectedDigest); err != nil {
		routes.problem(writer, request, invalidRequest(err))

		return
	}
	fallback, err := parseFallbackCondition(body.FallbackCondition)
	if err != nil {
		routes.problem(writer, request, err)

		return
	}
	sequence, err := parseTemporalFrame(body.Sequence)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	upload, err := routes.issuer.Issue(request.Context(), captureContext, key, authority.UploadRequest{
		RequirementKey: body.RequirementKey, Artefact: artefact, AcquisitionMethod: method,
		FallbackCondition: fallback, ExpectedBytes: body.ExpectedBytes,
		ExpectedDigest: body.ExpectedDigest, MediaType: string(body.MediaType), Region: body.Region,
		Sequence: sequence,
	})
	if err != nil {
		routes.problem(writer, request, err)

		return
	}
	writer.Header().Set("Location", "/v1/evidence-uploads/"+upload.ID().String())
	writer.Header().Set("ETag", entityTag(upload.Version()))
	routes.writeJSON(writer, request, http.StatusCreated, evidenceUploadResponse(upload))
}

func (routes *EvidenceUploadRoutes) upload(writer http.ResponseWriter, request *http.Request) {
	captureContext, ok := CaptureContext(request.Context())
	if !ok {
		routes.problem(writer, request, accessFailure())

		return
	}
	uploadID, err := id.ParseUpload(chi.URLParam(request, "uploadID"))
	if err != nil {
		routes.problem(writer, request, evidence.ErrUploadNotFound)

		return
	}
	metadata, err := parseUploadMetadata(request)
	if err != nil {
		routes.problem(writer, request, err)

		return
	}
	if metadata.ContentLength > routes.policy.MaximumBytes() {
		routes.problem(writer, request, requestTooLarge())

		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, routes.policy.MaximumBytes())
	upload, err := routes.accept.Accept(request.Context(), captureContext, uploadID, metadata, request.Body)
	if err != nil {
		routes.problem(writer, request, err)

		return
	}
	writer.Header().Set("ETag", entityTag(upload.Version()))
	routes.writeJSON(writer, request, http.StatusOK, evidenceUploadResponse(upload))
}

func parseFallbackCondition(
	value *openapiv1.CaptureFallbackCondition,
) (verification.FallbackCondition, error) {
	if value == nil {
		return "", nil
	}
	if !value.Valid() {
		return "", invalidRequest(errors.New("unsupported upload fallback condition"))
	}

	return verification.FallbackCondition(*value), nil
}

func evidenceUploadResponse(upload evidence.Upload) openapiv1.EvidenceUpload {
	record := upload.Record()
	allowed := make([]openapiv1.EvidenceUploadAllowedMediaTypes, len(record.AllowedMediaTypes))
	for index, value := range record.AllowedMediaTypes {
		allowed[index] = openapiv1.EvidenceUploadAllowedMediaTypes(value)
	}
	assurances := make([]string, len(record.Assurances))
	for index, value := range record.Assurances {
		assurances[index] = string(value)
	}

	return openapiv1.EvidenceUpload{
		ID: record.ID.String(), EvidenceID: record.EvidenceID.String(),
		State: openapiv1.EvidenceUploadState(record.State), Version: record.Version,
		Attempt: int(record.Attempt), RequirementKey: record.RequirementKey,
		EvidenceType: string(record.EvidenceType), Artefact: string(record.Artefact),
		AcquisitionMethod: string(record.AcquisitionMethod),
		FallbackCondition: optionalCaptureFallbackCondition(record.FallbackCondition), Assurances: assurances,
		Sequence:          temporalFrameResponse(record.Sequence),
		AllowedMediaTypes: allowed, MaximumBytes: record.MaximumBytes,
		ExpectedBytes: record.ExpectedBytes, MediaType: openapiv1.EvidenceUploadMediaType(record.MediaType),
		Region: record.Region, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
		ExpiresAt: record.ExpiresAt, AcceptedAt: record.AcceptedAt,
	}
}

func parseTemporalFrame(value *openapiv1.TemporalEvidenceFrame) (*evidence.TemporalFrame, error) {
	if value == nil {
		return nil, nil
	}
	if value.Index < 0 || value.Index > 31 || value.Count < 2 || value.Count > 32 {
		return nil, errors.New("temporal frame position is invalid")
	}
	previous := ""
	if value.PreviousDigest != nil {
		previous = *value.PreviousDigest
	}
	return &evidence.TemporalFrame{
		SequenceDigest: value.SequenceDigest, Index: uint16(value.Index), Count: uint16(value.Count),
		ChallengeID: value.ChallengeID, CapturedAt: value.CapturedAt.UTC(), PreviousDigest: previous,
	}, nil
}

func temporalFrameResponse(value *evidence.TemporalFrame) *openapiv1.TemporalEvidenceFrame {
	if value == nil {
		return nil
	}
	var previous *string
	if value.PreviousDigest != "" {
		previousDigest := value.PreviousDigest
		previous = &previousDigest
	}
	return &openapiv1.TemporalEvidenceFrame{
		SequenceDigest: value.SequenceDigest, Index: int(value.Index), Count: int(value.Count),
		ChallengeID: value.ChallengeID, CapturedAt: value.CapturedAt, PreviousDigest: previous,
	}
}

func optionalCaptureFallbackCondition(value string) *openapiv1.CaptureFallbackCondition {
	if value == "" {
		return nil
	}
	fallback := openapiv1.CaptureFallbackCondition(value)

	return &fallback
}

func accessFailure() error {
	return apierror.New(
		http.StatusUnauthorized,
		apierror.CodeUnauthenticated,
		"Unauthenticated",
		"Authentication is required.",
		nil,
	)
}

func requestTooLarge() error {
	return apierror.New(
		http.StatusRequestEntityTooLarge,
		apierror.CodeRequestTooLarge,
		"Request too large",
		"The request body exceeds the permitted size.",
		nil,
	)
}
