// Package adapterrunner composes an isolated tenant provider workload.
package adapterrunner

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/adapters/providers/dojah"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/bootstrap/runnersecret"
	"github.com/Mujhtech/idenqa/internal/config"
	runnerv1 "github.com/Mujhtech/idenqa/internal/gen/proto/runner/v1"
	"github.com/Mujhtech/idenqa/internal/platform/egress"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/secret"
	"github.com/Mujhtech/idenqa/internal/platform/secret/credential"
	"github.com/Mujhtech/idenqa/internal/transport/runner"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// partnerIDPattern is the reviewed Smile ID partner-identifier shape.
var partnerIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// Settings contains mounted credential paths and one tenant's reviewed binding.
// A *_reference field replaces the matching mounted path with a secret://
// reference resolved through the selected secret provider.
type Settings struct {
	Adapter               string                            `json:"adapter,omitempty"`
	PartnerIDFile         string                            `json:"partner_id_file,omitempty"`
	InputsFile            string                            `json:"inputs_file,omitempty"`
	CallbackURL           string                            `json:"callback_url,omitempty"`
	UploadOrigin          string                            `json:"upload_origin,omitempty"`
	UploadCAFile          string                            `json:"upload_ca_file,omitempty"`
	ListenAddress         string                            `json:"listen_address"`
	CertificateFile       string                            `json:"certificate_file"`
	PrivateKeyFile        string                            `json:"private_key_file"`
	CredentialFile        string                            `json:"credential_file"`
	TenantID              string                            `json:"tenant_id"`
	Configuration         providerv1.ConfigurationReference `json:"configuration"`
	BaseURL               string                            `json:"base_url"`
	ProviderCAFile        string                            `json:"provider_ca_file"`
	AppIDFile             string                            `json:"app_id_file"`
	APIKeyFile            string                            `json:"api_key_file"`
	GatewayURL            string                            `json:"gateway_url"`
	GatewayCAFile         string                            `json:"gateway_ca_file"`
	GatewayCredentialFile string                            `json:"gateway_credential_file"`
	Fixture               bool                              `json:"fixture"`

	SecretProvider             string `json:"secret_provider,omitempty"`
	SecretAWSRegion            string `json:"secret_aws_region,omitempty"`
	SecretCacheSeconds         int    `json:"secret_cache_seconds,omitempty"`
	SecretReloadSeconds        int    `json:"secret_reload_seconds,omitempty"`
	CredentialReference        string `json:"credential_reference,omitempty"`
	GatewayCredentialReference string `json:"gateway_credential_reference,omitempty"`
	APIKeyReference            string `json:"api_key_reference,omitempty"`
	AppIDReference             string `json:"app_id_reference,omitempty"`
	TLSReference               string `json:"tls_reference,omitempty"`
}

// Process owns one tenant-isolated provider runner and its outbound transports.
type Process struct {
	server      *grpc.Server
	listener    net.Listener
	clients     []*http.Client
	reload      *runnersecret.Reloader
	credentials *credential.Window
	interval    time.Duration
}

