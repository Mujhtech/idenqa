package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/evidence"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/Mujhtech/idenqa/internal/platform/cursor"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/apierror"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/go-chi/chi/v5"
)

const profileListQuery = "capture_profiles:list"

// CaptureProfileService is the application capability consumed by profile routes.
type CaptureProfileService interface {
	Create(context.Context, access.Context, string, string, verification.Profile) (verification.MutationResult, error)
	UpdateDraft(
		context.Context,
		access.Context,
		id.Profile,
		int64,
		string,
		verification.Profile,
	) (verification.MutationResult, error)
	Publish(context.Context, access.Context, id.Profile, int64, string) (verification.MutationResult, error)
	Supersede(
		context.Context,
		access.Context,
		id.Profile,
		int64,
		string,
		verification.Profile,
	) (verification.MutationResult, error)
	Deactivate(context.Context, access.Context, id.Profile, int64, string) (verification.MutationResult, error)
	Find(context.Context, access.Context, id.Profile) (verification.CaptureProfile, error)
	FindRevision(context.Context, access.Context, id.Profile, uint32) (verification.Revision, error)
	List(context.Context, access.Context, *verification.ListPosition, int) (verification.Page, error)
	ValidateDraft(context.Context, access.Context, id.Profile) (string, error)
}

// ProfileCursor signs and verifies tenant-bound list positions.
type ProfileCursor interface {
	Encode(id.Tenant, string, json.RawMessage) (string, error)
	Decode(string, id.Tenant, string) (cursor.Claims, error)
}

// ProfileRoutes adapts capture-profile application use cases to HTTP.
type ProfileRoutes struct {
	handlerBase
	access  *AccessMiddleware
	service CaptureProfileService
	catalog evidence.Catalog
	cursors ProfileCursor
}

// NewProfileRoutes constructs the protected capture-profile HTTP surface.
func NewProfileRoutes(
	accessMiddleware *AccessMiddleware,
	service CaptureProfileService,
	catalog evidence.Catalog,
	cursors ProfileCursor,
	logger *slog.Logger,
) (*ProfileRoutes, error) {
	if accessMiddleware == nil || service == nil || catalog.IsZero() || cursors == nil || logger == nil {
		return nil, errors.New("capture profile route dependencies are required")
	}

	return &ProfileRoutes{
		access:      accessMiddleware,
		service:     service,
		catalog:     catalog,
		cursors:     cursors,
		handlerBase: newHandlerBase(logger, "capture profile"),
	}, nil
}

// Register adds the authenticated capture-profile routes.
func (routes *ProfileRoutes) Register(router chi.Router) {
	read := routes.access.Authorize(access.PermissionCaptureProfilesRead)
	write := routes.access.Authorize(access.PermissionCaptureProfilesWrite)
	router.With(read).Get("/capture-profiles", routes.list)
	router.With(write).Post("/capture-profiles", routes.create)
	router.With(read).Get("/capture-profiles/{profileID}", routes.find)
	router.With(write).Put("/capture-profiles/{profileID}/draft", routes.updateDraft)
	router.With(write).Post("/capture-profiles/{profileID}/validate", routes.validateDraft)
	router.With(write).Post("/capture-profiles/{profileID}/publish", routes.publish)
	router.With(write).Post("/capture-profiles/{profileID}/supersede", routes.supersede)
	router.With(write).Post("/capture-profiles/{profileID}/deactivate", routes.deactivate)
	router.With(read).Get("/capture-profiles/{profileID}/revisions/{revision}", routes.findRevision)
}

func (routes *ProfileRoutes) create(writer http.ResponseWriter, request *http.Request) {
	authority, ok := routes.authority(writer, request)
	if !ok {
		return
	}
	key, err := parseIdempotencyKey(request.Header.Values("Idempotency-Key"))
	if err != nil {
		routes.problem(writer, request, err)

		return
	}
	body, err := decodeJSONBody[openapiv1.CaptureProfileWrite](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))

		return
	}
	document, err := verification.ParseProfileFromCatalog(body.Document, routes.catalog)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))

		return
	}
	result, err := routes.service.Create(request.Context(), authority, key, body.Name, document)
	if err != nil {
		routes.problem(writer, request, err)

		return
	}
	writer.Header().Set("ETag", entityTag(result.Version))
	writer.Header().Set("Location", "/v1/capture-profiles/"+result.ProfileID)
	routes.writeJSON(writer, request, http.StatusCreated, mutationResponse(result))
}

