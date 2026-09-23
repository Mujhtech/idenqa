package dojah_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/adapters/providers/dojah"
	providerconformance "github.com/Mujhtech/idenqa/conformance/provider"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/verification"
)

func TestCheckedInManifestMatchesRuntimeContract(t *testing.T) {
	t.Parallel()
	encoded, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var recorded providerv1.Manifest
	if err := json.Unmarshal(encoded, &recorded); err != nil {
		t.Fatal(err)
	}
	if err := recorded.Validate(); err != nil {
		t.Fatal(err)
	}
	adapter, err := dojah.New(secrets{}, inputs{}, evidence{}, &client{}, func() time.Time { return fixedNow })
	if err != nil {
		t.Fatal(err)
	}
	actual, err := adapter.Manifest(t.Context())
	if err != nil || !reflect.DeepEqual(actual, recorded) {
		t.Fatalf("runtime manifest = %#v, recorded = %#v, error = %v", actual, recorded, err)
	}
}

var fixedNow = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

type secrets struct{}

func (secrets) ResolveDojah(context.Context, string, string) (dojah.Config, error) {
	return dojah.Config{BaseURL: "https://api.dojah.io", AppID: "app", APIKey: "key", Mode: "sandbox", Region: "africa"}, nil
}

type inputs struct{}

func (inputs) ResolveProviderInput(_ context.Context, reference string) (string, error) {
	values := map[string]string{
		"secret://input/country":   "NG",
		"secret://input/id-type":   "NIN",
		"secret://input/id-number": "00000000000",
	}
	return values[reference], nil
}

type evidence struct{}

func (evidence) ReadProviderEvidence(context.Context, providerv1.EvidenceGrantReference, int64) ([]byte, error) {
	return []byte("synthetic-jpeg"), nil
}

type client struct {
	status      int
	body        string
	retry       string
	request     *http.Request
	requestBody []byte
}

func (value *client) Do(request *http.Request) (*http.Response, error) {
	value.request = request
	if request.Body != nil {
		var err error
		value.requestBody, err = io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
	}
	header := make(http.Header)
	header.Set("Retry-After", value.retry)
	return &http.Response{StatusCode: value.status, Header: header, Body: io.NopCloser(strings.NewReader(value.body))}, nil
}

func TestConformanceAndProviderNeutralNormalisation(t *testing.T) {
	t.Parallel()
	transport := &client{status: http.StatusOK, body: `{"entity":{"match":true}}`}
	adapter, err := dojah.New(secrets{}, inputs{}, evidence{}, transport, func() time.Time { return fixedNow })
	if err != nil {
		t.Fatal(err)
	}
	request, configuration := fixture(t, adapter, "idenqa.check.face_match_1to1")
	if err := providerconformance.Check(t.Context(), adapter, providerconformance.Fixture{Configuration: configuration, Request: request}); err != nil {
		t.Fatal(err)
	}
	if transport.request.Header.Get("AppId") != "app" || transport.request.Header.Get("Authorization") != "key" {
		t.Fatal("tenant-owned authentication headers were not applied")
	}
}

func TestStableFailureClasses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		status      int
		retry       string
		class       providerv1.FailureClass
		disposition providerv1.RetryDisposition
	}{
		{"invalid", 400, "", providerv1.FailureInvalidRequest, providerv1.RetryNever},
		{"authentication", 401, "", providerv1.FailureUnauthenticated, providerv1.RetryNever},
		{"throttling", 429, "7", providerv1.FailureRateLimited, providerv1.RetryBackoff},
		{"transient", 503, "", providerv1.FailureUnavailable, providerv1.RetryBackoff},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			transport := &client{status: test.status, body: `{}`, retry: test.retry}
			adapter, _ := dojah.New(secrets{}, inputs{}, evidence{}, transport, func() time.Time { return fixedNow })
			request, _ := fixture(t, adapter, "idenqa.check.passive_liveness")
			result, err := adapter.Execute(t.Context(), request)
			if err != nil || result.Failure == nil || result.Failure.Class != test.class || result.Failure.Retry != test.disposition {
				t.Fatalf("result = %+v, error = %v", result, err)
			}
			if test.status == 429 && result.Failure.RetryAfter != 7*time.Second {
				t.Fatalf("retry after = %s", result.Failure.RetryAfter)
			}
		})
	}
}

