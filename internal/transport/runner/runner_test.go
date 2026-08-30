package runner

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	runnerv1 "github.com/Mujhtech/idenqa/internal/gen/proto/runner/v1"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const testIDPayload = "01ARZ3NDEKTSV4RRFFQ69G5FAV"

func TestRunnerAuthenticationDeadlineCancellationAndTrace(t *testing.T) {
	t.Parallel()
	token := testCredential(1)
	previousToken := testCredential(2)
	credentialSet, err := NewCredentialSet(token, previousToken)
	if err != nil {
		t.Fatal(err)
	}
	serverTLS, clientTLS := testTLS(t)
	recorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = tracerProvider.Shutdown(context.Background()) })

	started := make(chan struct{})
	providerAdapter := &stubProvider{execute: func(ctx context.Context, _ providerv1.Request) (providerv1.Result, error) {
		close(started)
		<-ctx.Done()
		return providerv1.Result{}, ctx.Err()
	}}
	providerService, err := NewProviderServer(providerAdapter)
	if err != nil {
		t.Fatal(err)
	}
	modelService, err := NewModelServer(stubModel{})
	if err != nil {
		t.Fatal(err)
	}

	listener := bufconn.Listen(1024 * 1024)
	server, err := NewServer(ServerConfig{
		Credentials: credentialSet, TLS: serverTLS, MaximumDeadline: time.Minute,
		TracerProvider: tracerProvider, Propagator: propagation.TraceContext{},
	})
	if err != nil {
		t.Fatal(err)
	}
	runnerv1.RegisterProviderRunnerServiceServer(server, providerService)
	runnerv1.RegisterModelRunnerServiceServer(server, modelService)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })

	connection := dialRunner(t, listener, clientTLS, token, tracerProvider)
	client := runnerv1.NewProviderRunnerServiceClient(connection)
	modelClient := runnerv1.NewModelRunnerServiceClient(connection)
	providerPort, err := NewProviderClient(client)
	if err != nil {
		t.Fatal(err)
	}
	modelPort, err := NewModelClient(modelClient)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("provider and model adapters round trip", func(t *testing.T) {
		if _, manifestErr := providerPort.Manifest(context.Background()); manifestErr != nil {
			t.Fatal(manifestErr)
		}
		request := validModelRequest(time.Now().Add(time.Minute).UTC())
		result, executeErr := modelPort.Execute(context.Background(), request)
		if executeErr != nil {
			t.Fatal(executeErr)
		}
		if result.AttemptID != request.AttemptID || result.Outcome != modelv1.ResultOutcomeCompleted {
			t.Fatalf("unexpected model result: %+v", result)
		}
	})

	t.Run("missing deadline", func(t *testing.T) {
		_, callErr := client.Health(context.Background(), &runnerv1.ProviderRunnerServiceHealthRequest{})
		if status.Code(callErr) != codes.InvalidArgument {
			t.Fatalf("expected invalid argument, got %v", callErr)
		}
	})

	t.Run("excessive deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, callErr := client.Health(ctx, &runnerv1.ProviderRunnerServiceHealthRequest{})
		if status.Code(callErr) != codes.InvalidArgument {
			t.Fatalf("expected invalid argument, got %v", callErr)
		}
	})

	t.Run("payload deadline cannot exceed transport", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		request := validProviderRequest(time.Now().Add(30 * time.Second).UTC())
		_, callErr := client.Execute(ctx, &runnerv1.ProviderRunnerServiceExecuteRequest{Request: providerRequestToProto(request)})
		if status.Code(callErr) != codes.InvalidArgument {
			t.Fatalf("expected invalid argument, got %v", callErr)
		}
	})

	t.Run("missing credential", func(t *testing.T) {
		unauthenticated := dialRunnerWithoutCredential(t, listener, clientTLS)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, callErr := runnerv1.NewProviderRunnerServiceClient(unauthenticated).Health(ctx, &runnerv1.ProviderRunnerServiceHealthRequest{})
		if status.Code(callErr) != codes.Unauthenticated {
			t.Fatalf("expected unauthenticated, got %v", callErr)
		}
	})

	t.Run("wrong credential", func(t *testing.T) {
		wrong := dialRunner(t, listener, clientTLS, testCredential(9), nil)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, callErr := runnerv1.NewProviderRunnerServiceClient(wrong).Health(ctx, &runnerv1.ProviderRunnerServiceHealthRequest{})
		if status.Code(callErr) != codes.Unauthenticated {
			t.Fatalf("expected unauthenticated, got %v", callErr)
		}
	})

	t.Run("rotation overlap", func(t *testing.T) {
		previous := dialRunner(t, listener, clientTLS, previousToken, nil)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		response, callErr := runnerv1.NewModelRunnerServiceClient(previous).Health(ctx, &runnerv1.ModelRunnerServiceHealthRequest{})
		if callErr != nil || response.GetHealth().GetState() != runnerv1.HealthState_HEALTH_STATE_READY {
			t.Fatalf("previous credential should remain active: response=%v err=%v", response, callErr)
		}
	})

	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		ctx, deadlineCancel := context.WithTimeout(ctx, time.Minute)
		defer deadlineCancel()
		done := make(chan error, 1)
		go func() {
			_, callErr := client.Execute(ctx, &runnerv1.ProviderRunnerServiceExecuteRequest{Request: providerRequestToProto(validProviderRequest(time.Now().Add(time.Minute).UTC()))})
			done <- callErr
		}()
		<-started
		cancel()
		if callErr := <-done; status.Code(callErr) != codes.Canceled {
			t.Fatalf("expected cancellation, got %v", callErr)
		}
	})

	t.Run("trace propagation", func(t *testing.T) {
		ctx, span := tracerProvider.Tracer("runner-test").Start(context.Background(), "root")
		traceID := span.SpanContext().TraceID()
		callContext, cancel := context.WithTimeout(ctx, time.Second)
		_, callErr := modelClient.Health(callContext, &runnerv1.ModelRunnerServiceHealthRequest{})
		cancel()
		span.End()
		if callErr != nil {
			t.Fatal(callErr)
		}
		if err := tracerProvider.ForceFlush(context.Background()); err != nil {
			t.Fatal(err)
		}
		matched := 0
		for _, ended := range recorder.Ended() {
			if ended.SpanContext().TraceID() == traceID {
				matched++
			}
		}
		if matched < 3 {
			t.Fatalf("expected root, client, and server spans on one trace, got %d", matched)
		}
	})
}

