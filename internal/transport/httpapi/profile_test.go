package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/evidence"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/Mujhtech/idenqa/internal/platform/cursor"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/apierror"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/go-chi/chi/v5"
)

func TestProfileRoutesCreateStrictDraft(t *testing.T) {
	t.Parallel()

	fixture := newHTTPAccessFixture(t, nil, access.Pattern("capture_profiles:*"))
	registry, catalog, document := profileHTTPDocument(t)
	_ = registry
	now := time.Date(2026, time.August, 27, 20, 0, 0, 0, time.UTC)
	result := verification.MutationResult{
		ProfileID:      "prf_01K3P4NQF00000000000000000",
		Name:           "Standard identity",
		State:          verification.ProfileStateDraft,
		Version:        1,
		LatestRevision: 1,
		DraftRevision:  uint32PointerForHTTP(1),
		Revision:       1,
		Digest:         "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		UpdatedAt:      now,
	}
	service := &profileHTTPServiceStub{mutation: result}
	routes, err := NewProfileRoutes(
		fixture.middleware,
		service,
		catalog,
		profileHTTPCursor(t, now),
		fixture.logger,
	)
	if err != nil {
		t.Fatalf("NewProfileRoutes() error = %v", err)
	}
	router := chi.NewRouter()
	routes.Register(router)
	canonical, err := verification.CanonicalJSON(document, registry)
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	body, err := json.Marshal(openapiv1.CaptureProfileWrite{
		Name:     "Standard identity",
		Document: canonical,
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/capture-profiles", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+fixture.encoded)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", `"attempt-1"`)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusCreated, response.Body)
	}
	if response.Header().Get("ETag") != `"1"` ||
		response.Header().Get("Location") != "/v1/capture-profiles/"+result.ProfileID {
		t.Fatalf("response headers = %+v", response.Header())
	}
	if service.idempotencyKey != "attempt-1" || service.tenantID != fixture.tenantID.String() ||
		service.document.Registry != registry.Reference() {
		t.Fatalf("service input key=%q tenant=%q document=%+v", service.idempotencyKey, service.tenantID, service.document)
	}
	var resource openapiv1.CaptureProfileMutation
	if err := json.Unmarshal(response.Body.Bytes(), &resource); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resource.ProfileID != result.ProfileID || resource.Revision != 1 || resource.DraftRevision == nil {
		t.Fatalf("resource = %+v", resource)
	}
}

func TestProfileRoutesRejectInvalidHeadersAndBodies(t *testing.T) {
	t.Parallel()

	fixture := newHTTPAccessFixture(t, nil, access.Pattern("capture_profiles:*"))
	_, catalog, _ := profileHTTPDocument(t)
	service := &profileHTTPServiceStub{}
	routes, err := NewProfileRoutes(
		fixture.middleware,
		service,
		catalog,
		profileHTTPCursor(t, time.Now()),
		fixture.logger,
	)
	if err != nil {
		t.Fatalf("NewProfileRoutes() error = %v", err)
	}
	router := chi.NewRouter()
	routes.Register(router)

	tests := []struct {
		name       string
		method     string
		target     string
		body       string
		headers    map[string]string
		wantStatus int
		wantCode   string
	}{
		{
			name: "missing idempotency key", method: http.MethodPost, target: "/v1/capture-profiles",
			body: `{}`, headers: map[string]string{"Content-Type": "application/json"},
			wantStatus: http.StatusBadRequest, wantCode: apierror.CodeInvalidRequest,
		},
		{
			name: "unquoted idempotency key", method: http.MethodPost, target: "/v1/capture-profiles",
			body: `{}`, headers: map[string]string{"Content-Type": "application/json", "Idempotency-Key": "attempt"},
			wantStatus: http.StatusBadRequest, wantCode: apierror.CodeInvalidRequest,
		},
		{
			name: "missing if match", method: http.MethodPost,
			target:     "/v1/capture-profiles/prf_01K3P4NQF00000000000000000/publish",
			headers:    map[string]string{"Idempotency-Key": `"attempt"`},
			wantStatus: http.StatusPreconditionRequired, wantCode: apierror.CodePreconditionRequired,
		},
		{
			name: "weak if match", method: http.MethodPost,
			target:     "/v1/capture-profiles/prf_01K3P4NQF00000000000000000/publish",
			headers:    map[string]string{"Idempotency-Key": `"attempt"`, "If-Match": `W/"1"`},
			wantStatus: http.StatusBadRequest, wantCode: apierror.CodeInvalidRequest,
		},
		{
			name: "unknown create field", method: http.MethodPost, target: "/v1/capture-profiles",
			body:       `{"name":"one","document":{},"unknown":true}`,
			headers:    map[string]string{"Content-Type": "application/json", "Idempotency-Key": `"attempt"`},
			wantStatus: http.StatusBadRequest, wantCode: apierror.CodeInvalidRequest,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(
				context.Background(),
				test.method,
				test.target,
				bytes.NewBufferString(test.body),
			)
			request.Header.Set("Authorization", "Bearer "+fixture.encoded)
			for name, value := range test.headers {
				request.Header.Set(name, value)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body)
			}
			var problem openapiv1.Problem
			if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
				t.Fatalf("decode problem: %v", err)
			}
			if string(problem.Code) != test.wantCode {
				t.Fatalf("problem code = %q, want %q", problem.Code, test.wantCode)
			}
		})
	}
	if service.calls != 0 {
		t.Fatalf("invalid requests reached service %d times", service.calls)
	}
}

