// Package adapterrunner composes an isolated tenant provider workload.
package adapterrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/adapters/providers/dojah"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/config"
	runnerv1 "github.com/Mujhtech/idenqa/internal/gen/proto/runner/v1"
	"github.com/Mujhtech/idenqa/internal/platform/egress"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/transport/runner"
	"google.golang.org/grpc"
)

// Settings contains mounted credential paths and one tenant's reviewed binding.
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
}

// Process owns one tenant-isolated provider runner and its outbound transports.
type Process struct {
	server   *grpc.Server
	listener net.Listener
	clients  []*http.Client
}

// NewProcess validates mounted configuration and starts no background work.
func NewProcess(ctx context.Context, settings Settings) (*Process, error) {
	if _, _, err := net.SplitHostPort(settings.ListenAddress); err != nil {
		return nil, errors.New("explicit provider listen address required")
	}
	if _, err := id.ParseTenant(settings.TenantID); err != nil {
		return nil, errors.New("invalid provider tenant")
	}
	credential, err := config.ReadCredentialFile(settings.CredentialFile)
	if err != nil {
		return nil, err
	}
	credentials, err := runner.NewCredentialSet(credential)
	if err != nil {
		return nil, err
	}
	tlsCredentials, err := runner.ServerTLSCredentials(settings.CertificateFile, settings.PrivateKeyFile)
	if err != nil {
		return nil, err
	}
	description, err := runtimeManifest(settings.Adapter)
	if err != nil {
		return nil, err
	}
	if settings.Configuration.Validate() != nil || settings.Configuration.SchemaDigest != description.Configuration.Digest || settings.TenantID == "" {
		return nil, errors.New("invalid provider binding")
	}
	identifierFile := settings.AppIDFile
	if settings.Adapter == "smileid" {
		identifierFile = settings.PartnerIDFile
	}
	appID, err := config.ReadCredentialFile(identifierFile)
	if err != nil {
		return nil, err
	}
	apiKey, err := config.ReadCredentialFile(settings.APIKeyFile)
	if err != nil {
		return nil, err
	}
	gatewayCredential, err := config.ReadCredentialFile(settings.GatewayCredentialFile)
	if err != nil {
		return nil, err
	}
	sandboxOrigin := "https://sandbox.dojah.io"
	if settings.Adapter == "smileid" {
		sandboxOrigin = "https://testapi.smileidentity.com"
	}
	if !settings.Fixture && settings.BaseURL != sandboxOrigin {
		return nil, errors.New("provider runtime permits only the reviewed adapter sandbox origin")
	}
	if _, err := runner.NewCredentialSet(gatewayCredential); err != nil {
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
	adapter := &scopedAdapter{settings: settings, providerHTTP: providerHTTP, gatewayHTTP: gatewayHTTP, gatewayCredential: gatewayCredential, configuration: dojah.Config{BaseURL: settings.BaseURL, AppID: appID, APIKey: apiKey, Mode: "sandbox", Region: "africa"}}
	var implementation providerv1.Adapter = adapter
	clients := []*http.Client{providerHTTP, gatewayHTTP}
	if settings.Adapter == "smileid" {
		smile, uploadHTTP, err := newSmileAdapter(adapter, appID, apiKey)
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
	server, err := runner.NewServer(runner.ServerConfig{Credentials: credentials, TLS: tlsCredentials, MaximumDeadline: 10 * time.Minute})
	if err != nil {
		return nil, err
	}
	runnerv1.RegisterProviderRunnerServiceServer(server, service)
	var listenerConfig net.ListenConfig
	listener, err := listenerConfig.Listen(ctx, "tcp", settings.ListenAddress)
	if err != nil {
		return nil, errors.New("listen for provider runner")
	}
	return &Process{server, listener, clients}, nil
}

// Address returns the bound transport address for readiness and controlled tests.
func (process *Process) Address() string { return process.listener.Addr().String() }

// Run serves until cancellation, then drains or forcibly closes within five seconds.
func (process *Process) Run(ctx context.Context) error {
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

// Close releases the process-owned listener and pooled connections.
func (process *Process) Close() {
	process.server.Stop()
	_ = process.listener.Close()
	for _, client := range process.clients {
		client.CloseIdleConnections()
	}
}

type scopedAdapter struct {
	settings                  Settings
	providerHTTP, gatewayHTTP *http.Client
	gatewayCredential         string
	configuration             dojah.Config
}

func (adapter *scopedAdapter) Manifest(context.Context) (providerv1.Manifest, error) {
	return dojah.Description(), nil
}
func (adapter *scopedAdapter) Health(ctx context.Context) (providerv1.Health, error) {
	if err := ctx.Err(); err != nil {
		return providerv1.Health{}, err
	}
	return providerv1.Health{State: providerv1.HealthReady, Code: "adapter_ready", CheckedAt: time.Now().UTC()}, nil
}
func (adapter *scopedAdapter) ValidateConfiguration(_ context.Context, reference providerv1.ConfigurationReference) error {
	if reference != adapter.settings.Configuration {
		return dojah.ErrConfiguration
	}
	return nil
}
func (adapter *scopedAdapter) ResolveDojah(_ context.Context, reference, version string) (dojah.Config, error) {
	if reference != adapter.settings.Configuration.SecretReference || version != adapter.settings.Configuration.CredentialVersion {
		return dojah.Config{}, dojah.ErrConfiguration
	}
	return adapter.configuration, nil
}
func (*scopedAdapter) ResolveProviderInput(context.Context, string) (string, error) {
	return "", dojah.ErrInput
}
func (adapter *scopedAdapter) Execute(ctx context.Context, request providerv1.Request) (providerv1.Result, error) {
	if request.Validate() != nil || request.TenantID != adapter.settings.TenantID || request.Configuration != adapter.settings.Configuration || request.Check != "idenqa.check.document_analysis" || len(request.Evidence) != 1 {
		return providerv1.Result{}, dojah.ErrConfiguration
	}
	reader := &gatewayReader{adapter: adapter, request: request}
	implementation, err := dojah.New(adapter, adapter, reader, adapter.providerHTTP, time.Now)
	if err != nil {
		return providerv1.Result{}, err
	}
	return implementation.Execute(ctx, request)
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
	request.Header.Set("Authorization", "Bearer "+reader.adapter.gatewayCredential)
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
