package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/keycustody"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/support"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/go-chi/chi/v5"
)

type stubKeyRepository struct {
	result keycustody.Result
	err    error
}

func (repository *stubKeyRepository) Execute(context.Context, tenant.Scope, keycustody.Command) (keycustody.Result, error) {
	return repository.result, repository.err
}
func (repository *stubKeyRepository) Read(context.Context, tenant.Scope, string) (keycustody.Result, error) {
	return repository.result, repository.err
}
func (repository *stubKeyRepository) Version(context.Context, tenant.Scope, string, int64) (keycustody.Version, error) {
	return keycustody.Version{}, repository.err
}
func (repository *stubKeyRepository) Versions(context.Context, tenant.Scope, string) ([]keycustody.Version, error) {
	return nil, repository.err
}
func (repository *stubKeyRepository) Material(context.Context, tenant.Scope, string, int64) ([]byte, error) {
	return nil, repository.err
}

type stubSupportRepository struct {
	grant *support.Grant
	err   error
}

func (repository *stubSupportRepository) Execute(context.Context, tenant.Scope, support.Command) (support.Result, error) {
	return support.Result{Grant: repository.grant}, repository.err
}
func (repository *stubSupportRepository) Read(context.Context, tenant.Scope, string, string) (support.Result, error) {
	return support.Result{Grant: repository.grant}, repository.err
}

func mustKeyCustodyService(t *testing.T, repository keycustody.Repository) *keycustody.Service {
	t.Helper()
	generator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatalf("NewSystemGenerator() error = %v", err)
	}
	service, err := keycustody.NewService(repository, generator, time.Now)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	return service
}

func supportRequest(t *testing.T, handler http.Handler, credential, method, target, body, idempotencyKey string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), method, target, bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer "+credential)
	request.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", `"`+idempotencyKey+`"`)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	return response
}

func TestKMSRoutesEnforcePermissionsAndMapReferencedRetirement(t *testing.T) {
	t.Parallel()

	repository := &stubKeyRepository{result: keycustody.Result{Domain: "identity.identifier.v1", ActiveVersion: 1, Generation: 1}}
	fixture := newHTTPAccessFixture(t, nil, access.Pattern("kms:write"))
	routes, err := NewKMSRoutes(fixture.middleware, mustKeyCustodyService(t, repository), fixture.logger)
	if err != nil {
		t.Fatalf("NewKMSRoutes() error = %v", err)
	}
	router := chi.NewRouter()
	routes.Register(router)

	response := supportRequest(t, router, fixture.encoded, http.MethodPost, "/kms/domains",
		`{"domain":"identity.identifier.v1","reason":"integration create"}`, "kms-routes-key")
	if response.Code != http.StatusOK {
		t.Fatalf("create status = %d, body = %s", response.Code, response.Body.String())
	}
	// A read requires the read permission even when the credential may write.
	read := supportRequest(t, router, fixture.encoded, http.MethodGet, "/kms/domains/identity.identifier.v1", "", "")
	if read.Code != http.StatusForbidden {
		t.Fatalf("read status = %d, want 403", read.Code)
	}
	// The route maps a referenced retirement to a conflict without leaking
	// internals.
	repository.err = keycustody.ErrReferenced
	retire := supportRequest(t, router, fixture.encoded, http.MethodPost, "/kms/domains/identity.identifier.v1/versions/1/retire",
		`{"expected_version":1,"reason":"retire referenced version"}`, "kms-routes-retire")
	if retire.Code != http.StatusConflict {
		t.Fatalf("retire status = %d, want 409; body = %s", retire.Code, retire.Body.String())
	}
}

func TestSupportRoutesEnforceBreakGlassPermissions(t *testing.T) {
	t.Parallel()

	fixture := newHTTPAccessFixture(t, nil, access.Pattern("break_glass:request"))
	service, err := support.NewService(&stubSupportRepository{}, access.TenantRegistry(), time.Now)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	routes, err := NewSupportRoutes(fixture.middleware, service, fixture.logger)
	if err != nil {
		t.Fatalf("NewSupportRoutes() error = %v", err)
	}
	router := chi.NewRouter()
	routes.Register(router)

	response := supportRequest(t, router, fixture.encoded, http.MethodPost, "/support/break-glass",
		`{"permissions":["identity:reveal"],"duration":"1h","reason":"incident triage request"}`, "support-routes-request")
	if response.Code != http.StatusOK {
		t.Fatalf("request status = %d, body = %s", response.Code, response.Body.String())
	}
	// Granting delegated access requires the dedicated write permission.
	grant := supportRequest(t, router, fixture.encoded, http.MethodPost, "/support/grants",
		`{"grantee":"support_engineer","patterns":["subjects:read"],"duration":"1h","reason":"delegated read access"}`, "support-routes-grant")
	if grant.Code != http.StatusForbidden {
		t.Fatalf("grant status = %d, want 403", grant.Code)
	}
	// An out-of-allowlist emergency permission fails closed before persistence.
	denied := supportRequest(t, router, fixture.encoded, http.MethodPost, "/support/break-glass",
		`{"permissions":["webhooks:write"],"duration":"1h","reason":"incident triage request"}`, "support-routes-denied")
	if denied.Code != http.StatusForbidden {
		t.Fatalf("denied status = %d, want 403; body = %s", denied.Code, denied.Body.String())
	}
	// Reads require the read permission instead of the request permission.
	read := supportRequest(t, router, fixture.encoded, http.MethodGet, "/support/grants", "", "")
	if read.Code != http.StatusForbidden {
		t.Fatalf("read status = %d, want 403", read.Code)
	}
}
