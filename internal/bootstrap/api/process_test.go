package api

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/buildinfo"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/health"
	localkms "github.com/Mujhtech/idenqa/internal/platform/kms/local"
	"github.com/Mujhtech/idenqa/internal/platform/postgres"
)

func TestNewProcessConfiguresTLS(t *testing.T) {
	t.Parallel()

	certificateFile, keyFile := writeTestCertificate(t)
	configuration := config.API{
		HTTPHost:           "127.0.0.1",
		HTTPPort:           8443,
		HTTPTLSMode:        "file",
		HTTPTLSCertFile:    certificateFile,
		HTTPTLSKeyFile:     keyFile,
		ShutdownTimeout:    time.Second,
		HTTPRequestTimeout: time.Second,
		HTTPMaxBodyBytes:   1_048_576,
	}
	configureTestSecrets(t, &configuration)

	process, err := newProcess(
		context.Background(),
		configuration,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&health.State{},
		buildinfo.Info{},
		func(context.Context, postgres.Config) (database, error) {
			return &fakeDatabase{}, nil
		},
		EvidenceInfrastructure{},
	)
	if err != nil {
		t.Fatalf("NewProcess() error = %v", err)
	}
	t.Cleanup(func() {
		if err := process.telemetry.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown telemetry: %v", err)
		}
	})
	if !process.tlsEnabled {
		t.Fatal("TLS file mode did not enable TLS serving")
	}

	httpServer, ok := process.server.(*http.Server)
	if !ok {
		t.Fatalf("server type = %T, want *http.Server", process.server)
	}
	if httpServer.TLSConfig == nil {
		t.Fatal("TLSConfig is nil")
	}
	if got, want := httpServer.TLSConfig.MinVersion, uint16(tls.VersionTLS12); got != want {
		t.Errorf("minimum TLS version = %d, want %d", got, want)
	}
	if got := len(httpServer.TLSConfig.Certificates); got != 1 {
		t.Errorf("certificate count = %d, want 1", got)
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/capture/socket", nil)
	response := httptest.NewRecorder()
	httpServer.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("realtime socket route status = %d, want %d", response.Code, http.StatusForbidden)
	}
}