// NewProcess validates mounted configuration, resolves the initial secret
// values, and starts no background work.
func NewProcess(ctx context.Context, settings Settings) (*Process, error) {
	if _, _, err := net.SplitHostPort(settings.ListenAddress); err != nil {
		return nil, errors.New("explicit provider listen address required")
	}
	if _, err := id.ParseTenant(settings.TenantID); err != nil {
		return nil, errors.New("invalid provider tenant")
	}
	description, err := runtimeManifest(settings.Adapter)
	if err != nil {
		return nil, err
	}
	if settings.Configuration.Validate() != nil || settings.Configuration.SchemaDigest != description.Configuration.Digest || settings.TenantID == "" {
		return nil, errors.New("invalid provider binding")
	}
	// A tenant configuration reference whose provider segment names a real
	// secret manager is resolved on demand instead of accepting mounted files
	// only. The mounted-file reference remains the unchanged default.
	configurationReference, err := secret.ParseReference(settings.Configuration.SecretReference)
	if err != nil {
		return nil, errors.New("invalid provider configuration reference")
	}
	dynamicConfiguration := configurationReference.Provider() != "file"
	var credentials *credential.Window
	if dynamicConfiguration {
		if settings.SecretProvider != configurationReference.Provider() {
			return nil, errors.New("provider configuration reference requires the matching secret provider")
		}
		source, err := runnersecret.Open(ctx, runnersecret.Options{
			Provider: settings.SecretProvider, AWSRegion: settings.SecretAWSRegion,
			CacheTTL: secondsDuration(settings.SecretCacheSeconds, runnersecret.DefaultCacheTTL),
			Now:      time.Now,
		})
		if err != nil {
			return nil, err
		}
		window, err := credential.NewWindow(source, func(value secret.Value) error {
			return validateTenantCredentials(settings.Adapter, value)
		})
		if err != nil {
			return nil, err
		}
		if _, err := window.Resolve(ctx, configurationReference, settings.Configuration.CredentialVersion); err != nil {
			return nil, errors.New("provider configuration credential unavailable")
		}
		credentials = window
	}
	credentialSet := runner.NewEmptyCredentialSet()
	gatewayCredential := &runnersecret.Holder{}
	appID := &runnersecret.Holder{}
	apiKey := &runnersecret.Holder{}
	if err := hydrateStaticCredentials(settings, dynamicConfiguration, credentialSet, gatewayCredential, appID, apiKey); err != nil {
		return nil, err
	}
	tlsCredentials, rotatingTLS, err := composeTLS(settings)
	if err != nil {
		return nil, err
	}
	reloader, err := composeReloader(ctx, settings, credentialSet, gatewayCredential, appID, apiKey, rotatingTLS)
	if err != nil {
		return nil, err
	}
	if reloader != nil {
		if err := reloader.Prime(ctx); err != nil {
			return nil, err
		}
	}

	sandboxOrigin := "https://sandbox.dojah.io"
	if settings.Adapter == providerv1.AdapterSmileID {
		sandboxOrigin = "https://testapi.smileidentity.com"
	}
	if !settings.Fixture && settings.BaseURL != sandboxOrigin {
		return nil, errors.New("provider runtime permits only the reviewed adapter sandbox origin")
	}
	if _, err := runner.NewCredentialSet(gatewayCredential.Load()); err != nil {
		return nil, err
	}
	providerHTTP, err := egress.NewClient(settings.BaseURL, settings.ProviderCAFile, settings.Fixture)
	if err != nil {
		return nil, err
	}
	gatewayHTTP, err := egress.NewInternalClient(settings.GatewayURL, settings.GatewayCAFile)
	if err != nil {
		return nil, err
	}
	adapter := &scopedAdapter{settings: settings, providerHTTP: providerHTTP, gatewayHTTP: gatewayHTTP, gatewayCredential: gatewayCredential,
		appID: appID, apiKey: apiKey, credentials: credentials, dynamicConfiguration: dynamicConfiguration}
	var implementation providerv1.Adapter = adapter
	clients := []*http.Client{providerHTTP, gatewayHTTP}
	if settings.Adapter == providerv1.AdapterSmileID {
		smile, uploadHTTP, err := newSmileAdapter(adapter)
		if err != nil {
			return nil, err
		}
		implementation = smile
		clients = append(clients, uploadHTTP)
	}
	service, err := runner.NewProviderServer(implementation)
	if err != nil {
		return nil, err
	}
	server, err := runner.NewServer(runner.ServerConfig{Credentials: credentialSet, TLS: tlsCredentials, MaximumDeadline: 10 * time.Minute})
	if err != nil {
		return nil, err
	}
	runnerv1.RegisterProviderRunnerServiceServer(server, service)
	var listenerConfig net.ListenConfig
	listener, err := listenerConfig.Listen(ctx, "tcp", settings.ListenAddress)
	if err != nil {
		return nil, errors.New("listen for provider runner")
	}

	return &Process{server: server, listener: listener, clients: clients, reload: reloader, credentials: credentials,
		interval: secondsDuration(settings.SecretReloadSeconds, runnersecret.DefaultReloadInterval)}, nil
}

