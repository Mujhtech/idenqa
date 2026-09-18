package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/apierror"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/go-chi/chi/v5"
)

// AssuranceRoutes exposes the core assurance catalog independently of Console.
type AssuranceRoutes struct {
	access  *AccessMiddleware
	service *policy.AssuranceManagement
	logger  *slog.Logger
}

// NewAssuranceRoutes constructs authenticated assurance endpoints.
func NewAssuranceRoutes(a *AccessMiddleware, s *policy.AssuranceManagement, l *slog.Logger) (*AssuranceRoutes, error) {
	if a == nil || s == nil || l == nil {
		return nil, policy.ErrInvalid
	}
	return &AssuranceRoutes{a, s, l}, nil
}

// Register mounts the public assurance routes.
func (r *AssuranceRoutes) Register(router chi.Router) {
	router.With(r.access.Authenticate, r.access.Require(access.PermissionPoliciesRead)).Get("/assurance-capabilities", r.capabilities)
	router.With(r.access.Authenticate, r.access.Require(access.PermissionPoliciesRead)).Get("/assurance-profiles", r.read)
	router.With(r.access.Authenticate, r.access.Require(access.PermissionPoliciesRead)).Get("/assurance-profiles/{name}/revisions/{revision}", r.read)
	router.With(r.access.Authenticate, r.access.Require(access.PermissionPoliciesWrite)).Post("/assurance-profiles", r.publish)
	router.With(r.access.Authenticate, r.access.Require(access.PermissionPoliciesWrite)).Post("/assurance-profiles/validate", r.validate)
	router.With(r.access.Authenticate, r.access.Require(access.PermissionPoliciesRead)).Get("/policies/{policyID}/assurance", r.read)
	router.With(r.access.Authenticate, r.access.Require(access.PermissionPoliciesActivate)).Put("/policies/{policyID}/assurance", r.assign)
	router.With(r.access.Authenticate, r.access.Require(access.PermissionPoliciesRead)).Get("/verifications/{verificationID}/assurance", r.read)
}
func (r *AssuranceRoutes) read(w http.ResponseWriter, q *http.Request) {
	v := policy.AssuranceQuery{Name: chi.URLParam(q, "name"), PolicyID: chi.URLParam(q, "policyID"), VerificationID: chi.URLParam(q, "verificationID"), After: q.URL.Query().Get("after")}
	if v.Name != "" {
		n, e := strconv.ParseUint(chi.URLParam(q, "revision"), 10, 32)
		if e != nil {
			r.reply(w, q, nil, policy.ErrInvalid)
			return
		}
		v.Revision = uint32(n)
	}
	if values, ok := q.URL.Query()["limit"]; ok {
		if len(values) != 1 {
			r.reply(w, q, nil, policy.ErrInvalid)
			return
		}
		n, e := strconv.Atoi(values[0])
		if e != nil || n < 1 {
			r.reply(w, q, nil, policy.ErrInvalid)
			return
		}
		v.Limit = n
	}
	a, _ := AccessContext(q.Context())
	result, e := r.service.Read(q.Context(), a, v)
	r.reply(w, q, result, e)
}
func (r *AssuranceRoutes) publish(w http.ResponseWriter, q *http.Request) {
	p, e := decodeJSONBody[policy.AssuranceProfile](q)
	if e != nil {
		r.reply(w, q, nil, invalidRequest(e))
		return
	}
	r.write(w, q, policy.AssuranceCommand{Operation: "publish", Profile: &p})
}
func (r *AssuranceRoutes) assign(w http.ResponseWriter, q *http.Request) {
	v, e := decodeJSONBody[struct {
		ExpectedVersion int64           `json:"expected_version"`
		Selection       json.RawMessage `json:"selection"`
	}](q)
	if e != nil {
		r.reply(w, q, nil, invalidRequest(e))
		return
	}
	var selection *policy.AssuranceSelection
	decoder := json.NewDecoder(bytes.NewReader(v.Selection))
	decoder.DisallowUnknownFields()
	if len(v.Selection) == 0 || decoder.Decode(&selection) != nil {
		r.reply(w, q, nil, policy.ErrInvalid)
		return
	}
	r.write(w, q, policy.AssuranceCommand{Operation: "assign", PolicyID: chi.URLParam(q, "policyID"), ExpectedVersion: v.ExpectedVersion, Selection: selection})
}
func (r *AssuranceRoutes) write(w http.ResponseWriter, q *http.Request, c policy.AssuranceCommand) {
	key, e := parseIdempotencyKey(q.Header.Values("Idempotency-Key"))
	if e != nil {
		r.reply(w, q, nil, e)
		return
	}
	a, _ := AccessContext(q.Context())
	result, e := r.service.Write(q.Context(), a, key, c)
	r.reply(w, q, result, e)
}
func (r *AssuranceRoutes) validate(w http.ResponseWriter, q *http.Request) {
	p, e := decodeJSONBody[policy.AssuranceProfile](q)
	if e != nil {
		r.reply(w, q, nil, invalidRequest(e))
		return
	}
	a, _ := AccessContext(q.Context())
	result, e := r.service.Validate(q.Context(), a, p)
	r.reply(w, q, result, e)
}
func (r *AssuranceRoutes) reply(w http.ResponseWriter, q *http.Request, v any, e error) {
	w.Header().Set("Cache-Control", "no-store")
	if e != nil {
		switch {
		case errors.Is(e, policy.ErrInvalid):
			e = invalidRequest(e)
		case errors.Is(e, policy.ErrRevisionNotFound):
			e = apierror.New(404, apierror.CodeNotFound, "Not found", "The assurance resource was not found.", e)
		case errors.Is(e, policy.ErrRevisionConflict):
			e = apierror.New(409, apierror.CodeConflict, "Conflict", "The immutable revision or assignment version conflicts with current state.", e)
		}
		if err := respond.WriteProblem(w, q, e, requestIDString(q.Context())); err != nil {
			r.logger.ErrorContext(q.Context(), "write assurance problem")
		}
		return
	}
	if e = respond.JSON(w, q, 200, v); e != nil {
		r.logger.ErrorContext(q.Context(), "write assurance response")
	}
}

func (r *AssuranceRoutes) capabilities(w http.ResponseWriter, q *http.Request) {
	a, _ := AccessContext(q.Context())
	capabilities, e := r.service.Capabilities(q.Context(), a)
	r.reply(w, q, struct {
		Version        int                          `json:"version"`
		Capabilities   []policy.AssuranceCapability `json:"capabilities"`
		BuiltinDigests map[string]string            `json:"builtin_digests"`
	}{1, capabilities, map[string]string{"review": policy.BuiltinAssuranceDigest("review"), "identity": policy.BuiltinAssuranceDigest("identity"), "fraud": policy.BuiltinAssuranceDigest("fraud")}}, e)
}
