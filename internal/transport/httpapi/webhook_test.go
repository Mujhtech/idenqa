package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	webhookv1 "github.com/Mujhtech/idenqa/contracts/webhook/v1"
	"github.com/Mujhtech/idenqa/internal/access"
)

func TestWebhookEventTypesExposeAuthoritativeCatalogue(t *testing.T) {
	t.Parallel()
	fixture := newHTTPAccessFixture(t, nil, access.Pattern("webhooks:read"))
	routes := &WebhookRoutes{
		handlerBase: newHandlerBase(fixture.logger, "webhook"),
		access:      fixture.middleware,
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/webhook-event-types", nil)
	request.Header.Set("Authorization", "Bearer "+fixture.encoded)
	response := httptest.NewRecorder()
	versionedRouter(t, routes).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	var body struct {
		Data []webhookEventType `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	want := webhookv1.Catalogue()
	if len(body.Data) != len(want) {
		t.Fatalf("event type count = %d, want %d", len(body.Data), len(want))
	}
	for index, definition := range want {
		got := body.Data[index]
		if got.Type != definition.Type || got.SchemaVersion != definition.SchemaVersion {
			t.Fatalf("event type %d = %#v, want type %q at schema %q", index, got, definition.Type, definition.SchemaVersion)
		}
	}
	if body.Data[len(body.Data)-1].OptionalData == nil {
		t.Fatal("empty optional_data encoded as null, want an empty array")
	}
}

func TestWebhookEventTypesRequireReadPermission(t *testing.T) {
	t.Parallel()
	fixture := newHTTPAccessFixture(t, nil, access.Pattern("webhooks:configure"))
	routes := &WebhookRoutes{
		handlerBase: newHandlerBase(fixture.logger, "webhook"),
		access:      fixture.middleware,
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/webhook-event-types", nil)
	request.Header.Set("Authorization", "Bearer "+fixture.encoded)
	response := httptest.NewRecorder()
	versionedRouter(t, routes).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusForbidden, response.Body.String())
	}
}
