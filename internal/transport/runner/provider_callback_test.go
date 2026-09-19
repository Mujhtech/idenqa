package runner

import (
	"context"
	"strings"
	"testing"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	runnerv1 "github.com/Mujhtech/idenqa/internal/gen/proto/runner/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

type callbackStub struct {
	*stubProvider
	verify func(context.Context, providerv1.Request, providerv1.CallbackEnvelope) (providerv1.Progress, error)
}

func (adapter *callbackStub) VerifyCallback(ctx context.Context, request providerv1.Request, callback providerv1.CallbackEnvelope) (providerv1.Progress, error) {
	if adapter.verify == nil {
		return providerv1.Progress{}, providerv1.Reject(providerv1.CallbackRejectionUnsupported, "callback")
	}
	return adapter.verify(ctx, request, callback)
}

func callbackTestServer(t *testing.T, adapter providerv1.Adapter) (*ProviderClient, *runnerv1.ProviderRunnerServiceClient) {
	t.Helper()
	token := testCredential(7)
	credentials, err := NewCredentialSet(token)
	if err != nil {
		t.Fatal(err)
	}
	serverTLS, clientTLS := testTLS(t)
	service, err := NewProviderServer(adapter)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerConfig{Credentials: credentials, TLS: serverTLS, MaximumDeadline: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	listener := bufconn.Listen(1024 * 1024)
	runnerv1.RegisterProviderRunnerServiceServer(server, service)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	connection := dialRunner(t, listener, clientTLS, token, nil)
	raw := runnerv1.NewProviderRunnerServiceClient(connection)
	client, err := NewProviderClient(raw)
	if err != nil {
		t.Fatal(err)
	}
	return client, &raw
}

func runnerCallbackEnvelope(body string) providerv1.CallbackEnvelope {
	return providerv1.CallbackEnvelope{
		Method:  "POST",
		Headers: []providerv1.CallbackHeader{{Name: "Content-Type", Value: "application/json"}},
		Body:    []byte(body),
	}
}

func TestVerifyProviderCallbackRoundTripAndRejection(t *testing.T) {
	t.Parallel()
	request := validProviderRequest(time.Now().UTC().Add(5 * time.Minute))
	rawVerifier := func(_ context.Context, _ providerv1.Request, callback providerv1.CallbackEnvelope) (providerv1.Progress, error) {
		if strings.Contains(string(callback.Body), "reject") {
			return providerv1.Progress{}, providerv1.Reject(providerv1.CallbackRejectionSignature, "response-signature")
		}
		result := providerv1.Result{Contract: request.Contract, AttemptID: request.AttemptID, Outcome: providerv1.ResultOutcomeCompleted,
			Signals: []providerv1.Signal{{Name: "document", Outcome: providerv1.SignalOutcomeSatisfied}}, CompletedAt: time.Now().UTC()}
		return providerv1.Progress{ProviderJobID: "job-1", ReplayID: "job-1", Result: &result}, nil
	}
	client, _ := callbackTestServer(t, &callbackStub{stubProvider: &stubProvider{}, verify: rawVerifier})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	progress, err := client.VerifyCallback(ctx, request, runnerCallbackEnvelope(`{"status":"clear"}`))
	if err != nil || progress.ReplayID != "job-1" || progress.Result == nil {
		t.Fatalf("progress = %+v err = %v", progress, err)
	}
	if _, err := client.VerifyCallback(ctx, request, runnerCallbackEnvelope(`{"status":"reject"}`)); err == nil {
		t.Fatal("rejection was accepted")
	} else if rejection, ok := providerv1.AsCallbackRejection(err); !ok || rejection.Code != providerv1.CallbackRejectionSignature {
		t.Fatalf("unexpected rejection: %v", err)
	}
}

func TestVerifyProviderCallbackMissingCapabilityIsUnimplemented(t *testing.T) {
	t.Parallel()
	client, raw := callbackTestServer(t, &stubProvider{})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	request := validProviderRequest(time.Now().UTC().Add(5 * time.Minute))
	if _, err := client.VerifyCallback(ctx, request, runnerCallbackEnvelope(`{"status":"clear"}`)); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("unexpected client error: %v", err)
	}
	_, callErr := (*raw).VerifyProviderCallback(ctx, &runnerv1.ProviderRunnerServiceVerifyProviderCallbackRequest{
		Request:  providerRequestToProto(request),
		Callback: providerCallbackToProto(runnerCallbackEnvelope(`{"status":"clear"}`)),
	})
	if status.Code(callErr) != codes.Unimplemented {
		t.Fatalf("expected unimplemented, got %v", callErr)
	}
}

func TestVerifyProviderCallbackKeepsPayloadsBoundedAndPrivate(t *testing.T) {
	t.Parallel()
	sentinel := "raw-provider-callback-sentinel"
	client, _ := callbackTestServer(t, &callbackStub{stubProvider: &stubProvider{}, verify: func(context.Context, providerv1.Request, providerv1.CallbackEnvelope) (providerv1.Progress, error) {
		return providerv1.Progress{}, providerv1.Reject(providerv1.CallbackRejectionMalformed, "body")
	}})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	request := validProviderRequest(time.Now().UTC().Add(5 * time.Minute))
	if _, err := client.VerifyCallback(ctx, request, providerv1.CallbackEnvelope{Method: "POST"}); !providerv1IsMalformed(err) {
		t.Fatalf("empty callback was not rejected locally: %v", err)
	}
	oversized := runnerCallbackEnvelope(strings.Repeat("x", providerv1.MaxCallbackBodyBytes+1))
	if _, err := client.VerifyCallback(ctx, request, oversized); !providerv1IsMalformed(err) {
		t.Fatalf("oversized callback was not rejected locally: %v", err)
	}
	tooMany := runnerCallbackEnvelope(`{"status":"clear"}`)
	tooMany.Headers = make([]providerv1.CallbackHeader, providerv1.MaxCallbackHeaders+1)
	for index := range tooMany.Headers {
		tooMany.Headers[index] = providerv1.CallbackHeader{Name: "X-Header", Value: "value"}
	}
	if _, err := client.VerifyCallback(ctx, request, tooMany); !providerv1IsMalformed(err) {
		t.Fatalf("excessive headers were not rejected locally: %v", err)
	}
	_, err := client.VerifyCallback(ctx, request, runnerCallbackEnvelope(sentinel))
	rejection, ok := providerv1.AsCallbackRejection(err)
	if !ok || strings.Contains(rejection.Error(), sentinel) {
		t.Fatalf("rejection leaked the raw callback body: %v", err)
	}
	// No runner response schema field can carry raw callback material.
	wire := &runnerv1.ProviderRunnerServiceVerifyProviderCallbackResponse{ProviderJobId: "job-1", ReplayId: "job-1", RejectionCode: "invalid.malformed"}
	encoded, marshalErr := proto.Marshal(wire)
	if marshalErr != nil || strings.Contains(string(encoded), sentinel) {
		t.Fatal("runner response carried raw callback material")
	}
}

func providerv1IsMalformed(err error) bool {
	rejection, ok := providerv1.AsCallbackRejection(err)
	return ok && rejection.Code == providerv1.CallbackRejectionMalformed
}
