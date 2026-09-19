package api_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	bootstrapapi "github.com/Mujhtech/idenqa/internal/bootstrap/api"
	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/health"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/telemetry"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/tenantexport"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi"
	"github.com/go-chi/chi/v5"
)

func TestLive(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/livez", nil)
	response := httptest.NewRecorder()

	newTestHandler(t, &health.State{}).ServeHTTP(response, request)

	result := response.Result()
	if result.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", result.StatusCode, http.StatusOK)
	}
	if got := result.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want %q", got, "no-store")
	}
	if got := result.Header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q", got, "text/plain; charset=utf-8")
	}

	body, err := io.ReadAll(result.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if err := result.Body.Close(); err != nil {
		t.Fatalf("close response body: %v", err)
	}
	if got, want := string(body), "ok\n"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestLiveRejectsOtherMethods(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/livez", nil)
	response := httptest.NewRecorder()

	newTestHandler(t, &health.State{}).ServeHTTP(response, request)

	if got, want := response.Code, http.StatusMethodNotAllowed; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
}

func TestUnknownRoute(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/unknown", nil)
	response := httptest.NewRecorder()

	newTestHandler(t, &health.State{}).ServeHTTP(response, request)

	if got, want := response.Code, http.StatusNotFound; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
}

func TestHandlerMountsPublicAndInternalVersionedRoutes(t *testing.T) {
	t.Parallel()

	dependencies, _ := newTestHTTPComponents(t)
	handler, err := bootstrapapi.NewHandler(
		&health.State{},
		dependencies,
		[]bootstrapapi.RouteRegistrar{stubRouteRegistrar{register: func(router chi.Router) {
			router.Get("/genuine", func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusNoContent)
			})
		}}},
		[]bootstrapapi.InternalRouteRegistrar{stubInternalRouteRegistrar{register: func(router chi.Router) {
			router.Post("/evidence", func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusNoContent)
			})
		}}},
	)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{name: "public route below version prefix", method: http.MethodGet, path: "/v1/genuine", wantStatus: http.StatusNoContent},
		{name: "public route outside version prefix", method: http.MethodGet, path: "/genuine", wantStatus: http.StatusNotFound},
		{name: "internal route below internal prefix", method: http.MethodPost, path: "/internal/v1/evidence", wantStatus: http.StatusNoContent},
		{name: "internal route below public prefix", method: http.MethodPost, path: "/v1/internal/v1/evidence", wantStatus: http.StatusNotFound},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(context.Background(), test.method, test.path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if got := response.Code; got != test.wantStatus {
				t.Fatalf("status = %d, want %d", got, test.wantStatus)
			}
		})
	}
}

type stubRouteRegistrar struct {
	register func(chi.Router)
}

func (stub stubRouteRegistrar) Register(router chi.Router) {
	stub.register(router)
}

type stubInternalRouteRegistrar struct {
	register func(chi.Router)
}

func (stub stubInternalRouteRegistrar) RegisterInternal(router chi.Router) {
	stub.register(router)
}

func TestHealthLifecycle(t *testing.T) {
	t.Parallel()

	state := &health.State{}
	handler := newTestHandler(t, state)

	tests := []struct {
		name       string
		path       string
		transition func()
		wantStatus int
	}{
		{name: "startup before start", path: "/startupz", wantStatus: http.StatusServiceUnavailable},
		{name: "readiness before start", path: "/readyz", wantStatus: http.StatusServiceUnavailable},
		{name: "startup after start", path: "/startupz", transition: state.MarkStarted, wantStatus: http.StatusOK},
		{name: "readiness after ready", path: "/readyz", transition: state.MarkReady, wantStatus: http.StatusOK},
		{name: "readiness during drain", path: "/readyz", transition: state.BeginDrain, wantStatus: http.StatusServiceUnavailable},
		{name: "startup during drain", path: "/startupz", wantStatus: http.StatusOK},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.transition != nil {
				test.transition()
			}

			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, test.path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if got := response.Code; got != test.wantStatus {
				t.Fatalf("status = %d, want %d", got, test.wantStatus)
			}
		})
	}
}

func TestServerAllowsTheConfiguredEvidenceUploadAttemptAtTheSocketLayer(t *testing.T) {
	t.Parallel()

	dependencies, tenantRoutes := newTestHTTPComponents(t)
	policy := evidence.DefaultUploadPolicy()
	dependencies.EvidenceUploadPolicy = &policy
	server, err := bootstrapapi.NewServer(
		"127.0.0.1:0",
		&health.State{},
		dependencies,
		[]bootstrapapi.RouteRegistrar{tenantRoutes},
		nil,
	)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	if server.ReadTimeout <= policy.AttemptTimeout() {
		t.Fatalf(
			"ReadTimeout = %s, want greater than upload attempt timeout %s",
			server.ReadTimeout,
			policy.AttemptTimeout(),
		)
	}
	if server.WriteTimeout != server.ReadTimeout {
		t.Fatalf("WriteTimeout = %s, want ReadTimeout %s", server.WriteTimeout, server.ReadTimeout)
	}
}

type testClock struct{}

func (testClock) Now() time.Time {
	return time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
}

func newTestHandler(t *testing.T, state *health.State) http.Handler {
	t.Helper()

	dependencies, tenantRoutes := newTestHTTPComponents(t)
	handler, err := bootstrapapi.NewHandler(
		state,
		dependencies,
		[]bootstrapapi.RouteRegistrar{tenantRoutes},
		nil,
	)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	return handler
}

func newTestHTTPComponents(t *testing.T) (httpapi.Dependencies, *httpapi.TenantRoutes) {
	t.Helper()

	identifiers, err := id.NewGenerator(testClock{}, bytes.NewReader(bytes.Repeat([]byte{1}, 1_024)))
	if err != nil {
		t.Fatalf("NewGenerator() error = %v", err)
	}
	providers := telemetry.NewProviders("idenqa-api-test", "test")
	t.Cleanup(func() {
		if err := providers.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown telemetry: %v", err)
		}
	})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	accessMiddleware, err := httpapi.NewAccessMiddleware(stubAuthenticator{}, logger)
	if err != nil {
		t.Fatalf("NewAccessMiddleware() error = %v", err)
	}
	tenantRoutes, err := httpapi.NewTenantRoutes(accessMiddleware, stubTenantReader{}, stubTenantExporter{}, logger)
	if err != nil {
		t.Fatalf("NewTenantRoutes() error = %v", err)
	}
	return httpapi.Dependencies{
		Logger:         logger,
		IDs:            identifiers,
		TracerProvider: providers.TracerProvider(),
		MeterProvider:  providers.MeterProvider(),
		RequestTimeout: time.Second,
		MaxBodyBytes:   1_048_576,
	}, tenantRoutes
}

type stubAuthenticator struct{}

func (stubAuthenticator) Authenticate(context.Context, string) (access.Context, error) {
	return access.Context{}, access.ErrInvalidCredential
}

type stubTenantExporter struct{}

func (stubTenantExporter) Export(context.Context, tenantexport.Authority, []tenantexport.Collection, func([]byte) error) error {
	return nil
}

type stubTenantReader struct{}

func (stubTenantReader) Current(context.Context, access.Context) (tenant.Tenant, error) {
	return tenant.Tenant{}, tenant.ErrNotFound
}