func TestNoRecordIsInconclusiveNotTransportFailure(t *testing.T) {
	t.Parallel()
	transport := &client{status: http.StatusOK, body: `{"message":"no record","entity":null}`}
	adapter, _ := dojah.New(secrets{}, inputs{}, evidence{}, transport, func() time.Time { return fixedNow })
	request, _ := fixture(t, adapter, "idenqa.check.authority_lookup")
	request.Inputs = []providerv1.InputReference{
		{Name: "idenqa.input.country", Reference: "secret://input/country"},
		{Name: "idenqa.input.id_type", Reference: "secret://input/id-type"},
		{Name: "idenqa.input.id_number", Reference: "secret://input/id-number"},
	}
	if len(request.Evidence) != 0 {
		t.Fatalf("authority lookup received %d dummy evidence grants", len(request.Evidence))
	}
	result, err := adapter.Execute(t.Context(), request)
	if err != nil || result.Outcome != providerv1.ResultOutcomeCompleted || result.Signals[0].Outcome != providerv1.SignalOutcomeInconclusive {
		t.Fatalf("result = %+v, error = %v", result, err)
	}
}

func TestAuthorityLookupRejectsUndeclaredInputBeforeResolution(t *testing.T) {
	t.Parallel()
	transport := &client{status: http.StatusOK, body: `{"entity":{}}`}
	adapter, _ := dojah.New(secrets{}, inputs{}, evidence{}, transport, func() time.Time { return fixedNow })
	request, _ := fixture(t, adapter, "idenqa.check.authority_lookup")
	request.Inputs = []providerv1.InputReference{{Name: "idenqa.input.undeclared", Reference: "secret://input/country"}}
	result, err := adapter.Execute(t.Context(), request)
	if err != nil || result.Failure == nil || result.Failure.Code != "invalid_request" || transport.request != nil {
		t.Fatalf("result = %+v, request = %v, error = %v", result, transport.request, err)
	}
}

func TestProviderV10CannotCarryStructuredInputs(t *testing.T) {
	t.Parallel()
	transport := &client{status: http.StatusOK, body: `{"entity":{}}`}
	adapter, _ := dojah.New(secrets{}, inputs{}, evidence{}, transport, func() time.Time { return fixedNow })
	request, _ := fixture(t, adapter, "idenqa.check.authority_lookup")
	request.Contract = providerv1.Version{Major: 1}
	request.Adapter.Contract = request.Contract
	request.Inputs = []providerv1.InputReference{{Name: "idenqa.input.country", Reference: "secret://input/country"}}
	result, err := adapter.Execute(t.Context(), request)
	if err != nil || result.Failure == nil || result.Failure.Code != "invalid_request" || transport.request != nil {
		t.Fatalf("result = %+v, request = %v, error = %v", result, transport.request, err)
	}
}