func TestRunnerProtocolValidationSizeAndIsolation(t *testing.T) {
	t.Parallel()

	t.Run("dial requires bearer and TLS", func(t *testing.T) {
		if _, err := DialOptions(ClientConfig{}); err == nil {
			t.Fatal("expected missing TLS rejection")
		}
		_, clientTLS := testTLS(t)
		if _, err := DialOptions(ClientConfig{TLS: clientTLS}); err == nil {
			t.Fatal("expected missing bearer rejection")
		}
	})

	t.Run("protovalidate rejects empty execute", func(t *testing.T) {
		token := testCredential(3)
		set, err := NewCredentialSet(token)
		if err != nil {
			t.Fatal(err)
		}
		serverTLS, clientTLS := testTLS(t)
		listener := bufconn.Listen(1024 * 1024)
		server, err := NewServer(ServerConfig{Credentials: set, TLS: serverTLS, MaximumDeadline: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		service, err := NewProviderServer(&stubProvider{})
		if err != nil {
			t.Fatal(err)
		}
		runnerv1.RegisterProviderRunnerServiceServer(server, service)
		go func() { _ = server.Serve(listener) }()
		t.Cleanup(func() { server.Stop(); _ = listener.Close() })
		connection := dialRunner(t, listener, clientTLS, token, nil)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, callErr := runnerv1.NewProviderRunnerServiceClient(connection).Execute(ctx, &runnerv1.ProviderRunnerServiceExecuteRequest{})
		if status.Code(callErr) != codes.InvalidArgument {
			t.Fatalf("expected invalid argument, got %v", callErr)
		}
	})

	t.Run("client rejects oversized result", func(t *testing.T) {
		request := validProviderRequest(time.Now().Add(time.Minute).UTC())
		reason := strings.Repeat("x", 100)
		signals := make([]*runnerv1.Signal, 84)
		for index := range signals {
			reasons := make([]string, 32)
			for reasonIndex := range reasons {
				reasons[reasonIndex] = reason + string(rune('A'+reasonIndex))
			}
			signals[index] = &runnerv1.Signal{Name: "signal", Outcome: runnerv1.SignalOutcome_SIGNAL_OUTCOME_SATISFIED, ReasonCodes: reasons}
		}
		result := &runnerv1.ProviderResult{
			Contract: versionToProto(1, 0), AttemptId: request.AttemptID,
			Outcome: runnerv1.ResultOutcome_RESULT_OUTCOME_COMPLETED, Signals: signals,
			CompletedAt: timestamppb.Now(),
		}
		if proto.Size(result) <= providerv1.MaxResultBytes || proto.Size(result) >= MaxWireBytes {
			t.Fatalf("test result size %d is outside intended boundary", proto.Size(result))
		}
		client, err := NewProviderClient(oversizedProviderClient{result: result})
		if err != nil {
			t.Fatal(err)
		}
		if _, executeErr := client.Execute(context.Background(), request); executeErr == nil || !strings.Contains(executeErr.Error(), "exceeds limit") {
			t.Fatalf("expected size rejection, got %v", executeErr)
		}
	})

	t.Run("wire schema cannot represent evidence or credentials", func(t *testing.T) {
		assertClosedSchema(t, (&runnerv1.ProviderRequest{}).ProtoReflect().Descriptor())
		assertClosedSchema(t, (&runnerv1.ModelRequest{}).ProtoReflect().Descriptor())
		token := testCredential(4)
		message := providerRequestToProto(validProviderRequest(time.Now().Add(time.Minute).UTC()))
		encoded, err := proto.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{token, "raw-evidence-sentinel", "object://", "tenant-key"} {
			if bytes.Contains(encoded, []byte(forbidden)) {
				t.Fatalf("wire message contains forbidden value %q", forbidden)
			}
		}
	})
}

func assertClosedSchema(t *testing.T, descriptor protoreflect.MessageDescriptor) {
	t.Helper()
	visited := map[protoreflect.FullName]bool{}
	var inspect func(protoreflect.MessageDescriptor)
	inspect = func(current protoreflect.MessageDescriptor) {
		if visited[current.FullName()] {
			return
		}
		visited[current.FullName()] = true
		fields := current.Fields()
		for index := 0; index < fields.Len(); index++ {
			field := fields.Get(index)
			name := string(field.Name())
			if field.IsMap() || field.Kind() == protoreflect.BytesKind || name == "credential" || strings.Contains(name, "credential_secret") ||
				strings.Contains(name, "plaintext") || strings.Contains(name, "object_store") || strings.Contains(name, "key_encryption") {
				t.Fatalf("forbidden runner field %s.%s", current.FullName(), name)
			}
			if field.Kind() == protoreflect.MessageKind && field.Message().ParentFile().Package() == "idenqa.runner.v1" {
				inspect(field.Message())
			}
		}
	}
	inspect(descriptor)
}

func dialRunner(
	t *testing.T,
	listener *bufconn.Listener,
	tlsCredentials credentials.TransportCredentials,
	token string,
	tracerProvider *sdktrace.TracerProvider,
) *grpc.ClientConn {
	t.Helper()
	bearer, err := NewBearerCredential(token)
	if err != nil {
		t.Fatal(err)
	}
	configuration := ClientConfig{Credential: bearer, TLS: tlsCredentials, Propagator: propagation.TraceContext{}}
	if tracerProvider != nil {
		configuration.TracerProvider = tracerProvider
	}
	options, err := DialOptions(configuration)
	if err != nil {
		t.Fatal(err)
	}
	options = append(options, grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	connection, err := grpc.NewClient("passthrough:///runner.test", options...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return connection
}

func dialRunnerWithoutCredential(t *testing.T, listener *bufconn.Listener, tlsCredentials credentials.TransportCredentials) *grpc.ClientConn {
	t.Helper()
	connection, err := grpc.NewClient(
		"passthrough:///runner.test",
		grpc.WithTransportCredentials(tlsCredentials),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler(otelgrpc.WithPropagators(propagation.TraceContext{}))),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return connection
}

func testTLS(t *testing.T) (credentials.TransportCredentials, credentials.TransportCredentials) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "runner.test"}, DNSNames: []string{"runner.test"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true,
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(certificateDER)
	if err != nil {
		t.Fatal(err)
	}
	pair := tls.Certificate{Certificate: [][]byte{certificateDER}, PrivateKey: key, Leaf: certificate}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	return credentials.NewTLS(&tls.Config{
			MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair},
		}), credentials.NewTLS(&tls.Config{
			MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: "runner.test",
		})
}

func testCredential(seed byte) string {
	return credentialPrefix + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{seed}, credentialSecretBytes))
}

