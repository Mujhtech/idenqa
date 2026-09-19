package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/delivery"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/apierror"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/go-chi/chi/v5"
)

// WebhookEventWakeups blocks until a routing-only wake-up for one tenant
// arrives. Implementations are lossy by design; callers retain polling.
type WebhookEventWakeups interface {
	Wait(context.Context, id.Tenant) error
}

// WebhookRoutes exposes safe webhook administration using tenant API credentials.
type WebhookRoutes struct {
	access  *AccessMiddleware
	service *delivery.Management
	stream  *delivery.Stream
	wakeups WebhookEventWakeups
	cursors ProfileCursor
	logger  *slog.Logger
}

// NewWebhookRoutes constructs public webhook routes. A nil wake-ups port keeps
// the live stream correct through bounded polling.
func NewWebhookRoutes(middleware *AccessMiddleware, service *delivery.Management, stream *delivery.Stream, wakeups WebhookEventWakeups, cursors ProfileCursor, logger *slog.Logger) (*WebhookRoutes, error) {
	if middleware == nil || service == nil || stream == nil || cursors == nil || logger == nil {
		return nil, delivery.ErrInvalid
	}
	return &WebhookRoutes{access: middleware, service: service, stream: stream, wakeups: wakeups, cursors: cursors, logger: logger}, nil
}

// Register adds administration, payload-free inspection and the read-only
// tenant event feed.
func (routes *WebhookRoutes) Register(router chi.Router) {
	for _, route := range []struct {
		method, path string
		permission   access.Permission
		handler      http.HandlerFunc
	}{
		{"GET", "/webhook-endpoints", access.PermissionWebhooksRead, routes.endpoints},
		{"POST", "/webhook-endpoints", access.PermissionWebhooksConfigure, routes.mutate},
		{"GET", "/webhook-endpoints/{endpointID}", access.PermissionWebhooksRead, routes.endpoint},
		{"POST", "/webhook-endpoints/{endpointID}/rotate", access.PermissionWebhooksConfigure, routes.mutate},
		{"POST", "/webhook-endpoints/{endpointID}/disable", access.PermissionWebhooksConfigure, routes.mutate},
		{"POST", "/webhook-endpoints/{endpointID}/subscriptions", access.PermissionWebhooksConfigure, routes.mutate},
		{"GET", "/webhook-endpoints/{endpointID}/deliveries", access.PermissionWebhooksRead, routes.deliveries},
		{"GET", "/webhook-deliveries/{deliveryID}", access.PermissionWebhooksRead, routes.delivery},
		{"GET", "/webhook-deliveries/{deliveryID}/attempts", access.PermissionWebhooksRead, routes.attempts},
		{"POST", "/webhook-deliveries/{deliveryID}/replay", access.PermissionWebhooksReplay, routes.mutate},
		{"GET", "/webhook-events", access.PermissionWebhooksRead, routes.events},
		{"GET", "/webhook-events/stream", access.PermissionWebhooksRead, routes.eventStream},
	} {
		router.With(routes.access.Authenticate, routes.access.Require(route.permission)).MethodFunc(route.method, route.path, route.handler)
	}
}

