package adapterrunner

import (
	"context"
	"testing"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/platform/secret"
	"github.com/Mujhtech/idenqa/internal/platform/secret/credential"
)

type fakeCredentialSource struct {
	values map[string]string
	fail   map[string]error
}

func (source *fakeCredentialSource) Resolve(_ context.Context, reference secret.Reference) (secret.Value, error) {
	if err := source.fail[reference.String()]; err != nil {
		return secret.Value{}, err
	}
	value, ok := source.values[reference.String()]
	if !ok {
		return secret.Value{}, secret.ErrNotFound
	}
	return secret.NewValue(reference, "resolved-1", []byte(value))
}

func mustSecretReference(t *testing.T, value string) secret.Reference {
	t.Helper()
	reference, err := secret.ParseReference(value)
	if err != nil {
		t.Fatalf("ParseReference(%q) error = %v", value, err)
	}
	return reference
}

func tenantConfiguration(t *testing.T, reference, version string) providerv1.ConfigurationReference {
	t.Helper()
	configuration := providerv1.ConfigurationReference{
		ProviderID:        "pvd_01M11HEQG00000000000000000",
		SchemaDigest:      "sha256:284a4418399974a7d1ce691f96ea534e1cd97ee1a9bc0d09de25d4a3b9d48969",
		SecretReference:   reference,
		CredentialVersion: version,
	}
	if configuration.Validate() != nil {
		t.Fatalf("test configuration is invalid: %+v", configuration)
	}
	return configuration
}

func TestScopedAdapterResolvesTenantConfigurationBundle(t *testing.T) {
	t.Parallel()

	base := mustSecretReference(t, "secret://aws/prod/tenants/ten_one/providers/dojah")
	source := &fakeCredentialSource{
		values: map[string]string{ //nolint:gosec // fixture reference text, never a credential value.
			"secret://aws/prod/tenants/ten_one/providers/dojah?version=v1": `{"app_id":"app-one","api_key":"key-one"}`,
			"secret://aws/prod/tenants/ten_one/providers/dojah?version=v2": `{"app_id":"app-two","api_key":"key-two"}`,
		},
		fail: map[string]error{},
	}
	resolver, err := credential.NewResolver(source, time.Minute, time.Now)
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}
	window, err := credential.NewWindow(resolver, func(value secret.Value) error {
		return validateTenantCredentials(providerv1.AdapterDojah, value)
	})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	adapter := &scopedAdapter{
		settings: Settings{
			Adapter: providerv1.AdapterDojah, TenantID: "ten_01M11HEQG00000000000000000",
			BaseURL: "https://sandbox.dojah.io", Configuration: tenantConfiguration(t, base.String(), "v1"),
		},
		credentials:          window,
		dynamicConfiguration: true,
	}
	configuration, err := adapter.ResolveDojah(context.Background(), base.String(), "v2")
	if err != nil {
		t.Fatalf("ResolveDojah(v2) error = %v", err)
	}
	if configuration.AppID != "app-two" || configuration.APIKey != "key-two" {
		t.Fatalf("ResolveDojah(v2) = %+v", configuration)
	}
	// The launch-time generation still overlaps so an in-flight request pinned
	// to the previous version is not dropped by the rotation.
	configuration, err = adapter.ResolveDojah(context.Background(), base.String(), "v1")
	if err != nil {
		t.Fatalf("ResolveDojah(v1 overlap) error = %v", err)
	}
	if configuration.AppID != "app-one" || configuration.APIKey != "key-one" {
		t.Fatalf("ResolveDojah(v1 overlap) = %+v", configuration)
	}
	if err := adapter.ValidateConfiguration(context.Background(), adapter.settings.Configuration); err != nil {
		t.Fatalf("ValidateConfiguration(v1) error = %v", err)
	}
	rotated := adapter.settings.Configuration
	rotated.CredentialVersion = "v2"
	if err := adapter.ValidateConfiguration(context.Background(), rotated); err != nil {
		t.Fatalf("ValidateConfiguration(v2) error = %v", err)
	}
	// A tenant rotation may also move the secret reference itself while the
	// provider identity and schema remain pinned; a different provider identity
	// still fails closed.
	rotatedReference := rotated
	rotatedReference.SecretReference = "secret://aws/prod/tenants/ten_one/providers/dojah-2"
	if !adapter.acceptsConfiguration(rotatedReference) {
		t.Fatal("acceptsConfiguration(rotated reference) = false")
	}
	rotatedProvider := rotated
	rotatedProvider.ProviderID = "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWK"
	if adapter.acceptsConfiguration(rotatedProvider) {
		t.Fatal("acceptsConfiguration(other provider) = true")
	}
	rotatedSchema := rotated
	rotatedSchema.SchemaDigest = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if adapter.acceptsConfiguration(rotatedSchema) {
		t.Fatal("acceptsConfiguration(other schema) = true")
	}
}

