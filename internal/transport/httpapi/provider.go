package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/provider"
	"github.com/go-chi/chi/v5"
)

// ProviderRoutes exposes tenant provider registration administration. Routes
// stay reference-only: credentials are named, never accepted.
type ProviderRoutes struct {
	handlerBase
	access  *AccessMiddleware
	service *provider.RegistrationManagement
	cursors ProfileCursor
}

// NewProviderRoutes composes the authenticated provider registration surface.
func NewProviderRoutes(middleware *AccessMiddleware, service *provider.RegistrationManagement, cursors ProfileCursor, logger *slog.Logger) (*ProviderRoutes, error) {
	if middleware == nil || service == nil || cursors == nil || logger == nil {
		return nil, provider.ErrRegistrationInvalid
	}
	return &ProviderRoutes{
		handlerBase: newHandlerBase(logger, "provider registration"),
		access:      middleware,
		service:     service,
		cursors:     cursors,
	}, nil
}

// Register adds closed registration commands, bounded reads and the pure
// failure-classification preview.
func (routes *ProviderRoutes) Register(router chi.Router) {
	router.With(routes.access.Authorize(access.PermissionProvidersRead)).Get("/providers", routes.list)
	router.With(routes.access.Authorize(access.PermissionProvidersWrite)).Post("/providers", routes.create)
	router.With(routes.access.Authorize(access.PermissionProvidersRead)).Get("/providers/{providerID}", routes.get)
	router.With(routes.access.Authorize(access.PermissionProvidersWrite)).Put("/providers/{providerID}", routes.update)
	router.With(routes.access.Authorize(access.PermissionProvidersWrite)).Post("/providers/{providerID}/validate", routes.validate)
	router.With(routes.access.Authorize(access.PermissionProvidersWrite)).Post("/providers/{providerID}/enable", routes.mutateEnabled(true))
	router.With(routes.access.Authorize(access.PermissionProvidersWrite)).Post("/providers/{providerID}/disable", routes.mutateEnabled(false))
	router.With(routes.access.Authorize(access.PermissionProvidersWrite)).Post("/providers/{providerID}/rotate-credential", routes.rotateCredential)
	router.With(routes.access.Authorize(access.PermissionProvidersRead)).Get("/providers/{providerID}/health", routes.health)
	router.With(routes.access.Authorize(access.PermissionProvidersRead)).Post("/providers/{providerID}/failure-simulations", routes.simulateFailure)
}

type providerRegistrationCreate struct {
	Reason       string                     `json:"reason"`
	Registration provider.RegistrationWrite `json:"registration"`
}

type providerRegistrationUpdate struct {
	ExpectedVersion int64                      `json:"expected_version"`
	Reason          string                     `json:"reason"`
	Registration    provider.RegistrationWrite `json:"registration"`
}

type providerRegistrationToggle struct {
	ExpectedVersion int64  `json:"expected_version"`
	Reason          string `json:"reason"`
}

type providerCredentialRotationRequest struct {
	ExpectedVersion int64                       `json:"expected_version"`
	Reason          string                      `json:"reason"`
	Credential      provider.CredentialRotation `json:"credential"`
}

type providerFailureSimulationRequest struct {
	Class providerv1.FailureClass `json:"class"`
	Code  string                  `json:"code"`
}

func (routes *ProviderRoutes) create(w http.ResponseWriter, r *http.Request) {
	key, err := parseIdempotencyKey(r.Header.Values("Idempotency-Key"))
	if err != nil {
		routes.reply(w, r, nil, err)
		return
	}
	body, err := decodeJSONBody[providerRegistrationCreate](r)
	if err != nil {
		routes.reply(w, r, nil, invalidRequest(err))
		return
	}
	actor, _ := AccessContext(r.Context())
	result, err := routes.service.Execute(r.Context(), actor, key, provider.RegistrationCommand{Operation: "create", Reason: body.Reason, Write: &body.Registration})
	routes.reply(w, r, result, err)
}

func (routes *ProviderRoutes) update(w http.ResponseWriter, r *http.Request) {
	body, err := decodeJSONBody[providerRegistrationUpdate](r)
	if err != nil {
		routes.reply(w, r, nil, invalidRequest(err))
		return
	}
	actor, _ := AccessContext(r.Context())
	result, err := routes.service.Execute(r.Context(), actor, "", provider.RegistrationCommand{Operation: "update", RegistrationID: chi.URLParam(r, "providerID"), ExpectedVersion: body.ExpectedVersion, Reason: body.Reason, Write: &body.Registration})
	routes.reply(w, r, result, err)
}