func (routes *ProfileRoutes) updateDraft(writer http.ResponseWriter, request *http.Request) {
	authority, identifier, ok := routes.authorityAndProfile(writer, request)
	if !ok {
		return
	}
	expected, err := parseIfMatch(request.Header.Values("If-Match"))
	if err != nil {
		routes.problem(writer, request, err)

		return
	}
	body, err := decodeJSONBody[openapiv1.CaptureProfileWrite](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))

		return
	}
	document, err := verification.ParseProfileFromCatalog(body.Document, routes.catalog)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))

		return
	}
	result, err := routes.service.UpdateDraft(
		request.Context(),
		authority,
		identifier,
		expected,
		body.Name,
		document,
	)
	if err != nil {
		routes.problem(writer, request, err)

		return
	}
	writer.Header().Set("ETag", entityTag(result.Version))
	routes.writeJSON(writer, request, http.StatusOK, mutationResponse(result))
}

func (routes *ProfileRoutes) publish(writer http.ResponseWriter, request *http.Request) {
	routes.conditionalCommand(writer, request, routes.service.Publish)
}

func (routes *ProfileRoutes) deactivate(writer http.ResponseWriter, request *http.Request) {
	routes.conditionalCommand(writer, request, routes.service.Deactivate)
}

func (routes *ProfileRoutes) supersede(writer http.ResponseWriter, request *http.Request) {
	authority, identifier, ok := routes.authorityAndProfile(writer, request)
	if !ok {
		return
	}
	expected, key, ok := routes.commandHeaders(writer, request)
	if !ok {
		return
	}
	body, err := decodeJSONBody[openapiv1.CaptureProfileSupersede](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))

		return
	}
	document, err := verification.ParseProfileFromCatalog(body.Document, routes.catalog)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))

		return
	}
	result, err := routes.service.Supersede(
		request.Context(),
		authority,
		identifier,
		expected,
		key,
		document,
	)
	if err != nil {
		routes.problem(writer, request, err)

		return
	}
	writer.Header().Set("ETag", entityTag(result.Version))
	routes.writeJSON(writer, request, http.StatusOK, mutationResponse(result))
}

func (routes *ProfileRoutes) validateDraft(writer http.ResponseWriter, request *http.Request) {
	authority, identifier, ok := routes.authorityAndProfile(writer, request)
	if !ok {
		return
	}
	digest, err := routes.service.ValidateDraft(request.Context(), authority, identifier)
	if err != nil {
		routes.problem(writer, request, err)

		return
	}
	routes.writeJSON(writer, request, http.StatusOK, openapiv1.CaptureProfileValidation{
		Valid:  openapiv1.CaptureProfileValidationValid(true),
		Digest: digest,
	})
}

func (routes *ProfileRoutes) find(writer http.ResponseWriter, request *http.Request) {
	authority, identifier, ok := routes.authorityAndProfile(writer, request)
	if !ok {
		return
	}
	profile, err := routes.service.Find(request.Context(), authority, identifier)
	if err != nil {
		routes.problem(writer, request, err)

		return
	}
	writer.Header().Set("ETag", entityTag(profile.Version()))
	routes.writeJSON(writer, request, http.StatusOK, profileResponse(profile))
}

func (routes *ProfileRoutes) findRevision(writer http.ResponseWriter, request *http.Request) {
	authority, identifier, ok := routes.authorityAndProfile(writer, request)
	if !ok {
		return
	}
	parsed, err := strconv.ParseUint(chi.URLParam(request, "revision"), 10, 32)
	if err != nil || parsed == 0 {
		routes.problem(writer, request, verification.ErrProfileNotFound)

		return
	}
	revision, err := routes.service.FindRevision(request.Context(), authority, identifier, uint32(parsed))
	if err != nil {
		routes.problem(writer, request, err)

		return
	}
	registry, err := routes.catalog.Resolve(revision.Document().Registry)
	if err != nil {
		routes.problem(writer, request, err)

		return
	}
	document, err := verification.CanonicalJSON(revision.Document(), registry)
	if err != nil {
		routes.problem(writer, request, err)

		return
	}
	routes.writeJSON(writer, request, http.StatusOK, revisionResponse(revision, document))
}