func fixture(t *testing.T, adapter *dojah.Adapter, check string) (providerv1.Request, providerv1.ConfigurationReference) {
	t.Helper()
	manifest, err := adapter.Manifest(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var capability providerv1.Capability
	for _, value := range manifest.Capabilities {
		if value.Check == check {
			capability = value
		}
	}
	configuration := providerv1.ConfigurationReference{
		ProviderID: "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWH", SchemaDigest: manifest.Configuration.Digest,
		SecretReference: "secret://provider/dojah", CredentialVersion: "2026-09-04",
	}
	variants := []string{"selfie", "document.front"}
	evidenceReferences := make([]providerv1.EvidenceGrantReference, len(capability.AcceptedEvidence))
	for index := range evidenceReferences {
		evidenceReferences[index] = providerv1.EvidenceGrantReference{
			GrantID: id("grt", byte(index+1)), RedemptionID: id("rdm", byte(index+3)), EvidenceID: id("evd", byte(index+5)),
			Purpose: "idenqa.purpose.identity_verification", Variant: variants[index], ExpiresAt: fixedNow.Add(time.Hour),
		}
	}
	return providerv1.Request{
		Contract: providerv1.CurrentVersion, AttemptID: "atm_01K4AR9V8FQ2G7ZXCPNM5T6JWH", ProviderID: configuration.ProviderID,
		TenantID: "ten_01K4AR9V8FQ2G7ZXCPNM5T6JWH", VerificationID: "ver_01K4AR9V8FQ2G7ZXCPNM5T6JWH",
		Check: check, IdempotencyKey: "idenqa-fixture-key", Adapter: manifest.Package, Capability: capability,
		Restrictions: manifest.Restrictions, Configuration: configuration, Evidence: evidenceReferences, Deadline: fixedNow.Add(time.Minute),
	}, configuration
}

func id(prefix string, seed byte) string {
	alphabet := "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	return prefix + "_" + strings.Repeat(string(alphabet[int(seed)%len(alphabet)]), 26)
}

func TestDocumentAnalysisUsesExplicitStatusAndDocumentedBody(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, body string
		want       providerv1.SignalOutcome
	}{
		{"valid", `{"entity":{"status":{"overall_status":1}}}`, providerv1.SignalOutcomeSatisfied},
		{"invalid with extracted entity", `{"entity":{"status":{"overall_status":0},"valid":true}}`, providerv1.SignalOutcomeNotSatisfied},
		{"missing status", `{"entity":{"text_data":[{"value":"private"}]}}`, providerv1.SignalOutcomeInconclusive},
		{"unknown status", `{"entity":{"status":{"overall_status":2}}}`, providerv1.SignalOutcomeInconclusive},
		{"wrong type", `{"entity":{"status":{"overall_status":"1"}}}`, providerv1.SignalOutcomeInconclusive},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			transport := &client{status: 200, body: test.body}
			adapter, err := dojah.New(secrets{}, inputs{}, evidence{}, transport, func() time.Time { return fixedNow })
			if err != nil {
				t.Fatal(err)
			}
			request, _ := fixture(t, adapter, "idenqa.check.document_analysis")
			request.Evidence[0].Variant = "document.front"
			result, err := adapter.Execute(t.Context(), request)
			if err != nil || len(result.Signals) != 1 || result.Signals[0].Outcome != test.want {
				t.Fatalf("document outcome: %#v %v", result, err)
			}
			var body map[string]string
			if err := json.Unmarshal(transport.requestBody, &body); err != nil {
				t.Fatal(err)
			}
			if len(body) != 2 || body["input_type"] != "base64" || body["imagefrontside"] == "" {
				t.Fatalf("document body keys differ from reviewed contract")
			}
		})
	}
}