func TestNewProcessComposesLocalEvidenceUploadRoutes(t *testing.T) {
	t.Parallel()

	keyringFile := filepath.Join(t.TempDir(), "evidence-keyring.json")
	keyring, err := localkms.Create(keyringFile)
	if err != nil {
		t.Fatalf("create local evidence keyring: %v", err)
	}
	if err := keyring.Close(); err != nil {
		t.Fatalf("close initial local evidence keyring: %v", err)
	}
	configuration := config.API{
		EvidenceUploadConfiguration: config.EvidenceUploadConfiguration{
			EvidenceUploadMaximumBytes:   evidence.DefaultUploadMaximumBytes,
			EvidenceUploadIntentLifetime: evidence.DefaultUploadIntentLifetime,
			EvidenceUploadAttemptTimeout: evidence.DefaultUploadAttemptTimeout,
			EvidenceUploadMediaTypes:     []string{evidence.MediaTypeJPEG, evidence.MediaTypePNG},
		},
		EvidenceLocalDirectory:    t.TempDir(),
		EvidenceLocalKeyringFile:  keyringFile,
		EvidenceProtectionCleanup: time.Second,
		HTTPHost:                  "127.0.0.1",
		HTTPPort:                  8080,
		ShutdownTimeout:           time.Second,
		HTTPRequestTimeout:        time.Second,
		HTTPMaxBodyBytes:          1_048_576,
	}
	configureTestSecrets(t, &configuration)
	infrastructure, err := configuredLocalEvidence(configuration)
	if err != nil {
		t.Fatalf("configure local evidence infrastructure: %v", err)
	}
	process, err := newProcess(
		context.Background(),
		configuration,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&health.State{},
		buildinfo.Info{},
		func(context.Context, postgres.Config) (database, error) {
			return &fakeDatabase{}, nil
		},
		infrastructure,
	)
	if err != nil {
		t.Fatalf("newProcess() error = %v", err)
	}
	t.Cleanup(func() {
		if err := process.evidence.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown evidence infrastructure: %v", err)
		}
		if err := process.telemetry.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown telemetry: %v", err)
		}
	})

	httpServer, ok := process.server.(*http.Server)
	if !ok {
		t.Fatalf("server type = %T, want *http.Server", process.server)
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/evidence-uploads", nil)
	response := httptest.NewRecorder()
	httpServer.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("upload route status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestNewProcessRequiresAPIKeyPeppersBeforeOpeningDatabase(t *testing.T) {
	t.Parallel()

	configuration := config.API{HTTPHost: "127.0.0.1", HTTPPort: 8080}
	configureTestRealtime(&configuration)
	opened := false
	_, err := newProcess(
		context.Background(),
		configuration,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&health.State{},
		buildinfo.Info{},
		func(context.Context, postgres.Config) (database, error) {
			opened = true

			return &fakeDatabase{}, nil
		},
		EvidenceInfrastructure{},
	)
	if err == nil || !strings.Contains(err.Error(), "configure API key peppers") {
		t.Fatalf("newProcess() error = %v, want pepper configuration error", err)
	}
	if opened {
		t.Fatal("database opened before mandatory pepper validation")
	}
}

func TestNewProcessRequiresCursorKeysBeforeOpeningDatabase(t *testing.T) {
	t.Parallel()

	configuration := config.API{HTTPHost: "127.0.0.1", HTTPPort: 8080}
	configureTestRealtime(&configuration)
	encoded := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	if err := configuration.APIKeyPeppers.Decode("1=" + encoded); err != nil {
		t.Fatalf("decode test API key pepper: %v", err)
	}
	configuration.APIKeyActivePepperVersion = 1
	opened := false
	_, err := newProcess(
		context.Background(),
		configuration,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&health.State{},
		buildinfo.Info{},
		func(context.Context, postgres.Config) (database, error) {
			opened = true

			return &fakeDatabase{}, nil
		},
		EvidenceInfrastructure{},
	)
	if err == nil || !strings.Contains(err.Error(), "configure cursor keys") {
		t.Fatalf("newProcess() error = %v, want cursor configuration error", err)
	}
	if opened {
		t.Fatal("database opened before mandatory cursor-key validation")
	}
}

func TestNewProcessRequiresCaptureTokenKeysBeforeOpeningDatabase(t *testing.T) {
	t.Parallel()

	configuration := config.API{HTTPHost: "127.0.0.1", HTTPPort: 8080}
	configureTestRealtime(&configuration)
	encoded := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	if err := configuration.APIKeyPeppers.Decode("1=" + encoded); err != nil {
		t.Fatalf("decode test API key pepper: %v", err)
	}
	configuration.APIKeyActivePepperVersion = 1
	if err := configuration.CursorKeys.Decode("1=" + encoded); err != nil {
		t.Fatalf("decode test cursor key: %v", err)
	}
	configuration.CursorActiveKeyVersion = 1
	configuration.CursorTTL = 15 * time.Minute
	opened := false
	_, err := newProcess(
		context.Background(),
		configuration,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&health.State{},
		buildinfo.Info{},
		func(context.Context, postgres.Config) (database, error) {
			opened = true

			return &fakeDatabase{}, nil
		},
		EvidenceInfrastructure{},
	)
	if err == nil || !strings.Contains(err.Error(), "configure capture-token keys") {
		t.Fatalf("newProcess() error = %v, want capture-token configuration error", err)
	}
	if opened {
		t.Fatal("database opened before mandatory capture-token key validation")
	}
}

func TestNewProcessRequiresOutcomeTokenKeysBeforeOpeningDatabase(t *testing.T) {
	t.Parallel()

	configuration := config.API{HTTPHost: "127.0.0.1", HTTPPort: 8080}
	configureTestRealtime(&configuration)
	encoded := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	if err := configuration.APIKeyPeppers.Decode("1=" + encoded); err != nil {
		t.Fatalf("decode test API key pepper: %v", err)
	}
	configuration.APIKeyActivePepperVersion = 1
	if err := configuration.CursorKeys.Decode("1=" + encoded); err != nil {
		t.Fatalf("decode test cursor key: %v", err)
	}
	configuration.CursorActiveKeyVersion = 1
	configuration.CursorTTL = 15 * time.Minute
	if err := configuration.CaptureTokenKeys.Decode("1=" + encoded); err != nil {
		t.Fatalf("decode test capture-token key: %v", err)
	}
	configuration.CaptureTokenActiveVersion = 1
	opened := false
	_, err := newProcess(
		context.Background(),
		configuration,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&health.State{},
		buildinfo.Info{},
		func(context.Context, postgres.Config) (database, error) {
			opened = true

			return &fakeDatabase{}, nil
		},
		EvidenceInfrastructure{},
	)
	if err == nil || !strings.Contains(err.Error(), "configure outcome-token keys") {
		t.Fatalf("newProcess() error = %v, want outcome-token configuration error", err)
	}
	if opened {
		t.Fatal("database opened before mandatory outcome-token key validation")
	}
}

func TestNewProcessRejectsIncompatibleHeadgateSchema(t *testing.T) {
	t.Parallel()
	configuration := config.API{
		HTTPHost: "127.0.0.1", HTTPPort: 8080, HeadgateSchema: "headgate",
		ShutdownTimeout: time.Second, HTTPRequestTimeout: time.Second,
		HTTPMaxBodyBytes: 1_048_576, DatabaseHealthTimeout: time.Second,
	}
	configureTestSecrets(t, &configuration)
	db := &fakeDatabase{headgateChecks: make(chan error, 1)}
	db.headgateChecks <- errors.New("schema incompatible")
	_, err := newProcess(
		t.Context(), configuration,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&health.State{}, buildinfo.Info{},
		func(context.Context, postgres.Config) (database, error) { return db, nil },
		EvidenceInfrastructure{},
	)
	if err == nil || !strings.Contains(err.Error(), "check API Headgate schema") {
		t.Fatalf("newProcess() error = %v, want Headgate compatibility error", err)
	}
}

func TestNewProcessRejectsInvalidTLSWithoutLeakingPaths(t *testing.T) {
	t.Parallel()

	configuration := config.API{
		HTTPHost:        "127.0.0.1",
		HTTPPort:        8443,
		HTTPTLSMode:     "file",
		HTTPTLSCertFile: "/sensitive/location/server.crt",
		HTTPTLSKeyFile:  "/sensitive/location/server.key",
		ShutdownTimeout: time.Second,
	}

	_, err := NewProcess(
		context.Background(),
		configuration,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&health.State{},
		buildinfo.Info{},
	)
	if err == nil {
		t.Fatal("NewProcess() error = nil, want invalid certificate error")
	}
	if strings.Contains(err.Error(), "/sensitive/location") {
		t.Fatalf("NewProcess() error leaks certificate path: %v", err)
	}
}

func TestProcessRunTracksDatabaseReadinessRecovery(t *testing.T) {
	t.Parallel()

	state := &health.State{}
	fake := newFakeServer()
	database := &fakeDatabase{checks: make(chan error, 2)}
	process := newTestProcess(state, fake)
	process.database = database
	process.databaseInterval = time.Millisecond
	process.databaseTimeout = time.Second

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- process.Run(ctx)
	}()

	<-fake.serveStarted
	database.checks <- errors.New("database unavailable")
	waitForReadiness(t, state, false)
	database.checks <- nil
	waitForReadiness(t, state, true)

	cancel()
	<-fake.shutdownStarted
	close(fake.allowShutdown)
	if err := <-result; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestProcessRunTracksHeadgateReadinessRecovery(t *testing.T) {
	t.Parallel()
	state := &health.State{}
	fake := newFakeServer()
	database := &fakeDatabase{headgateChecks: make(chan error, 2)}
	process := newTestProcess(state, fake)
	process.database = database
	process.databaseInterval = time.Millisecond
	process.databaseTimeout = time.Second
	process.headgateSchema = "headgate_test"

	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() { result <- process.Run(ctx) }()
	<-fake.serveStarted
	database.headgateChecks <- errors.New("headgate unavailable")
	waitForReadiness(t, state, false)
	database.headgateChecks <- nil
	waitForReadiness(t, state, true)
	cancel()
	<-fake.shutdownStarted
	close(fake.allowShutdown)
	if err := <-result; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestProcessRunDrainsWithDeadline(t *testing.T) {
	t.Parallel()

	state := &health.State{}
	fake := newFakeServer()
	process := newTestProcess(state, fake)
	drainer := newFakeConnectionDrainer()
	process.realtime = drainer

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- process.Run(ctx)
	}()

	<-fake.serveStarted
	if !state.Started() || !state.Ready() {
		t.Fatalf("process health after start = started:%t ready:%t", state.Started(), state.Ready())
	}

	cancel()
	shutdownContext := <-fake.shutdownStarted
	drainContext := <-drainer.started
	deadline, exists := shutdownContext.Deadline()
	if !exists {
		t.Error("shutdown context has no deadline")
	} else if remaining := time.Until(deadline); remaining <= 0 || remaining > time.Second {
		t.Errorf("shutdown deadline remaining = %s, want within (0s, 1s]", remaining)
	}
	drainDeadline, drainDeadlineExists := drainContext.Deadline()
	if !drainDeadlineExists || !drainDeadline.Equal(deadline) {
		t.Errorf("realtime drain deadline = %s/%t, want %s", drainDeadline, drainDeadlineExists, deadline)
	}
	if state.Ready() {
		t.Error("process remained ready during drain")
	}

	close(fake.allowShutdown)
	select {
	case err := <-result:
		t.Fatalf("Run() returned before realtime drain completed: %v", err)
	default:
	}
	close(drainer.allow)
	if err := <-result; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if state.Ready() {
		t.Error("process became ready after shutdown")
	}
}

func TestProcessRunReportsStartupFailure(t *testing.T) {
	t.Parallel()

	state := &health.State{}
	process := newTestProcess(state, newFakeServer())
	process.listen = func(context.Context, string, string) (net.Listener, error) {
		return nil, errors.New("bind failed")
	}

	err := process.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "listen for API traffic") {
		t.Fatalf("Run() error = %v, want listener failure", err)
	}
	if state.Started() || state.Ready() {
		t.Fatalf("failed process health = started:%t ready:%t", state.Started(), state.Ready())
	}
}

