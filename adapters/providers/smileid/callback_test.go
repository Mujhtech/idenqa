package smileid_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/adapters/providers/smileid"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
)

const callbackReference = "pcb_01K4AR9V8FQ2G7ZXCPNM5T6JWH"

func callbackEnvelope(t *testing.T, request providerv1.Request, status string) providerv1.CallbackEnvelope {
	t.Helper()
	timestamp := fixedNow.Format(time.RFC3339Nano)
	payload := map[string]any{
		"status": status, "message": "Verification result", "product": "document_verification",
		"completed_at": timestamp, "partner_params": map[string]any{"job_type": 6},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	headers := []providerv1.CallbackHeader{
		{Name: "Content-Type", Value: "application/json"},
		{Name: "Response-Timestamp", Value: timestamp},
		{Name: "Response-Signature", Value: signature("test-key", timestamp, "085")},
		{Name: "Job-ID", Value: "job-123"},
		{Name: "User-ID", Value: request.VerificationID},
	}
	return providerv1.CallbackEnvelope{Method: http.MethodPost, Headers: headers, Body: body}
}

func TestCallbackVerificationBindsSignatureIdentityAndReplayReference(t *testing.T) {
	t.Parallel()
	adapter, err := smileid.New(secrets{}, inputs{}, evidence{}, &client{}, waiter{}, func() time.Time { return fixedNow })
	if err != nil {
		t.Fatal(err)
	}
	request, _ := fixture(t, adapter)
	request.CallbackReference = callbackReference
	progress, err := adapter.VerifyCallback(t.Context(), request, callbackEnvelope(t, request, "clear"))
	if err != nil {
		t.Fatal(err)
	}
	if progress.ProviderJobID != "job-123" || progress.ReplayID != "job-123" || progress.Result == nil || progress.Result.Outcome != providerv1.ResultOutcomeCompleted {
		t.Fatalf("progress = %+v", progress)
	}
	for _, signal := range progress.Result.Signals {
		if signal.Name == "idenqa.signal.liveness" && signal.Outcome != providerv1.SignalOutcomeInconclusive {
			t.Fatal("callback implied liveness assurance")
		}
	}
	if progress.Result.Signals[0].Name != "idenqa.signal.provider_job" || progress.Result.Signals[0].Outcome != providerv1.SignalOutcomeSatisfied {
		t.Fatalf("overall status not normalised: %+v", progress.Result.Signals)
	}
}

func TestCallbackVerificationFailsClosed(t *testing.T) {
	t.Parallel()
	adapter, err := smileid.New(secrets{}, inputs{}, evidence{}, &client{}, waiter{}, func() time.Time { return fixedNow })
	if err != nil {
		t.Fatal(err)
	}
	base, _ := fixture(t, adapter)
	base.CallbackReference = callbackReference
	tests := []struct {
		name   string
		mutate func(*providerv1.CallbackEnvelope)
		code   providerv1.CallbackRejectionCode
	}{
		{name: "invalid signature", mutate: func(envelope *providerv1.CallbackEnvelope) {
			envelope.Headers[2].Value = "invalid"
		}, code: providerv1.CallbackRejectionSignature},
		{name: "foreign key signature", mutate: func(envelope *providerv1.CallbackEnvelope) {
			envelope.Headers[2].Value = signature("other-key", envelope.Headers[1].Value, "085")
		}, code: providerv1.CallbackRejectionSignature},
		{name: "stale timestamp", mutate: func(envelope *providerv1.CallbackEnvelope) {
			timestamp := fixedNow.Add(-time.Hour).Format(time.RFC3339Nano)
			envelope.Headers[1].Value = timestamp
			envelope.Headers[2].Value = signature("test-key", timestamp, "085")
		}, code: providerv1.CallbackRejectionStale},
		{name: "wrong subject", mutate: func(envelope *providerv1.CallbackEnvelope) {
			envelope.Headers[4].Value = "ver_01K4AR9V8FQ2G7ZXCPNM5T6JWW"
		}, code: providerv1.CallbackRejectionIdentity},
		{name: "missing job header", mutate: func(envelope *providerv1.CallbackEnvelope) {
			envelope.Headers[3].Value = "bad job"
		}, code: providerv1.CallbackRejectionIdentity},
		{name: "malformed body", mutate: func(envelope *providerv1.CallbackEnvelope) {
			envelope.Body = []byte("{")
		}, code: providerv1.CallbackRejectionMalformed},
		{name: "unsupported status", mutate: func(envelope *providerv1.CallbackEnvelope) {
			payload, _ := json.Marshal(map[string]any{"status": "pending", "partner_params": map[string]any{"job_type": 6}})
			envelope.Body = payload
		}, code: providerv1.CallbackRejectionUnsupported},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			envelope := callbackEnvelope(t, base, "clear")
			test.mutate(&envelope)
			_, err := adapter.VerifyCallback(t.Context(), base, envelope)
			rejection, ok := providerv1.AsCallbackRejection(err)
			if !ok || rejection.Code != test.code {
				t.Fatalf("error = %v, want code %s", err, test.code)
			}
		})
	}
}

func TestCallbackTargetIsBoundToTheAttemptReference(t *testing.T) {
	t.Parallel()
	request, _ := fixture(t, newAdapterFixture(t))
	request.CallbackReference = callbackReference
	request.Evidence = request.Evidence[:2]
	transport := &capturingClient{responses: []response{{status: 200, body: `{"code":"2202","smile_job_id":"job-123","upload_url":"https://uploads.example/videos/085/085-job-123-random/attachments.zip"}`}, {status: 200, body: ""}}}
	adapter, err := smileid.New(secrets{}, inputs{}, &countedEvidence{}, transport, waiter{}, func() time.Time { return fixedNow })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Advance(t.Context(), request, false); err != nil {
		t.Fatal(err)
	}
	if len(transport.bodies) == 0 {
		t.Fatal("no submission was sent")
	}
	if !strings.Contains(transport.bodies[0], "https://core.example/callback/v1/provider-callbacks/"+callbackReference) {
		t.Fatalf("callback reference not bound in prep: %s", transport.bodies[0])
	}
	if strings.Count(transport.bodies[0], callbackReference) != 1 {
		t.Fatal("callback reference duplicated in prep")
	}
}

// capturingClient retains request bodies before the adapter wipes them.
type capturingClient struct {
	bodies    []string
	responses []response
}

func (value *capturingClient) Do(request *http.Request) (*http.Response, error) {
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	_ = request.Body.Close()
	value.bodies = append(value.bodies, string(raw))
	if len(value.responses) == 0 {
		return nil, io.ErrUnexpectedEOF
	}
	next := value.responses[0]
	value.responses = value.responses[1:]
	return &http.Response{StatusCode: next.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(next.body))}, nil
}

// newAdapterFixture keeps the fixture helper usable with a plain adapter.
func newAdapterFixture(t *testing.T) *smileid.Adapter {
	t.Helper()
	adapter, err := smileid.New(secrets{}, inputs{}, evidence{}, &client{}, waiter{}, func() time.Time { return fixedNow })
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}