// TestDocumentAnalysisMapsOnlyDocumentedExtraction proves the adapter forwards
// extraction only from the documented POST /api/v1/document/analysis response
// keys and only when status.overall_status marks the analysis valid. Unknown
// keys, unreadable entries, wrong types, malformed or oversized values, and the
// undocumented MRZ and barcode shapes never produce an observation.
func TestDocumentAnalysisMapsOnlyDocumentedExtraction(t *testing.T) {
	t.Parallel()
	documented := `{"field_name":"Document Number","field_key":"document_number","status":1,"value":"12345678"},` +
		`{"field_name":"Sex","field_key":"sex","status":1,"value":"M"},` +
		`{"field_name":"Date of Birth","field_key":"dob","status":1,"value":"1990-08-01"},` +
		`{"field_name":"Date of Expiry","field_key":"expiry_date","status":1,"value":"2031-01-15"}`
	documentType := `{"document_name":"United States - Permanent Resident Card (2010)","document_country_name":"United States","document_country_code":"USA"}`
	tests := []struct {
		name        string
		body        string
		check       string
		wantOutcome providerv1.SignalOutcome
		want        *providerv1.DocumentObservation
	}{
		{
			name:        "documented valid extraction",
			body:        `{"entity":{"status":{"overall_status":1},"document_type":` + documentType + `,"text_data":[` + documented + `]}}`,
			check:       "idenqa.check.document_analysis",
			wantOutcome: providerv1.SignalOutcomeSatisfied,
			want: &providerv1.DocumentObservation{Fields: []providerv1.DocumentField{
				{Name: "issuing_state", Value: "USA"},
				{Name: "document_number", Value: "12345678"},
				{Name: "sex", Value: "M"},
				{Name: "date_of_birth", Value: "1990-08-01"},
				{Name: "date_of_expiry", Value: "2031-01-15"},
			}},
		},
		{
			name:        "invalid status never carries extraction",
			body:        `{"entity":{"status":{"overall_status":0,"reason":"NOT_VALID"},"document_type":` + documentType + `,"text_data":[` + documented + `]}}`,
			check:       "idenqa.check.document_analysis",
			wantOutcome: providerv1.SignalOutcomeNotSatisfied,
			want:        nil,
		},
		{
			name:        "inconclusive status never carries extraction",
			body:        `{"entity":{"status":{"overall_status":2},"document_type":` + documentType + `,"text_data":[` + documented + `]}}`,
			check:       "idenqa.check.document_analysis",
			wantOutcome: providerv1.SignalOutcomeInconclusive,
			want:        nil,
		},
		{
			name:        "missing status never carries extraction",
			body:        `{"entity":{"document_type":` + documentType + `,"text_data":[` + documented + `]}}`,
			check:       "idenqa.check.document_analysis",
			wantOutcome: providerv1.SignalOutcomeInconclusive,
			want:        nil,
		},
		{
			name: "unreadable absent and wrongly typed entries are ignored",
			body: `{"entity":{"status":{"overall_status":1},"text_data":[` +
				`{"field_key":"document_number","status":2,"value":"PRIVATE-VALUE"},` +
				`{"field_key":"sex","status":0,"value":"M"},` +
				`{"field_key":"dob","status":"1","value":"1990-08-01"}]}}`,
			check:       "idenqa.check.document_analysis",
			wantOutcome: providerv1.SignalOutcomeSatisfied,
			want:        nil,
		},
		{
			name: "undocumented keys are never forwarded",
			body: `{"entity":{"status":{"overall_status":1},"text_data":[` +
				`{"field_key":"surname","status":1,"value":"Doe"},` +
				`{"field_key":"given_names","status":1,"value":"John"},` +
				`{"field_key":"nationality","status":1,"value":"USA"},` +
				`{"field_key":"mrz","status":1,"value":"P<UTODOE<<JOHN<<<<<<<<<<<<<<<<<<<<<<<<<<"},` +
				`{"field_key":"barcode_payload","status":1,"value":"@\n\u001e\rANSI 636000080002"}]}}`,
			check:       "idenqa.check.document_analysis",
			wantOutcome: providerv1.SignalOutcomeSatisfied,
			want:        nil,
		},
		{
			name:        "wrong types and malformed documents are ignored",
			body:        `{"entity":{"status":{"overall_status":1},"document_type":"USA","text_data":[{"field_key":42,"status":1,"value":"x"},{"field_key":"document_number","status":1,"value":12345678},{"field_key":"dob","status":1,"value":"not-a-date"},{"field_key":"expiry_date","status":true,"value":"2031-01-15"}]}}`,
			check:       "idenqa.check.document_analysis",
			wantOutcome: providerv1.SignalOutcomeSatisfied,
			want:        nil,
		},
		{
			name: "oversized malformed and duplicate values are dropped",
			body: `{"entity":{"status":{"overall_status":1},"text_data":[` +
				`{"field_key":"document_number","status":1,"value":"` + strings.Repeat("X", providerv1.MaximumDocumentFieldBytes+1) + `"},` +
				`{"field_key":"sex","status":1,"value":"M\u0001"},` +
				`{"field_key":"dob","status":1,"value":"01/08/1990"},` +
				`{"field_key":"expiry_date","status":1,"value":"2031-01-15"},` +
				`{"field_key":"expiry_date","status":1,"value":"2040-01-01"}]}}`,
			check:       "idenqa.check.document_analysis",
			wantOutcome: providerv1.SignalOutcomeSatisfied,
			want:        nil,
		},
		{
			name:        "non-document checks never attach an observation",
			body:        `{"entity":{"status":{"overall_status":1},"document_type":` + documentType + `,"text_data":[` + documented + `]}}`,
			check:       "idenqa.check.passive_liveness",
			wantOutcome: providerv1.SignalOutcomeSatisfied,
			want:        nil,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			transport := &client{status: 200, body: test.body}
			adapter, err := dojah.New(secrets{}, inputs{}, evidence{}, transport, func() time.Time { return fixedNow })
			if err != nil {
				t.Fatal(err)
			}
			request, _ := fixture(t, adapter, test.check)
			if test.check == "idenqa.check.document_analysis" {
				request.Evidence[0].Variant = "document.front"
			}
			result, err := adapter.Execute(t.Context(), request)
			if err != nil || result.Signals[0].Outcome != test.wantOutcome {
				t.Fatalf("result = %+v, error = %v", result, err)
			}
			if !reflect.DeepEqual(result.Document, test.want) {
				t.Fatalf("document = %+v, want = %+v", result.Document, test.want)
			}
			if err := result.Validate(); err != nil {
				t.Fatalf("result failed contract validation: %v", err)
			}
		})
	}
}

