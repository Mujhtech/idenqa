package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	contract "github.com/Mujhtech/idenqa/contracts/experience/v1"
	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/experience"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/go-chi/chi/v5"
)

var experienceHTTPFixtureID = mustExperienceTestID("exp_01J00000000000000000000000")

func mustExperienceTestID(value string) id.Experience {
	identifier, err := id.ParseExperience(value)
	if err != nil {
		panic(err)
	}
	return identifier
}

type experienceHTTPServiceStub struct {
	value       experience.Experience
	manifest    contract.Manifest
	resolution  contract.Resolution
	lastDraft   experience.DraftRequest
	lastReason  string
	lastTarget  uint32
	lastImport  []byte
	lastRequest experience.ResolutionRequest
	err         error
}

func (stub *experienceHTTPServiceStub) Create(_ context.Context, _ access.Context, request experience.DraftRequest) (experience.Experience, error) {
	stub.lastDraft = request
	return stub.value, stub.err
}

func (stub *experienceHTTPServiceStub) UpdateDraft(_ context.Context, _ access.Context, _ id.Experience, _ int64, request experience.DraftRequest) (experience.Experience, error) {
	stub.lastDraft = request
	return stub.value, stub.err
}

func (stub *experienceHTTPServiceStub) Approve(context.Context, access.Context, id.Experience, int64) (experience.Experience, error) {
	return stub.value, stub.err
}

func (stub *experienceHTTPServiceStub) Publish(context.Context, access.Context, id.Experience, int64) (experience.Experience, error) {
	return stub.value, stub.err
}

func (stub *experienceHTTPServiceStub) Revoke(_ context.Context, _ access.Context, _ id.Experience, _ int64, reason string) (experience.Experience, error) {
	stub.lastReason = reason
	return stub.value, stub.err
}

func (stub *experienceHTTPServiceStub) Rollback(_ context.Context, _ access.Context, _ id.Experience, _ int64, target uint32, reason string) (experience.Experience, error) {
	stub.lastTarget, stub.lastReason = target, reason
	return stub.value, stub.err
}

func (stub *experienceHTTPServiceStub) Get(context.Context, access.Context, id.Experience) (experience.Experience, error) {
	return stub.value, stub.err
}

func (stub *experienceHTTPServiceStub) List(context.Context, access.Context, *experience.Position, int) (experience.Page, error) {
	return experience.Page{Experiences: []experience.Experience{stub.value}}, stub.err
}

func (stub *experienceHTTPServiceStub) Export(context.Context, access.Context, id.Experience) (contract.Manifest, error) {
	return stub.manifest, stub.err
}

func (stub *experienceHTTPServiceStub) Import(_ context.Context, _ access.Context, raw []byte) (experience.Experience, error) {
	stub.lastImport = raw
	return stub.value, stub.err
}

func (stub *experienceHTTPServiceStub) ResolveForSession(_ context.Context, _ tenant.Scope, _ id.Verification, request experience.ResolutionRequest) (contract.Resolution, error) {
	stub.lastRequest = request
	return stub.resolution, stub.err
}

func (stub *experienceHTTPServiceStub) SafeDefault(time.Time) contract.Resolution {
	return stub.resolution
}

func newExperienceTestRouter(t *testing.T, stub *experienceHTTPServiceStub, patterns ...access.Pattern) (http.Handler, string) {
	t.Helper()
	fixture := newHTTPAccessFixture(t, nil, patterns...)
	routes, err := NewExperienceRoutes(fixture.middleware, &CaptureAccessMiddleware{}, stub, profileHTTPCursor(t, time.Date(2026, time.August, 27, 20, 0, 0, 0, time.UTC)), fixture.logger)
	if err != nil {
		t.Fatalf("NewExperienceRoutes() error = %v", err)
	}
	router := chi.NewRouter()
	routes.Register(router)
	return router, fixture.encoded
}

func experienceStubValue() experience.Experience {
	document := experience.SafeDefaultDocument()
	return experience.Experience{
		ID: experienceHTTPFixtureID, State: experience.StateDraft, Revision: 1, LatestVersion: 1,
		Document: document, CreatedAt: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC),
	}
}