// hydrateStaticCredentials reads mounted files for every setting that is not
// bound to a secret reference. Bound settings remain unset until the reloader
// primes them, so an unresolvable reference fails closed instead of silently
// using a stale mounted file. When the tenant configuration is resolved through
// a secret provider the mounted provider credential files are not consulted.
func hydrateStaticCredentials(
	settings Settings,
	dynamicConfiguration bool,
	credentials *runner.CredentialSet,
	gateway, appID, apiKey *runnersecret.Holder,
) error {
	if settings.CredentialReference == "" {
		credential, err := config.ReadCredentialFile(settings.CredentialFile)
		if err != nil {
			return err
		}
		if err := credentials.Replace(credential); err != nil {
			return err
		}
	}
	if settings.GatewayCredentialReference == "" {
		credential, err := config.ReadCredentialFile(settings.GatewayCredentialFile)
		if err != nil {
			return err
		}
		if _, err := runner.NewCredentialSet(credential); err != nil {
			return err
		}
		gateway.Store(credential)
	}
	if dynamicConfiguration {
		return nil
	}
	identifierFile := settings.AppIDFile
	if settings.Adapter == providerv1.AdapterSmileID {
		identifierFile = settings.PartnerIDFile
	}
	if settings.AppIDReference == "" {
		identifier, err := config.ReadCredentialFile(identifierFile)
		if err != nil {
			return err
		}
		appID.Store(identifier)
	}
	if settings.APIKeyReference == "" {
		key, err := config.ReadCredentialFile(settings.APIKeyFile)
		if err != nil {
			return err
		}
		apiKey.Store(key)
	}

	return nil
}

// composeTLS loads the mounted certificate pair or prepares a rotating identity
// primed from a secret reference.
func composeTLS(settings Settings) (credentials.TransportCredentials, *runner.RotatingServerTLS, error) {
	if settings.TLSReference == "" {
		transport, err := runner.ServerTLSCredentials(settings.CertificateFile, settings.PrivateKeyFile)
		if err != nil {
			return nil, nil, err
		}

		return transport, nil, nil
	}
	rotating, err := runner.NewRotatingServerTLSCredentials(tls.Certificate{})
	if err != nil {
		return nil, nil, err
	}

	return rotating.Credentials(), rotating, nil
}