// TestDocumentExtractionNeverLeaksRawValuesIntoErrorsOrFailures proves the
// mapped and malformed document paths never place raw provider values, or any
// fragment of them, into returned errors, failure classifications, or logs.
func TestDocumentExtractionNeverLeaksRawValuesIntoErrorsOrFailures(t *testing.T) {
	const sentinel = "SENTINELRAWDOCUMENTVALUE"
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)

	tests := []struct {
		name      string
		transport *client
	}{
		{
			name: "mapped extraction",
			transport: &client{status: http.StatusOK, body: `{"entity":{"status":{"overall_status":1},"document_type":{"document_country_code":"USA"},` +
				`"text_data":[{"field_key":"document_number","status":1,"value":"` + sentinel + `"}]}}`},
		},
		{
			name:      "malformed success body",
			transport: &client{status: http.StatusOK, body: `{"entity":{"status":{"overall_status":1},"text_data":[{"field_key":"document_number","status":1,"value":"` + sentinel + `"}`},
		},
		{
			name:      "provider rejection body",
			transport: &client{status: http.StatusBadRequest, body: `{"error":"` + sentinel + `"}`},
		},
		{
			name:      "oversized response body",
			transport: &client{status: http.StatusOK, body: sentinel + strings.Repeat("X", 1<<20)},
		},
		{
			name: "unknown and oversized keys",
			transport: &client{status: http.StatusOK, body: `{"entity":{"status":{"overall_status":1},"text_data":[` +
				`{"field_key":"` + sentinel + `","status":1,"value":"` + sentinel + `"},` +
				`{"field_key":"document_number","status":1,"value":"` + sentinel + strings.Repeat("X", providerv1.MaximumDocumentFieldBytes) + `"}]}}`},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter, err := dojah.New(secrets{}, inputs{}, evidence{}, test.transport, func() time.Time { return fixedNow })
			if err != nil {
				t.Fatal(err)
			}
			request, _ := fixture(t, adapter, "idenqa.check.document_analysis")
			request.Evidence[0].Variant = "document.front"
			result, executeErr := adapter.Execute(t.Context(), request)
			if executeErr != nil {
				t.Fatalf("adapter returned a raw execution error: %v", executeErr)
			}
			encoded, err := json.Marshal(struct {
				Failure *providerv1.Failure `json:"failure,omitempty"`
				Signals []providerv1.Signal `json:"signals,omitempty"`
			}{Failure: result.Failure, Signals: result.Signals})
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(encoded, []byte(sentinel)) || bytes.Contains(logs.Bytes(), []byte(sentinel)) {
				t.Fatalf("raw provider value escaped into failure state or logs: %s", encoded)
			}
		})
	}
	if strings.Contains(logs.String(), sentinel) {
		t.Fatalf("raw provider value reached logs: %s", logs.String())
	}
}