func TestExperienceRoutesLifecycle(t *testing.T) {
	t.Parallel()
	stub := &experienceHTTPServiceStub{value: experienceStubValue()}
	handler, credential := newExperienceTestRouter(t, stub, access.Pattern("experiences:*"))

	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/experiences", bytes.NewReader(mustExperienceJSON(t, map[string]any{
		"name": "Acme", "copy": map[string]any{"version": "tc_acme_v1", "locales": []any{map[string]any{"locale": "en", "entries": []any{map[string]any{"key": "capture.title", "value": "Verify"}}}}},
		"mandatory_copy_version": experience.DefaultMandatoryVersion, "default_locale": "en",
		"targeting": []any{}, "links": map[string]any{"support": "https://acme.example/support", "privacy": "https://acme.example/privacy", "terms": "https://acme.example/terms"},
		"theme": map[string]any{"primary_color": "#1f6feb", "accent_color": "#0b3d91", "background_color": "#ffffff", "text_color": "#1b1f23"},
	})))
	request.Header.Set("Authorization", "Bearer "+credential)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", response.Code, response.Body.String())
	}
	if stub.lastDraft.Name != "Acme" || stub.lastDraft.DefaultLocale != "en" {
		t.Fatalf("create draft = %+v", stub.lastDraft)
	}
	if got := response.Header().Get("ETag"); got != `"experience:1"` {
		t.Fatalf("etag = %q", got)
	}

	for _, test := range []struct {
		name   string
		method string
		path   string
		body   map[string]any
	}{
		{name: "get", method: http.MethodGet, path: "/experiences/" + experienceHTTPFixtureID.String()},
		{name: "update", method: http.MethodPut, path: "/experiences/" + experienceHTTPFixtureID.String(), body: map[string]any{
			"expected_version": 1, "document": map[string]any{
				"name": "Acme 2", "copy": map[string]any{"version": "tc_acme_v1", "locales": []any{map[string]any{"locale": "en", "entries": []any{map[string]any{"key": "capture.title", "value": "Verify"}}}}},
				"mandatory_copy_version": experience.DefaultMandatoryVersion, "default_locale": "en",
				"targeting": []any{}, "links": map[string]any{"support": "https://acme.example/support", "privacy": "https://acme.example/privacy", "terms": "https://acme.example/terms"},
				"theme": map[string]any{"primary_color": "#1f6feb", "accent_color": "#0b3d91", "background_color": "#ffffff", "text_color": "#1b1f23"},
			},
		}},
		{name: "approve", method: http.MethodPost, path: "/experiences/" + experienceHTTPFixtureID.String() + "/approve", body: map[string]any{"expected_version": 2}},
		{name: "publish", method: http.MethodPost, path: "/experiences/" + experienceHTTPFixtureID.String() + "/publish", body: map[string]any{"expected_version": 3}},
		{name: "revoke", method: http.MethodPost, path: "/experiences/" + experienceHTTPFixtureID.String() + "/revoke", body: map[string]any{"expected_version": 4, "reason": "incident"}},
		{name: "rollback", method: http.MethodPost, path: "/experiences/" + experienceHTTPFixtureID.String() + "/rollback", body: map[string]any{"expected_version": 5, "target_version": 1, "reason": "restore"}},
		{name: "export", method: http.MethodGet, path: "/experiences/" + experienceHTTPFixtureID.String() + "/export"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var body bytes.Reader
			if test.body != nil {
				body = *bytes.NewReader(mustExperienceJSON(t, test.body))
			}
			req := httptest.NewRequestWithContext(t.Context(), test.method, test.path, &body)
			req.Header.Set("Authorization", "Bearer "+credential)
			if test.body != nil {
				req.Header.Set("Content-Type", "application/json")
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
		})
	}
	if stub.lastReason != "restore" || stub.lastTarget != 1 {
		t.Fatalf("rollback command = %+v", stub)
	}
}

func TestExperienceRoutesEnforceScopesAndClosedBodies(t *testing.T) {
	t.Parallel()
	stub := &experienceHTTPServiceStub{value: experienceStubValue()}
	handler, credential := newExperienceTestRouter(t, stub, access.Pattern("experiences:read"))

	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/experiences", bytes.NewReader([]byte(`{}`)))
	request.Header.Set("Authorization", "Bearer "+credential)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("create without write scope status = %d, body = %s", response.Code, response.Body.String())
	}

	unknown := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/experiences/import", bytes.NewReader([]byte(`{"document":{}}`)))
	unknown.Header.Set("Authorization", "Bearer "+credential)
	unknown.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, unknown)
	if response.Code != http.StatusForbidden {
		t.Fatalf("import without write scope status = %d", response.Code)
	}

	missing := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/experiences/not-an-experience", nil)
	missing.Header.Set("Authorization", "Bearer "+credential)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, missing)
	if response.Code != http.StatusNotFound {
		t.Fatalf("invalid id status = %d, body = %s", response.Code, response.Body.String())
	}
}

func mustExperienceJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	return encoded
}
