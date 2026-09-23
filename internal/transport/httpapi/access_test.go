package httpapi

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/apierror"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/go-chi/chi/v5"
)

func TestAccessMiddlewareAuthenticatesAndCarriesAuthority(t *testing.T) {
	t.Parallel()

	fixture := newHTTPAccessFixture(t, nil, access.Pattern("tenant:read"))
	handler := fixture.protectedHandler(t)
	request := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"/tenants/"+fixture.tenantID.String(),
		nil,
	)
	request.Header.Set("Authorization", "bearer "+fixture.encoded)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body)
	}
	if !fixture.applicationCalled {
		t.Fatal("protected application handler was not called")
	}
}

func TestAccessMiddlewareAcceptsOnlyBearerHeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		configure func(*http.Request, string)
	}{
		{name: "missing"},
		{name: "basic", configure: func(request *http.Request, encoded string) {
			request.Header.Set("Authorization", "Basic "+encoded)
		}},
		{name: "extra space", configure: func(request *http.Request, encoded string) {
			request.Header.Set("Authorization", "Bearer  "+encoded)
		}},
		{name: "tab separator", configure: func(request *http.Request, encoded string) {
			request.Header.Set("Authorization", "Bearer\t"+encoded)
		}},
		{name: "duplicate", configure: func(request *http.Request, encoded string) {
			request.Header.Add("Authorization", "Bearer "+encoded)
			request.Header.Add("Authorization", "Bearer "+encoded)
		}},
		{name: "query", configure: func(request *http.Request, encoded string) {
			query := request.URL.Query()
			query.Set("api_key", encoded)
			request.URL.RawQuery = query.Encode()
		}},
		{name: "cookie", configure: func(request *http.Request, encoded string) {
			request.AddCookie(&http.Cookie{
				Name: "api_key", Value: encoded, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
			})
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fixture := newHTTPAccessFixture(t, nil, access.Pattern("tenant:read"))
			handler := fixture.protectedHandler(t)
			request := httptest.NewRequestWithContext(
				context.Background(),
				http.MethodGet,
				"/tenants/"+fixture.tenantID.String(),
				nil,
			)
			if test.configure != nil {
				test.configure(request, fixture.encoded)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			assertAccessProblem(t, response, http.StatusUnauthorized, apierror.CodeUnauthenticated)
			if got, want := response.Header().Get("WWW-Authenticate"), `Bearer realm="idenqa"`; got != want {
				t.Fatalf("WWW-Authenticate = %q, want %q", got, want)
			}
			if fixture.applicationCalled {
				t.Fatal("unauthenticated request reached the application handler")
			}
		})
	}
}

func TestAccessMiddlewareReturnsNonDisclosingAuthenticationFailure(t *testing.T) {
	t.Parallel()

	fixture := newHTTPAccessFixture(t, nil, access.Pattern("tenant:read"))
	handler := fixture.protectedHandler(t)
	request := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"/tenants/"+fixture.tenantID.String(),
		nil,
	)
	request.Header.Set("Authorization", "Bearer "+fixture.wrongSecret)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assertAccessProblem(t, response, http.StatusUnauthorized, apierror.CodeUnauthenticated)
	combined := response.Body.String() + fixture.logs.String()
	if strings.Contains(combined, fixture.wrongSecret) || strings.Contains(combined, fixture.encoded) {
		t.Fatal("authentication response or logs exposed credential material")
	}
}

func TestAccessMiddlewareDistinguishesScopeOnlyAfterAuthentication(t *testing.T) {
	t.Parallel()

	fixture := newHTTPAccessFixture(t, nil, access.Pattern("capture_profiles:read"))
	handler := fixture.protectedHandler(t)
	request := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"/tenants/"+fixture.tenantID.String(),
		nil,
	)
	request.Header.Set("Authorization", "Bearer "+fixture.encoded)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assertAccessProblem(t, response, http.StatusForbidden, apierror.CodeInsufficientScope)
	if response.Header().Get("WWW-Authenticate") != "" {
		t.Fatal("insufficient-scope response contained an authentication challenge")
	}
	if fixture.applicationCalled {
		t.Fatal("wrong-scope request reached the application handler")
	}
}

