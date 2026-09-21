package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/support"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/apierror"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/go-chi/chi/v5"
)

// SupportRoutes is the authenticated delegated and emergency access surface.
type SupportRoutes struct {
	access  *AccessMiddleware
	service *support.Service
	logger  *slog.Logger
}

// NewSupportRoutes constructs API-key protected support-access routes.
func NewSupportRoutes(a *AccessMiddleware, s *support.Service, l *slog.Logger) (*SupportRoutes, error) {
	if a == nil || s == nil || l == nil {
		return nil, support.ErrInvalid
	}

	return &SupportRoutes{access: a, service: s, logger: l}, nil
}

// Register mounts delegated-grant and break-glass commands with safe reads.
func (r *SupportRoutes) Register(router chi.Router) {
	router.With(r.access.Authenticate, r.access.Require(access.PermissionSupportAccessRead)).
		Get("/support/grants", r.listGrants)
	router.With(r.access.Authenticate, r.access.Require(access.PermissionSupportAccessRead)).
		Get("/support/grants/{id}", r.readGrant)
	router.With(r.access.Authenticate, r.access.Require(access.PermissionSupportAccessWrite)).
		Post("/support/grants", r.grant)
	router.With(r.access.Authenticate, r.access.Require(access.PermissionSupportAccessWrite)).
		Post("/support/grants/{id}/revoke", r.revokeGrant)

	router.With(r.access.Authenticate, r.access.Require(access.PermissionSupportAccessRead)).
		Get("/support/break-glass/{id}", r.readEmergency)
	router.With(r.access.Authenticate, r.access.Require(access.PermissionBreakGlassRequest)).
		Post("/support/break-glass", r.requestEmergency)
	router.With(r.access.Authenticate, r.access.Require(access.PermissionBreakGlassApprove)).
		Post("/support/break-glass/{id}/approve", r.approveEmergency)
	router.With(r.access.Authenticate, r.access.Require(access.PermissionBreakGlassApprove)).
		Post("/support/break-glass/{id}/deny", r.denyEmergency)
	router.With(r.access.Authenticate, r.access.Require(access.PermissionSupportAccessWrite)).
		Post("/support/break-glass/{id}/revoke", r.revokeEmergency)
	router.With(r.access.Authenticate, r.access.Require(access.PermissionBreakGlassUse)).
		Post("/support/break-glass/{id}/uses", r.useEmergency)
}

func (r *SupportRoutes) grant(w http.ResponseWriter, q *http.Request) {
	body, err := decodeJSONBody[struct {
		Grantee  string   `json:"grantee"`
		Patterns []string `json:"patterns"`
		Duration string   `json:"duration"`
		Reason   string   `json:"reason"`
	}](q)
	if err != nil {
		r.reply(w, q, nil, invalidRequest(err))

		return
	}
	duration, err := time.ParseDuration(body.Duration)
	if err != nil {
		r.reply(w, q, nil, invalidRequest(errors.New("duration must be a Go duration such as 24h")))

		return
	}
	r.execute(w, q, support.Command{Operation: "grant", Grantee: body.Grantee, Patterns: body.Patterns, Duration: duration, Reason: body.Reason})
}

func (r *SupportRoutes) revokeGrant(w http.ResponseWriter, q *http.Request) {
	r.versioned(w, q, "grant_revoke")
}

func (r *SupportRoutes) requestEmergency(w http.ResponseWriter, q *http.Request) {
	body, err := decodeJSONBody[struct {
		Permissions []string `json:"permissions"`
		Duration    string   `json:"duration"`
		Reason      string   `json:"reason"`
	}](q)
	if err != nil {
		r.reply(w, q, nil, invalidRequest(err))

		return
	}
	duration, err := time.ParseDuration(body.Duration)
	if err != nil {
		r.reply(w, q, nil, invalidRequest(errors.New("duration must be a Go duration such as 1h")))

		return
	}
	r.execute(w, q, support.Command{Operation: "break_glass_request", Permissions: body.Permissions, Duration: duration, Reason: body.Reason})
}

func (r *SupportRoutes) approveEmergency(w http.ResponseWriter, q *http.Request) {
	r.versioned(w, q, "break_glass_approve")
}

func (r *SupportRoutes) denyEmergency(w http.ResponseWriter, q *http.Request) {
	r.versioned(w, q, "break_glass_deny")
}

