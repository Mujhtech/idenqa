package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mujhtech/idenqa/internal/access"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/Mujhtech/idenqa/internal/tenantexport"
)

func TestTenantExportRoutesStreamNDJSON(t *testing.T) {
	t.Parallel()

	fixture := newHTTPAccessFixture(t, nil, access.Pattern("tenant:export"))
	exporter := &tenantExportStub{lines: [][]byte{
		[]byte("{\"record\":\"header\",\"schema\":\"idenqa.tenant-export\"}\n"),
		[]byte("{\"record\":\"tenant\",\"id\":\"ten_01\"}\n"),
		[]byte("{\"record\":\"footer\",\"counts\":{\"tenant\":1},\"digest\":\"sha256:abc\"}\n"),
	}}
	routes := newTenantExportRoutes(t, fixture, exporter)
	router := versionedRouter(t, routes)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/tenant/export", nil)
	request.Header.Set("Authorization", "Bearer "+fixture.encoded)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body)
	}
	if got := response.Header().Get("Content-Type"); got != tenantExportMediaType {
		t.Fatalf("Content-Type = %q, want %q", got, tenantExportMediaType)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := response.Header().Get("Content-Disposition"); !strings.Contains(got, "idenqa-tenant-export.ndjson") {
		t.Fatalf("Content-Disposition = %q", got)
	}
	if response.Body.String() != string(concat(exporter.lines)) {
		t.Fatalf("body = %q", response.Body.String())
	}
	if exporter.selection != nil {
		t.Fatalf("selection = %v, want nil", exporter.selection)
	}
	if exporter.tenantID != fixture.tenantID.String() {
		t.Fatalf("authority tenant = %q, want %q", exporter.tenantID, fixture.tenantID)
	}
}

func TestTenantExportRoutesValidateCollections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		target     string
		wantStatus int
		want       []tenantexport.Collection
	}{
		{name: "all", target: "/v1/tenant/export", wantStatus: http.StatusOK},
		{
			name:       "filter",
			target:     "/v1/tenant/export?collections=tenant,policies",
			wantStatus: http.StatusOK,
			want:       []tenantexport.Collection{tenantexport.CollectionTenant, tenantexport.CollectionPolicies},
		},
		{name: "unknown collection", target: "/v1/tenant/export?collections=secrets", wantStatus: http.StatusBadRequest},
		{name: "duplicate collection", target: "/v1/tenant/export?collections=tenant,tenant", wantStatus: http.StatusBadRequest},
		{name: "unknown parameter", target: "/v1/tenant/export?cursor=abc", wantStatus: http.StatusBadRequest},
		{name: "repeated parameter", target: "/v1/tenant/export?collections=tenant&collections=policies", wantStatus: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fixture := newHTTPAccessFixture(t, nil, access.Pattern("tenant:export"))
			exporter := &tenantExportStub{}
			routes := newTenantExportRoutes(t, fixture, exporter)
			router := versionedRouter(t, routes)
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, test.target, nil)
			request.Header.Set("Authorization", "Bearer "+fixture.encoded)
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body)
			}
			if test.wantStatus != http.StatusOK {
				return
			}
			if len(exporter.selection) != len(test.want) {
				t.Fatalf("selection = %v, want %v", exporter.selection, test.want)
			}
			for index := range exporter.selection {
				if exporter.selection[index] != test.want[index] {
					t.Fatalf("selection = %v, want %v", exporter.selection, test.want)
				}
			}
		})
	}
}

func TestTenantExportRoutesRequireExportPermission(t *testing.T) {
	t.Parallel()

	fixture := newHTTPAccessFixture(t, nil, access.Pattern("tenant:read"))
	exporter := &tenantExportStub{}
	routes := newTenantExportRoutes(t, fixture, exporter)
	router := versionedRouter(t, routes)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/tenant/export", nil)
	request.Header.Set("Authorization", "Bearer "+fixture.encoded)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusForbidden, response.Body)
	}
	if exporter.called {
		t.Fatal("exporter was called without tenant:export authority")
	}
}