func TestAccessMiddlewareAuthorizeCombinesAuthenticationAndScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		pattern    access.Pattern
		credential bool
		wantStatus int
		wantCode   string
	}{
		{
			name: "missing credential", pattern: access.Pattern("tenant:read"),
			wantStatus: http.StatusUnauthorized, wantCode: apierror.CodeUnauthenticated,
		},
		{
			name: "missing permission", pattern: access.Pattern("capture_profiles:read"), credential: true,
			wantStatus: http.StatusForbidden, wantCode: apierror.CodeInsufficientScope,
		},
		{
			name: "authorized", pattern: access.Pattern("tenant:read"), credential: true,
			wantStatus: http.StatusOK,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fixture := newHTTPAccessFixture(t, nil, test.pattern)
			called := false
			router := chi.NewRouter()
			router.With(fixture.middleware.Authorize(access.PermissionTenantRead)).Get("/authorized",
				func(writer http.ResponseWriter, _ *http.Request) {
					called = true
					writer.WriteHeader(http.StatusOK)
				})
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/authorized", nil)
			if test.credential {
				request.Header.Set("Authorization", "Bearer "+fixture.encoded)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if wantCalled := test.wantStatus == http.StatusOK; called != wantCalled {
				t.Fatalf("handler called = %v, want %v", called, wantCalled)
			}
			if test.wantCode == "" {
				if response.Code != test.wantStatus {
					t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
				}
				return
			}
			assertAccessProblem(t, response, test.wantStatus, test.wantCode)
		})
	}
}

func TestAccessMiddlewareCrossTenantProbeIsNotFound(t *testing.T) {
	t.Parallel()

	fixture := newHTTPAccessFixture(t, nil, access.Pattern("tenant:read"))
	handler := fixture.protectedHandler(t)
	request := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"/tenants/"+fixture.otherTenantID.String(),
		nil,
	)
	request.Header.Set("Authorization", "Bearer "+fixture.encoded)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assertAccessProblem(t, response, http.StatusNotFound, apierror.CodeNotFound)
	if !fixture.applicationCalled {
		t.Fatal("authenticated cross-tenant request did not reach application authorisation")
	}
}

func TestAccessMiddlewareMapsOperationalAuthenticationFailureToInternalError(t *testing.T) {
	t.Parallel()

	operationalErr := errors.New("database unavailable password=sensitive")
	fixture := newHTTPAccessFixture(t, operationalErr, access.Pattern("tenant:read"))
	handler := fixture.protectedHandler(t)
	request := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"/tenants/"+fixture.tenantID.String(),
		nil,
	)
	request.Header.Set("Authorization", "Bearer "+fixture.encoded)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assertAccessProblem(t, response, http.StatusInternalServerError, apierror.CodeInternalError)
	combined := response.Body.String() + fixture.logs.String()
	if strings.Contains(combined, "password=sensitive") || strings.Contains(combined, fixture.encoded) {
		t.Fatal("operational authentication failure exposed internal or credential material")
	}
}

func TestAccessMiddlewareRejectsMissingDependenciesAndAuthority(t *testing.T) {
	t.Parallel()

	fixture := newHTTPAccessFixture(t, nil, access.Pattern("tenant:read"))
	if _, err := NewAccessMiddleware(nil, fixture.logger); err == nil {
		t.Error("NewAccessMiddleware(nil authenticator) error = nil")
	}
	if _, err := NewAccessMiddleware(fixture.authenticator, nil); err == nil {
		t.Error("NewAccessMiddleware(nil logger) error = nil")
	}
	if _, ok := AccessContext(context.Background()); ok {
		t.Fatal("AccessContext(empty) returned authority")
	}

	response := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	fixture.middleware.Require(access.PermissionTenantRead)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler called without authenticated context")
	})).ServeHTTP(response, request)
	assertAccessProblem(t, response, http.StatusUnauthorized, apierror.CodeUnauthenticated)
}

type httpAccessClock struct{ now time.Time }

func (source httpAccessClock) Now() time.Time { return source.now }

type httpVerificationRepository struct {
	record access.VerificationRecord
	err    error
}

func (repository httpVerificationRepository) FindForVerification(
	_ context.Context,
	_ id.Tenant,
	_ id.APIKey,
) (access.VerificationRecord, error) {
	return repository.record, repository.err
}

type httpAccessFixture struct {
	authenticator     *access.Authenticator
	middleware        *AccessMiddleware
	logger            *slog.Logger
	logs              *bytes.Buffer
	encoded           string
	wrongSecret       string
	tenantID          id.Tenant
	otherTenantID     id.Tenant
	applicationCalled bool
}

