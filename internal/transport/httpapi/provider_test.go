package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/adapters/providers/dojah"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/cursor"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/provider"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type providerRegistrationProbe struct {
	provider.RegistrationRepository
	applyCalls   int
	getCalls     int
	healthCalls  int
	registration provider.Registration
	page         provider.RegistrationPage
	err          error
}

func (probe *providerRegistrationProbe) Apply(_ context.Context, _ tenant.Scope, _ idempotency.Request, _ id.Event, command provider.RegistrationCommand) (provider.RegistrationReceipt, error) {
	probe.applyCalls++
	if probe.err != nil {
		return provider.RegistrationReceipt{}, probe.err
	}
	return provider.RegistrationReceipt{Registration: probe.registration, Operation: command.Operation, Reason: command.Reason}, nil
}

func (probe *providerRegistrationProbe) Get(context.Context, tenant.Scope, string) (provider.Registration, error) {
	probe.getCalls++
	if probe.err != nil {
		return provider.Registration{}, probe.err
	}
	return probe.registration, nil
}

func (probe *providerRegistrationProbe) List(context.Context, tenant.Scope, *provider.RegistrationPosition, int) (provider.RegistrationPage, error) {
	if probe.err != nil {
		return provider.RegistrationPage{}, probe.err
	}
	return probe.page, nil
}

func (probe *providerRegistrationProbe) Health(context.Context, tenant.Scope, provider.Registration) (provider.RegistrationHealth, error) {
	probe.healthCalls++
	if probe.err != nil {
		return provider.RegistrationHealth{}, probe.err
	}
	return provider.RegistrationHealth{RegistrationID: probe.registration.ID, AdapterID: probe.registration.AdapterID}, nil
}