func composeReloader(
	ctx context.Context,
	settings Settings,
	credentials *runner.CredentialSet,
	gateway, appID, apiKey *runnersecret.Holder,
	rotatingTLS *runner.RotatingServerTLS,
) (*runnersecret.Reloader, error) {
	references := []string{settings.CredentialReference, settings.GatewayCredentialReference, settings.APIKeyReference, settings.AppIDReference, settings.TLSReference}
	if !runnersecret.Enabled(references...) {
		return nil, nil
	}
	resolver, err := runnersecret.Open(ctx, runnersecret.Options{
		Provider: settings.SecretProvider, AWSRegion: settings.SecretAWSRegion,
		CacheTTL:       secondsDuration(settings.SecretCacheSeconds, runnersecret.DefaultCacheTTL),
		ReloadInterval: secondsDuration(settings.SecretReloadSeconds, runnersecret.DefaultReloadInterval),
	})
	if err != nil {
		return nil, err
	}
	roles := make([]runnersecret.Role, 0, len(references))
	previousCredential := ""
	if settings.CredentialReference != "" {
		reference, err := secret.ParseReference(settings.CredentialReference)
		if err != nil {
			return nil, err
		}
		roles = append(roles, runnersecret.Role{Name: "credential", Reference: reference, Apply: func(value secret.Value) error {
			text, err := value.Text()
			if err != nil {
				return err
			}
			if text == previousCredential {
				return nil
			}
			var accepted []string
			if previousCredential != "" {
				accepted = append(accepted, previousCredential)
			}
			accepted = append(accepted, text)
			if err := credentials.Replace(accepted...); err != nil {
				return err
			}
			previousCredential = text

			return nil
		}})
	}
	if settings.GatewayCredentialReference != "" {
		reference, err := secret.ParseReference(settings.GatewayCredentialReference)
		if err != nil {
			return nil, err
		}
		roles = append(roles, runnersecret.Role{Name: "gateway_credential", Reference: reference, Apply: func(value secret.Value) error {
			text, err := value.Text()
			if err != nil {
				return err
			}
			if _, err := runner.NewCredentialSet(text); err != nil {
				return err
			}
			gateway.Store(text)

			return nil
		}})
	}
	if settings.APIKeyReference != "" {
		reference, err := secret.ParseReference(settings.APIKeyReference)
		if err != nil {
			return nil, err
		}
		roles = append(roles, runnersecret.Role{Name: "api_key", Reference: reference, Apply: func(value secret.Value) error {
			text, err := value.Text()
			if err != nil {
				return err
			}
			apiKey.Store(text)

			return nil
		}})
	}
	if settings.AppIDReference != "" {
		reference, err := secret.ParseReference(settings.AppIDReference)
		if err != nil {
			return nil, err
		}
		roles = append(roles, runnersecret.Role{Name: "app_id", Reference: reference, Apply: func(value secret.Value) error {
			text, err := value.Text()
			if err != nil {
				return err
			}
			if settings.Adapter == providerv1.AdapterSmileID && !partnerIDPattern.MatchString(text) {
				return errors.New("reloaded provider partner identifier is invalid")
			}
			appID.Store(text)

			return nil
		}})
	}
	if settings.TLSReference != "" {
		if rotatingTLS == nil {
			return nil, errors.New("rotating runner TLS is not initialised")
		}
		reference, err := secret.ParseReference(settings.TLSReference)
		if err != nil {
			return nil, err
		}
		roles = append(roles, runnersecret.Role{Name: "tls", Reference: reference, Apply: func(value secret.Value) error {
			var pair struct {
				Certificate string `json:"certificate"`
				PrivateKey  string `json:"private_key"`
			}
			if err := value.JSON(&pair); err != nil {
				return err
			}
			identity, err := runner.ParseServerTLSIdentity([]byte(pair.Certificate), []byte(pair.PrivateKey))
			if err != nil {
				return err
			}

			return rotatingTLS.Rotate(identity)
		}})
	}

	for index := range roles {
		roles[index].Apply = runnersecret.RoleApply(resolver, roles[index].Reference, roles[index].Apply)
	}

	return runnersecret.NewReloader(resolver, roles, secondsDuration(settings.SecretReloadSeconds, runnersecret.DefaultReloadInterval), nil)
}

func secondsDuration(seconds int, fallback time.Duration) time.Duration {
	if seconds <= 0 || seconds > 3600 {
		return fallback
	}

	return time.Duration(seconds) * time.Second
}

// Address returns the bound transport address for readiness and controlled tests.
func (process *Process) Address() string { return process.listener.Addr().String() }

// Run primes the reload plan, serves until cancellation, and refreshes
// configured secrets until shutdown.
func (process *Process) Run(ctx context.Context) error {
	if process.reload != nil {
		reloadCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			_ = process.reload.Run(reloadCtx)
		}()
	}
	if process.credentials != nil {
		refreshCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go refreshCredentials(refreshCtx, process.credentials, process.interval)
	}
	done := make(chan error, 1)
	go func() { done <- process.server.Serve(process.listener) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}
	drained := make(chan struct{})
	go func() { process.server.GracefulStop(); close(drained) }()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-drained:
	case <-timer.C:
		process.server.Stop()
		<-drained
	}
	err := <-done
	if errors.Is(err, grpc.ErrServerStopped) {
		return nil
	}
	return err
}

