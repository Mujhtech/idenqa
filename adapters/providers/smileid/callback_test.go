package smileid_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/adapters/providers/smileid"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
)

const callbackReference = "pcb_01K4AR9V8FQ2G7ZXCPNM5T6JWH"

func callbackEnvelope(t *testing.T, request providerv1.Request, status string) providerv1.CallbackEnvelope {
	t.Helper()
	return callbackDocumentEnvelope(t, request, status, "document_verification", nil)
}

// callbackDocumentEnvelope builds the reviewed v3 verification-webhook shape,
// including the documented id_fields object when supplied.
func callbackDocumentEnvelope(t *testing.T, request providerv1.Request, status, product string, fields map[string]any) providerv1.CallbackEnvelope {
	t.Helper()
	timestamp := fixedNow.Format(time.RFC3339Nano)
	payload := map[string]any{
		"status": status, "message": "Verification result", "reason": nil, "product": product,
		"created_at": timestamp, "completed_at": timestamp, "partner_params": map[string]any{"job_type": 6},
	}
	if fields != nil {
		payload["id_fields"] = fields
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	headers := []providerv1.CallbackHeader{
		{Name: "Content-Type", Value: "application/json"},
		{Name: "Response-Timestamp", Value: timestamp},
		{Name: "Response-Signature", Value: signature("test-key", timestamp)},
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
			envelope.Headers[2].Value = signature("other-key", envelope.Headers[1].Value)
		}, code: providerv1.CallbackRejectionSignature},
		{name: "stale timestamp", mutate: func(envelope *providerv1.CallbackEnvelope) {
			timestamp := fixedNow.Add(-time.Hour).Format(time.RFC3339Nano)
			envelope.Headers[1].Value = timestamp
			envelope.Headers[2].Value = signature("test-key", timestamp)
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

// TestCallbackDocumentExtractionMapsOnlyDocumentedIDFields proves the v3
// webhook id_fields mapping is attached only to the documented clear Document
// Verification result and forwards only the bounded canonical allow-list.
func TestCallbackDocumentExtractionMapsOnlyDocumentedIDFields(t *testing.T) {
	t.Parallel()
	documented := map[string]any{
		"first_name": "Amina", "other_names": "Fatou", "last_name": "Clearwater",
		"full_name": "Amina Fatou Clearwater", "id_number": "00000000000",
		"country": "NG", "id_type": "PASSPORT", "date_of_birth": "1992-08-22",
		"gender": "Female", "issuance_date": "2021-01-15", "expiration_date": "2031-01-15",
		"nationality": nil, "is_deceased": nil, "secondary_id_number": "SECONDARY",
		"address": map[string]any{"address_line_1": "123 Main Street", "country": "NG"},
	}
	tests := []struct {
		name    string
		status  string
		product string
		fields  map[string]any
		want    *providerv1.DocumentObservation
	}{
		{
			name: "clear document verification maps documented fields", status: "clear", product: "document_verification", fields: documented,
			want: &providerv1.DocumentObservation{Fields: []providerv1.DocumentField{
				{Name: "document_number", Value: "00000000000"},
				{Name: "last_name", Value: "Clearwater"},
				{Name: "first_name", Value: "Amina"},
				{Name: "other_names", Value: "Fatou"},
				{Name: "date_of_birth", Value: "1992-08-22"},
				{Name: "date_of_expiry", Value: "2031-01-15"},
				{Name: "sex", Value: "Female"},
				{Name: "issuing_state", Value: "NG"},
				{Name: "document_type", Value: "PASSPORT"},
			}},
		},
		{name: "attention never carries extraction", status: "attention", product: "document_verification", fields: documented, want: nil},
		{name: "block never carries extraction", status: "block", product: "document_verification", fields: documented, want: nil},
		{name: "error never carries extraction", status: "error", product: "document_verification", fields: documented, want: nil},
		{name: "smartselfie product never carries extraction", status: "clear", product: "smartselfie_authentication", fields: documented, want: nil},
		{name: "missing fields never carry extraction", status: "clear", product: "document_verification", fields: nil, want: nil},
		{
			name: "unknown keys are never forwarded", status: "clear", product: "document_verification",
			fields: map[string]any{
				"full_name": "Amina Fatou Clearwater", "issuance_date": "2021-01-15",
				"place_of_birth": "Lagos", "address_unparsed": "123 Main Street", "is_deceased": true,
			},
			want: nil,
		},
		{
			name: "wrong types and malformed values are dropped", status: "clear", product: "document_verification",
			fields: map[string]any{
				"id_number": float64(12345678), "last_name": float64(1), "first_name": strings.Repeat("X", providerv1.MaximumDocumentFieldBytes+1),
				"date_of_birth": "not-a-date", "expiration_date": "31/01/2030", "gender": []string{"F"}, "country": true, "id_type": nil,
			},
			want: nil,
		},
		{
			name: "partial extraction keeps only bounded valid fields", status: "clear", product: "document_verification",
			fields: map[string]any{
				"id_number": "X10000001", "last_name": strings.Repeat("X", providerv1.MaximumDocumentFieldBytes+1),
				"country": "NG", "date_of_birth": "1985-12-31",
			},
			want: &providerv1.DocumentObservation{Fields: []providerv1.DocumentField{
				{Name: "document_number", Value: "X10000001"},
				{Name: "date_of_birth", Value: "1985-12-31"},
				{Name: "issuing_state", Value: "NG"},
			}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			adapter, err := smileid.New(secrets{}, inputs{}, evidence{}, &client{}, waiter{}, func() time.Time { return fixedNow })
			if err != nil {
				t.Fatal(err)
			}
			request, _ := fixture(t, adapter)
			request.CallbackReference = callbackReference
			progress, err := adapter.VerifyCallback(t.Context(), request, callbackDocumentEnvelope(t, request, test.status, test.product, test.fields))
			if err != nil || progress.Result == nil {
				t.Fatalf("progress = %+v, error = %v", progress, err)
			}
			if !reflect.DeepEqual(progress.Result.Document, test.want) {
				t.Fatalf("document = %+v, want = %+v", progress.Result.Document, test.want)
			}
			if err := progress.Result.Validate(); err != nil {
				t.Fatalf("result failed contract validation: %v", err)
			}
		})
	}
}

// TestCallbackDocumentExtractionNeverLeaksRawValuesIntoErrorsOrLogs proves the
// mapped and malformed callback paths never place raw id_fields values, or any
// fragment of them, into returned errors, failure state, or logs.
func TestCallbackDocumentExtractionNeverLeaksRawValuesIntoErrorsOrLogs(t *testing.T) {
	const sentinel = "SENTINELRAWCALLBACKVALUE"
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)

	fields := map[string]any{"id_number": sentinel, "last_name": sentinel, "date_of_birth": "1992-08-22"}
	tests := []struct {
		name     string
		envelope func(t *testing.T, request providerv1.Request) providerv1.CallbackEnvelope
	}{
		{name: "mapped extraction", envelope: func(t *testing.T, request providerv1.Request) providerv1.CallbackEnvelope {
			return callbackDocumentEnvelope(t, request, "clear", "document_verification", fields)
		}},
		{name: "blocked extraction", envelope: func(t *testing.T, request providerv1.Request) providerv1.CallbackEnvelope {
			return callbackDocumentEnvelope(t, request, "block", "document_verification", fields)
		}},
		{name: "malformed body", envelope: func(t *testing.T, request providerv1.Request) providerv1.CallbackEnvelope {
			envelope := callbackDocumentEnvelope(t, request, "clear", "document_verification", nil)
			envelope.Body = []byte(`{"status":"clear","id_fields":{"id_number":"` + sentinel + `"`)
			return envelope
		}},
		{name: "wrong field type", envelope: func(t *testing.T, request providerv1.Request) providerv1.CallbackEnvelope {
			envelope := callbackDocumentEnvelope(t, request, "clear", "document_verification", nil)
			envelope.Body = []byte(`{"status":"clear","product":"document_verification","id_fields":"` + sentinel + `"}`)
			return envelope
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter, err := smileid.New(secrets{}, inputs{}, evidence{}, &client{}, waiter{}, func() time.Time { return fixedNow })
			if err != nil {
				t.Fatal(err)
			}
			request, _ := fixture(t, adapter)
			request.CallbackReference = callbackReference
			progress, verifyErr := adapter.VerifyCallback(t.Context(), request, test.envelope(t, request))
			if verifyErr != nil {
				rejection, ok := providerv1.AsCallbackRejection(verifyErr)
				if !ok {
					t.Fatalf("unexpected callback error: %v", verifyErr)
				}
				if strings.Contains(verifyErr.Error(), sentinel) || strings.Contains(rejection.Field, sentinel) {
					t.Fatalf("raw provider value escaped into rejection: %v", verifyErr)
				}
				return
			}
			if progress.Result == nil {
				t.Fatal("terminal result missing")
			}
			// The accepted result carries raw values only in the transient
			// observation that Core consumes; failure state and logs must not.
			stripped := *progress.Result
			stripped.Document = nil
			encoded, err := json.Marshal(stripped)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(encoded, []byte(sentinel)) {
				t.Fatalf("raw provider value escaped into terminal state: %s", encoded)
			}
		})
	}
	if strings.Contains(logs.String(), sentinel) {
		t.Fatalf("raw provider value reached logs: %s", logs.String())
	}
}
