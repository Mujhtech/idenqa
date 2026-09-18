package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
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
}

// PrivacyRoutes exposes safe observable lifecycle administration.
type PrivacyRoutes struct {
	access  *AccessMiddleware
	service PrivacyService
	logger  *slog.Logger
}

// NewPrivacyRoutes constructs privacy administration routes.
func NewPrivacyRoutes(accessMiddleware *AccessMiddleware, service PrivacyService, logger *slog.Logger) (*PrivacyRoutes, error) {
	if accessMiddleware == nil || service == nil || logger == nil {
		return nil, errors.New("privacy route dependencies are required")
	}
	return &PrivacyRoutes{access: accessMiddleware, service: service, logger: logger}, nil
}

// Register mounts privacy administration endpoints on router.
func (routes *PrivacyRoutes) Register(router chi.Router) {
	deletions := []func(http.Handler) http.Handler{routes.access.Authenticate, routes.access.Require(access.PermissionDeletionsWrite)}
	holds := []func(http.Handler) http.Handler{routes.access.Authenticate, routes.access.Require(access.PermissionLegalHoldsWrite)}
	router.With(deletions...).Post("/deletions", routes.requestDeletion)
	router.With(deletions...).Post("/deletions/{deletionID}/run", routes.runDeletion)
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