// refreshCredentials re-resolves the active tenant configuration until
// cancellation. A failed refresh keeps the last good generation in force.
func refreshCredentials(ctx context.Context, credentials *credential.Window, interval time.Duration) {
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = credentials.Refresh(ctx)
		}
	}
}

// Close releases the process-owned listener and pooled connections.
func (process *Process) Close() {
	process.server.Stop()
	_ = process.listener.Close()
	for _, client := range process.clients {
		client.CloseIdleConnections()
	}
}

type scopedAdapter struct {
	settings             Settings
	providerHTTP         *http.Client
	gatewayHTTP          *http.Client
	gatewayCredential    *runnersecret.Holder
	appID                *runnersecret.Holder
	apiKey               *runnersecret.Holder
	credentials          *credential.Window
	dynamicConfiguration bool
}

// configuration returns the current provider credentials, including any value
// published by a completed reload.
func (adapter *scopedAdapter) configuration() dojah.Config {
	return dojah.Config{BaseURL: adapter.settings.BaseURL, AppID: adapter.appID.Load(), APIKey: adapter.apiKey.Load(), Mode: "sandbox", Region: "africa"}
}

// acceptsConfiguration reports whether one request configuration still binds
// this runner. In dynamic mode the provider identity and schema must match the
// deployment binding while the secret reference and its version may advance
// through tenant rotation; mounted-file mode keeps the exact launch-time pin.
func (adapter *scopedAdapter) acceptsConfiguration(reference providerv1.ConfigurationReference) bool {
	if reference == adapter.settings.Configuration {
		return true
	}
	return adapter.dynamicConfiguration &&
		reference.ProviderID == adapter.settings.Configuration.ProviderID &&
		reference.SchemaDigest == adapter.settings.Configuration.SchemaDigest
}

func (adapter *scopedAdapter) Manifest(context.Context) (providerv1.Manifest, error) {
	return dojah.Description(), nil
}

// Health reports the active configuration credential readiness. A configuration
// that no longer resolves or validates is not_ready rather than silently
// dispatching with a stale value.
func (adapter *scopedAdapter) Health(ctx context.Context) (providerv1.Health, error) {
	if err := ctx.Err(); err != nil {
		return providerv1.Health{}, err
	}
	if adapter.credentials != nil {
		if err := adapter.credentials.Refresh(ctx); err != nil {
			//nolint:nilerr // a bounded not_ready health response is the result; resolution details stay inside the runner.
			return providerv1.Health{State: providerv1.HealthNotReady, Code: "configuration_unavailable", CheckedAt: time.Now().UTC()}, nil
		}
	}
	return providerv1.Health{State: providerv1.HealthReady, Code: "adapter_ready", CheckedAt: time.Now().UTC()}, nil
}

func (adapter *scopedAdapter) ValidateConfiguration(ctx context.Context, reference providerv1.ConfigurationReference) error {
	if !adapter.acceptsConfiguration(reference) {
		return dojah.ErrConfiguration
	}
	if adapter.credentials == nil {
		return nil
	}
	_, err := adapter.resolveConfiguration(ctx, reference.SecretReference, reference.CredentialVersion)
	return err
}

// resolveConfiguration resolves one accepted tenant configuration reference and
// version through the selected secret provider. An unknown, denied or
// unparsable reference fails closed.
func (adapter *scopedAdapter) resolveConfiguration(ctx context.Context, reference, version string) (secret.Value, error) {
	parsed, err := secret.ParseReference(reference)
	if err != nil {
		return secret.Value{}, dojah.ErrConfiguration
	}
	return adapter.credentials.Resolve(ctx, parsed, version)
}

func (adapter *scopedAdapter) ResolveDojah(ctx context.Context, reference, version string) (dojah.Config, error) {
	if adapter.credentials == nil {
		if reference != adapter.settings.Configuration.SecretReference || version != adapter.settings.Configuration.CredentialVersion {
			return dojah.Config{}, dojah.ErrConfiguration
		}
		return adapter.configuration(), nil
	}
	value, err := adapter.resolveConfiguration(ctx, reference, version)
	if err != nil {
		return dojah.Config{}, dojah.ErrConfiguration
	}
	configuration, err := dojahConfiguration(adapter.settings, value)
	if err != nil {
		return dojah.Config{}, dojah.ErrConfiguration
	}
	return configuration, nil
}