func validProviderRequest(deadline time.Time) providerv1.Request {
	digest := "sha256:" + strings.Repeat("0", 64)
	capability := providerv1.Capability{
		Check: "document", AcceptedEvidence: []string{"document"}, AcceptedAssurances: []string{"uploaded"},
		AcceptedInputs:    []string{"idenqa.input.country"},
		ProcessingRegions: []string{"eu"}, SupportsIdempotency: true, SupportsCancellation: true,
	}
	restrictions := providerv1.Restrictions{MaximumGrants: 1, MaximumResultSize: providerv1.MaxResultBytes, MaximumDuration: time.Minute}
	return providerv1.Request{
		Contract: providerv1.CurrentVersion, AttemptID: "atm_" + testIDPayload, ProviderID: "pvd_" + testIDPayload,
		TenantID: "ten_" + testIDPayload, VerificationID: "ver_" + testIDPayload, Check: capability.Check,
		IdempotencyKey: "provider-attempt-idempotency", Adapter: providerv1.PackageProvenance{
			AdapterID: "pvd_" + testIDPayload, AdapterVersion: "1.0.0", PackageDigest: digest, Contract: providerv1.CurrentVersion,
		},
		Capability: capability, Restrictions: restrictions,
		Configuration: providerv1.ConfigurationReference{
			ProviderID: "pvd_" + testIDPayload, SchemaDigest: digest, SecretReference: "secret://provider/test", CredentialVersion: "1.0.0",
		},
		Inputs: []providerv1.InputReference{{Name: "idenqa.input.country", Reference: "secret://input/country"}},
		Evidence: []providerv1.EvidenceGrantReference{{
			GrantID: "grt_" + testIDPayload, RedemptionID: "rdm_" + testIDPayload, EvidenceID: "evd_" + testIDPayload,
			Purpose: "document_check", Variant: "front", ExpiresAt: deadline,
		}},
		Deadline: deadline,
	}
}

