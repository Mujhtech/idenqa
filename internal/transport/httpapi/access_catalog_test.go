package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mujhtech/idenqa/internal/access"
)

func TestAccessCatalogRoutesExposeTheAuthoritativeTenantRegistry(t *testing.T) {
	t.Parallel()
	fixture := newHTTPAccessFixture(t, nil, access.Pattern("tenant:read"))
	routes, err := NewAccessCatalogRoutes(fixture.middleware, access.TenantRegistry(), fixture.logger)
	if err != nil {
		t.Fatalf("NewAccessCatalogRoutes() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/access/permissions", nil)
	request.Header.Set("Authorization", "Bearer "+fixture.encoded)
	response := httptest.NewRecorder()
	versionedRouter(t, routes).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	var body struct {
		Version string                   `json:"version"`
		Data    []permissionCatalogEntry `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Version != "idenqa.core/access-permissions/v1" || len(body.Data) != len(access.TenantRegistry().Permissions()) {
		t.Fatalf("catalog = version %q, count %d", body.Version, len(body.Data))
	}
}