func (*scopedAdapter) ResolveProviderInput(context.Context, string) (string, error) {
	return "", dojah.ErrInput
}
func (adapter *scopedAdapter) Execute(ctx context.Context, request providerv1.Request) (providerv1.Result, error) {
	if request.Validate() != nil || request.TenantID != adapter.settings.TenantID || !adapter.acceptsConfiguration(request.Configuration) || request.Check != "idenqa.check.document_analysis" || len(request.Evidence) < 1 || len(request.Evidence) > 2 {
		return providerv1.Result{}, dojah.ErrConfiguration
	}
	reader := &gatewayReader{adapter: adapter, request: request}
	implementation, err := dojah.New(adapter, adapter, reader, adapter.providerHTTP, time.Now)
	if err != nil {
		return providerv1.Result{}, err
	}
	return implementation.Execute(ctx, request)
}

// tenantCredentials is the closed secret-manager bundle for one tenant provider
// configuration. The bundled values never leave the runner.
type tenantCredentials struct {
	AppID     string `json:"app_id"`
	PartnerID string `json:"partner_id"`
	APIKey    string `json:"api_key"`
}

func validateTenantCredentials(adapter string, value secret.Value) error {
	credentials, err := parseTenantCredentials(value)
	if err != nil {
		return err
	}
	if adapter == providerv1.AdapterSmileID {
		if !partnerIDPattern.MatchString(credentials.PartnerID) || credentials.APIKey == "" {
			return errors.New("invalid provider credential bundle")
		}
		return nil
	}
	if credentials.AppID == "" || credentials.APIKey == "" {
		return errors.New("invalid provider credential bundle")
	}
	return nil
}

func parseTenantCredentials(value secret.Value) (tenantCredentials, error) {
	var credentials tenantCredentials
	if err := value.JSON(&credentials); err != nil {
		return tenantCredentials{}, err
	}
	return credentials, nil
}

func dojahConfiguration(settings Settings, value secret.Value) (dojah.Config, error) {
	credentials, err := parseTenantCredentials(value)
	if err != nil {
		return dojah.Config{}, err
	}
	return dojah.Config{BaseURL: settings.BaseURL, AppID: credentials.AppID, APIKey: credentials.APIKey, Mode: "sandbox", Region: "africa"}, nil
}

type gatewayReader struct {
	adapter *scopedAdapter
	request providerv1.Request
}

func (reader *gatewayReader) ReadProviderEvidence(ctx context.Context, reference providerv1.EvidenceGrantReference, limit int64) ([]byte, error) {
	if !slices.Contains(reader.request.Evidence, reference) || limit <= 0 || limit > 10<<20 {
		return nil, dojah.ErrEvidence
	}
	encoded, err := json.Marshal(map[string]string{"attempt_id": reader.request.AttemptID, "grant_id": reference.GrantID, "redemption_id": reference.RedemptionID})
	if err != nil {
		return nil, dojah.ErrEvidence
	}
	request, err := http.NewRequestWithContext(ctx, "POST", strings.TrimSuffix(reader.adapter.settings.GatewayURL, "/")+"/internal/v1/provider-evidence", bytes.NewReader(encoded))
	if err != nil {
		return nil, dojah.ErrEvidence
	}
	request.Header.Set("Authorization", "Bearer "+reader.adapter.gatewayCredential.Load())
	request.Header.Set("Content-Type", "application/json")
	response, err := reader.adapter.gatewayHTTP.Do(request)
	if err != nil {
		return nil, dojah.ErrEvidence
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 200 {
		return nil, dojah.ErrEvidence
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(body)) > limit || len(body) == 0 {
		clear(body)
		return nil, dojah.ErrEvidence
	}
	return body, nil
}
