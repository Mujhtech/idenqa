// Package modelrunner composes an isolated ONNX model workload.
package modelrunner

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

	"github.com/Mujhtech/idenqa/adapters/models/onnx"
	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	"github.com/Mujhtech/idenqa/internal/config"
	runnerv1 "github.com/Mujhtech/idenqa/internal/gen/proto/runner/v1"
	"github.com/Mujhtech/idenqa/internal/platform/egress"
	"github.com/Mujhtech/idenqa/internal/transport/runner"
	"google.golang.org/grpc"
)

// Settings contains operator-mounted paths and one immutable model binding.
type Settings struct {
	ListenAddress         string             `json:"listen_address"`
	CertificateFile       string             `json:"certificate_file"`
	PrivateKeyFile        string             `json:"private_key_file"`
	CredentialFile        string             `json:"credential_file"`
	Python                string             `json:"python"`
	DetectorFile          string             `json:"detector_file,omitempty"`
	ModelFile             string             `json:"model_file"`
	Model                 onnx.Configuration `json:"model"`
	GatewayURL            string             `json:"gateway_url"`
	GatewayCAFile         string             `json:"gateway_ca_file"`
	GatewayCredentialFile string             `json:"gateway_credential_file"`
}

// Process owns the private TLS model server.
type Process struct {
	server   *grpc.Server
	listener net.Listener
	client   *http.Client
}

// NewProcess validates pinned model settings before listening.
func NewProcess(ctx context.Context, settings Settings) (*Process, error) {
	if _, _, err := net.SplitHostPort(settings.ListenAddress); err != nil {
		return nil, errors.New("explicit model listen address required")
	}
	secret, err := config.ReadCredentialFile(settings.CredentialFile)
	if err != nil {
		return nil, err
	}
	credentials, err := runner.NewCredentialSet(secret)
	if err != nil {
		return nil, err
	}
	tls, err := runner.ServerTLSCredentials(settings.CertificateFile, settings.PrivateKeyFile)
	if err != nil {
		return nil, err
	}
	gatewaySecret, err := config.ReadCredentialFile(settings.GatewayCredentialFile)
	if err != nil {
		return nil, err
	}
	if _, err := runner.NewCredentialSet(gatewaySecret); err != nil {
		return nil, err
	}
	client, err := egress.NewInternalClient(settings.GatewayURL, settings.GatewayCAFile)
	if err != nil {
		return nil, err
	}
	engine, err := newEngine(ctx, settings)
	if err != nil {
		return nil, err
	}
	implementation, err := onnx.New(settings.Model, engine, &gatewayReader{client, settings.GatewayURL, gatewaySecret}, time.Now)
	if err != nil {
		return nil, err
	}
	service, err := runner.NewModelServer(implementation)
	if err != nil {
		return nil, err
	}
	server, err := runner.NewServer(runner.ServerConfig{Credentials: credentials, TLS: tls, MaximumDeadline: 30 * time.Second})
	if err != nil {
		return nil, err
	}
	runnerv1.RegisterModelRunnerServiceServer(server, service)
	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", settings.ListenAddress)
	if err != nil {
		return nil, errors.New("listen for model runner")
	}
	return &Process{server, listener, client}, nil
}

// Address returns the listening address.
func (process *Process) Address() string { return process.listener.Addr().String() }

// Run serves until cancellation and bounds shutdown.
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

// Close releases owned resources.
func (process *Process) Close() {
	process.server.Stop()
	_ = process.listener.Close()
	process.client.CloseIdleConnections()
}

type gatewayReader struct {
	client             *http.Client
	origin, credential string
}

func (reader *gatewayReader) ReadModelEvidence(ctx context.Context, envelope modelv1.Request, grant modelv1.EvidenceGrantReference, limit int64) ([]byte, error) {
	if !slices.Contains(envelope.Evidence, grant) || limit <= 0 || limit > 10<<20 {
		return nil, onnx.ErrRuntime
	}
	raw, err := json.Marshal(map[string]string{"attempt_id": envelope.AttemptID, "grant_id": grant.GrantID, "redemption_id": grant.RedemptionID})
	if err != nil {
		return nil, onnx.ErrRuntime
	}
	request, err := http.NewRequestWithContext(ctx, "POST", strings.TrimSuffix(reader.origin, "/")+"/internal/v1/model-evidence", bytes.NewReader(raw))
	if err != nil {
		return nil, onnx.ErrRuntime
	}
	request.Header.Set("Authorization", "Bearer "+reader.credential)
	request.Header.Set("Content-Type", "application/json")
	response, err := reader.client.Do(request)
	if err != nil {
		return nil, onnx.ErrRuntime
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 200 {
		return nil, onnx.ErrRuntime
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(body)) > limit || len(body) == 0 {
		clear(body)
		return nil, onnx.ErrRuntime
	}
	return body, nil
}

func newEngine(ctx context.Context, settings Settings) (*onnx.Engine, error) {
	p := settings.Model.Manifest.Provenance
	if settings.Model.FaceMatching {
		if settings.Model.FacePreparation == nil {
			return nil, onnx.ErrRuntime
		}
		return onnx.NewMatchingEngine(ctx, settings.Python, settings.ModelFile, p.ModelDigest, p.RuntimeDigest, settings.DetectorFile, *settings.Model.FacePreparation)
	}
	if settings.Model.FacePreparation != nil {
		return onnx.NewFaceEngine(ctx, settings.Python, settings.ModelFile, p.ModelDigest, p.RuntimeDigest, settings.DetectorFile, *settings.Model.FacePreparation)
	}
	if settings.DetectorFile != "" {
		return nil, onnx.ErrRuntime
	}
	return onnx.NewEngine(ctx, settings.Python, settings.ModelFile, p.ModelDigest, p.RuntimeDigest, []int{1, 3, settings.Model.Height, settings.Model.Width})
}
