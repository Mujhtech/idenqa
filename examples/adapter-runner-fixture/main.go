// Command adapter-runner-fixture writes a development-only isolated provider
// runner configuration for the self-hosted compose stack.
//
// Usage:
//
//	adapter-runner-fixture --output /run/idenqa/adapter-runner.json \
//	  --listen-address 0.0.0.0:9090 \
//	  --certificate-file /run/idenqa/tls/adapter-runner.crt \
//	  --private-key-file /run/idenqa/tls/adapter-runner.key \
//	  --credential-file /run/idenqa/runner/credential \
//	  --app-id-file /run/idenqa/runner/app_id \
//	  --api-key-file /run/idenqa/runner/api_key \
//	  --gateway-url https://api:8443 \
//	  --gateway-ca-file /run/idenqa/tls/ca.crt \
//	  --gateway-credential-file /run/idenqa/runner/gateway_credential
//
// The written configuration uses the reviewed Dojah sandbox manifest digest
// from the repository and the runner's fixture transport. It never contacts a
// provider: it exists so the packaged adapter-runner service can prove the
// isolated runner boundary starts with mounted credentials until an operator
// supplies a real tenant binding.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/Mujhtech/idenqa/adapters/providers/dojah"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/bootstrap/adapterrunner"
)

const (
	// The fixture tenant and provider identifiers are syntactically valid
	// ULIDs; they are not registered tenants.
	fixtureTenantID   = "ten_01K4AR9V8FQ2G7ZXCPNM5T6JWH"
	fixtureProviderID = "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWH"
	// The fixture transport requires a literal loopback provider origin and is
	// never contacted by the packaged smoke gate.
	fixtureProviderOrigin = "https://127.0.0.1:1"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "adapter-runner-fixture: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var output, listenAddress, certificateFile, privateKeyFile string
	var credentialFile, appIDFile, apiKeyFile string
	var gatewayURL, gatewayCAFile, gatewayCredentialFile string
	flag.StringVar(&output, "output", "", "destination runner configuration file")
	flag.StringVar(&listenAddress, "listen-address", "0.0.0.0:9090", "runner gRPC listen address")
	flag.StringVar(&certificateFile, "certificate-file", "", "runner TLS certificate file")
	flag.StringVar(&privateKeyFile, "private-key-file", "", "runner TLS private key file")
	flag.StringVar(&credentialFile, "credential-file", "", "runner bearer credential file")
	flag.StringVar(&appIDFile, "app-id-file", "", "provider application identifier file")
	flag.StringVar(&apiKeyFile, "api-key-file", "", "provider API key file")
	flag.StringVar(&gatewayURL, "gateway-url", "https://api:8443", "core evidence gateway HTTPS origin")
	flag.StringVar(&gatewayCAFile, "gateway-ca-file", "", "core evidence gateway CA file")
	flag.StringVar(&gatewayCredentialFile, "gateway-credential-file", "", "core evidence gateway bearer credential file")
	flag.Parse()
	if output == "" || certificateFile == "" || privateKeyFile == "" || credentialFile == "" ||
		appIDFile == "" || apiKeyFile == "" || gatewayCAFile == "" || gatewayCredentialFile == "" {
		return errors.New("all mounted path flags are required")
	}

	manifest := dojah.Description()
	configuration := providerv1.ConfigurationReference{ //nolint:gosec // fixture reference URI, not a credential
		ProviderID:        fixtureProviderID,
		SchemaDigest:      manifest.Configuration.Digest,
		SecretReference:   "secret://dev/self-hosted-adapter-runner",
		CredentialVersion: "v1",
	}
	settings := adapterrunner.Settings{
		Adapter:               providerv1.AdapterDojah,
		AppIDFile:             appIDFile,
		APIKeyFile:            apiKeyFile,
		ListenAddress:         listenAddress,
		CertificateFile:       certificateFile,
		PrivateKeyFile:        privateKeyFile,
		CredentialFile:        credentialFile,
		TenantID:              fixtureTenantID,
		Configuration:         configuration,
		BaseURL:               fixtureProviderOrigin,
		GatewayURL:            gatewayURL,
		GatewayCAFile:         gatewayCAFile,
		GatewayCredentialFile: gatewayCredentialFile,
		Fixture:               true,
	}
	encoded, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return errors.New("encode runner configuration")
	}
	if err := os.WriteFile(output, append(encoded, '\n'), 0o600); err != nil {
		return errors.New("write runner configuration")
	}
	fmt.Printf("adapter_runner_config written=%s schema_digest=%s\n", output, configuration.SchemaDigest)
	return nil
}
