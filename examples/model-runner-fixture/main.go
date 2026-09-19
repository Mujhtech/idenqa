// Command model-runner-fixture writes a development-only isolated ONNX model
// runner configuration pinned to the repository's evaluation fixture.
//
// Usage (inside the packaged model-runner image):
//
//	model-runner-fixture --output /run/idenqa/model-runner.json \
//	  --python /usr/local/bin/python3 \
//	  --model-file /opt/idenqa/models/pad_fixture.onnx \
//	  --listen-address 0.0.0.0:9091 \
//	  --certificate-file /run/idenqa/tls/model-runner.crt \
//	  --private-key-file /run/idenqa/tls/model-runner.key \
//	  --credential-file /run/idenqa/runner/model_credential \
//	  --gateway-url https://api:8443 \
//	  --gateway-ca-file /run/idenqa/tls/ca.crt \
//	  --gateway-credential-file /run/idenqa/runner/model_gateway_credential
//
// The fixture model is `evaluation_only` and produces an inconclusive-grade
// native runtime identity check only. It establishes no PAD accuracy and must
// never be presented as a trained or accepted model.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Mujhtech/idenqa/adapters/models/onnx"
	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	"github.com/Mujhtech/idenqa/internal/bootstrap/modelrunner"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "model-runner-fixture: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var output, python, modelFile, listenAddress string
	var certificateFile, privateKeyFile, credentialFile string
	var gatewayURL, gatewayCAFile, gatewayCredentialFile string
	flag.StringVar(&output, "output", "", "destination runner configuration file")
	flag.StringVar(&python, "python", "", "absolute path to the isolated Python interpreter")
	flag.StringVar(&modelFile, "model-file", "", "absolute path to the pinned evaluation model")
	flag.StringVar(&listenAddress, "listen-address", "0.0.0.0:9091", "runner gRPC listen address")
	flag.StringVar(&certificateFile, "certificate-file", "", "runner TLS certificate file")
	flag.StringVar(&privateKeyFile, "private-key-file", "", "runner TLS private key file")
	flag.StringVar(&credentialFile, "credential-file", "", "runner bearer credential file")
	flag.StringVar(&gatewayURL, "gateway-url", "https://api:8443", "core evidence gateway HTTPS origin")
	flag.StringVar(&gatewayCAFile, "gateway-ca-file", "", "core evidence gateway CA file")
	flag.StringVar(&gatewayCredentialFile, "gateway-credential-file", "", "core evidence gateway bearer credential file")
	flag.Parse()
	if output == "" || python == "" || modelFile == "" || certificateFile == "" || privateKeyFile == "" ||
		credentialFile == "" || gatewayCAFile == "" || gatewayCredentialFile == "" {
		return errors.New("all mounted path flags are required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	runtimeDigest, err := onnx.RuntimeDigest(ctx, python)
	if err != nil {
		return errors.New("measure pinned ONNX runtime")
	}
	raw, err := os.ReadFile(modelFile) //nolint:gosec // operator-mounted evaluation model
	if err != nil || len(raw) == 0 {
		return errors.New("read pinned evaluation model")
	}
	sum := sha256.Sum256(raw)
	identifiers, err := id.NewSystemGenerator()
	if err != nil {
		return errors.New("construct identifier generator")
	}
	tenantID, err := identifiers.NewTenant()
	if err != nil {
		return errors.New("generate fixture tenant identifier")
	}
	modelID, err := identifiers.NewModel()
	if err != nil {
		return errors.New("generate fixture model identifier")
	}
	configuration := onnx.Configuration{
		TenantID: tenantID.String(), Width: 128, Height: 128, EvaluationOnly: true,
		Registration: modelv1.ConfigurationReference{
			ModelID: modelID.String(), ConfigurationRef: "configuration://model/pad-fixture",
		},
		Manifest: modelv1.Manifest{
			Provenance: modelv1.Provenance{
				ModelID: modelID.String(), ModelVersion: "0.1.0",
				ModelDigest:         "sha256:" + hex.EncodeToString(sum[:]),
				RuntimeDigest:       runtimeDigest,
				PreprocessingDigest: onnx.PreprocessingDigest(128, 128),
				OutputSchemaDigest:  onnx.OutputSchemaDigest(),
				Contract:            modelv1.CurrentVersion,
			},
			Capabilities: []modelv1.Capability{{
				Evaluation:       "idenqa.check.passive_pad",
				AcceptedEvidence: []string{"idenqa.evidence.selfie_image"},
				OutputSignals:    []string{"idenqa.signal.passive_pad"},
			}},
			Restrictions: modelv1.Restrictions{
				MaximumGrants: 1, MaximumInputBytes: 10 << 20,
				MaximumResultSize: 4096, MaximumDuration: 30 * time.Second,
			},
		},
	}
	configuration.Registration.ConfigurationDigest = onnx.ConfigurationDigest(configuration)

	settings := modelrunner.Settings{
		ListenAddress:         listenAddress,
		CertificateFile:       certificateFile,
		PrivateKeyFile:        privateKeyFile,
		CredentialFile:        credentialFile,
		Python:                python,
		ModelFile:             modelFile,
		Model:                 configuration,
		GatewayURL:            gatewayURL,
		GatewayCAFile:         gatewayCAFile,
		GatewayCredentialFile: gatewayCredentialFile,
	}
	encoded, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return errors.New("encode runner configuration")
	}
	if err := os.WriteFile(output, append(encoded, '\n'), 0o600); err != nil {
		return errors.New("write runner configuration")
	}
	fmt.Printf("model_runner_config written=%s model_id=%s configuration_digest=%s\n",
		output, modelID.String(), configuration.Registration.ConfigurationDigest)
	return nil
}