func TestDocumentExtractionDuplicateAndConsumptionBoundary(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, first, second string
		wantDocument        bool
	}{
		{"identical duplicates", "2031-01-15", "2031-01-15", true},
		{"trimmed identical duplicates", " 2031-01-15 ", "2031-01-15", true},
		{"conflicting future dates", "2031-01-15", "2040-01-01", false},
		{"valid then expired", "2031-01-15", "2000-01-01", false},
		{"expired then valid", "2000-01-01", "2031-01-15", false},
		{"malformed then valid", "not-a-date", "2031-01-15", true},
		{"valid then malformed", "2031-01-15", "not-a-date", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			const sentinel = "SENTINEL-DOCUMENT-123"
			body, err := json.Marshal(map[string]any{"entity": map[string]any{
				"status": map[string]int{"overall_status": 1},
				"text_data": []map[string]any{
					{"field_key": "document_number", "status": 1, "value": sentinel},
					{"field_key": "expiry_date", "status": 1, "value": tt.first},
					{"field_key": "expiry_date", "status": 1, "value": tt.second},
				},
			}})
			if err != nil {
				t.Fatal(err)
			}
			adapter, err := dojah.New(secrets{}, inputs{}, evidence{}, &client{status: 200, body: string(body)}, func() time.Time { return fixedNow })
			if err != nil {
				t.Fatal(err)
			}
			request, _ := fixture(t, adapter, "idenqa.check.document_analysis")
			request.Evidence[0].Variant = "document.front"
			result, err := adapter.Execute(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if (result.Document != nil) != tt.wantDocument {
				t.Fatal("unexpected document observation presence")
			}
			if result.Signals[0].Outcome != providerv1.SignalOutcomeSatisfied {
				t.Fatal("extraction changed provider quality meaning")
			}
			if tt.wantDocument && len(result.Document.Fields) != 2 {
				t.Fatal("identical/invalid duplicates changed field count")
			}
			consumed, err := verification.ConsumeProviderDocument(result)
			if err != nil {
				t.Fatal(err)
			}
			if consumed.Document != nil {
				t.Fatal("raw observation survived consumption")
			}
			if !tt.wantDocument && len(consumed.Signals) != 1 {
				t.Fatal("conflicting fields supplied derived assurance")
			}
			encoded, err := json.Marshal(consumed)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(encoded, []byte(sentinel)) || bytes.Contains(encoded, []byte(tt.first)) || bytes.Contains(encoded, []byte(tt.second)) {
				t.Fatal("raw document data survived the persistence boundary")
			}
		})
	}
}

func TestDocumentExtractionRejectsConflictingFields(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, first, second string }{
		{"document_number", "ID123", "ID456"},
		{"sex", "M", "F"},
		{"dob", "1990-08-01", "1991-08-01"},
		{"expiry_date", "2031-01-15", "2040-01-01"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, values := range [][2]string{{tt.first, tt.second}, {tt.second, tt.first}} {
				body, err := json.Marshal(map[string]any{"entity": map[string]any{
					"status": map[string]int{"overall_status": 1},
					"text_data": []map[string]any{
						{"field_key": tt.name, "status": 1, "value": values[0]},
						{"field_key": tt.name, "status": 1, "value": values[1]},
						{"field_key": tt.name, "status": 1, "value": values[0]},
					},
				}})
				if err != nil {
					t.Fatal(err)
				}
				adapter, err := dojah.New(secrets{}, inputs{}, evidence{}, &client{status: 200, body: string(body)}, func() time.Time { return fixedNow })
				if err != nil {
					t.Fatal(err)
				}
				request, _ := fixture(t, adapter, "idenqa.check.document_analysis")
				request.Evidence[0].Variant = "document.front"
				result, err := adapter.Execute(t.Context(), request)
				if err != nil {
					t.Fatal(err)
				}
				if result.Document != nil {
					t.Fatal("conflicting extraction was accepted")
				}
				if err := result.ValidateForRequest(request); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
