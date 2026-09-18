//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/Mujhtech/idenqa/adapters/models/onnx"
	"github.com/Mujhtech/idenqa/adapters/providers/dojah"
	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/bootstrap/adapterrunner"
	"github.com/Mujhtech/idenqa/internal/bootstrap/modelrunner"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/provider"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func startComposedRunners(t *testing.T, primary config.ModelRuntime, settings modelrunner.Settings, base, ca, key, gateway, runnerKey string, scope tenant.Scope, policyID, profileDigest string) (config.ModelRuntime, string) {
	t.Helper()
	directory := t.TempDir()
	write := func(name string, raw []byte) string {
		t.Helper()
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	ids, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	modelID, err := ids.NewModel()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../adapters/models/onnx/testdata/pad_fixture.onnx")
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	settings.ModelFile, err = filepath.Abs("../../adapters/models/onnx/testdata/pad_fixture.onnx")
	if err != nil {
		t.Fatal(err)
	}
	settings.Model.FaceMatching = false
	settings.Model.Width, settings.Model.Height = 128, 128
	settings.Model.Registration.ModelID = modelID.String()
	settings.Model.Manifest.Provenance.ModelID = modelID.String()
	settings.Model.Manifest.Provenance.ModelDigest = "sha256:" + hex.EncodeToString(hash[:])
	settings.Model.Manifest.Provenance.PreprocessingDigest = onnx.FacePreprocessingDigest(*settings.Model.FacePreparation)
	settings.Model.Manifest.Provenance.OutputSchemaDigest = onnx.OutputSchemaDigest()
	settings.Model.Manifest.Restrictions.MaximumGrants = 1
	settings.Model.Manifest.Capabilities = []modelv1.Capability{{Evaluation: "idenqa.check.passive_pad", AcceptedEvidence: []string{"idenqa.evidence.selfie_image"}, OutputSignals: []string{"idenqa.signal.passive_pad"}}}
	settings.Model.Registration.ConfigurationDigest = onnx.ConfigurationDigest(settings.Model)
	settings.ListenAddress = "127.0.0.1:0"
	settings.CertificateFile = ca
	settings.PrivateKeyFile = key
	settings.CredentialFile = runnerKey
	settings.GatewayURL = gateway
	settings.GatewayCAFile = ca
	settings.GatewayCredentialFile = write("pad-gateway.key", []byte("idq_wrk_v1_"+base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x63}, 32))))
	process, err := modelrunner.NewProcess(t.Context(), settings)
	if err != nil {
		t.Fatal(err)
	}
	startComposedProcess(t, process.Run, process.Close)
	extra := primary
	extra.Manifest = settings.Model.Manifest
	extra.Binding.Configuration = settings.Model.Registration
	extra.Binding.DocumentRequirement = ""
	extra.RunnerAddress = process.Address()
	extra.GatewayCredentialFile = settings.GatewayCredentialFile

	fixture := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/document/analysis" {
			t.Error("unexpected provider operation")
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"entity":{"status":{"overall_status":1}}}`)
	}))
	t.Cleanup(fixture.Close)
	fixtureCA := write("provider-ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: fixture.TLS.Certificates[0].Certificate[0]}))
	target, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewTLSServer(httputil.NewSingleHostReverseProxy(target))
	t.Cleanup(proxy.Close)
	proxyCA := write("provider-gateway-ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: proxy.TLS.Certificates[0].Certificate[0]}))
	gatewayKey := write("provider-gateway.key", []byte("idq_wrk_v1_"+base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x64}, 32))))
	providerID, err := ids.NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	reference := providerv1.ConfigurationReference{ProviderID: providerID.String(), SchemaDigest: dojah.Description().Configuration.Digest, SecretReference: "secret://provider/dojah/fixture", CredentialVersion: "v1"}
	providerProcess, err := adapterrunner.NewProcess(t.Context(), adapterrunner.Settings{ListenAddress: "127.0.0.1:0", CertificateFile: ca, PrivateKeyFile: key, CredentialFile: runnerKey, TenantID: scope.ID().String(), Configuration: reference, BaseURL: fixture.URL, ProviderCAFile: fixtureCA, AppIDFile: write("app.key", []byte("fixture-app")), APIKeyFile: write("api.key", []byte("fixture-key")), GatewayURL: proxy.URL, GatewayCAFile: proxyCA, GatewayCredentialFile: gatewayKey, Fixture: true})
	if err != nil {
		t.Fatal(err)
	}
	startComposedProcess(t, providerProcess.Run, providerProcess.Close)
	providerSettings := config.ProviderRuntime{Binding: provider.Binding{TenantID: scope.ID().String(), PolicyID: policyID, ProfileDigest: profileDigest, Requirement: "document", Region: primary.Binding.Region, Purpose: primary.Binding.Purpose, Recipient: primary.Binding.Recipient, Configuration: reference}, RunnerAddress: providerProcess.Address(), RunnerCAFile: ca, RunnerServerName: "127.0.0.1", RunnerCredentialFile: runnerKey, GatewayCredentialFile: gatewayKey}
	encoded, err := json.Marshal(providerSettings)
	if err != nil {
		t.Fatal(err)
	}
	return extra, write("provider.json", encoded)
}
func startComposedProcess(t *testing.T, run func(context.Context) error, closeProcess func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
		closeProcess()
	})
}