func (routes *WebhookRoutes) mutate(writer http.ResponseWriter, request *http.Request) {
	actor, ok := AccessContext(request.Context())
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	key, err := parseIdempotencyKey(request.Header.Values("Idempotency-Key"))
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	command := delivery.ManagementCommand{EndpointID: chi.URLParam(request, "endpointID"), DeliveryID: chi.URLParam(request, "deliveryID")}
	switch chi.RouteContext(request.Context()).RoutePattern() {
	case "/v1/webhook-endpoints":
		body, decodeErr := decodeJSONBody[struct {
			URL           string   `json:"url"`
			EventTypes    []string `json:"event_types,omitempty"`
			SchemaVersion string   `json:"schema_version,omitempty"`
		}](request)
		err = decodeErr
		command.Operation, command.URL, command.EventTypes, command.SchemaVersion = "create", body.URL, body.EventTypes, body.SchemaVersion
	case "/v1/webhook-endpoints/{endpointID}/subscriptions":
		body, decodeErr := decodeJSONBody[struct {
			ExpectedVersion int64    `json:"expected_version"`
			EventTypes      []string `json:"event_types"`
			SchemaVersion   string   `json:"schema_version,omitempty"`
		}](request)
		err = decodeErr
		command.Operation, command.ExpectedVersion, command.EventTypes, command.SchemaVersion = "subscribe", body.ExpectedVersion, body.EventTypes, body.SchemaVersion
	case "/v1/webhook-endpoints/{endpointID}/rotate":
		body, decodeErr := decodeJSONBody[struct {
			ExpectedVersion int64 `json:"expected_version"`
			OverlapSeconds  int64 `json:"overlap_seconds"`
		}](request)
		err = decodeErr
		command.Operation, command.ExpectedVersion, command.OverlapSeconds = "rotate", body.ExpectedVersion, body.OverlapSeconds
	case "/v1/webhook-endpoints/{endpointID}/disable":
		body, decodeErr := decodeJSONBody[struct {
			ExpectedVersion int64  `json:"expected_version"`
			Reason          string `json:"reason"`
		}](request)
		err = decodeErr
		command.Operation, command.ExpectedVersion, command.Reason = "disable", body.ExpectedVersion, body.Reason
	default:
		body, decodeErr := decodeJSONBody[struct {
			Reason string `json:"reason"`
		}](request)
		err = decodeErr
		command.Operation, command.Reason = "replay", body.Reason
	}
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	result, secret, err := routes.service.Execute(request.Context(), actor, key, command)
	defer clear(secret)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	response := struct {
		delivery.ManagementResult
		SigningSecret string `json:"signing_secret,omitempty"`
	}{ManagementResult: result}
	if len(secret) > 0 {
		response.SigningSecret = base64.RawURLEncoding.EncodeToString(secret)
	}
	routes.json(writer, request, response)
}
func (routes *WebhookRoutes) endpoint(writer http.ResponseWriter, request *http.Request) {
	actor, _ := AccessContext(request.Context())
	identifier, err := id.ParseWebhookEndpoint(chi.URLParam(request, "endpointID"))
	if err != nil {
		routes.problem(writer, request, delivery.ErrNotFound)
		return
	}
	view, err := routes.service.Endpoint(request.Context(), actor, identifier)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.json(writer, request, view)
}
func (routes *WebhookRoutes) delivery(writer http.ResponseWriter, request *http.Request) {
	actor, _ := AccessContext(request.Context())
	identifier, err := id.ParseDelivery(chi.URLParam(request, "deliveryID"))
	if err != nil {
		routes.problem(writer, request, delivery.ErrNotFound)
		return
	}
	view, err := routes.service.Delivery(request.Context(), actor, identifier)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.json(writer, request, view)
}
func (routes *WebhookRoutes) attempts(writer http.ResponseWriter, request *http.Request) {
	actor, _ := AccessContext(request.Context())
	identifier, err := id.ParseDelivery(chi.URLParam(request, "deliveryID"))
	if err != nil {
		routes.problem(writer, request, delivery.ErrNotFound)
		return
	}
	views, err := routes.service.Attempts(request.Context(), actor, identifier)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.json(writer, request, struct {
		Data []delivery.AttemptView `json:"data"`
	}{views})
}
func (routes *WebhookRoutes) endpoints(writer http.ResponseWriter, request *http.Request) {
	actor, _ := AccessContext(request.Context())
	after, limit, query, err := routes.position(request, "endpoints")
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	views, err := routes.service.Endpoints(request.Context(), actor, after, limit+1)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	more := len(views) > limit
	if more {
		views = views[:limit]
	}
	last := ""
	if len(views) > 0 {
		last = views[len(views)-1].ID
	}
	routes.page(writer, request, views, more, last, query)
}
func (routes *WebhookRoutes) deliveries(writer http.ResponseWriter, request *http.Request) {
	actor, _ := AccessContext(request.Context())
	identifier, err := id.ParseWebhookEndpoint(chi.URLParam(request, "endpointID"))
	if err != nil {
		routes.problem(writer, request, delivery.ErrNotFound)
		return
	}
	after, limit, query, err := routes.position(request, "deliveries:"+identifier.String())
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	views, err := routes.service.Deliveries(request.Context(), actor, identifier, after, limit+1)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	more := len(views) > limit
	if more {
		views = views[:limit]
	}
	last := ""
	if len(views) > 0 {
		last = views[len(views)-1].ID
	}
	routes.page(writer, request, views, more, last, query)
}
func (routes *WebhookRoutes) position(request *http.Request, collection string) (string, int, string, error) {
	limit, token, err := parseListQuery(request)
	if err != nil {
		return "", 0, "", err
	}
	query := "webhooks:" + collection + ";limit=" + strconv.Itoa(limit)
	after := ""
	if token != "" {
		actor, _ := AccessContext(request.Context())
		claims, err := routes.cursors.Decode(token, actor.TenantScope().ID(), query)
		if err != nil {
			return "", 0, "", err
		}
		if err := json.Unmarshal(claims.Position, &after); err != nil {
			return "", 0, "", err
		}
	}
	return after, limit, query, nil
}
func (routes *WebhookRoutes) page(writer http.ResponseWriter, request *http.Request, data any, more bool, last, query string) {
	page := openapiv1.Page{HasMore: more}
	if more {
		actor, _ := AccessContext(request.Context())
		encoded, err := json.Marshal(last)
		if err != nil {
			routes.problem(writer, request, err)
			return
		}
		next, err := routes.cursors.Encode(actor.TenantScope().ID(), query, encoded)
		if err != nil {
			routes.problem(writer, request, err)
			return
		}
		page.NextCursor = &next
	}
	routes.json(writer, request, struct {
		Data any            `json:"data"`
		Page openapiv1.Page `json:"page"`
	}{data, page})
}
func (routes *WebhookRoutes) json(writer http.ResponseWriter, request *http.Request, value any) {
	writer.Header().Set("Cache-Control", "no-store")
	if err := respond.JSON(writer, request, http.StatusOK, value); err != nil {
		routes.logger.ErrorContext(request.Context(), "write webhook response")
	}
}
func (routes *WebhookRoutes) problem(writer http.ResponseWriter, request *http.Request, err error) {
	switch {
	case errors.Is(err, delivery.ErrNotFound):
		err = apierror.New(404, apierror.CodeNotFound, "Not found", "The webhook resource was not found.", err)
	case errors.Is(err, delivery.ErrDisabled), errors.Is(err, delivery.ErrConflict):
		err = apierror.New(409, apierror.CodeConflict, "Conflict", "The webhook operation conflicts with current state.", err)
	case errors.Is(err, delivery.ErrExpired):
		err = apierror.New(410, apierror.CodeGone, "Gone", "The webhook payload retention window has closed.", err)
	case errors.Is(err, delivery.ErrInvalid):
		err = invalidRequest(err)
	case errors.Is(err, delivery.ErrQueueUnavailable):
		err = apierror.New(503, apierror.CodeServiceUnavailable, "Unavailable", "Webhook replay configuration is unavailable.", err)
	case errors.Is(err, delivery.ErrSigningUnavailable):
		err = apierror.New(503, apierror.CodeServiceUnavailable, "Unavailable", "Webhook signing configuration is unavailable.", err)
	}
	if writeErr := respond.WriteProblem(writer, request, err, requestIDString(request.Context())); writeErr != nil {
		routes.logger.ErrorContext(request.Context(), "write webhook problem")
	}
}