func (routes *ProfileRoutes) list(writer http.ResponseWriter, request *http.Request) {
	authority, ok := routes.authority(writer, request)
	if !ok {
		return
	}
	limit, encodedCursor, err := parseListQuery(request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))

		return
	}
	query := profileListQuery + ";limit=" + strconv.Itoa(limit)
	var after *verification.ListPosition
	if encodedCursor != "" {
		claims, err := routes.cursors.Decode(encodedCursor, authority.TenantScope().ID(), query)
		if err != nil {
			routes.problem(writer, request, invalidRequest(err))

			return
		}
		var position verification.ListPosition
		if err := decodeStrictJSON(claims.Position, &position); err != nil {
			routes.problem(writer, request, invalidRequest(err))

			return
		}
		after = &position
	}
	page, err := routes.service.List(request.Context(), authority, after, limit)
	if err != nil {
		routes.problem(writer, request, err)

		return
	}
	response := openapiv1.CaptureProfileList{
		Data: make([]openapiv1.CaptureProfile, 0, len(page.Profiles)),
		Page: openapiv1.Page{HasMore: page.Next != nil},
	}
	for _, profile := range page.Profiles {
		response.Data = append(response.Data, profileResponse(profile))
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
	routes.writeJSON(writer, request, http.StatusOK, response)
}

type conditionalCommand func(
	context.Context,
	access.Context,
	id.Profile,
	int64,
	string,
) (verification.MutationResult, error)

func (routes *ProfileRoutes) conditionalCommand(
	writer http.ResponseWriter,
	request *http.Request,
	command conditionalCommand,
) {
	authority, identifier, ok := routes.authorityAndProfile(writer, request)
	if !ok {
		return
	}
	expected, key, ok := routes.commandHeaders(writer, request)
	if !ok {
		return
	}
	result, err := command(request.Context(), authority, identifier, expected, key)
	if err != nil {
		routes.problem(writer, request, err)

		return
	}
	writer.Header().Set("ETag", entityTag(result.Version))
	routes.writeJSON(writer, request, http.StatusOK, mutationResponse(result))
}

func (routes *ProfileRoutes) authority(
	writer http.ResponseWriter,
	request *http.Request,
) (access.Context, bool) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		writeAccessProblem(writer, request, routes.logger, access.ErrInvalidCredential, true)

		return access.Context{}, false
	}

	return authority, true
}

func (routes *ProfileRoutes) authorityAndProfile(
	writer http.ResponseWriter,
	request *http.Request,
) (access.Context, id.Profile, bool) {
	authority, ok := routes.authority(writer, request)
	if !ok {
		return access.Context{}, id.Profile{}, false
	}
	identifier, err := id.ParseProfile(chi.URLParam(request, "profileID"))
	if err != nil {
		routes.problem(writer, request, verification.ErrProfileNotFound)

		return access.Context{}, id.Profile{}, false
	}

	return authority, identifier, true
}

func (routes *ProfileRoutes) commandHeaders(
	writer http.ResponseWriter,
	request *http.Request,
) (int64, string, bool) {
	expected, err := parseIfMatch(request.Header.Values("If-Match"))
	if err != nil {
		routes.problem(writer, request, err)

		return 0, "", false
	}
	key, err := parseIdempotencyKey(request.Header.Values("Idempotency-Key"))
	if err != nil {
		routes.problem(writer, request, err)

		return 0, "", false
	}

	return expected, key, true
}

func decodeJSONBody[T any](request *http.Request) (T, error) {
	var result T
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return result, errors.New("content type must be application/json")
	}
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, fmt.Errorf("decode JSON request: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return result, errors.New("decode JSON request: trailing JSON value")
	}

	return result, nil
}

func decodeStrictJSON(encoded []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON value")
	}

	return nil
}

func parseIdempotencyKey(values []string) (string, error) {
	if len(values) != 1 || len(values[0]) < 3 || len(values[0]) > 130 {
		return "", invalidRequest(errors.New("one Idempotency-Key structured string is required"))
	}
	encoded := values[0]
	if encoded[0] != '"' || encoded[len(encoded)-1] != '"' {
		return "", invalidRequest(errors.New("Idempotency-Key must be an RFC 9651 string"))
	}
	var result strings.Builder
	result.Grow(len(encoded) - 2)
	for index := 1; index < len(encoded)-1; index++ {
		character := encoded[index]
		if character == '\\' {
			index++
			if index >= len(encoded)-1 || (encoded[index] != '\\' && encoded[index] != '"') {
				return "", invalidRequest(errors.New("Idempotency-Key contains an invalid escape"))
			}
			result.WriteByte(encoded[index])

			continue
		}
		if character < 0x20 || character > 0x7e || character == '"' {
			return "", invalidRequest(errors.New("Idempotency-Key contains an invalid character"))
		}
		result.WriteByte(character)
	}
	if result.Len() == 0 {
		return "", invalidRequest(errors.New("Idempotency-Key is empty"))
	}

	return result.String(), nil
}

