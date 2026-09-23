package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/identity"
	"github.com/Mujhtech/idenqa/internal/privacy"
	"github.com/go-chi/chi/v5"
)

// IdentityRoutes exposes the public tenant subject and immutable identity record APIs.
type IdentityRoutes struct {
	handlerBase
	access  *AccessMiddleware
	service *identity.Service
}

// NewIdentityRoutes constructs explicit authenticated tenant routes.
func NewIdentityRoutes(a *AccessMiddleware, s *identity.Service, l *slog.Logger) (*IdentityRoutes, error) {
	if a == nil || s == nil || l == nil {
		return nil, identity.ErrInvalid
	}
	return &IdentityRoutes{
		handlerBase: newHandlerBase(l, "identity"),
		access:      a,
		service:     s,
	}, nil
}

// Register mounts subject, record, lookup and configuration operations.
func (r *IdentityRoutes) Register(router chi.Router) {
	router.With(r.access.Authorize(access.PermissionSubjectsWrite)).Post("/subjects", r.create)
	router.With(r.access.Authorize(access.PermissionSubjectsWrite)).Put("/subjects/{subjectID}", r.update)
	router.With(r.access.Authorize(access.PermissionSubjectsDelete)).Delete("/subjects/{subjectID}", r.remove)
	router.With(r.access.Authorize(access.PermissionSubjectsWrite)).Put("/subjects/{subjectID}/verifications/{verificationID}", r.link)
	router.With(r.access.Authorize(access.PermissionIdentityWrite)).Post("/subjects/{subjectID}/records", r.record)
	router.With(r.access.Authorize(access.PermissionSubjectsWrite)).Post("/subjects/{subjectID}/projection/rebuild", r.rebuild)
	router.With(r.access.Authorize(access.PermissionSubjectsRead)).Post("/subjects/lookup", r.externalLookup)
	router.With(r.access.Authorize(access.PermissionIdentityRead)).Post("/identity/identifiers/lookup", r.identifierLookup)
	router.With(r.access.Authorize(access.PermissionIdentityConfigure)).Put("/identity/configuration", r.configure)
	for _, route := range []struct{ path, kind string }{
		{"/subjects", "subjects"}, {"/subjects/{subjectID}", "subject"}, {"/subjects/{subjectID}/verifications", "verifications"},
		{"/subjects/{subjectID}/records", "records"}, {"/subjects/{subjectID}/records/{recordID}", "record"},
		{"/identity/configuration", "configuration"}, {"/identity/configuration/revisions/{version}", "configuration"},
		{"/identity/receipts/{digest}", "receipt"},
	} {
		router.With(r.access.Authenticate).Get(route.path, func(w http.ResponseWriter, q *http.Request) { r.read(w, q, route.kind) })
	}
}
func (r *IdentityRoutes) create(w http.ResponseWriter, q *http.Request) {
	body, e := decodeJSONBody[struct {
		ExternalReference *string `json:"external_reference,omitempty"`
	}](q)
	if e != nil {
		r.reply(w, q, 0, nil, invalidRequest(e))
		return
	}
	r.execute(w, q, identity.Command{Operation: "create", ExternalReference: body.ExternalReference})
}
func (r *IdentityRoutes) update(w http.ResponseWriter, q *http.Request) {
	body, e := decodeJSONBody[struct {
		ExpectedVersion   int64   `json:"expected_version"`
		ExternalReference *string `json:"external_reference,omitempty"`
		State             string  `json:"state,omitempty"`
	}](q)
	if e != nil {
		r.reply(w, q, 0, nil, invalidRequest(e))
		return
	}
	r.execute(w, q, identity.Command{Operation: "update", ExpectedVersion: body.ExpectedVersion, ExternalReference: body.ExternalReference, State: body.State})
}
func (r *IdentityRoutes) remove(w http.ResponseWriter, q *http.Request) {
	version, e := strconv.ParseInt(q.URL.Query().Get("expected_version"), 10, 64)
	if e != nil {
		r.reply(w, q, 0, nil, invalidRequest(e))
		return
	}
	r.execute(w, q, identity.Command{Operation: "delete", ExpectedVersion: version})
}
func (r *IdentityRoutes) link(w http.ResponseWriter, q *http.Request) { r.versionCommand(w, q, "link") }
func (r *IdentityRoutes) rebuild(w http.ResponseWriter, q *http.Request) {
	r.versionCommand(w, q, "rebuild")
}
func (r *IdentityRoutes) versionCommand(w http.ResponseWriter, q *http.Request, operation string) {
	body, e := decodeJSONBody[struct {
		ExpectedVersion int64 `json:"expected_version"`
	}](q)
	if e != nil {
		r.reply(w, q, 0, nil, invalidRequest(e))
		return
	}
	r.execute(w, q, identity.Command{Operation: operation, ExpectedVersion: body.ExpectedVersion, VerificationID: chi.URLParam(q, "verificationID")})
}
func (r *IdentityRoutes) record(w http.ResponseWriter, q *http.Request) {
	body, e := decodeJSONBody[struct {
		ExpectedVersion int64                `json:"expected_version"`
		Record          identity.RecordInput `json:"record"`
	}](q)
	if e != nil {
		r.reply(w, q, 0, nil, invalidRequest(e))
		return
	}
	r.execute(w, q, identity.Command{Operation: "record", ExpectedVersion: body.ExpectedVersion, Record: &body.Record})
}
func (r *IdentityRoutes) configure(w http.ResponseWriter, q *http.Request) {
	body, e := decodeJSONBody[struct {
		ExpectedVersion int64                  `json:"expected_version"`
		Configuration   identity.Configuration `json:"configuration"`
	}](q)
	if e != nil {
		r.reply(w, q, 0, nil, invalidRequest(e))
		return
	}
	r.execute(w, q, identity.Command{Operation: "configure", ExpectedVersion: body.ExpectedVersion, Configuration: &body.Configuration})
}
func (r *IdentityRoutes) execute(w http.ResponseWriter, q *http.Request, c identity.Command) {
	key, e := parseIdempotencyKey(q.Header.Values("Idempotency-Key"))
	if e != nil {
		r.reply(w, q, 0, nil, e)
		return
	}
	c.SubjectID = chi.URLParam(q, "subjectID")
	auth, _ := AccessContext(q.Context())
	result, e := r.service.Execute(q.Context(), auth, key, c)
	status := 200
	if c.Operation == "create" || c.Operation == "record" {
		status = 201
	}
	if c.Operation == "delete" {
		status = 202
	}
	r.reply(w, q, status, result, e)
}
func identityQuery(q *http.Request, kind string) (identity.Query, error) {
	query := identity.Query{Kind: kind, SubjectID: chi.URLParam(q, "subjectID"), After: q.URL.Query().Get("after"), RecordKind: q.URL.Query().Get("kind")}
	if values, ok := q.URL.Query()["limit"]; ok {
		if len(values) != 1 {
			return query, identity.ErrInvalid
		}
		n, e := strconv.Atoi(values[0])
		if e != nil {
			return query, identity.ErrInvalid
		}
		query.Limit = n
	}
	for name, destination := range map[string]*bool{"reveal": &query.Reveal, "current": &query.Current} {
		if values, ok := q.URL.Query()[name]; ok {
			if len(values) != 1 || (values[0] != "true" && values[0] != "false") {
				return query, identity.ErrInvalid
			}
			*destination = values[0] == "true"
		}
	}
	switch kind {
	case "record":
		query.Reference = chi.URLParam(q, "recordID")
	case "receipt":
		query.Reference = chi.URLParam(q, "digest")
	case "configuration":
		query.Reference = chi.URLParam(q, "version")
	}
	return query, nil
}
func (r *IdentityRoutes) read(w http.ResponseWriter, q *http.Request, kind string) {
	query, e := identityQuery(q, kind)
	if e != nil {
		r.reply(w, q, 0, nil, e)
		return
	}
	r.query(w, q, query)
}
func (r *IdentityRoutes) externalLookup(w http.ResponseWriter, q *http.Request) {
	body, e := decodeJSONBody[struct {
		ExternalReference string `json:"external_reference"`
		After             string `json:"after,omitempty"`
		Limit             int    `json:"limit,omitempty"`
	}](q)
	if e != nil {
		r.reply(w, q, 0, nil, invalidRequest(e))
		return
	}
	r.query(w, q, identity.Query{Kind: "external_lookup", ExternalReference: body.ExternalReference, After: body.After, Limit: body.Limit})
}
func (r *IdentityRoutes) identifierLookup(w http.ResponseWriter, q *http.Request) {
	body, e := decodeJSONBody[struct {
		Identifier identity.Lookup `json:"identifier"`
		After      string          `json:"after,omitempty"`
		Limit      int             `json:"limit,omitempty"`
	}](q)
	if e != nil {
		r.reply(w, q, 0, nil, invalidRequest(e))
		return
	}
	r.query(w, q, identity.Query{Kind: "identifier_lookup", Identifier: &body.Identifier, After: body.After, Limit: body.Limit})
}
func (r *IdentityRoutes) query(w http.ResponseWriter, q *http.Request, query identity.Query) {
	auth, _ := AccessContext(q.Context())
	result, e := r.service.Read(q.Context(), auth, query)
	r.reply(w, q, 200, result, e)
}
func (r *IdentityRoutes) reply(w http.ResponseWriter, q *http.Request, status int, value any, e error) {
	w.Header().Set("Cache-Control", "no-store")
	if e != nil {
		// The shared table maps privacy.ErrInvalid to the lifecycle not-found
		// response; identity reads raise it for request validation instead.
		if errors.Is(e, privacy.ErrInvalid) {
			e = invalidRequest(e)
		}
		r.problem(w, q, e)

		return
	}
	r.writeJSON(w, q, status, value)
}