func TestProfileRoutesEnforceReadAndWriteScopes(t *testing.T) {
	t.Parallel()

	fixture := newHTTPAccessFixture(t, nil, access.Pattern("capture_profiles:read"))
	_, catalog, _ := profileHTTPDocument(t)
	service := &profileHTTPServiceStub{}
	routes, err := NewProfileRoutes(
		fixture.middleware,
		service,
		catalog,
		profileHTTPCursor(t, time.Now()),
		fixture.logger,
	)
	if err != nil {
		t.Fatalf("NewProfileRoutes() error = %v", err)
	}
	router := chi.NewRouter()
	routes.Register(router)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/capture-profiles", nil)
	request.Header.Set("Authorization", "Bearer "+fixture.encoded)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusForbidden, response.Body)
	}
	if service.calls != 0 {
		t.Fatal("wrong-scope request reached service")
	}
}

type profileHTTPServiceStub struct {
	mutation       verification.MutationResult
	err            error
	calls          int
	idempotencyKey string
	tenantID       string
	document       verification.Profile
}

func (service *profileHTTPServiceStub) Create(
	_ context.Context,
	authority access.Context,
	key string,
	_ string,
	document verification.Profile,
) (verification.MutationResult, error) {
	service.calls++
	service.idempotencyKey = key
	service.tenantID = authority.TenantScope().ID().String()
	service.document = document

	return service.mutation, service.err
}

func (service *profileHTTPServiceStub) UpdateDraft(
	context.Context,
	access.Context,
	id.Profile,
	int64,
	string,
	verification.Profile,
) (verification.MutationResult, error) {
	service.calls++

	return service.mutation, service.err
}

func (service *profileHTTPServiceStub) Publish(
	context.Context,
	access.Context,
	id.Profile,
	int64,
	string,
) (verification.MutationResult, error) {
	service.calls++

	return service.mutation, service.err
}

func (service *profileHTTPServiceStub) Supersede(
	context.Context,
	access.Context,
	id.Profile,
	int64,
	string,
	verification.Profile,
) (verification.MutationResult, error) {
	service.calls++

	return service.mutation, service.err
}

func (service *profileHTTPServiceStub) Deactivate(
	context.Context,
	access.Context,
	id.Profile,
	int64,
	string,
) (verification.MutationResult, error) {
	service.calls++

	return service.mutation, service.err
}

func (service *profileHTTPServiceStub) Find(
	context.Context,
	access.Context,
	id.Profile,
) (verification.CaptureProfile, error) {
	service.calls++

	return verification.CaptureProfile{}, service.err
}

func (service *profileHTTPServiceStub) FindRevision(
	context.Context,
	access.Context,
	id.Profile,
	uint32,
) (verification.Revision, error) {
	service.calls++

	return verification.Revision{}, service.err
}

func (service *profileHTTPServiceStub) List(
	context.Context,
	access.Context,
	*verification.ListPosition,
	int,
) (verification.Page, error) {
	service.calls++

	return verification.Page{Profiles: []verification.CaptureProfile{}}, service.err
}

func (service *profileHTTPServiceStub) ValidateDraft(
	context.Context,
	access.Context,
	id.Profile,
) (string, error) {
	service.calls++

	return service.mutation.Digest, service.err
}

func profileHTTPDocument(t *testing.T) (evidence.Registry, evidence.Catalog, verification.Profile) {
	t.Helper()

	registry, err := evidence.BuiltInRegistry()
	if err != nil {
		t.Fatalf("BuiltInRegistry() error = %v", err)
	}
	catalog, err := evidence.NewCatalog(registry)
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	document, err := verification.NewProfile(registry, []verification.Requirement{{
		Key:          "selfie",
		Purpose:      evidence.PurposeIdentityVerification,
		EvidenceType: evidence.EvidenceSelfieImage,
		Artefacts:    []evidence.Name{evidence.ArtefactSelfieImage},
		Acquisition: verification.Acquisition{
			Strategy: verification.StrategyAnyOf,
			Methods:  []evidence.Name{evidence.MethodFileUpload},
		},
	}})
	if err != nil {
		t.Fatalf("NewProfile() error = %v", err)
	}

	return registry, catalog, document
}

func profileHTTPCursor(t *testing.T, now time.Time) *cursor.Codec {
	t.Helper()

	keys, err := cursor.NewKeyring(1, map[cursor.KeyVersion][]byte{1: bytes.Repeat([]byte{7}, 32)})
	if err != nil {
		t.Fatalf("NewKeyring() error = %v", err)
	}
	codec, err := cursor.New(keys, httpAccessClock{now: now}, 15*time.Minute)
	if err != nil {
		t.Fatalf("cursor.New() error = %v", err)
	}

	return codec
}

func uint32PointerForHTTP(value uint32) *uint32 { return &value }
