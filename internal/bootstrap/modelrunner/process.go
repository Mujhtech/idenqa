// Package modelrunner composes an isolated ONNX model workload.
package modelrunner

import (
	"bytes"
	"context"
	"crypto/tls"
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
	"github.com/Mujhtech/idenqa/internal/bootstrap/runnersecret"
	"github.com/Mujhtech/idenqa/internal/config"
	runnerv1 "github.com/Mujhtech/idenqa/internal/gen/proto/runner/v1"
	"github.com/Mujhtech/idenqa/internal/platform/egress"
	"github.com/Mujhtech/idenqa/internal/platform/secret"
	"github.com/Mujhtech/idenqa/internal/transport/runner"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
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

	SecretProvider             string `json:"secret_provider,omitempty"`
	SecretAWSRegion            string `json:"secret_aws_region,omitempty"`
	SecretCacheSeconds         int    `json:"secret_cache_seconds,omitempty"`
	SecretReloadSeconds        int    `json:"secret_reload_seconds,omitempty"`
	CredentialReference        string `json:"credential_reference,omitempty"`
	GatewayCredentialReference string `json:"gateway_credential_reference,omitempty"`
	TLSReference               string `json:"tls_reference,omitempty"`
}

// Process owns the private TLS model server.
type Process struct {
	server   *grpc.Server
	listener net.Listener
	client   *http.Client
	reload   *runnersecret.Reloader
}

// NewProcess validates pinned model settings, resolves the initial secret
// values, and starts no background work.
func NewProcess(ctx context.Context, settings Settings) (*Process, error) {
	if _, _, err := net.SplitHostPort(settings.ListenAddress); err != nil {
		return nil, errors.New("explicit model listen address required")
	}
	credentials := runner.NewEmptyCredentialSet()
	gatewayCredential := &runnersecret.Holder{}
	if settings.CredentialReference == "" {
		credential, err := config.ReadCredentialFile(settings.CredentialFile)
		if err != nil {
			return nil, err
		}
		if err := credentials.Replace(credential); err != nil {
			return nil, err
		}
	}
	if settings.GatewayCredentialReference == "" {
		gatewaySecret, err := config.ReadCredentialFile(settings.GatewayCredentialFile)
		if err != nil {
			return nil, err
		}
		if _, err := runner.NewCredentialSet(gatewaySecret); err != nil {
			return nil, err
		}
		gatewayCredential.Store(gatewaySecret)
	}
	transport, rotatingTLS, err := composeModelTLS(settings)
	if err != nil {
		return nil, err
	}
	reloader, err := composeModelReloader(ctx, settings, credentials, gatewayCredential, rotatingTLS)
	if err != nil {
		return nil, err
	}
	if reloader != nil {
		if err := reloader.Prime(ctx); err != nil {
			return nil, err
		}
	}
	client, err := egress.NewInternalClient(settings.GatewayURL, settings.GatewayCAFile)
	if err != nil {
		return nil, err
	}
	engine, err := newEngine(ctx, settings)
	if err != nil {
		return nil, err
	}
	implementation, err := onnx.New(settings.Model, engine, &gatewayReader{client, settings.GatewayURL, gatewayCredential}, time.Now)
	if err != nil {
		return nil, err
	}
	service, err := runner.NewModelServer(implementation)
	if err != nil {
		return nil, err
	}
	server, err := runner.NewServer(runner.ServerConfig{Credentials: credentials, TLS: transport, MaximumDeadline: 30 * time.Second})
	if err != nil {
		return nil, err
	}
	runnerv1.RegisterModelRunnerServiceServer(server, service)
	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", settings.ListenAddress)
	if err != nil {
		return nil, errors.New("listen for model runner")
	}
	return &Process{server: server, listener: listener, client: client, reload: reloader}, nil
}

func composeModelTLS(settings Settings) (credentials.TransportCredentials, *runner.RotatingServerTLS, error) {
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

func composeModelReloader(
	ctx context.Context,
	settings Settings,
	credentials *runner.CredentialSet,
	gatewayCredential *runnersecret.Holder,
	rotatingTLS *runner.RotatingServerTLS,
) (*runnersecret.Reloader, error) {
	references := []string{settings.CredentialReference, settings.GatewayCredentialReference, settings.TLSReference}
	if !runnersecret.Enabled(references...) {
		return nil, nil
	}
	resolver, err := runnersecret.Open(ctx, runnersecret.Options{
		Provider: settings.SecretProvider, AWSRegion: settings.SecretAWSRegion,
		CacheTTL:       modelSeconds(settings.SecretCacheSeconds, runnersecret.DefaultCacheTTL),
		ReloadInterval: modelSeconds(settings.SecretReloadSeconds, runnersecret.DefaultReloadInterval),
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
			accepted := []string{}
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
			gatewayCredential.Store(text)
			return nil
		}})
	}
	if settings.TLSReference != "" {
		if rotatingTLS == nil {
			return nil, errors.New("rotating model runner TLS is not initialised")
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

	return runnersecret.NewReloader(resolver, roles, modelSeconds(settings.SecretReloadSeconds, runnersecret.DefaultReloadInterval), nil)
}

func modelSeconds(seconds int, fallback time.Duration) time.Duration {
	if seconds <= 0 || seconds > 3600 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

// Address returns the listening address.
func (process *Process) Address() string { return process.listener.Addr().String() }

// Run starts bounded secret refresh, serves until cancellation, and bounds
// shutdown.
func (process *Process) Run(ctx context.Context) error {
	if process.reload != nil {
		reloadCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() { _ = process.reload.Run(reloadCtx) }()
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

// Close releases owned resources.
func (process *Process) Close() {
	process.server.Stop()
	_ = process.listener.Close()
	process.client.CloseIdleConnections()
}

type gatewayReader struct {
	client     *http.Client
	origin     string
	credential *runnersecret.Holder
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
	request.Header.Set("Authorization", "Bearer "+reader.credential.Load())
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
	if settings.Model.SelfieAnalysis {
		if settings.Model.FacePreparation == nil {
			return nil, onnx.ErrRuntime
		}
		return onnx.NewAnalysisEngine(ctx, settings.Python, p.RuntimeDigest, settings.DetectorFile, *settings.Model.FacePreparation)
	}
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