func TestProcessRunClosesTelemetryAfterStartupFailure(t *testing.T) {
	t.Parallel()

	state := &health.State{}
	process := newTestProcess(state, newFakeServer())
	telemetry := &fakeShutdowner{called: make(chan struct{}, 1)}
	process.telemetry = telemetry
	process.listen = func(context.Context, string, string) (net.Listener, error) {
		return nil, errors.New("bind failed")
	}

	if err := process.Run(context.Background()); err == nil {
		t.Fatal("Run() error = nil")
	}
	select {
	case <-telemetry.called:
	default:
		t.Fatal("telemetry was not shut down")
	}
}

func TestProcessRunReportsShutdownFailure(t *testing.T) {
	t.Parallel()

	state := &health.State{}
	fake := newFakeServer()
	fake.shutdownErr = errors.New("shutdown failed")
	process := newTestProcess(state, fake)

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- process.Run(ctx)
	}()

	<-fake.serveStarted
	cancel()
	<-fake.shutdownStarted
	close(fake.allowShutdown)

	err := <-result
	if err == nil || !strings.Contains(err.Error(), "shut down API") {
		t.Fatalf("Run() error = %v, want shutdown failure", err)
	}
}

func TestProcessRunReportsRealtimeDrainFailure(t *testing.T) {
	t.Parallel()

	state := &health.State{}
	fake := newFakeServer()
	process := newTestProcess(state, fake)
	drainer := newFakeConnectionDrainer()
	drainer.err = errors.New("realtime drain failed")
	process.realtime = drainer

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- process.Run(ctx)
	}()

	<-fake.serveStarted
	cancel()
	<-fake.shutdownStarted
	<-drainer.started
	close(fake.allowShutdown)
	close(drainer.allow)

	err := <-result
	if err == nil || !strings.Contains(err.Error(), "drain realtime connections") {
		t.Fatalf("Run() error = %v, want realtime drain failure", err)
	}
}