func (routes *ProviderRoutes) mutateEnabled(enabled bool) http.HandlerFunc {
	operation := "disable"
	if enabled {
		operation = "enable"
	}
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeJSONBody[providerRegistrationToggle](r)
		if err != nil {
			routes.reply(w, r, nil, invalidRequest(err))
			return
		}
		actor, _ := AccessContext(r.Context())
		result, err := routes.service.Execute(r.Context(), actor, "", provider.RegistrationCommand{Operation: operation, RegistrationID: chi.URLParam(r, "providerID"), ExpectedVersion: body.ExpectedVersion, Reason: body.Reason})
		routes.reply(w, r, result, err)
	}
}

func (routes *ProviderRoutes) rotateCredential(w http.ResponseWriter, r *http.Request) {
	body, err := decodeJSONBody[providerCredentialRotationRequest](r)
	if err != nil {
		routes.reply(w, r, nil, invalidRequest(err))
		return
	}
	actor, _ := AccessContext(r.Context())
	result, err := routes.service.Execute(r.Context(), actor, "", provider.RegistrationCommand{Operation: "rotate-credential",
		RegistrationID: chi.URLParam(r, "providerID"), ExpectedVersion: body.ExpectedVersion, Credential: &body.Credential, Reason: body.Reason})
	routes.reply(w, r, result, err)
}

func (routes *ProviderRoutes) list(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()
	for key, entries := range values {
		if len(entries) != 1 || (key != "limit" && key != "cursor") {
			routes.reply(w, r, nil, provider.ErrRegistrationInvalid)
			return
		}
	}
	limit := 25
	if raw := values.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			routes.reply(w, r, nil, provider.ErrRegistrationInvalid)
			return
		}
		limit = parsed
	}
	actor, _ := AccessContext(r.Context())
	query := "providers.list;limit=" + strconv.Itoa(limit)
	var after *provider.RegistrationPosition
	token := values.Get("cursor")
	if token != "" {
		claims, err := routes.cursors.Decode(token, actor.TenantScope().ID(), query)
		if err != nil {
			routes.reply(w, r, nil, provider.ErrRegistrationInvalid)
			return
		}
		var position provider.RegistrationPosition
		if decodeStrictJSON(claims.Position, &position) != nil || position.ID == "" || position.CreatedAt.IsZero() {
			routes.reply(w, r, nil, provider.ErrRegistrationInvalid)
			return
		}
		after = &position
	}
	page, err := routes.service.List(r.Context(), actor, after, limit)
	if err != nil {
		routes.reply(w, r, nil, err)
		return
	}
	response := struct {
		Data []provider.Registration `json:"data"`
		Page struct {
			HasMore    bool    `json:"has_more"`
			NextCursor *string `json:"next_cursor,omitempty"`
		} `json:"page"`
	}{Data: page.Registrations}
	if page.Registrations == nil {
		response.Data = []provider.Registration{}
	}
	if page.Next != nil {
		encoded, err := json.Marshal(page.Next)
		if err != nil {
			routes.reply(w, r, nil, provider.ErrRegistrationInvalid)
			return
		}
		next, err := routes.cursors.Encode(actor.TenantScope().ID(), query, encoded)
		if err != nil {
			routes.reply(w, r, nil, provider.ErrRegistrationInvalid)
			return
		}
		response.Page.HasMore = true
		response.Page.NextCursor = &next
	}
	routes.reply(w, r, response, nil)
}

func (routes *ProviderRoutes) get(w http.ResponseWriter, r *http.Request) {
	actor, _ := AccessContext(r.Context())
	result, err := routes.service.Get(r.Context(), actor, chi.URLParam(r, "providerID"))
	routes.reply(w, r, result, err)
}

func (routes *ProviderRoutes) validate(w http.ResponseWriter, r *http.Request) {
	if _, err := id.ParseProviderRegistration(chi.URLParam(r, "providerID")); err != nil {
		routes.reply(w, r, nil, provider.ErrRegistrationNotFound)
		return
	}
	body, err := decodeJSONBody[provider.RegistrationWrite](r)
	if err != nil {
		routes.reply(w, r, nil, invalidRequest(err))
		return
	}
	actor, _ := AccessContext(r.Context())
	report, err := routes.service.Validate(r.Context(), actor, body)
	routes.reply(w, r, report, err)
}

func (routes *ProviderRoutes) health(w http.ResponseWriter, r *http.Request) {
	actor, _ := AccessContext(r.Context())
	result, err := routes.service.Health(r.Context(), actor, chi.URLParam(r, "providerID"))
	routes.reply(w, r, result, err)
}

func (routes *ProviderRoutes) simulateFailure(w http.ResponseWriter, r *http.Request) {
	body, err := decodeJSONBody[providerFailureSimulationRequest](r)
	if err != nil {
		routes.reply(w, r, nil, invalidRequest(err))
		return
	}
	actor, _ := AccessContext(r.Context())
	result, err := routes.service.SimulateFailure(r.Context(), actor, chi.URLParam(r, "providerID"), body.Class, body.Code)
	routes.reply(w, r, result, err)
}
