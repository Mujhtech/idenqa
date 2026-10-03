package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/requestlog"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/go-chi/chi/v5"
)

type capturedRequestLog struct{ record requestlog.Record }

func (capture *capturedRequestLog) Append(_ context.Context, _ tenant.Scope, record requestlog.Record) error {
	capture.record = record
	return nil
}

func TestOperationalRequestJournalStoresRouteTemplateOnly(t *testing.T) {
	t.Parallel()
	tenantID, _ := id.ParseTenant("ten_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	scope, _ := tenant.NewScope(tenantID)
	capture := &capturedRequestLog{}
	router := chi.NewRouter()
	router.Use(operationalRequestJournal(capture, slog.New(slog.NewTextHandler(io.Discard, nil))))
	router.Get("/v1/verifications/{verificationID}", func(writer http.ResponseWriter, request *http.Request) {
		metadata := request.Context().Value(requestLogAuthorityKey{}).(*requestLogAuthority)
		metadata.scope, metadata.actor = scope, "key_01ARZ3NDEKTSV4RRFFQ69G5FAV"
		writer.WriteHeader(http.StatusAccepted)
	})
	request := httptest.NewRequest(http.MethodGet, "/v1/verifications/vrf_secret?subject=hidden", nil)
	requestID, _ := id.ParseRequest("req_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	request = request.WithContext(context.WithValue(request.Context(), requestIDContextKey{}, requestID))
	router.ServeHTTP(httptest.NewRecorder(), request)

	if capture.record.RouteTemplate != "/v1/verifications/{verificationID}" {
		t.Fatalf("route template = %q", capture.record.RouteTemplate)
	}
	if capture.record.RequestID != requestID.String() || capture.record.StatusCode != http.StatusAccepted {
		t.Fatalf("record = %#v", capture.record)
	}
}

func TestOperationalRequestJournalCapturesBoundedRedactedDebugParameters(t *testing.T) {
	t.Parallel()
	tenantID, _ := id.ParseTenant("ten_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	scope, _ := tenant.NewScope(tenantID)
	capture := &capturedRequestLog{}
	router := chi.NewRouter()
	router.Use(operationalRequestJournal(capture, slog.New(slog.NewTextHandler(io.Discard, nil))))
	router.Post("/v1/verifications", func(writer http.ResponseWriter, request *http.Request) {
		metadata := request.Context().Value(requestLogAuthorityKey{}).(*requestLogAuthority)
		metadata.scope, metadata.actor = scope, "key_01ARZ3NDEKTSV4RRFFQ69G5FAV"
		_, _ = io.ReadAll(request.Body)
		writer.WriteHeader(http.StatusCreated)
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/verifications?mode=fast&ticket=secret", strings.NewReader(`{"workflow":"document","password":"secret","nested":{"token":"secret"},"items":[{"email":"person@example.com"}]}`))
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.RemoteAddr = "192.0.2.10:4321"
	requestID, _ := id.ParseRequest("req_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	request = request.WithContext(context.WithValue(request.Context(), requestIDContextKey{}, requestID))
	router.ServeHTTP(httptest.NewRecorder(), request)

	if capture.record.ClientIPAddress != "192.0.2.10" {
		t.Fatalf("client IP = %q", capture.record.ClientIPAddress)
	}
	var query, body map[string]any
	if err := json.Unmarshal(capture.record.QueryParameters, &query); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(capture.record.BodyParameters, &body); err != nil {
		t.Fatal(err)
	}
	if query["mode"] != "fast" || query["ticket"] != "[REDACTED]" || body["password"] != "[REDACTED]" {
		t.Fatalf("query=%v body=%v", query, body)
	}
	nested := body["nested"].(map[string]any)
	if nested["token"] != "[REDACTED]" {
		t.Fatalf("nested body = %v", nested)
	}
	items := body["items"].([]any)
	if items[0].(map[string]any)["email"] != "[REDACTED]" {
		t.Fatalf("array body = %v", items)
	}
}