func TestTenantExportRoutesMapFailures(t *testing.T) {
	t.Parallel()

	t.Run("pre-stream", func(t *testing.T) {
		t.Parallel()

		fixture := newHTTPAccessFixture(t, nil, access.Pattern("tenant:export"))
		exporter := &tenantExportStub{err: access.ErrInsufficientScope}
		routes := newTenantExportRoutes(t, fixture, exporter)
		router := versionedRouter(t, routes)
		request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/tenant/export", nil)
		request.Header.Set("Authorization", "Bearer "+fixture.encoded)
		response := httptest.NewRecorder()

		router.ServeHTTP(response, request)

		if response.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusForbidden, response.Body)
		}
		if !strings.Contains(response.Body.String(), "application/problem+json") &&
			!strings.Contains(response.Body.String(), `"code"`) {
			t.Fatalf("body = %q, want a problem response", response.Body.String())
		}
	})

	t.Run("mid-stream", func(t *testing.T) {
		t.Parallel()

		fixture := newHTTPAccessFixture(t, nil, access.Pattern("tenant:export"))
		exporter := &tenantExportStub{
			lines: [][]byte{[]byte("{\"record\":\"header\"}\n")},
			err:   context.Canceled,
		}
		routes := newTenantExportRoutes(t, fixture, exporter)
		router := versionedRouter(t, routes)
		request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/tenant/export", nil)
		request.Header.Set("Authorization", "Bearer "+fixture.encoded)
		response := httptest.NewRecorder()

		router.ServeHTTP(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
		}
		if response.Body.String() != "{\"record\":\"header\"}\n" {
			t.Fatalf("body = %q", response.Body.String())
		}
	})
}

func TestTenantExportRoutesSupportGeneratedClient(t *testing.T) {
	t.Parallel()

	fixture := newHTTPAccessFixture(t, nil, access.Pattern("tenant:export"))
	exporter := &tenantExportStub{lines: [][]byte{
		[]byte("{\"record\":\"header\"}\n"),
		[]byte("{\"record\":\"footer\",\"counts\":{},\"digest\":\"sha256:abc\"}\n"),
	}}
	routes := newTenantExportRoutes(t, fixture, exporter)
	router := versionedRouter(t, routes)
	client, err := openapiv1.NewClientWithResponses(
		"http://idenqa.test",
		openapiv1.WithHTTPClient(handlerDoer{handler: router}),
		openapiv1.WithRequestEditorFn(func(_ context.Context, request *http.Request) error {
			request.Header.Set("Authorization", "Bearer "+fixture.encoded)

			return nil
		}),
	)
	if err != nil {
		t.Fatalf("NewClientWithResponses() error = %v", err)
	}
	response, err := client.ExportTenant(context.Background(), &openapiv1.ExportTenantParams{})
	if err != nil {
		t.Fatalf("ExportTenant() error = %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	material, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read export body: %v", err)
	}
	if string(material) != string(concat(exporter.lines)) {
		t.Fatalf("body = %q", material)
	}
}

func TestTenantExportRoutesRequireDependencies(t *testing.T) {
	t.Parallel()

	fixture := newHTTPAccessFixture(t, nil, access.Pattern("tenant:export"))
	if _, err := NewTenantRoutes(fixture.middleware, &tenantHTTPReaderStub{}, nil, fixture.logger); err == nil {
		t.Error("NewTenantRoutes(nil exporter) error = nil")
	}
}

func newTenantExportRoutes(t *testing.T, fixture *httpAccessFixture, exporter TenantExporter) *TenantRoutes {
	t.Helper()

	routes, err := NewTenantRoutes(
		fixture.middleware,
		&tenantHTTPReaderStub{},
		exporter,
		fixture.logger,
	)
	if err != nil {
		t.Fatalf("NewTenantRoutes() error = %v", err)
	}
	return routes
}

type tenantExportStub struct {
	lines     [][]byte
	err       error
	called    bool
	selection []tenantexport.Collection
	tenantID  string
}

func (stub *tenantExportStub) Export(
	_ context.Context,
	authority tenantexport.Authority,
	selection []tenantexport.Collection,
	emit func([]byte) error,
) error {
	stub.called = true
	stub.selection = selection
	stub.tenantID = authority.TenantScope().ID().String()
	for _, line := range stub.lines {
		if err := emit(line); err != nil {
			return err
		}
	}
	return stub.err
}

func concat(lines [][]byte) []byte {
	result := []byte{}
	for _, line := range lines {
		result = append(result, line...)
	}
	return result
}
