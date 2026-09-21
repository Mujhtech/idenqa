package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/pack"
	"github.com/go-chi/chi/v5"
)

func newPackTestRouter(t *testing.T, patterns ...access.Pattern) (http.Handler, string) {
	t.Helper()
	seeds, err := pack.Seeds()
	if err != nil {
		t.Fatalf("Seeds() error = %v", err)
	}
	registry, err := pack.NewRegistry(seeds, nil, func() time.Time { return time.Date(2026, time.September, 20, 0, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	fixture := newHTTPAccessFixture(t, nil, patterns...)
	routes, err := NewPackRoutes(fixture.middleware, registry, fixture.logger)
	if err != nil {
		t.Fatalf("NewPackRoutes() error = %v", err)
	}
	router := chi.NewRouter()
	routes.Register(router)
	return router, fixture.encoded
}

func packTestRequest(t *testing.T, handler http.Handler, target, credential string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	request.Header.Set("Authorization", "Bearer "+credential)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestPackRoutesExposeRegistryAndSupportHonestly(t *testing.T) {
	handler, credential := newPackTestRouter(t, access.Pattern("packs:read"))

	response := packTestRequest(t, handler, "/packs", credential)
	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", response.Code, response.Body.String())
	}
	var page struct {
		Data []pack.Summary `json:"data"`
		Page struct {
			HasMore bool `json:"has_more"`
		} `json:"page"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatalf("list body = %s: %v", response.Body.String(), err)
	}
	if len(page.Data) != 4 || page.Page.HasMore {
		t.Fatalf("list page = %+v", page)
	}
	if page.Data[0].Country != "GH" || page.Data[0].LifecycleState != pack.LifecycleActive {
		t.Fatalf("first summary = %+v", page.Data[0])
	}

	response = packTestRequest(t, handler, "/packs/NG", credential)
	if response.Code != http.StatusOK {
		t.Fatalf("country status = %d, body = %s", response.Code, response.Body.String())
	}
	var resource struct {
		Pack           pack.Pack           `json:"pack"`
		LifecycleState pack.LifecycleState `json:"lifecycle_state"`
		Version        int64               `json:"version"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &resource); err != nil {
		t.Fatalf("country body = %s: %v", response.Body.String(), err)
	}
	if resource.Pack.CountryAlpha3 != "NGA" || resource.LifecycleState != pack.LifecycleActive || resource.Version != 0 {
		t.Fatalf("country resource = %+v", resource)
	}

	response = packTestRequest(t, handler, "/packs/NGA/revisions/1", credential)
	if response.Code != http.StatusOK {
		t.Fatalf("revision status = %d, body = %s", response.Code, response.Body.String())
	}

	response = packTestRequest(t, handler, "/document-support?country=NG&type=passport", credential)
	if response.Code != http.StatusOK {
		t.Fatalf("support status = %d, body = %s", response.Code, response.Body.String())
	}
	var support pack.SupportProjection
	if err := json.Unmarshal(response.Body.Bytes(), &support); err != nil {
		t.Fatalf("support body = %s: %v", response.Body.String(), err)
	}
	if support.SupportLevel != pack.SupportStructurallySupported || support.LegalReview.State != pack.LegalReviewNotReviewed {
		t.Fatalf("support projection = %+v", support)
	}
	if len(support.RequiredSides) != 1 || support.RequiredSides[0] != pack.SideFront {
		t.Fatalf("support sides = %+v", support.RequiredSides)
	}
	if support.MRZ.State != pack.DeclarationSupported || len(support.Evidence) == 0 {
		t.Fatalf("support declarations = %+v", support)
	}

	response = packTestRequest(t, handler, "/document-support?country=NG&type=drivers_license", credential)
	if response.Code != http.StatusOK {
		t.Fatalf("canonical type status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestPackRoutesFailClosed(t *testing.T) {
	handler, credential := newPackTestRouter(t, access.Pattern("packs:read"))

	for _, test := range []struct {
		name   string
		target string
		status int
	}{
		{name: "unknown country", target: "/packs/XX", status: http.StatusNotFound},
		{name: "unknown revision", target: "/packs/NG/revisions/9", status: http.StatusNotFound},
		{name: "zero revision", target: "/packs/NG/revisions/0", status: http.StatusBadRequest},
		{name: "missing query", target: "/document-support?country=NG", status: http.StatusBadRequest},
		{name: "unknown document", target: "/document-support?country=NG&type=residence_permit", status: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := packTestRequest(t, handler, test.target, credential)
			if response.Code != test.status {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if got := response.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q", got)
			}
		})
	}

	limited, limitedCredential := newPackTestRouter(t, access.Pattern("tenant:read"))
	response := packTestRequest(t, limited, "/packs", limitedCredential)
	if response.Code != http.StatusForbidden {
		t.Fatalf("insufficient scope status = %d, body = %s", response.Code, response.Body.String())
	}
}
