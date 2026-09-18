package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestTenantRoutesReturnAuthenticatedTenant(t *testing.T) {
	t.Parallel()

	fixture := newHTTPAccessFixture(t, nil, access.Pattern("tenant:read"))
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	value, err := tenant.Restore(fixture.tenantID, tenant.StateActive, 1, now, now, nil)
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	reader := &tenantHTTPReaderStub{value: value}
	routes, err := NewTenantRoutes(fixture.middleware, reader, fixture.logger)
	if err != nil {
		t.Fatalf("NewTenantRoutes() error = %v", err)
	}
	router := versionedRouter(t, routes)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/tenant", nil)
	request.Header.Set("Authorization", "Bearer "+fixture.encoded)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body)
	}
	var resource openapiv1.Tenant
	if err := json.Unmarshal(response.Body.Bytes(), &resource); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resource.ID != fixture.tenantID.String() || resource.State != openapiv1.TenantStateActive {
		t.Fatalf("resource = %+v, want authenticated tenant", resource)
	}
	if got, want := response.Header().Get("ETag"), `"1"`; got != want {
		t.Fatalf("ETag = %q, want %q", got, want)
	}
	if reader.tenantID != fixture.tenantID.String() {
		t.Fatalf("reader authority tenant = %q, want %q", reader.tenantID, fixture.tenantID)
	}
}

func TestTenantRoutesSupportGeneratedClient(t *testing.T) {
	t.Parallel()

	fixture := newHTTPAccessFixture(t, nil, access.Pattern("tenant:read"))
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	value, err := tenant.Restore(fixture.tenantID, tenant.StateActive, 1, now, now, nil)
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	routes, err := NewTenantRoutes(
		fixture.middleware,
		&tenantHTTPReaderStub{value: value},
		fixture.logger,
	)
	if err != nil {
		t.Fatalf("NewTenantRoutes() error = %v", err)
	}
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
	response, err := client.GetCurrentTenantWithResponse(context.Background())
	if err != nil {
		t.Fatalf("GetCurrentTenantWithResponse() error = %v", err)
	}
	if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
		t.Fatalf("response status = %d body=%s", response.StatusCode(), response.Body)
	}
	if response.JSON200.ID != fixture.tenantID.String() || response.JSON200.State != openapiv1.TenantStateActive {
		t.Fatalf("tenant = %+v, want authenticated tenant", response.JSON200)
	}
	if response.Headers200 == nil || response.Headers200.ETag == nil || *response.Headers200.ETag != `"1"` {
		t.Fatalf("response headers = %+v, want ETag", response.Headers200)
	}

	unauthenticatedClient, err := openapiv1.NewClientWithResponses(
		"http://idenqa.test",
		openapiv1.WithHTTPClient(handlerDoer{handler: router}),
	)
	if err != nil {
		t.Fatalf("NewClientWithResponses(unauthenticated) error = %v", err)
	}
	unauthenticated, err := unauthenticatedClient.GetCurrentTenantWithResponse(context.Background())
	if err != nil {
		t.Fatalf("GetCurrentTenantWithResponse(unauthenticated) error = %v", err)
	}
	if unauthenticated.ApplicationProblemJSON401 == nil ||
		unauthenticated.ApplicationProblemJSON401.Code != openapiv1.UNAUTHENTICATED {
		t.Fatalf("unauthenticated response = %+v", unauthenticated.ApplicationProblemJSON401)
	}
}

func TestTenantRoutesMapApplicationFailure(t *testing.T) {
	t.Parallel()

	fixture := newHTTPAccessFixture(t, nil, access.Pattern("tenant:read"))
	routes, err := NewTenantRoutes(
		fixture.middleware,
		&tenantHTTPReaderStub{err: tenant.ErrNotFound},
		fixture.logger,
	)
	if err != nil {
		t.Fatalf("NewTenantRoutes() error = %v", err)
	}
	router := versionedRouter(t, routes)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/tenant", nil)
	request.Header.Set("Authorization", "Bearer "+fixture.encoded)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusNotFound, response.Body)
	}
}

func TestNewTenantRoutesRequiresDependencies(t *testing.T) {
	t.Parallel()

	fixture := newHTTPAccessFixture(t, nil, access.Pattern("tenant:read"))
	reader := &tenantHTTPReaderStub{}
	if _, err := NewTenantRoutes(nil, reader, fixture.logger); err == nil {
		t.Error("NewTenantRoutes(nil middleware) error = nil")
	}
	if _, err := NewTenantRoutes(fixture.middleware, nil, fixture.logger); err == nil {
		t.Error("NewTenantRoutes(nil reader) error = nil")
	}
	if _, err := NewTenantRoutes(fixture.middleware, reader, nil); err == nil {
		t.Error("NewTenantRoutes(nil logger) error = nil")
	}
}

type tenantHTTPReaderStub struct {
	value    tenant.Tenant
	err      error
	tenantID string
}

type handlerDoer struct{ handler http.Handler }

func (doer handlerDoer) Do(request *http.Request) (*http.Response, error) {
	response := httptest.NewRecorder()
	doer.handler.ServeHTTP(response, request)

	return response.Result(), nil
}

func (reader *tenantHTTPReaderStub) Current(_ context.Context, authority access.Context) (tenant.Tenant, error) {
	reader.tenantID = authority.TenantScope().ID().String()

	return reader.value, reader.err
}