func newHTTPAccessFixture(t *testing.T, repositoryErr error, patterns ...access.Pattern) *httpAccessFixture {
	t.Helper()

	now := time.Date(2026, time.August, 27, 20, 0, 0, 0, time.UTC)
	identifiers, err := id.NewGenerator(httpAccessClock{now: now}, bytes.NewReader(bytes.Repeat([]byte{1}, 512)))
	if err != nil {
		t.Fatalf("NewGenerator() error = %v", err)
	}
	tenantID, err := identifiers.NewTenant()
	if err != nil {
		t.Fatalf("NewTenant() error = %v", err)
	}
	otherTenantID, err := identifiers.NewTenant()
	if err != nil {
		t.Fatalf("NewTenant(other) error = %v", err)
	}
	keyID, err := identifiers.NewAPIKey()
	if err != nil {
		t.Fatalf("NewAPIKey() error = %v", err)
	}
	secretGenerator, err := access.NewKeyGenerator(bytes.NewReader(bytes.Repeat([]byte{0x51}, 32)))
	if err != nil {
		t.Fatalf("NewKeyGenerator() error = %v", err)
	}
	presented, err := secretGenerator.Generate(tenantID, keyID)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	wrongGenerator, err := access.NewKeyGenerator(bytes.NewReader(bytes.Repeat([]byte{0x52}, 32)))
	if err != nil {
		t.Fatalf("NewKeyGenerator(wrong) error = %v", err)
	}
	wrong, err := wrongGenerator.Generate(tenantID, keyID)
	if err != nil {
		t.Fatalf("Generate(wrong) error = %v", err)
	}
	peppers, err := access.NewPepperSet(1, map[access.PepperVersion][]byte{
		1: bytes.Repeat([]byte{0x61}, 32),
	})
	if err != nil {
		t.Fatalf("NewPepperSet() error = %v", err)
	}
	digest, pepperVersion, err := peppers.Digest(presented)
	if err != nil {
		t.Fatalf("Digest() error = %v", err)
	}
	grant, err := access.TenantRegistry().Resolve(patterns...)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	key, err := access.RestoreKey(access.KeyRecord{
		ID: keyID, TenantID: tenantID, Label: "HTTP test backend",
		Digest: digest, PepperVersion: pepperVersion, Grant: grant, Version: 1,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("RestoreKey() error = %v", err)
	}
	record, err := access.NewVerificationRecord(key, tenant.StateActive)
	if err != nil {
		t.Fatalf("NewVerificationRecord() error = %v", err)
	}
	authenticator, err := access.NewAuthenticator(
		httpVerificationRepository{record: record, err: repositoryErr},
		peppers,
		httpAccessClock{now: now},
	)
	if err != nil {
		t.Fatalf("NewAuthenticator() error = %v", err)
	}
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	middleware, err := NewAccessMiddleware(authenticator, logger)
	if err != nil {
		t.Fatalf("NewAccessMiddleware() error = %v", err)
	}

	return &httpAccessFixture{
		authenticator: authenticator,
		middleware:    middleware,
		logger:        logger,
		logs:          logs,
		encoded:       presented.Reveal(),
		wrongSecret:   wrong.Reveal(),
		tenantID:      tenantID,
		otherTenantID: otherTenantID,
	}
}

func (fixture *httpAccessFixture) protectedHandler(t *testing.T) http.Handler {
	t.Helper()

	router := chi.NewRouter()
	router.With(fixture.middleware.Authenticate, fixture.middleware.Require(access.PermissionTenantRead)).Get(
		"/tenants/{tenantID}",
		func(writer http.ResponseWriter, request *http.Request) {
			fixture.applicationCalled = true
			accessContext, ok := AccessContext(request.Context())
			if !ok {
				t.Fatal("protected handler has no access context")
			}
			// Application-level authorisation remains mandatory even though the
			// route middleware already rejected an insufficient grant.
			if err := accessContext.Require(access.PermissionTenantRead); err != nil {
				writeFixtureProblem(t, writer, request, err)

				return
			}
			target, err := id.ParseTenant(chi.URLParam(request, "tenantID"))
			if err != nil || target.String() != accessContext.TenantScope().ID().String() {
				writeFixtureProblem(t, writer, request, tenant.ErrNotFound)

				return
			}
			if err := respond.JSON(writer, request, http.StatusOK, map[string]string{"tenant_id": target.String()}); err != nil {
				t.Fatalf("write success response: %v", err)
			}
		},
	)

	return router
}

func writeFixtureProblem(t *testing.T, writer http.ResponseWriter, request *http.Request, err error) {
	t.Helper()

	if writeErr := respond.WriteProblem(writer, request, err, ""); writeErr != nil {
		t.Fatalf("write problem response: %v", writeErr)
	}
}

func assertAccessProblem(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()

	if response.Code != status {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, status, response.Body)
	}
	problem := decodeProblem(t, response)
	if problem.Code != code {
		t.Fatalf("problem code = %q, want %q", problem.Code, code)
	}
}
