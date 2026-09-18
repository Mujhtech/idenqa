package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/fraud"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/apierror"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/go-chi/chi/v5"
)

// FraudRoutes is the authenticated open-source tenant risk surface.
type FraudRoutes struct {
	access  *AccessMiddleware
	service *fraud.Service
	logger  *slog.Logger
}

// NewFraudRoutes constructs tenant API-key protected fraud routes.
func NewFraudRoutes(a *AccessMiddleware, s *fraud.Service, l *slog.Logger) (*FraudRoutes, error) {
	if a == nil || s == nil || l == nil {
		return nil, fraud.ErrInvalid
	}
	return &FraudRoutes{a, s, l}, nil
}

// Register mounts closed commands and safe receipt reads.
func (r *FraudRoutes) Register(router chi.Router) {
	router.With(r.access.Authenticate, r.access.Require(access.PermissionFraudRead)).Get("/fraud/configuration/revisions/{version}", func(w http.ResponseWriter, q *http.Request) {
		r.read(w, q, "configuration_revision", chi.URLParam(q, "version"))
	})
	router.With(r.access.Authenticate, r.access.Require(access.PermissionFraudRead)).Get("/fraud/proposals/{digest}", func(w http.ResponseWriter, q *http.Request) { r.read(w, q, "proposal", chi.URLParam(q, "digest")) })

	router.With(r.access.Authenticate, r.access.Require(access.PermissionFraudConfigure)).Put("/fraud/configuration", r.configure)
	router.With(r.access.Authenticate, r.access.Require(access.PermissionFraudWrite)).Post("/fraud/inputs", r.ingest)
	router.With(r.access.Authenticate, r.access.Require(access.PermissionFraudWrite)).Post("/fraud/proposals", r.propose)
	router.With(r.access.Authenticate, r.access.Require(access.PermissionFraudRead)).Get("/fraud/configuration", func(w http.ResponseWriter, q *http.Request) { r.read(w, q, "configuration", "") })
	router.With(r.access.Authenticate, r.access.Require(access.PermissionFraudRead)).Get("/fraud/receipts/{digest}", func(w http.ResponseWriter, q *http.Request) { r.read(w, q, "receipt", chi.URLParam(q, "digest")) })
}
func (r *FraudRoutes) configure(w http.ResponseWriter, q *http.Request) {
	body, e := decodeJSONBody[struct {
		ExpectedVersion int64               `json:"expected_version"`
		Configuration   fraud.Configuration `json:"configuration"`
	}](q)
	if e != nil {
		r.reply(w, q, nil, invalidRequest(e))
		return
	}
	r.execute(w, q, fraud.Command{Operation: "configure", ExpectedVersion: body.ExpectedVersion, Configuration: &body.Configuration})
}
func (r *FraudRoutes) ingest(w http.ResponseWriter, q *http.Request) {
	body, e := decodeJSONBody[fraud.Input](q)
	if e != nil {
		r.reply(w, q, nil, invalidRequest(e))
		return
	}
	r.execute(w, q, fraud.Command{Operation: "ingest", Input: &body})
}
func (r *FraudRoutes) propose(w http.ResponseWriter, q *http.Request) {
	body, e := decodeJSONBody[fraud.Proposal](q)
	if e != nil {
		r.reply(w, q, nil, invalidRequest(e))
		return
	}
	r.execute(w, q, fraud.Command{Operation: "propose", Proposal: &body})
}
func (r *FraudRoutes) execute(w http.ResponseWriter, q *http.Request, c fraud.Command) {
	key, e := parseIdempotencyKey(q.Header.Values("Idempotency-Key"))
	if e != nil {
		r.reply(w, q, nil, e)
		return
	}
	auth, _ := AccessContext(q.Context())
	result, e := r.service.Execute(q.Context(), auth, key, c)
	r.reply(w, q, result, e)
}
func (r *FraudRoutes) read(w http.ResponseWriter, q *http.Request, kind, ref string) {
	auth, _ := AccessContext(q.Context())
	result, e := r.service.Read(q.Context(), auth, kind, ref)
	r.reply(w, q, result, e)
}
func (r *FraudRoutes) reply(w http.ResponseWriter, q *http.Request, value any, e error) {
	w.Header().Set("Cache-Control", "no-store")
	if e != nil {
		switch {
		case errors.Is(e, fraud.ErrInvalid):
			e = invalidRequest(e)
		case errors.Is(e, fraud.ErrNotFound):
			e = apierror.New(404, apierror.CodeNotFound, "Not found", "The fraud resource was not found.", e)
		case errors.Is(e, fraud.ErrConflict):
			e = apierror.New(409, apierror.CodeConflict, "Conflict", "The fraud revision or source conflicts with current state.", e)
		case errors.Is(e, fraud.ErrUnavailable):
			e = apierror.New(503, apierror.CodeServiceUnavailable, "Unavailable", "Fraud key custody is unavailable.", e)
		}
		if err := respond.WriteProblem(w, q, e, requestIDString(q.Context())); err != nil {
			r.logger.ErrorContext(q.Context(), "write fraud problem")
		}
		return
	}
	if err := respond.JSON(w, q, 200, value); err != nil {
		r.logger.ErrorContext(q.Context(), "write fraud response")
	}
}
