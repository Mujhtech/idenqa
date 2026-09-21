package runner

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	runnerv1 "github.com/Mujhtech/idenqa/internal/gen/proto/runner/v1"
	"google.golang.org/grpc/test/bufconn"
)

func TestProviderRunnerCarriesBoundedDocumentObservation(t *testing.T) {
	t.Parallel()
	token := testCredential(6)
	credentials, err := NewCredentialSet(token)
	if err != nil {
		t.Fatal(err)
	}
	serverTLS, clientTLS := testTLS(t)
	observation := providerv1.DocumentObservation{
		MRZLines:       []string{"P<UTODOE<<JOHN" + strings.Repeat("<", 30)},
		BarcodePayload: "@\n\x1e\rANSI 636000080002PP00410272\nDCSDOE\n",
		Fields:         []providerv1.DocumentField{{Name: "document_number", Value: "X10000001"}},
	}
	adapter := &stubProvider{execute: func(_ context.Context, request providerv1.Request) (providerv1.Result, error) {
		return providerv1.Result{
			Contract: request.Contract, AttemptID: request.AttemptID, Outcome: providerv1.ResultOutcomeCompleted,
			Signals:  []providerv1.Signal{{Name: "idenqa.signal.document_quality", Outcome: providerv1.SignalOutcomeSatisfied}},
			Document: &observation, CompletedAt: time.Now().UTC(),
		}, nil
	}}
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
	client, err := NewProviderClient(runnerv1.NewProviderRunnerServiceClient(connection))
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Execute(t.Context(), validProviderRequest(time.Now().UTC().Add(time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	if result.Document == nil || !reflect.DeepEqual(*result.Document, observation) {
		t.Fatalf("document observation = %+v", result.Document)
	}
}

func TestProviderDocumentObservationWireBounds(t *testing.T) {
	t.Parallel()
	validator, err := protovalidate.New()
	if err != nil {
		t.Fatal(err)
	}
	valid := &runnerv1.ProviderDocumentObservation{
		MrzLines:       []string{strings.Repeat("A", 44)},
		BarcodePayload: "@\n\x1e\rANSI 636000080002PP00410272\nDCSDOE\n",
		Fields:         []*runnerv1.ProviderDocumentField{{Name: "document_number", Value: "X10000001"}},
	}
	if err := validator.Validate(valid); err != nil {
		t.Fatalf("valid observation rejected: %v", err)
	}
	tests := []struct {
		name        string
		observation *runnerv1.ProviderDocumentObservation
	}{
		{"too many mrz lines", &runnerv1.ProviderDocumentObservation{MrzLines: []string{"A", "B", "C", "D"}}},
		{"oversized mrz line", &runnerv1.ProviderDocumentObservation{MrzLines: []string{strings.Repeat("A", 45)}}},
		{"oversized barcode payload", &runnerv1.ProviderDocumentObservation{BarcodePayload: strings.Repeat("a", 4097)}},
		{"too many fields", &runnerv1.ProviderDocumentObservation{Fields: make([]*runnerv1.ProviderDocumentField, 33)}},
		{"empty field value", &runnerv1.ProviderDocumentObservation{Fields: []*runnerv1.ProviderDocumentField{{Name: "document_number", Value: ""}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := validator.Validate(test.observation); err == nil {
				t.Fatal("out-of-bounds observation accepted on the wire")
			}
			result := &runnerv1.ProviderResult{Document: test.observation}
			if err := validator.Validate(result); err == nil {
				t.Fatal("out-of-bounds observation accepted on a result")
			}
		})
	}
}
