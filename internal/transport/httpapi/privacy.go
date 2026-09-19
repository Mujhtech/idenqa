package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/privacy"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/go-chi/chi/v5"
)

// PrivacyService defines lifecycle operations exposed by the administration routes.
type PrivacyService interface {
	RequestEvidenceDeletion(context.Context, tenant.Scope, privacy.Actor, string, string) (privacy.Deletion, error)
	Run(context.Context, tenant.Scope, privacy.Actor, id.Deletion) (privacy.Deletion, error)
	CreateHold(context.Context, tenant.Scope, privacy.Actor, string, string, string, time.Time, time.Time) (privacy.Hold, error)
	ReleaseHold(context.Context, tenant.Scope, privacy.Actor, id.LegalHold) (privacy.Hold, error)
	FindDeletion(context.Context, tenant.Scope, privacy.Actor, id.Deletion) (privacy.Deletion, error)
	ListDeletions(context.Context, tenant.Scope, privacy.Actor, string, string, int) (privacy.DeletionPage, error)
	DeletionStatus(context.Context, tenant.Scope, privacy.Actor, id.Deletion) (privacy.DeletionStatus, error)
	ResolveRetention(context.Context, tenant.Scope, privacy.Actor, string) (privacy.RetentionResolution, error)
}

// PrivacyRoutes exposes safe observable lifecycle administration.
type PrivacyRoutes struct {
	access  *AccessMiddleware
	service PrivacyService
	cursors ProfileCursor
	logger  *slog.Logger
}

// NewPrivacyRoutes constructs privacy administration routes.
func NewPrivacyRoutes(accessMiddleware *AccessMiddleware, service PrivacyService, cursors ProfileCursor, logger *slog.Logger) (*PrivacyRoutes, error) {
	if accessMiddleware == nil || service == nil || cursors == nil || logger == nil {
		return nil, errors.New("privacy route dependencies are required")
	}
	return &PrivacyRoutes{access: accessMiddleware, service: service, cursors: cursors, logger: logger}, nil
}

// Register mounts privacy administration endpoints on router.
func (routes *PrivacyRoutes) Register(router chi.Router) {
	deletions := []func(http.Handler) http.Handler{routes.access.Authenticate, routes.access.Require(access.PermissionDeletionsWrite)}
	holds := []func(http.Handler) http.Handler{routes.access.Authenticate, routes.access.Require(access.PermissionLegalHoldsWrite)}
	reads := []func(http.Handler) http.Handler{routes.access.Authenticate, routes.access.Require(access.PermissionDeletionsRead)}
	router.With(deletions...).Post("/deletions", routes.requestDeletion)
	router.With(deletions...).Post("/deletions/{deletionID}/run", routes.runDeletion)
	router.With(reads...).Get("/deletions", routes.listDeletions)
	router.With(reads...).Get("/deletions/{deletionID}", routes.deletionStatus)
	router.With(reads...).Get("/retention/resolutions", routes.retentionResolution)
	router.With(holds...).Post("/legal-holds", routes.createHold)
	router.With(holds...).Post("/legal-holds/{holdID}/release", routes.releaseHold)
}

type deletionRequest struct {
	AggregateID string `json:"aggregate_id"`
	Region      string `json:"region"`
}
type holdRequest struct {
	AggregateID string    `json:"aggregate_id"`
	Authority   string    `json:"authority"`
	Reason      string    `json:"reason"`
	StartsAt    time.Time `json:"starts_at"`
	ReviewAt    time.Time `json:"review_at"`
}
type deletionResource struct {
	ID              string                `json:"id"`
	AggregateID     string                `json:"aggregate_id"`
	Region          string                `json:"region"`
	State           privacy.DeletionState `json:"state"`
	TargetCount     int                   `json:"target_count"`
	BackupExpiresAt time.Time             `json:"backup_expires_at"`
	FailureClass    string                `json:"failure_class,omitempty"`
	Version         int64                 `json:"version"`
}
type holdResource struct {
	ID          string     `json:"id"`
	AggregateID string     `json:"aggregate_id"`
	Authority   string     `json:"authority"`
	Reason      string     `json:"reason"`
	StartsAt    time.Time  `json:"starts_at"`
	ReviewAt    time.Time  `json:"review_at"`
	ReleasedAt  *time.Time `json:"released_at,omitempty"`
}

