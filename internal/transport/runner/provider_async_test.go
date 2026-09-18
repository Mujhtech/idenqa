package runner

import (
	"context"
	"testing"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	runnerv1 "github.com/Mujhtech/idenqa/internal/gen/proto/runner/v1"
	"google.golang.org/grpc/test/bufconn"
)

type asyncStub struct{ stubProvider }

func (*asyncStub) Advance(ctx context.Context, request providerv1.Request, resume bool) (providerv1.Progress, error) {
	deadline, ok := ctx.Deadline()
	if !ok || deadline.After(time.Now().Add(31*time.Second)) {
		return providerv1.Progress{}, context.DeadlineExceeded
	}
	progress := providerv1.Progress{ProviderJobID: "job-123"}
	if resume {
		progress.Result = &providerv1.Result{Contract: request.Contract, AttemptID: request.AttemptID, Outcome: providerv1.ResultOutcomeCompleted, Signals: []providerv1.Signal{{Name: "document", Outcome: providerv1.SignalOutcomeSatisfied}}, CompletedAt: time.Now().UTC()}
	}
	return progress, nil
}
func TestAdvanceTLSRoundTripUsesShortRPCAndPreservesPending(t *testing.T) {
	t.Parallel()
	token := testCredential(5)
	credentials, err := NewCredentialSet(token)
	if err != nil {
		t.Fatal(err)
	}
	serverTLS, clientTLS := testTLS(t)
	service, err := NewProviderServer(&asyncStub{})
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
	client, err := NewProviderClient(runnerv1.NewProviderRunnerServiceClient(connection))
	if err != nil {
		t.Fatal(err)
	}
	request := validProviderRequest(time.Now().UTC().Add(5 * time.Minute))
	request.Restrictions.MaximumDuration = 10 * time.Minute
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	pending, err := client.Advance(ctx, request, false)
	if err != nil || pending.Result != nil || pending.ProviderJobID != "job-123" {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	complete, err := client.Advance(ctx, request, true)
	if err != nil || complete.Result == nil {
		t.Fatalf("complete=%+v err=%v", complete, err)
	}
}