func providerHandlerFixture(t *testing.T, probe *providerRegistrationProbe, patterns ...access.Pattern) (*httpAccessFixture, *ProviderRoutes) {
	t.Helper()
	fixture := newHTTPAccessFixture(t, nil, patterns...)
	identifiers, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	manifest := dojah.Description()
	service, err := provider.NewRegistrationManagement(probe, identifiers, time.Now, time.Hour, map[string]providerv1.Manifest{manifest.Package.AdapterID: manifest})
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := cursor.NewKeyring(1, map[cursor.KeyVersion][]byte{1: bytes.Repeat([]byte{0x71}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	codec, err := cursor.New(keyring, httpAccessClock{now: time.Now()}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	routes, err := NewProviderRoutes(fixture.middleware, service, codec, fixture.logger)
	if err != nil {
		t.Fatal(err)
	}
	return fixture, routes
}

func providerRegistrationBody(t *testing.T) string {
	t.Helper()
	manifest := dojah.Description()
	write := provider.RegistrationWrite{AdapterID: manifest.Package.AdapterID, Region: "africa",
		Configuration: providerv1.ConfigurationReference{ProviderID: "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWH", SchemaDigest: manifest.Configuration.Digest, SecretReference: "secret://provider/dojah/tenant", CredentialVersion: "v1"}}
	body, err := json.Marshal(map[string]any{"reason": "onboarding", "registration": write})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func providerRequest(t *testing.T, fixture *httpAccessFixture, method, path, body string, idempotency string) *http.Request {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+fixture.encoded)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotency != "" {
		request.Header.Set("Idempotency-Key", idempotency)
	}
	return request
}

func TestProviderRoutesRequireScopesAndClosedBodies(t *testing.T) {
	manifest := dojah.Description()
	write := provider.RegistrationWrite{AdapterID: manifest.Package.AdapterID, Region: "africa",
		Configuration: providerv1.ConfigurationReference{ProviderID: "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWH", SchemaDigest: manifest.Configuration.Digest, SecretReference: "secret://provider/dojah/tenant", CredentialVersion: "v1"}}
	writeBody, err := json.Marshal(write)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name        string
		scope       access.Pattern
		method      string
		path        string
		body        string
		idempotency string
		status      int
		wantApply   int
	}{
		{"create requires write scope", "providers:read", "POST", "/v1/providers", providerRegistrationBody(t), `"create"`, 403, 0},
		{"create accepted", "providers:write", "POST", "/v1/providers", providerRegistrationBody(t), `"create"`, 200, 1},
		{"create requires idempotency key", "providers:write", "POST", "/v1/providers", providerRegistrationBody(t), "", 400, 0},
		{"create rejects unknown field", "providers:write", "POST", "/v1/providers", `{"reason":"onboarding","registration":{},"extra":true}`, `"create"`, 400, 0},
		{"update requires version", "providers:write", "PUT", "/v1/providers/pvr_01K4AR9V8FQ2G7ZXCPNM5T6JWH", `{"reason":"rotation","registration":{}}`, "", 400, 0},
		{"update accepted", "providers:write", "PUT", "/v1/providers/pvr_01K4AR9V8FQ2G7ZXCPNM5T6JWH", `{"expected_version":1,"reason":"rotation","registration":` + string(writeBody) + `}`, "", 200, 1},
		{"enable accepted", "providers:write", "POST", "/v1/providers/pvr_01K4AR9V8FQ2G7ZXCPNM5T6JWH/enable", `{"expected_version":1,"reason":"enable"}`, "", 200, 1},
		{"disable accepted", "providers:write", "POST", "/v1/providers/pvr_01K4AR9V8FQ2G7ZXCPNM5T6JWH/disable", `{"expected_version":2,"reason":"disable"}`, "", 200, 1},
		{"validate is write-scoped", "providers:read", "POST", "/v1/providers/pvr_01K4AR9V8FQ2G7ZXCPNM5T6JWH/validate", string(writeBody), "", 403, 0},
		{"validate accepted", "providers:write", "POST", "/v1/providers/pvr_01K4AR9V8FQ2G7ZXCPNM5T6JWH/validate", string(writeBody), "", 200, 0},
		{"validate rejects secret material", "providers:write", "POST", "/v1/providers/pvr_01K4AR9V8FQ2G7ZXCPNM5T6JWH/validate", strings.Replace(string(writeBody), `"v1"`, `"my_api_key"`, 1), "", 200, 0},
		{"health requires read scope", "providers:write", "GET", "/v1/providers/pvr_01K4AR9V8FQ2G7ZXCPNM5T6JWH/health", "", "", 403, 0},
		{"health accepted", "providers:read", "GET", "/v1/providers/pvr_01K4AR9V8FQ2G7ZXCPNM5T6JWH/health", "", "", 200, 0},
		{"simulation accepted", "providers:read", "POST", "/v1/providers/pvr_01K4AR9V8FQ2G7ZXCPNM5T6JWH/failure-simulations", `{"class":"unavailable","code":"provider_unavailable"}`, "", 200, 0},
		{"simulation rejects unknown class", "providers:read", "POST", "/v1/providers/pvr_01K4AR9V8FQ2G7ZXCPNM5T6JWH/failure-simulations", `{"class":"made_up","code":"provider_failure"}`, "", 400, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			probe := &providerRegistrationProbe{registration: provider.Registration{ID: "pvr_01K4AR9V8FQ2G7ZXCPNM5T6JWH", AdapterID: "dojah", Region: "africa", Version: 1}}
			fixture, routes := providerHandlerFixture(t, probe, test.scope)
			router := versionedRouter(t, routes)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, providerRequest(t, fixture, test.method, test.path, test.body, test.idempotency))
			if response.Code != test.status || probe.applyCalls != test.wantApply {
				t.Fatalf("status=%d apply=%d body=%s", response.Code, probe.applyCalls, response.Body.String())
			}
			if test.name == "validate rejects secret material" && !strings.Contains(response.Body.String(), provider.RegistrationSecretMaterial) {
				t.Fatalf("secret material report = %s", response.Body.String())
			}
			if test.name == "simulation accepted" && !strings.Contains(response.Body.String(), `"produces_identity_outcome":false`) {
				t.Fatalf("simulation = %s", response.Body.String())
			}
		})
	}
}

func TestProviderRoutesNotFoundAndConflict(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
	}{
		{"missing registration", provider.ErrRegistrationNotFound, 404},
		{"version conflict", provider.ErrRegistrationConflict, 409},
	} {
		t.Run(test.name, func(t *testing.T) {
			probe := &providerRegistrationProbe{err: test.err}
			fixture, routes := providerHandlerFixture(t, probe, "providers:read")
			router := versionedRouter(t, routes)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, providerRequest(t, fixture, "GET", "/v1/providers/pvr_01K4AR9V8FQ2G7ZXCPNM5T6JWH", "", ""))
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestProviderListBindsCursorToTenantAndQuery(t *testing.T) {
	probe := &providerRegistrationProbe{page: provider.RegistrationPage{Registrations: []provider.Registration{{ID: "pvr_01K4AR9V8FQ2G7ZXCPNM5T6JWH", AdapterID: "dojah", Region: "africa", Version: 1, CreatedAt: time.Now().UTC()}}, Next: &provider.RegistrationPosition{CreatedAt: time.Now().UTC(), ID: "pvr_01K4AR9V8FQ2G7ZXCPNM5T6JWH"}}}
	fixture, routes := providerHandlerFixture(t, probe, "providers:read")
	router := versionedRouter(t, routes)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, providerRequest(t, fixture, "GET", "/v1/providers?limit=1", "", ""))
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"next_cursor"`) {
		t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	router.ServeHTTP(response, providerRequest(t, fixture, "GET", "/v1/providers?limit=1&cursor=tampered", "", ""))
	if response.Code != 400 {
		t.Fatalf("tampered cursor status=%d body=%s", response.Code, response.Body.String())
	}
	_ = http.MethodGet
}