func newTestProcess(state *health.State, fake *fakeServer) *Process {
	return &Process{
		address:         "127.0.0.1:8080",
		shutdownTimeout: time.Second,
		server:          fake,
		listen: func(context.Context, string, string) (net.Listener, error) {
			return stubListener{}, nil
		},
		health: state,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		build:  buildinfo.Info{Version: "test", Commit: "test", Date: "test"},
	}
}

type fakeServer struct {
	serveStarted    chan struct{}
	serveStopped    chan struct{}
	shutdownStarted chan context.Context
	allowShutdown   chan struct{}
	shutdownErr     error
}

type fakeShutdowner struct {
	called chan struct{}
}

type fakeConnectionDrainer struct {
	started chan context.Context
	allow   chan struct{}
	err     error
}

type fakeDatabase struct {
	checks         chan error
	headgateChecks chan error
}

func (database *fakeDatabase) CheckHeadgate(ctx context.Context, _ string) error {
	if database.headgateChecks == nil {
		return nil
	}
	select {
	case err := <-database.headgateChecks:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (database *fakeDatabase) Check(ctx context.Context, _ uint) error {
	if database.checks == nil {
		return nil
	}
	select {
	case err := <-database.checks:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (*fakeDatabase) Close() {}

func (*fakeDatabase) WithinTransaction(
	context.Context,
	postgres.TransactionOptions,
	func(context.Context, postgres.Transaction) error,
) error {
	return errors.New("fake database transaction is not implemented")
}

func configureTestSecrets(t *testing.T, configuration *config.API) {
	t.Helper()

	encoded := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	if err := configuration.APIKeyPeppers.Decode("1=" + encoded); err != nil {
		t.Fatalf("decode test API key pepper: %v", err)
	}
	configuration.APIKeyActivePepperVersion = 1
	if err := configuration.CursorKeys.Decode("1=" + encoded); err != nil {
		t.Fatalf("decode test cursor key: %v", err)
	}
	configuration.CursorActiveKeyVersion = 1
	configuration.CursorTTL = 15 * time.Minute
	configuration.ProfileIdempotencyTTL = 24 * time.Hour
	if err := configuration.CaptureTokenKeys.Decode("1=" + encoded); err != nil {
		t.Fatalf("decode test capture-token key: %v", err)
	}
	configuration.CaptureTokenActiveVersion = 1
	if err := configuration.OutcomeTokenKeys.Decode("1=" + encoded); err != nil {
		t.Fatalf("decode test outcome-token key: %v", err)
	}
	configuration.OutcomeTokenActiveVersion = 1
	configuration.VerificationDefaultTTL = 24 * time.Hour
	configuration.VerificationMaximumTTL = 7 * 24 * time.Hour
	configuration.CaptureTokenDefaultTTL = 30 * time.Minute
	configuration.CaptureTokenMaximumTTL = 2 * time.Hour
	configuration.OutcomeTokenDefaultPostTTL = 24 * time.Hour
	configuration.OutcomeTokenMaximumPostTTL = 7 * 24 * time.Hour
	configuration.VerificationIdempotencyTTL = 24 * time.Hour
	configureTestRealtime(configuration)
}

func configureTestRealtime(configuration *config.API) {
	configuration.Region = "local"
	configuration.RealtimeWebSocketURL = "ws://127.0.0.1:8080/v1/capture/socket"
	configuration.RealtimeTicketLifetime = 30 * time.Second
	configuration.HTTPCORSAllowedOrigins = []string{"http://localhost:3000"}
}

func (shutdowner *fakeShutdowner) Shutdown(context.Context) error {
	shutdowner.called <- struct{}{}

	return nil
}

func newFakeConnectionDrainer() *fakeConnectionDrainer {
	return &fakeConnectionDrainer{
		started: make(chan context.Context, 1),
		allow:   make(chan struct{}),
	}
}

func (drainer *fakeConnectionDrainer) Drain(ctx context.Context) error {
	drainer.started <- ctx
	select {
	case <-drainer.allow:
		return drainer.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func waitForReadiness(t *testing.T, state *health.State, ready bool) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for state.Ready() != ready {
		if time.Now().After(deadline) {
			t.Fatalf("readiness = %t, want %t", state.Ready(), ready)
		}
		time.Sleep(time.Millisecond)
	}
}

func newFakeServer() *fakeServer {
	return &fakeServer{
		serveStarted:    make(chan struct{}),
		serveStopped:    make(chan struct{}),
		shutdownStarted: make(chan context.Context, 1),
		allowShutdown:   make(chan struct{}),
	}
}

func (server *fakeServer) Serve(net.Listener) error {
	close(server.serveStarted)
	<-server.serveStopped

	return http.ErrServerClosed
}

func (server *fakeServer) ServeTLS(listener net.Listener, _, _ string) error {
	return server.Serve(listener)
}

func (server *fakeServer) Shutdown(ctx context.Context) error {
	server.shutdownStarted <- ctx

	select {
	case <-server.allowShutdown:
		close(server.serveStopped)

		return server.shutdownErr
	case <-ctx.Done():
		close(server.serveStopped)

		return ctx.Err()
	}
}

type stubListener struct{}

func (stubListener) Accept() (net.Conn, error) {
	return nil, errors.New("accept is not implemented")
}

func (stubListener) Close() error {
	return nil
}

func (stubListener) Addr() net.Addr {
	return stubAddress("test")
}

type stubAddress string

func (address stubAddress) Network() string {
	return string(address)
}

func (address stubAddress) String() string {
	return string(address)
}

func writeTestCertificate(t *testing.T) (string, string) {
	t.Helper()

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate test private key: %v", err)
	}

	validFrom := time.Date(2026, time.August, 27, 0, 0, 0, 0, time.UTC)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    validFrom,
		NotAfter:     validFrom.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("create test certificate: %v", err)
	}

	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("marshal test private key: %v", err)
	}

	directory := t.TempDir()
	certificateFile := filepath.Join(directory, "server.crt")
	keyFile := filepath.Join(directory, "server.key")
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER})
	if err := os.WriteFile(certificateFile, certificatePEM, 0o600); err != nil {
		t.Fatalf("write test certificate: %v", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatalf("write test private key: %v", err)
	}

	return certificateFile, keyFile
}
