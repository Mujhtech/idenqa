package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/keycustody"
	"github.com/go-chi/chi/v5"
)

// KMSRoutes is the authenticated tenant HMAC key lifecycle surface.
type KMSRoutes struct {
	handlerBase
	access  *AccessMiddleware
	service *keycustody.Service
}

// NewKMSRoutes constructs API-key protected key lifecycle routes.
func NewKMSRoutes(a *AccessMiddleware, s *keycustody.Service, l *slog.Logger) (*KMSRoutes, error) {
	if a == nil || s == nil || l == nil {
		return nil, keycustody.ErrInvalid
	}

	return &KMSRoutes{
		access:      a,
		service:     s,
		handlerBase: newHandlerBase(l, "kms"),
	}, nil
}

// Register mounts closed lifecycle commands and safe metadata reads.
func (r *KMSRoutes) Register(router chi.Router) {
	router.With(r.access.Authorize(access.PermissionKMSRead)).
		Get("/kms/domains/{domain}", r.read)
	router.With(r.access.Authorize(access.PermissionKMSWrite)).
		Post("/kms/domains", r.create)
	router.With(r.access.Authorize(access.PermissionKMSWrite)).
		Post("/kms/domains/{domain}/rotate", r.rotate)
	router.With(r.access.Authorize(access.PermissionKMSWrite)).
		Post("/kms/domains/{domain}/versions/{version}/disable", r.disable)
	router.With(r.access.Authorize(access.PermissionKMSWrite)).
		Post("/kms/domains/{domain}/versions/{version}/retire", r.retire)
}

func (r *KMSRoutes) create(w http.ResponseWriter, q *http.Request) {
	body, err := decodeJSONBody[struct {
		Domain string `json:"domain"`
		Reason string `json:"reason"`
	}](q)
	if err != nil {
		r.reply(w, q, nil, invalidRequest(err))

		return
	}
	r.execute(w, q, keycustody.Command{Operation: "create", Domain: body.Domain, Reason: body.Reason})
}

func (r *KMSRoutes) rotate(w http.ResponseWriter, q *http.Request) {
	body, err := decodeJSONBody[struct {
		ExpectedVersion int64  `json:"expected_version"`
		Reason          string `json:"reason"`
	}](q)
	if err != nil {
		r.reply(w, q, nil, invalidRequest(err))

		return
	}
	r.execute(w, q, keycustody.Command{
		Operation: "rotate", Domain: chi.URLParam(q, "domain"),
		ExpectedVersion: body.ExpectedVersion, Reason: body.Reason,
	})
}

func (r *KMSRoutes) disable(w http.ResponseWriter, q *http.Request) {
	r.transition(w, q, "disable")
}

func (r *KMSRoutes) retire(w http.ResponseWriter, q *http.Request) {
	r.transition(w, q, "retire")
}

func (r *KMSRoutes) transition(w http.ResponseWriter, q *http.Request, operation string) {
	body, err := decodeJSONBody[struct {
		ExpectedVersion int64  `json:"expected_version"`
		Reason          string `json:"reason"`
	}](q)
	if err != nil {
		r.reply(w, q, nil, invalidRequest(err))

		return
	}
	version, err := strconv.ParseInt(chi.URLParam(q, "version"), 10, 64)
	if err != nil || version < 1 || version != body.ExpectedVersion {
		r.reply(w, q, nil, invalidRequest(errors.New("version must match the request body expected_version")))

		return
	}
	r.execute(w, q, keycustody.Command{
		Operation: operation, Domain: chi.URLParam(q, "domain"),
		ExpectedVersion: body.ExpectedVersion, Reason: body.Reason,
	})
}

func (r *KMSRoutes) read(w http.ResponseWriter, q *http.Request) {
	auth, _ := AccessContext(q.Context())
	result, err := r.service.Read(q.Context(), auth, chi.URLParam(q, "domain"))
	r.reply(w, q, result, err)
}

func (r *KMSRoutes) execute(w http.ResponseWriter, q *http.Request, command keycustody.Command) {
	key, err := parseIdempotencyKey(q.Header.Values("Idempotency-Key"))
	if err != nil {
		r.reply(w, q, nil, err)

		return
	}
	auth, _ := AccessContext(q.Context())
	result, err := r.service.Execute(q.Context(), auth, key, command)
	r.reply(w, q, result, err)
}