func (routes *PrivacyRoutes) authority(request *http.Request, permissions ...privacy.Permission) (access.Context, privacy.Actor, bool) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		return access.Context{}, privacy.Actor{}, false
	}
	return authority, privacy.Actor{ID: authority.Principal().KeyID().String(), Permissions: permissions}, true
}

func (routes *PrivacyRoutes) requestDeletion(writer http.ResponseWriter, request *http.Request) {
	authority, actor, ok := routes.authority(request, privacy.PermissionRequestDeletion)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	body, err := decodeJSONBody[deletionRequest](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	value, err := routes.service.RequestEvidenceDeletion(request.Context(), authority.TenantScope(), actor, body.AggregateID, body.Region)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeDeletion(writer, request, http.StatusAccepted, value)
}
func (routes *PrivacyRoutes) runDeletion(writer http.ResponseWriter, request *http.Request) {
	authority, actor, ok := routes.authority(request, privacy.PermissionRunDeletion)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	identifier, err := id.ParseDeletion(chi.URLParam(request, "deletionID"))
	if err != nil {
		routes.problem(writer, request, privacy.ErrInvalid)
		return
	}
	value, err := routes.service.Run(request.Context(), authority.TenantScope(), actor, identifier)
	if err != nil && !errors.Is(err, privacy.ErrHeld) {
		routes.problem(writer, request, err)
		return
	}
	routes.writeDeletion(writer, request, http.StatusOK, value)
}
func (routes *PrivacyRoutes) createHold(writer http.ResponseWriter, request *http.Request) {
	authority, actor, ok := routes.authority(request, privacy.PermissionManageHold)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	body, err := decodeJSONBody[holdRequest](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	value, err := routes.service.CreateHold(request.Context(), authority.TenantScope(), actor, body.AggregateID, body.Authority, body.Reason, body.StartsAt, body.ReviewAt)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeHold(writer, request, http.StatusCreated, value)
}
func (routes *PrivacyRoutes) releaseHold(writer http.ResponseWriter, request *http.Request) {
	authority, actor, ok := routes.authority(request, privacy.PermissionManageHold)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	identifier, err := id.ParseLegalHold(chi.URLParam(request, "holdID"))
	if err != nil {
		routes.problem(writer, request, privacy.ErrInvalid)
		return
	}
	value, err := routes.service.ReleaseHold(request.Context(), authority.TenantScope(), actor, identifier)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeHold(writer, request, http.StatusOK, value)
}
func (routes *PrivacyRoutes) listDeletions(writer http.ResponseWriter, request *http.Request) {
	authority, actor, ok := routes.authority(request, privacy.PermissionReadDeletion)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	limit, token, aggregateID, err := parsePrivacyListQuery(request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	bound := "privacy:deletions;aggregate_id=" + aggregateID + ";limit=" + strconv.Itoa(limit)
	position := ""
	if token != "" {
		claims, decodeErr := routes.cursors.Decode(token, authority.TenantScope().ID(), bound)
		if decodeErr != nil {
			routes.problem(writer, request, invalidRequest(decodeErr))
			return
		}
		if unmarshalErr := json.Unmarshal(claims.Position, &position); unmarshalErr != nil || !privacyToken(position, 64) {
			routes.problem(writer, request, invalidRequest(errors.New("cursor position is invalid")))
			return
		}
	}
	page, err := routes.service.ListDeletions(request.Context(), authority.TenantScope(), actor, aggregateID, position, limit)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	data := make([]openapiv1.PrivacyDeletionSummary, 0, len(page.Deletions))
	for _, deletion := range page.Deletions {
		data = append(data, privacyDeletionSummary(deletion))
	}
	result := openapiv1.PrivacyDeletionList{Data: data, Page: openapiv1.Page{HasMore: page.HasMore}}
	if page.HasMore {
		encoded, marshalErr := json.Marshal(page.Deletions[len(page.Deletions)-1].ID.String())
		if marshalErr != nil {
			routes.problem(writer, request, marshalErr)
			return
		}
		next, encodeErr := routes.cursors.Encode(authority.TenantScope().ID(), bound, encoded)
		if encodeErr != nil {
			routes.problem(writer, request, encodeErr)
			return
		}
		result.Page.NextCursor = &next
	}
	routes.write(writer, request, http.StatusOK, result)
}
func (routes *PrivacyRoutes) deletionStatus(writer http.ResponseWriter, request *http.Request) {
	authority, actor, ok := routes.authority(request, privacy.PermissionReadDeletion)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	identifier, err := id.ParseDeletion(chi.URLParam(request, "deletionID"))
	if err != nil {
		routes.problem(writer, request, privacy.ErrInvalid)
		return
	}
	value, err := routes.service.DeletionStatus(request.Context(), authority.TenantScope(), actor, identifier)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.write(writer, request, http.StatusOK, privacyDeletionStatus(value))
}
func (routes *PrivacyRoutes) retentionResolution(writer http.ResponseWriter, request *http.Request) {
	authority, actor, ok := routes.authority(request, privacy.PermissionReadDeletion)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	query := request.URL.Query()
	if len(query) != 1 || len(query["aggregate_id"]) != 1 || !privacyToken(query.Get("aggregate_id"), 200) {
		routes.problem(writer, request, invalidRequest(errors.New("aggregate_id must appear once and be a bounded token")))
		return
	}
	value, err := routes.service.ResolveRetention(request.Context(), authority.TenantScope(), actor, query.Get("aggregate_id"))
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.write(writer, request, http.StatusOK, privacyRetentionResolution(value))
}
func parsePrivacyListQuery(request *http.Request) (int, string, string, error) {
	query := request.URL.Query()
	for name, values := range query {
		if name != "limit" && name != "cursor" && name != "aggregate_id" {
			return 0, "", "", errors.New("unknown query parameter " + strconv.Quote(name))
		}
		if len(values) != 1 {
			return 0, "", "", errors.New("query parameter " + strconv.Quote(name) + " must appear once")
		}
	}
	limit := 25
	if encoded := query.Get("limit"); encoded != "" {
		parsed, err := strconv.Atoi(encoded)
		if err != nil || parsed < 1 || parsed > 100 {
			return 0, "", "", errors.New("limit must be from 1 to 100")
		}
		limit = parsed
	}
	aggregateID := query.Get("aggregate_id")
	if aggregateID != "" && !privacyToken(aggregateID, 200) {
		return 0, "", "", errors.New("aggregate_id must be a bounded token")
	}
	cursor := query.Get("cursor")
	if cursor != "" && len(cursor) > 1024 {
		return 0, "", "", errors.New("cursor is malformed")
	}
	return limit, cursor, aggregateID, nil
}
func privacyToken(value string, maximum int) bool {
	if len(value) == 0 || len(value) > maximum {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}
func privacyDeletionSummary(value privacy.Deletion) openapiv1.PrivacyDeletionSummary {
	return openapiv1.PrivacyDeletionSummary{
		ID: value.ID.String(), AggregateID: value.AggregateID, Region: value.Region, State: openapiv1.PrivacyDeletionState(value.State),
		TargetCount: len(value.Targets), BackupExpiresAt: value.BackupExpiresAt, FailureClass: optionalString(value.FailureClass),
		Version: value.Version, RequestedAt: value.RequestedAt, UpdatedAt: value.UpdatedAt,
	}
}
func privacyDeletionStatus(value privacy.DeletionStatus) openapiv1.PrivacyDeletionStatus {
	targets := make([]openapiv1.PrivacyTarget, 0, len(value.Targets))
	for _, target := range value.Targets {
		targets = append(targets, openapiv1.PrivacyTarget{
			Kind: target.Kind, Reference: target.Reference, State: openapiv1.PrivacyTargetState(target.State),
			FailureClass: optionalString(target.FailureClass),
		})
	}
	return openapiv1.PrivacyDeletionStatus{
		ID: value.Deletion.ID.String(), AggregateID: value.Deletion.AggregateID, Region: value.Deletion.Region,
		State: openapiv1.PrivacyDeletionState(value.Deletion.State), TargetCount: len(value.Deletion.Targets),
		BackupExpiresAt: value.Deletion.BackupExpiresAt, FailureClass: optionalString(value.Deletion.FailureClass),
		Version: value.Deletion.Version, RequestedAt: value.Deletion.RequestedAt, UpdatedAt: value.Deletion.UpdatedAt,
		Targets: targets, Holds: privacyHolds(value.Holds),
	}
}
func privacyRetentionResolution(value privacy.RetentionResolution) openapiv1.PrivacyRetentionResolution {
	records := make([]openapiv1.PrivacyRetentionRecord, 0, len(value.Records))
	for _, record := range value.Records {
		records = append(records, openapiv1.PrivacyRetentionRecord{
			ID: record.ID, DataClass: openapiv1.PrivacyRetentionRecordDataClass(record.Class), Region: record.Region,
			DurationSeconds: int64(record.Duration / time.Second), ExpiresAt: record.ExpiresAt,
		})
	}
	return openapiv1.PrivacyRetentionResolution{AggregateID: value.AggregateID, Records: records, Holds: privacyHolds(value.Holds)}
}
func privacyHolds(values []privacy.Hold) []openapiv1.PrivacyHold {
	holds := make([]openapiv1.PrivacyHold, 0, len(values))
	for _, hold := range values {
		holds = append(holds, openapiv1.PrivacyHold{
			ID: hold.ID.String(), AggregateID: hold.AggregateID, Authority: hold.Authority, Reason: hold.Reason,
			StartsAt: hold.StartsAt, ReviewAt: hold.ReviewAt, ReleasedAt: optionalTime(hold.ReleasedAt),
		})
	}
	return holds
}
func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
func optionalTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}
func (routes *PrivacyRoutes) writeDeletion(writer http.ResponseWriter, request *http.Request, status int, value privacy.Deletion) {
	routes.write(writer, request, status, deletionResource{ID: value.ID.String(), AggregateID: value.AggregateID, Region: value.Region, State: value.State, TargetCount: len(value.Targets), BackupExpiresAt: value.BackupExpiresAt, FailureClass: value.FailureClass, Version: value.Version})
}
func (routes *PrivacyRoutes) writeHold(writer http.ResponseWriter, request *http.Request, status int, value privacy.Hold) {
	var released *time.Time
	if !value.ReleasedAt.IsZero() {
		releasedAt := value.ReleasedAt
		released = &releasedAt
	}
	routes.write(writer, request, status, holdResource{ID: value.ID.String(), AggregateID: value.AggregateID, Authority: value.Authority, Reason: value.Reason, StartsAt: value.StartsAt, ReviewAt: value.ReviewAt, ReleasedAt: released})
}
func (routes *PrivacyRoutes) write(writer http.ResponseWriter, request *http.Request, status int, value any) {
	writer.Header().Set("Cache-Control", "no-store")
	if err := respond.JSON(writer, request, status, value); err != nil {
		routes.logger.ErrorContext(request.Context(), "write privacy response")
	}
}
func (routes *PrivacyRoutes) problem(writer http.ResponseWriter, request *http.Request, err error) {
	if writeErr := respond.WriteProblem(writer, request, err, requestIDString(request.Context())); writeErr != nil {
		routes.logger.ErrorContext(request.Context(), "write privacy failure response")
	}
}