func parseIfMatch(values []string) (int64, error) {
	if len(values) == 0 {
		return 0, apierror.New(
			http.StatusPreconditionRequired,
			apierror.CodePreconditionRequired,
			"Precondition required",
			"A strong If-Match entity tag is required.",
			nil,
		)
	}
	if len(values) != 1 || strings.HasPrefix(values[0], "W/") || strings.Contains(values[0], ",") {
		return 0, invalidRequest(errors.New("If-Match must contain one strong entity tag"))
	}
	value, err := strconv.Unquote(values[0])
	if err != nil {
		return 0, invalidRequest(errors.New("If-Match is malformed"))
	}
	version, err := strconv.ParseInt(value, 10, 64)
	if err != nil || version <= 0 {
		return 0, invalidRequest(errors.New("If-Match must contain a positive version"))
	}

	return version, nil
}

func parseListQuery(request *http.Request) (int, string, error) {
	query := request.URL.Query()
	for name, values := range query {
		if name != "limit" && name != "cursor" {
			return 0, "", fmt.Errorf("unknown query parameter %q", name)
		}
		if len(values) != 1 {
			return 0, "", fmt.Errorf("query parameter %q must appear once", name)
		}
	}
	limit := 25
	if encoded := query.Get("limit"); encoded != "" {
		parsed, err := strconv.Atoi(encoded)
		if err != nil || parsed < 1 || parsed > 100 {
			return 0, "", errors.New("limit must be from 1 to 100")
		}
		limit = parsed
	}

	return limit, query.Get("cursor"), nil
}

func profileResponse(profile verification.CaptureProfile) openapiv1.CaptureProfile {
	return openapiv1.CaptureProfile{
		ID:                profile.ID().String(),
		Name:              profile.Name(),
		State:             openapiv1.CaptureProfileState(profile.State()),
		Version:           profile.Version(),
		LatestRevision:    int(profile.LatestRevision()),
		DraftRevision:     intPointer(profile.DraftRevision()),
		PublishedRevision: intPointer(profile.PublishedRevision()),
		CreatedAt:         profile.CreatedAt(),
		UpdatedAt:         profile.UpdatedAt(),
		DeactivatedAt:     profile.DeactivatedAt(),
	}
}

func mutationResponse(result verification.MutationResult) openapiv1.CaptureProfileMutation {
	return openapiv1.CaptureProfileMutation{
		ProfileID:         result.ProfileID,
		Name:              result.Name,
		State:             openapiv1.CaptureProfileMutationState(result.State),
		Version:           result.Version,
		LatestRevision:    int(result.LatestRevision),
		DraftRevision:     intPointer(result.DraftRevision),
		PublishedRevision: intPointer(result.PublishedRevision),
		Revision:          int(result.Revision),
		Digest:            result.Digest,
		UpdatedAt:         result.UpdatedAt,
	}
}

func revisionResponse(revision verification.Revision, document []byte) openapiv1.CaptureProfileRevision {
	return openapiv1.CaptureProfileRevision{
		ProfileID:   revision.ProfileID().String(),
		Revision:    int(revision.Number()),
		State:       openapiv1.CaptureProfileRevisionState(revision.State()),
		Document:    append(json.RawMessage(nil), document...),
		Digest:      revision.Digest(),
		CreatedAt:   revision.CreatedAt(),
		UpdatedAt:   revision.UpdatedAt(),
		PublishedAt: revision.PublishedAt(),
		EndedAt:     revision.EndedAt(),
	}
}

func intPointer(value *uint32) *int {
	if value == nil {
		return nil
	}
	result := int(*value)

	return &result
}

func entityTag(version int64) string { return strconv.Quote(strconv.FormatInt(version, 10)) }

func invalidRequest(cause error) error {
	return apierror.New(
		http.StatusBadRequest,
		apierror.CodeInvalidRequest,
		"Invalid request",
		"The request does not satisfy the public contract.",
		cause,
	)
}