type stubProvider struct {
	execute func(context.Context, providerv1.Request) (providerv1.Result, error)
}

func (*stubProvider) Manifest(context.Context) (providerv1.Manifest, error) {
	request := validProviderRequest(time.Now().Add(time.Minute).UTC())
	return providerv1.Manifest{
		Package: request.Adapter, Configuration: providerv1.ConfigurationSchema{ID: "provider", Digest: request.Configuration.SchemaDigest},
		Capabilities: []providerv1.Capability{request.Capability}, Restrictions: request.Restrictions,
	}, nil
}
func (*stubProvider) ValidateConfiguration(context.Context, providerv1.ConfigurationReference) error {
	return nil
}
func (adapter *stubProvider) Execute(ctx context.Context, request providerv1.Request) (providerv1.Result, error) {
	if adapter.execute != nil {
		return adapter.execute(ctx, request)
	}
	return providerv1.Result{
		Contract: request.Contract, AttemptID: request.AttemptID, Outcome: providerv1.ResultOutcomeCompleted,
		Signals: []providerv1.Signal{{Name: "document", Outcome: providerv1.SignalOutcomeSatisfied}}, CompletedAt: time.Now().UTC(),
	}, nil
}
func (*stubProvider) Health(context.Context) (providerv1.Health, error) {
	return providerv1.Health{State: providerv1.HealthReady, Code: "ready", CheckedAt: time.Now().UTC()}, nil
}

type stubModel struct{}

