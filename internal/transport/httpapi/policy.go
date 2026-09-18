package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	policyv1 "github.com/Mujhtech/idenqa/contracts/policy/v1"

	"github.com/Mujhtech/idenqa/internal/access"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/apierror"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/go-chi/chi/v5"
)

// PolicyRoutes exposes tenant policy administration without a commercial control plane.
type PolicyRoutes struct {
	access  *AccessMiddleware
	service *policy.Management
	cursors ProfileCursor
	logger  *slog.Logger
}

// NewPolicyRoutes constructs strict public policy administration routes.
func NewPolicyRoutes(middleware *AccessMiddleware, service *policy.Management, cursors ProfileCursor, logger *slog.Logger) (*PolicyRoutes, error) {
	if middleware == nil || service == nil || cursors == nil || logger == nil {
		return nil, policy.ErrInvalid
	}
	return &PolicyRoutes{middleware, service, cursors, logger}, nil
}

// Register adds policy catalog, validation, revision and activation operations.
func (routes *PolicyRoutes) Register(router chi.Router) {
	for _, route := range []struct {
		method, path string
		permission   access.Permission
		handler      http.HandlerFunc
	}{
		{"GET", "/policies", access.PermissionPoliciesRead, routes.list},
		{"POST", "/policies", access.PermissionPoliciesWrite, routes.mutate},
		{"POST", "/policies/validate", access.PermissionPoliciesWrite, routes.validate},
		{"GET", "/policies/{policyID}", access.PermissionPoliciesRead, routes.get},
		{"GET", "/policies/{policyID}/revisions", access.PermissionPoliciesRead, routes.history},
		{"POST", "/policies/{policyID}/revisions", access.PermissionPoliciesWrite, routes.mutate},
		{"GET", "/policies/{policyID}/revisions/{revision}", access.PermissionPoliciesRead, routes.revision},
		{"GET", "/policies/{policyID}/activations", access.PermissionPoliciesRead, routes.history},
		{"POST", "/policies/{policyID}/activate", access.PermissionPoliciesActivate, routes.mutate},
		{"POST", "/policies/{policyID}/rollback", access.PermissionPoliciesActivate, routes.mutate},
	} {
		router.With(routes.access.Authenticate, routes.access.Require(route.permission)).MethodFunc(route.method, route.path, route.handler)
	}
}
func (routes *PolicyRoutes) mutate(w http.ResponseWriter, r *http.Request) {
	actor, _ := AccessContext(r.Context())
	key, err := parseIdempotencyKey(r.Header.Values("Idempotency-Key"))
	if err != nil {
		routes.problem(w, r, err)
		return
	}
	command := policy.Command{PolicyID: chi.URLParam(r, "policyID")}
	switch chi.RouteContext(r.Context()).RoutePattern() {
	case "/v1/policies":
		body, decodeErr := decodePolicyJSON[struct {
			Definition policy.Definition `json:"definition"`
		}](r)
		err = decodeErr
		command.Operation, command.Definition = "create", &body.Definition
	case "/v1/policies/{policyID}/revisions":
		body, decodeErr := decodePolicyJSON[struct {
			Definition       policy.Definition `json:"definition"`
			ExpectedRevision uint32            `json:"expected_revision"`
		}](r)
		err = decodeErr
		command.Operation, command.Definition, command.ExpectedRevision = "revision", &body.Definition, body.ExpectedRevision
	default:
		body, decodeErr := decodePolicyJSON[struct {
			Revision        uint32 `json:"revision"`
			ExpectedVersion *int64 `json:"expected_version"`
			Reason          string `json:"reason"`
		}](r)
		err = decodeErr
		if body.ExpectedVersion == nil {
			err = policy.ErrInvalid
		} else {
			command.ExpectedVersion = *body.ExpectedVersion
		}
		command.Operation = "activate"
		if chi.RouteContext(r.Context()).RoutePattern() == "/v1/policies/{policyID}/rollback" {
			command.Operation = "rollback"
		}
		command.Revision, command.Reason = body.Revision, body.Reason
	}
	if err != nil {
		routes.problem(w, r, invalidRequest(err))
		return
	}
	result, err := routes.service.Execute(r.Context(), actor, key, command)
	if err != nil {
		routes.problem(w, r, err)
		return
	}
	routes.json(w, r, result)
}
func (routes *PolicyRoutes) validate(w http.ResponseWriter, r *http.Request) {
	body, err := decodePolicyJSON[struct {
		Definition policy.Definition `json:"definition"`
	}](r)
	if err != nil {
		routes.problem(w, r, invalidRequest(err))
		return
	}
	actor, _ := AccessContext(r.Context())
	result, err := routes.service.Validate(r.Context(), actor, body.Definition)
	if err != nil {
		routes.problem(w, r, err)
		return
	}
	routes.json(w, r, result)
}
func policyIdentifier(r *http.Request) (id.Policy, error) {
	identifier, err := id.ParsePolicy(chi.URLParam(r, "policyID"))
	if err != nil {
		return id.Policy{}, policy.ErrRevisionNotFound
	}
	return identifier, nil
}
func (routes *PolicyRoutes) get(w http.ResponseWriter, r *http.Request) {
	identifier, err := policyIdentifier(r)
	if err != nil {
		routes.problem(w, r, err)
		return
	}
	actor, _ := AccessContext(r.Context())
	result, err := routes.service.Get(r.Context(), actor, identifier)
	if err != nil {
		routes.problem(w, r, err)
		return
	}
	routes.json(w, r, result)
}
func (routes *PolicyRoutes) revision(w http.ResponseWriter, r *http.Request) {
	identifier, err := policyIdentifier(r)
	if err != nil {
		routes.problem(w, r, err)
		return
	}
	number, err := strconv.ParseUint(chi.URLParam(r, "revision"), 10, 32)
	if err != nil || number == 0 {
		routes.problem(w, r, policy.ErrRevisionNotFound)
		return
	}
	actor, _ := AccessContext(r.Context())
	result, err := routes.service.GetRevision(r.Context(), actor, identifier, uint32(number))
	if err != nil {
		routes.problem(w, r, err)
		return
	}
	routes.json(w, r, result)
}
func (routes *PolicyRoutes) position(r *http.Request, collection string) (json.RawMessage, int, string, error) {
	limit, token, err := parseListQuery(r)
	if err != nil {
		return nil, 0, "", err
	}
	query := "policies:" + collection + ";limit=" + strconv.Itoa(limit)
	if token == "" {
		return nil, limit, query, nil
	}
	actor, _ := AccessContext(r.Context())
	claims, err := routes.cursors.Decode(token, actor.TenantScope().ID(), query)
	return claims.Position, limit, query, err
}
func (routes *PolicyRoutes) list(w http.ResponseWriter, r *http.Request) {
	position, limit, query, err := routes.position(r, "list")
	if err != nil {
		routes.problem(w, r, invalidRequest(err))
		return
	}
	before := ""
	if position != nil {
		if err := json.Unmarshal(position, &before); err != nil {
			routes.problem(w, r, invalidRequest(err))
			return
		}
	}
	actor, _ := AccessContext(r.Context())
	items, err := routes.service.List(r.Context(), actor, before, limit+1)
	if err != nil {
		routes.problem(w, r, err)
		return
	}
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	last := ""
	if len(items) > 0 {
		last = items[len(items)-1].ID
	}
	routes.page(w, r, items, more, last, query)
}
func (routes *PolicyRoutes) history(w http.ResponseWriter, r *http.Request) {
	identifier, err := policyIdentifier(r)
	if err != nil {
		routes.problem(w, r, err)
		return
	}
	activations := chi.RouteContext(r.Context()).RoutePattern() == "/v1/policies/{policyID}/activations"
	kind := "revisions"
	if activations {
		kind = "activations"
	}
	position, limit, query, err := routes.position(r, identifier.String()+":"+kind)
	if err != nil {
		routes.problem(w, r, invalidRequest(err))
		return
	}
	actor, _ := AccessContext(r.Context())
	if activations {
		var before int64
		if position != nil {
			if err := json.Unmarshal(position, &before); err != nil {
				routes.problem(w, r, invalidRequest(err))
				return
			}
		}
		items, more, last, err := routes.service.Activations(r.Context(), actor, identifier, before, limit)
		if err != nil {
			routes.problem(w, r, err)
			return
		}
		routes.page(w, r, items, more, last, query)
	} else {
		var before uint32
		if position != nil {
			if err := json.Unmarshal(position, &before); err != nil {
				routes.problem(w, r, invalidRequest(err))
				return
			}
		}
		items, more, last, err := routes.service.Revisions(r.Context(), actor, identifier, before, limit)
		if err != nil {
			routes.problem(w, r, err)
			return
		}
		routes.page(w, r, items, more, last, query)
	}
}
func (routes *PolicyRoutes) page(w http.ResponseWriter, r *http.Request, data any, more bool, last any, query string) {
	page := openapiv1.Page{HasMore: more}
	if more {
		position, err := json.Marshal(last)
		if err != nil {
			routes.problem(w, r, err)
			return
		}
		actor, _ := AccessContext(r.Context())
		next, err := routes.cursors.Encode(actor.TenantScope().ID(), query, position)
		if err != nil {
			routes.problem(w, r, err)
			return
		}
		page.NextCursor = &next
	}
	routes.json(w, r, struct {
		Data any            `json:"data"`
		Page openapiv1.Page `json:"page"`
	}{data, page})
}
func (routes *PolicyRoutes) json(w http.ResponseWriter, r *http.Request, value any) {
	w.Header().Set("Cache-Control", "no-store")
	if err := respond.JSON(w, r, 200, value); err != nil {
		routes.logger.ErrorContext(r.Context(), "write policy administration response")
	}
}
func (routes *PolicyRoutes) problem(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, policy.ErrRevisionNotFound), errors.Is(err, policy.ErrActivationNotFound):
		err = apierror.New(404, apierror.CodeNotFound, "Not found", "The policy resource was not found.", err)
	case errors.Is(err, policy.ErrRevisionConflict), errors.Is(err, policy.ErrActivationConflict):
		err = apierror.New(409, apierror.CodeConflict, "Conflict", "The policy operation conflicts with current state.", err)
	case errors.Is(err, policy.ErrInvalid):
		err = invalidRequest(err)
	}
	if writeErr := respond.WriteProblem(w, r, err, requestIDString(r.Context())); writeErr != nil {
		routes.logger.ErrorContext(r.Context(), "write policy administration problem")
	}
}

