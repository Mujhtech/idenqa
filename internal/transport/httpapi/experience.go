package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"time"

	contract "github.com/Mujhtech/idenqa/contracts/experience/v1"
	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/experience"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/go-chi/chi/v5"
)

const experienceListQuery = "experiences:list"

var (
	experienceCountryPattern = regexp.MustCompile(`^[A-Z]{2}$`)
	experienceSDKPattern     = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	experienceLocalePattern  = regexp.MustCompile(`^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$`)
	experienceWorkflowPatt   = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,127}$`)
)

// ExperienceService is the portable-experience application capability the
// HTTP boundary consumes.
type ExperienceService interface {
	Create(context.Context, access.Context, experience.DraftRequest) (experience.Experience, error)
	UpdateDraft(context.Context, access.Context, id.Experience, int64, experience.DraftRequest) (experience.Experience, error)
	Approve(context.Context, access.Context, id.Experience, int64) (experience.Experience, error)
	Publish(context.Context, access.Context, id.Experience, int64) (experience.Experience, error)
	Revoke(context.Context, access.Context, id.Experience, int64, string) (experience.Experience, error)
	Rollback(context.Context, access.Context, id.Experience, int64, uint32, string) (experience.Experience, error)
	Get(context.Context, access.Context, id.Experience) (experience.Experience, error)
	List(context.Context, access.Context, *experience.Position, int) (experience.Page, error)
	Export(context.Context, access.Context, id.Experience) (contract.Manifest, error)
	Import(context.Context, access.Context, []byte) (experience.Experience, error)
	ResolveForSession(context.Context, tenant.Scope, id.Verification, experience.ResolutionRequest) (contract.Resolution, error)
	SafeDefault(time.Time) contract.Resolution
}

// ExperienceRoutes adapts portable-experience use cases to HTTP.
type ExperienceRoutes struct {
	access  *AccessMiddleware
	capture *CaptureAccessMiddleware
	service ExperienceService
	cursors ProfileCursor
	logger  *slog.Logger
}

// NewExperienceRoutes constructs the experience administration and capture
// resolution surface.
func NewExperienceRoutes(
	accessMiddleware *AccessMiddleware,
	captureMiddleware *CaptureAccessMiddleware,
	service ExperienceService,
	cursors ProfileCursor,
	logger *slog.Logger,
) (*ExperienceRoutes, error) {
	if accessMiddleware == nil || captureMiddleware == nil || service == nil || cursors == nil || logger == nil {
		return nil, errors.New("experience route dependencies are required")
	}
	return &ExperienceRoutes{access: accessMiddleware, capture: captureMiddleware, service: service, cursors: cursors, logger: logger}, nil
}

// Register mounts the tenant administration and capture-token resolution routes.
func (routes *ExperienceRoutes) Register(router chi.Router) {
	read := []func(http.Handler) http.Handler{routes.access.Authenticate, routes.access.Require(access.PermissionExperiencesRead)}
	write := []func(http.Handler) http.Handler{routes.access.Authenticate, routes.access.Require(access.PermissionExperiencesWrite)}
	publish := []func(http.Handler) http.Handler{routes.access.Authenticate, routes.access.Require(access.PermissionExperiencesPublish)}

	router.With(read...).Get("/experience-default", routes.safeDefault)
	router.With(read...).Get("/experiences", routes.list)
	router.With(write...).Post("/experiences", routes.create)
	router.With(write...).Post("/experiences/import", routes.importManifest)
	router.With(read...).Get("/experiences/{experienceID}", routes.get)
	router.With(write...).Put("/experiences/{experienceID}", routes.updateDraft)
	router.With(write...).Get("/experiences/{experienceID}/export", routes.export)
	router.With(publish...).Post("/experiences/{experienceID}/approve", routes.approve)
	router.With(publish...).Post("/experiences/{experienceID}/publish", routes.publish)
	router.With(publish...).Post("/experiences/{experienceID}/revoke", routes.revoke)
	router.With(publish...).Post("/experiences/{experienceID}/rollback", routes.rollback)
	router.With(routes.capture.Authenticate).Get("/capture/experience", routes.resolve)
}

type experienceMutationRequest struct {
	Name                 string            `json:"name"`
	Copy                 contract.Copy     `json:"copy"`
	MandatoryCopyVersion string            `json:"mandatory_copy_version"`
	DefaultLocale        string            `json:"default_locale"`
	Targeting            []contract.Target `json:"targeting"`
	Assets               []contract.Asset  `json:"assets,omitempty"`
	Links                contract.Links    `json:"links"`
	Theme                contract.Theme    `json:"theme"`
	AllowedOrigins       []string          `json:"allowed_origins,omitempty"`
}

type experienceDraftRequest struct {
	ExpectedVersion int64                     `json:"expected_version"`
	Document        experienceMutationRequest `json:"document"`
}

type experienceTransitionRequest struct {
	ExpectedVersion int64  `json:"expected_version"`
	Reason          string `json:"reason,omitempty"`
}

type experienceRevocationRequest struct {
	ExpectedVersion int64  `json:"expected_version"`
	Reason          string `json:"reason"`
}

type experienceRollbackRequest struct {
	ExpectedVersion int64  `json:"expected_version"`
	TargetVersion   uint32 `json:"target_version"`
	Reason          string `json:"reason,omitempty"`
}

type experienceResource struct {
	ID               string            `json:"id"`
	State            string            `json:"state"`
	Revision         int64             `json:"revision"`
	LatestVersion    uint32            `json:"latest_version"`
	ApprovedVersion  uint32            `json:"approved_version"`
	PublishedVersion uint32            `json:"published_version"`
	Document         contract.Document `json:"document"`
	CreatedAt        string            `json:"created_at"`
	UpdatedAt        string            `json:"updated_at"`
}

type experienceListResource struct {
	Data []experienceResource `json:"data"`
	Page experiencePage       `json:"page"`
}

type experiencePage struct {
	NextCursor *string `json:"next_cursor,omitempty"`
	HasMore    bool    `json:"has_more"`
}

func (routes *ExperienceRoutes) create(writer http.ResponseWriter, request *http.Request) {
	authority, ok := routes.authority(writer, request)
	if !ok {
		return
	}
	body, err := decodeJSONBody[experienceMutationRequest](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	value, err := routes.service.Create(request.Context(), authority, draftRequest(body))
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeMutation(writer, request, http.StatusCreated, value)
}

func (routes *ExperienceRoutes) importManifest(writer http.ResponseWriter, request *http.Request) {
	authority, ok := routes.authority(writer, request)
	if !ok {
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		routes.problem(writer, request, invalidRequest(errors.New("content type must be application/json")))
		return
	}
	raw, err := io.ReadAll(io.LimitReader(request.Body, 2*contract.MaxDocumentBytes+1))
	if err != nil || len(raw) > 2*contract.MaxDocumentBytes {
		routes.problem(writer, request, invalidRequest(errors.New("import body is invalid or too large")))
		return
	}
	value, err := routes.service.Import(request.Context(), authority, raw)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeMutation(writer, request, http.StatusCreated, value)
}

func (routes *ExperienceRoutes) updateDraft(writer http.ResponseWriter, request *http.Request) {
	authority, ok := routes.authority(writer, request)
	if !ok {
		return
	}
	identifier, err := routes.identifier(request)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	body, err := decodeJSONBody[experienceDraftRequest](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	value, err := routes.service.UpdateDraft(request.Context(), authority, identifier, body.ExpectedVersion, draftRequest(body.Document))
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeMutation(writer, request, http.StatusOK, value)
}

func (routes *ExperienceRoutes) approve(writer http.ResponseWriter, request *http.Request) {
	routes.transition(writer, request, func(ctx context.Context, authority access.Context, identifier id.Experience, body experienceTransitionRequest) (experience.Experience, error) {
		return routes.service.Approve(ctx, authority, identifier, body.ExpectedVersion)
	})
}

func (routes *ExperienceRoutes) publish(writer http.ResponseWriter, request *http.Request) {
	routes.transition(writer, request, func(ctx context.Context, authority access.Context, identifier id.Experience, body experienceTransitionRequest) (experience.Experience, error) {
		return routes.service.Publish(ctx, authority, identifier, body.ExpectedVersion)
	})
}

func (routes *ExperienceRoutes) transition(
	writer http.ResponseWriter,
	request *http.Request,
	apply func(context.Context, access.Context, id.Experience, experienceTransitionRequest) (experience.Experience, error),
) {
	authority, ok := routes.authority(writer, request)
	if !ok {
		return
	}
	identifier, err := routes.identifier(request)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	body, err := decodeJSONBody[experienceTransitionRequest](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	value, err := apply(request.Context(), authority, identifier, body)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeMutation(writer, request, http.StatusOK, value)
}

func (routes *ExperienceRoutes) revoke(writer http.ResponseWriter, request *http.Request) {
	authority, ok := routes.authority(writer, request)
	if !ok {
		return
	}
	identifier, err := routes.identifier(request)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	body, err := decodeJSONBody[experienceRevocationRequest](request)
	if err != nil || body.Reason == "" {
		routes.problem(writer, request, invalidRequest(errors.New("a bounded revocation reason is required")))
		return
	}
	value, err := routes.service.Revoke(request.Context(), authority, identifier, body.ExpectedVersion, body.Reason)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeMutation(writer, request, http.StatusOK, value)
}

func (routes *ExperienceRoutes) rollback(writer http.ResponseWriter, request *http.Request) {
	authority, ok := routes.authority(writer, request)
	if !ok {
		return
	}
	identifier, err := routes.identifier(request)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	body, err := decodeJSONBody[experienceRollbackRequest](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	value, err := routes.service.Rollback(request.Context(), authority, identifier, body.ExpectedVersion, body.TargetVersion, body.Reason)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeMutation(writer, request, http.StatusOK, value)
}

func (routes *ExperienceRoutes) get(writer http.ResponseWriter, request *http.Request) {
	authority, ok := routes.authority(writer, request)
	if !ok {
		return
	}
	identifier, err := routes.identifier(request)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	value, err := routes.service.Get(request.Context(), authority, identifier)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeMutation(writer, request, http.StatusOK, value)
}

func (routes *ExperienceRoutes) export(writer http.ResponseWriter, request *http.Request) {
	authority, ok := routes.authority(writer, request)
	if !ok {
		return
	}
	identifier, err := routes.identifier(request)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	manifest, err := routes.service.Export(request.Context(), authority, identifier)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	if err := respond.JSON(writer, request, http.StatusOK, manifest); err != nil {
		routes.logger.ErrorContext(request.Context(), "write experience export response")
	}
}

func (routes *ExperienceRoutes) list(writer http.ResponseWriter, request *http.Request) {
	authority, ok := routes.authority(writer, request)
	if !ok {
		return
	}
	limit, encodedCursor, err := parseListQuery(request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	query := experienceListQuery + ";limit=" + strconv.Itoa(limit)
	var after *experience.Position
	if encodedCursor != "" {
		claims, err := routes.cursors.Decode(encodedCursor, authority.TenantScope().ID(), query)
		if err != nil {
			routes.problem(writer, request, invalidRequest(err))
			return
		}
		var position experience.Position
		if err := decodeStrictJSON(claims.Position, &position); err != nil || position.Before.IsZero() {
			routes.problem(writer, request, invalidRequest(errors.New("cursor is invalid")))
			return
		}
		after = &position
	}
	page, err := routes.service.List(request.Context(), authority, after, limit)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	response := experienceListResource{Data: make([]experienceResource, 0, len(page.Experiences)), Page: experiencePage{HasMore: page.Next != nil}}
	for _, value := range page.Experiences {
		response.Data = append(response.Data, experienceResponse(value))
	}
	if page.Next != nil {
		position, err := json.Marshal(page.Next)
		if err != nil {
			routes.problem(writer, request, err)
			return
		}
		next, err := routes.cursors.Encode(authority.TenantScope().ID(), query, position)
		if err != nil {
			routes.problem(writer, request, err)
			return
		}
		response.Page.NextCursor = &next
	}
	if err := respond.JSON(writer, request, http.StatusOK, response); err != nil {
		routes.logger.ErrorContext(request.Context(), "write experience list response")
	}
}

func (routes *ExperienceRoutes) safeDefault(writer http.ResponseWriter, request *http.Request) {
	if _, ok := routes.authority(writer, request); !ok {
		return
	}
	resolution := routes.service.SafeDefault(time.Now().UTC())
	if err := respond.JSON(writer, request, http.StatusOK, resolution); err != nil {
		routes.logger.ErrorContext(request.Context(), "write safe-default response")
	}
}

func (routes *ExperienceRoutes) resolve(writer http.ResponseWriter, request *http.Request) {
	captureContext, ok := CaptureContext(request.Context())
	if !ok {
		writeCaptureProblem(writer, request, routes.logger, access.ErrInvalidCaptureToken, true)
		return
	}
	resolutionRequest, err := resolutionRequestFromQuery(request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	resolution, err := routes.service.ResolveForSession(
		request.Context(), captureContext.TenantScope(), captureContext.Session().ID(), resolutionRequest,
	)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	if err := respond.JSON(writer, request, http.StatusOK, resolution); err != nil {
		routes.logger.ErrorContext(request.Context(), "write experience resolution response")
	}
}

func resolutionRequestFromQuery(request *http.Request) (experience.ResolutionRequest, error) {
	query := request.URL.Query()
	result := experience.ResolutionRequest{
		Workflow: query.Get("workflow"), Country: query.Get("country"), ApplicationID: query.Get("application_id"),
		Origin: query.Get("origin"), SDKVersion: query.Get("sdk_version"), Locale: query.Get("locale"),
	}
	if len(result.Workflow) > 128 || (result.Workflow != "" && !experienceWorkflowPatt.MatchString(result.Workflow)) {
		return result, errors.New("workflow is invalid")
	}
	if result.Country != "" && !experienceCountryPattern.MatchString(result.Country) {
		return result, errors.New("country is invalid")
	}
	if len(result.ApplicationID) > 200 {
		return result, errors.New("application_id is invalid")
	}
	if len(result.Origin) > contract.MaxURLBytes {
		return result, errors.New("origin is invalid")
	}
	if result.SDKVersion != "" && !experienceSDKPattern.MatchString(result.SDKVersion) {
		return result, errors.New("sdk_version is invalid")
	}
	if result.Locale != "" && (len(result.Locale) > contract.MaxLocaleBytes || !experienceLocalePattern.MatchString(result.Locale)) {
		return result, errors.New("locale is invalid")
	}
	return result, nil
}

func draftRequest(body experienceMutationRequest) experience.DraftRequest {
	return experience.DraftRequest{
		Name: body.Name, Copy: body.Copy, MandatoryCopyVersion: body.MandatoryCopyVersion,
		DefaultLocale: body.DefaultLocale, Targeting: body.Targeting, Assets: body.Assets,
		Links: body.Links, Theme: body.Theme, AllowedOrigins: body.AllowedOrigins,
	}
}

func experienceResponse(value experience.Experience) experienceResource {
	return experienceResource{
		ID: value.ID.String(), State: string(value.State), Revision: value.Revision,
		LatestVersion: value.LatestVersion, ApprovedVersion: value.ApprovedVersion,
		PublishedVersion: value.PublishedVersion, Document: value.Document,
		CreatedAt: value.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: value.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func (routes *ExperienceRoutes) writeMutation(writer http.ResponseWriter, request *http.Request, status int, value experience.Experience) {
	writer.Header().Set("ETag", strconv.Quote("experience:"+strconv.FormatInt(value.Revision, 10)))
	if status == http.StatusCreated {
		writer.Header().Set("Location", "/v1/experiences/"+value.ID.String())
	}
	if err := respond.JSON(writer, request, status, experienceResponse(value)); err != nil {
		routes.logger.ErrorContext(request.Context(), "write experience response")
	}
}

func (routes *ExperienceRoutes) authority(writer http.ResponseWriter, request *http.Request) (access.Context, bool) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return access.Context{}, false
	}
	return authority, true
}

func (routes *ExperienceRoutes) identifier(request *http.Request) (id.Experience, error) {
	identifier, err := id.ParseExperience(chi.URLParam(request, "experienceID"))
	if err != nil {
		return id.Experience{}, experience.ErrNotFound
	}
	return identifier, nil
}

func (routes *ExperienceRoutes) problem(writer http.ResponseWriter, request *http.Request, err error) {
	if writeErr := respond.WriteProblem(writer, request, err, requestIDString(request.Context())); writeErr != nil {
		routes.logger.ErrorContext(request.Context(), "write experience problem response")
	}
}