func (stubModel) Manifest(context.Context) (modelv1.Manifest, error) {
	return validModelManifest(), nil
}
func (stubModel) ValidateConfiguration(context.Context, modelv1.ConfigurationReference) error {
	return nil
}
func (stubModel) Execute(_ context.Context, request modelv1.Request) (modelv1.Result, error) {
	return modelv1.Result{
		Contract: request.Contract, AttemptID: request.AttemptID, Outcome: modelv1.ResultOutcomeCompleted,
		Signals: []modelv1.Signal{{Name: "face", Outcome: modelv1.SignalOutcomeSatisfied}}, CompletedAt: time.Now().UTC(),
	}, nil
}
func (stubModel) Health(context.Context) (modelv1.Health, error) {
	return modelv1.Health{State: modelv1.HealthReady, Code: "ready", CheckedAt: time.Now().UTC()}, nil
}

func validModelManifest() modelv1.Manifest {
	digest := "sha256:" + strings.Repeat("1", 64)
	return modelv1.Manifest{
		Provenance: modelv1.Provenance{
			ModelID: "mdl_" + testIDPayload, ModelVersion: "1.0.0", ModelDigest: digest, RuntimeDigest: digest,
			PreprocessingDigest: digest, OutputSchemaDigest: digest, Contract: modelv1.CurrentVersion,
		},
		Capabilities: []modelv1.Capability{{Evaluation: "face", AcceptedEvidence: []string{"selfie"}, OutputSignals: []string{"face"}}},
		Restrictions: modelv1.Restrictions{
			MaximumGrants: 1, MaximumInputBytes: 1024, MaximumResultSize: modelv1.MaxResultBytes, MaximumDuration: time.Minute,
		},
	}
}

func validModelRequest(deadline time.Time) modelv1.Request {
	manifest := validModelManifest()
	return modelv1.Request{
		Contract: modelv1.CurrentVersion, AttemptID: "atm_" + testIDPayload, ModelID: "mdl_" + testIDPayload,
		TenantID: "ten_" + testIDPayload, VerificationID: "ver_" + testIDPayload,
		Evaluation: manifest.Capabilities[0].Evaluation, IdempotencyKey: "model-attempt-idempotency",
		Provenance: manifest.Provenance, Capability: manifest.Capabilities[0], Restrictions: manifest.Restrictions,
		Configuration: modelv1.ConfigurationReference{
			ModelID: "mdl_" + testIDPayload, ConfigurationDigest: manifest.Provenance.ModelDigest,
			ConfigurationRef: "configuration://model/test",
		},
		Evidence: []modelv1.EvidenceGrantReference{{
			GrantID: "grt_" + testIDPayload, RedemptionID: "rdm_" + testIDPayload, EvidenceID: "evd_" + testIDPayload,
			Purpose: "face_check", Variant: "selfie", ExpiresAt: deadline,
		}},
		Deadline: deadline,
	}
}

type oversizedProviderClient struct {
	result *runnerv1.ProviderResult
}

func (client oversizedProviderClient) Execute(context.Context, *runnerv1.ProviderRunnerServiceExecuteRequest, ...grpc.CallOption) (*runnerv1.ProviderRunnerServiceExecuteResponse, error) {
	return &runnerv1.ProviderRunnerServiceExecuteResponse{Result: client.result}, nil
}
func (oversizedProviderClient) Manifest(context.Context, *runnerv1.ProviderRunnerServiceManifestRequest, ...grpc.CallOption) (*runnerv1.ProviderRunnerServiceManifestResponse, error) {
	return nil, errors.New("not implemented")
}
func (oversizedProviderClient) ValidateConfiguration(context.Context, *runnerv1.ProviderRunnerServiceValidateConfigurationRequest, ...grpc.CallOption) (*runnerv1.ProviderRunnerServiceValidateConfigurationResponse, error) {
	return nil, errors.New("not implemented")
}
func (oversizedProviderClient) Health(context.Context, *runnerv1.ProviderRunnerServiceHealthRequest, ...grpc.CallOption) (*runnerv1.ProviderRunnerServiceHealthResponse, error) {
	return nil, errors.New("not implemented")
}