func decodePolicyJSON[T any](request *http.Request) (T, error) {
	var zero T
	raw, err := io.ReadAll(io.LimitReader(request.Body, 2*policyv1.MaximumDocumentBytes+1))
	if err != nil || len(raw) > 2*policyv1.MaximumDocumentBytes || !utf8.Valid(raw) {
		return zero, policy.ErrInvalid
	}
	if err := rejectPolicyDuplicates(raw); err != nil {
		return zero, err
	}
	if err := requirePolicyShape(raw, reflect.TypeFor[T]()); err != nil {
		return zero, err
	}
	cloned := request.Clone(request.Context())
	cloned.Body = io.NopCloser(bytes.NewReader(raw))
	return decodeJSONBody[T](cloned)
}
func rejectPolicyDuplicates(input []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(input))
	var depth int
	var visit func() error
	visit = func() error {
		depth++
		defer func() { depth-- }()
		if depth > 32 {
			return policy.ErrInvalid
		}
		token, err := decoder.Token()
		if err != nil {
			return policy.ErrInvalid
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := make(map[string]struct{})
			for decoder.More() {
				keyToken, keyErr := decoder.Token()
				if keyErr != nil {
					return policy.ErrInvalid
				}
				key, keyOK := keyToken.(string)
				if !keyOK || key != strings.ToLower(key) {
					return policy.ErrInvalid
				}
				if _, exists := seen[key]; exists {
					return policy.ErrInvalid
				}
				seen[key] = struct{}{}
				if err := visit(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := visit(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return policy.ErrInvalid
		}
	}
	if err := visit(); err != nil {
		return err
	}
	if decoder.More() {
		return policy.ErrInvalid
	}
	return nil
}

// Every field on the closed policy command structs is required and non-null.
// Validate presence before Go decoding can erase missing versus zero values.
func requirePolicyShape(raw json.RawMessage, kind reflect.Type) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return policy.ErrInvalid
	}
	if kind.Kind() == reflect.Pointer {
		return requirePolicyShape(raw, kind.Elem())
	}
	switch kind.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil || len(fields) != kind.NumField() {
			return policy.ErrInvalid
		}
		for index := 0; index < kind.NumField(); index++ {
			field := kind.Field(index)
			value, exists := fields[field.Tag.Get("json")]
			if !exists {
				return policy.ErrInvalid
			}
			if err := requirePolicyShape(value, field.Type); err != nil {
				return err
			}
		}
	case reflect.Slice:
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return policy.ErrInvalid
		}
		for _, value := range values {
			if err := requirePolicyShape(value, kind.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}