func (r *SupportRoutes) revokeEmergency(w http.ResponseWriter, q *http.Request) {
	r.versioned(w, q, "break_glass_revoke")
}

func (r *SupportRoutes) useEmergency(w http.ResponseWriter, q *http.Request) {
	body, err := decodeJSONBody[struct {
		ExpectedVersion int64    `json:"expected_version"`
		Permissions     []string `json:"permissions"`
		Target          string   `json:"target"`
	}](q)
	if err != nil {
		r.reply(w, q, nil, invalidRequest(err))

		return
	}
	r.execute(w, q, support.Command{
		Operation: "break_glass_use", Identifier: chi.URLParam(q, "id"),
		ExpectedVersion: body.ExpectedVersion, Permissions: body.Permissions, Target: body.Target,
		Reason: "break-glass use recorded",
	})
}

func (r *SupportRoutes) versioned(w http.ResponseWriter, q *http.Request, operation string) {
	body, err := decodeJSONBody[struct {
		ExpectedVersion int64  `json:"expected_version"`
		Reason          string `json:"reason"`
	}](q)
	if err != nil {
		r.reply(w, q, nil, invalidRequest(err))

		return
	}
	if strings.TrimSpace(body.Reason) == "" && operation == "break_glass_approve" {
		body.Reason = "approved break-glass request"
	}
	if strings.TrimSpace(body.Reason) == "" && operation == "break_glass_deny" {
		body.Reason = "denied break-glass request"
	}
	r.execute(w, q, support.Command{
		Operation: operation, Identifier: chi.URLParam(q, "id"),
		ExpectedVersion: body.ExpectedVersion, Reason: body.Reason,
	})
}

func (r *SupportRoutes) listGrants(w http.ResponseWriter, q *http.Request) {
	r.read(w, q, "grants", "")
}

func (r *SupportRoutes) readGrant(w http.ResponseWriter, q *http.Request) {
	r.read(w, q, "grant", chi.URLParam(q, "id"))
}

func (r *SupportRoutes) readEmergency(w http.ResponseWriter, q *http.Request) {
	r.read(w, q, "emergency", chi.URLParam(q, "id"))
}

func (r *SupportRoutes) read(w http.ResponseWriter, q *http.Request, kind, reference string) {
	auth, _ := AccessContext(q.Context())
	result, err := r.service.Read(q.Context(), auth, kind, reference)
	r.reply(w, q, result, err)
}

func (r *SupportRoutes) execute(w http.ResponseWriter, q *http.Request, command support.Command) {
	key, err := parseIdempotencyKey(q.Header.Values("Idempotency-Key"))
	if err != nil {
		r.reply(w, q, nil, err)

		return
	}
	auth, _ := AccessContext(q.Context())
	result, err := r.service.Execute(q.Context(), auth, key, command)
	r.reply(w, q, result, err)
}

func (r *SupportRoutes) reply(w http.ResponseWriter, q *http.Request, value any, err error) {
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		switch {
		case errors.Is(err, support.ErrInvalid):
			err = invalidRequest(err)
		case errors.Is(err, support.ErrForbidden):
			err = apierror.New(403, apierror.CodeInsufficientScope, "Forbidden", "The support operation is not permitted.", err)
		case errors.Is(err, support.ErrNotFound):
			err = apierror.New(404, apierror.CodeNotFound, "Not found", "The support record was not found.", err)
		case errors.Is(err, support.ErrConflict):
			err = apierror.New(409, apierror.CodeConflict, "Conflict", "The support record changed since it was read.", err)
		case errors.Is(err, support.ErrExpired):
			err = apierror.New(409, apierror.CodeConflict, "Conflict", "The support access window has expired.", err)
		case errors.Is(err, support.ErrUnavailable):
			err = apierror.New(503, apierror.CodeServiceUnavailable, "Unavailable", "Support access is unavailable.", err)
		}
		if writeErr := respond.WriteProblem(w, q, err, requestIDString(q.Context())); writeErr != nil {
			r.logger.ErrorContext(q.Context(), "write support problem")
		}

		return
	}
	if err := respond.JSON(w, q, 200, value); err != nil {
		r.logger.ErrorContext(q.Context(), "write support response")
	}
}