func TestScopedAdapterFailsClosedAndReportsNotReady(t *testing.T) {
	t.Parallel()

	base := mustSecretReference(t, "secret://aws/prod/tenants/ten_one/providers/dojah")
	source := &fakeCredentialSource{
		values: map[string]string{ //nolint:gosec // fixture reference text, never a credential value.
			"secret://aws/prod/tenants/ten_one/providers/dojah?version=v1": `{"app_id":"app-one","api_key":"key-one"}`,
		},
		fail: map[string]error{},
	}
	resolver, err := credential.NewResolver(source, time.Minute, time.Now)
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}
	window, err := credential.NewWindow(resolver, func(value secret.Value) error {
		return validateTenantCredentials(providerv1.AdapterDojah, value)
	})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	if _, err := window.Resolve(context.Background(), base, "v1"); err != nil {
		t.Fatalf("Resolve(v1) error = %v", err)
	}
	adapter := &scopedAdapter{
		settings: Settings{
			Adapter: providerv1.AdapterDojah, TenantID: "ten_01M11HEQG00000000000000000",
			BaseURL: "https://sandbox.dojah.io", Configuration: tenantConfiguration(t, base.String(), "v1"),
		},
		credentials:          window,
		dynamicConfiguration: true,
	}
	if _, err := adapter.ResolveDojah(context.Background(), base.String(), "v9"); err == nil {
		t.Fatal("ResolveDojah(unknown version) = nil error")
	}
	source.fail["secret://aws/prod/tenants/ten_one/providers/dojah?version=v1"] = secret.ErrDenied
	health, err := adapter.Health(context.Background())
	if err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if health.State != providerv1.HealthNotReady || health.Code != "configuration_unavailable" {
		t.Fatalf("Health() = %+v, want not_ready", health)
	}
}

func TestValidateTenantCredentials(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		adapter string
		value   string
		wantErr bool
	}{
		{name: "dojah", adapter: providerv1.AdapterDojah, value: `{"app_id":"app","api_key":"key"}`},
		{name: "smileid", adapter: providerv1.AdapterSmileID, value: `{"partner_id":"partner_1","api_key":"key"}`},
		{name: "dojah missing key", adapter: providerv1.AdapterDojah, value: `{"app_id":"app"}`, wantErr: true},
		{name: "smileid missing partner", adapter: providerv1.AdapterSmileID, value: `{"api_key":"key"}`, wantErr: true},
		{name: "smileid invalid partner", adapter: providerv1.AdapterSmileID, value: `{"partner_id":"not a partner","api_key":"key"}`, wantErr: true},
		{name: "unknown field", adapter: providerv1.AdapterDojah, value: `{"app_id":"app","api_key":"key","extra":"x"}`, wantErr: true},
		{name: "malformed", adapter: providerv1.AdapterDojah, value: `{`, wantErr: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			reference := mustSecretReference(t, "secret://aws/prod/tenant")
			value, err := secret.NewValue(reference, "v1", []byte(test.value))
			if err != nil {
				t.Fatalf("NewValue() error = %v", err)
			}
			if err := validateTenantCredentials(test.adapter, value); (err != nil) != test.wantErr {
				t.Fatalf("validateTenantCredentials() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}
