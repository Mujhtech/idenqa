package smileid_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/adapters/providers/smileid"
	providerconformance "github.com/Mujhtech/idenqa/conformance/provider"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
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
	adapter, err := smileid.New(secrets{}, inputs{}, evidence{}, &client{}, waiter{}, func() time.Time { return fixedNow })
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

func (secrets) ResolveSmileID(context.Context, string, string) (smileid.Config, error) {
	return smileid.Config{BaseURL: "https://testapi.smileidentity.com", PartnerID: "085", APIKey: "test-key", CallbackURL: "https://core.example/callback", Mode: "sandbox", Region: "africa", PollInterval: time.Millisecond}, nil
}

type inputs struct{}

func (inputs) ResolveProviderInput(_ context.Context, reference string) (string, error) {
	values := map[string]string{
		"secret://input/country":   "NG",
		"secret://input/id-type":   "NIN_V2",
		"secret://input/id-number": "00000000000",
	}
	return values[reference], nil
}

type evidence struct{}

func (evidence) ReadProviderEvidence(context.Context, providerv1.EvidenceGrantReference, int64) ([]byte, error) {
	return []byte("synthetic-jpeg"), nil
}

type waiter struct{}

func (waiter) Wait(context.Context, time.Duration) error { return nil }

type response struct {
	status int
	body   string
	retry  string
}
type client struct {
	mu        sync.Mutex
	responses []response
	requests  []*http.Request
}

func (value *client) Do(request *http.Request) (*http.Response, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	value.requests = append(value.requests, request)
	if len(value.responses) == 0 {
		return nil, fmt.Errorf("unexpected request")
	}
	next := value.responses[0]
	value.responses = value.responses[1:]
	header := make(http.Header)
	header.Set("Retry-After", next.retry)
	return &http.Response{StatusCode: next.status, Header: header, Body: io.NopCloser(strings.NewReader(next.body))}, nil
}

func TestConformanceExercisesSignedPrepUploadAndPolling(t *testing.T) {
	t.Parallel()
	timestamp := fixedNow.Format("2006-01-02T15:04:05.000Z")
	transport := &client{responses: []response{
		{200, `{"upload_url":"https://uploads.example/job.zip","code":"2202"}`, ""},
		{200, `{}`, ""},
		{200, statusBody(timestamp, true), ""},
	}}
	adapter, err := smileid.New(secrets{}, inputs{}, evidence{}, transport, waiter{}, func() time.Time { return fixedNow })
	if err != nil {
		t.Fatal(err)
	}
	request, configuration := fixture(t, adapter)
	if err := providerconformance.Check(t.Context(), adapter, providerconformance.Fixture{Configuration: configuration, Request: request}); err != nil {
		t.Fatal(err)
	}
	if len(transport.requests) != 3 || transport.requests[0].URL.Path != "/v1/upload" || transport.requests[1].Method != http.MethodPut || transport.requests[2].URL.Path != "/v1/job_status" {
		t.Fatalf("requests = %d", len(transport.requests))
	}
}

func TestDuplicateJobReconcilesWithoutRepeatingEvidenceUpload(t *testing.T) {
	t.Parallel()
	timestamp := fixedNow.Format("2006-01-02T15:04:05.000Z")
	transport := &client{responses: []response{
		{400, `{"code":"2215","error":"Job already exists"}`, ""},
		{200, statusBody(timestamp, false), ""},
	}}
	adapter, _ := smileid.New(secrets{}, inputs{}, evidence{}, transport, waiter{}, func() time.Time { return fixedNow })
	request, _ := fixture(t, adapter)
	result, err := adapter.Execute(t.Context(), request)
	if err != nil || result.Outcome != providerv1.ResultOutcomeCompleted || result.Signals[0].Outcome != providerv1.SignalOutcomeNotSatisfied {
		t.Fatalf("result = %+v, error = %v", result, err)
	}
	if len(transport.requests) != 2 || transport.requests[1].URL.Path != "/v1/job_status" {
		t.Fatalf("duplicate issued %d requests", len(transport.requests))
	}
}

func TestInvalidStatusSignatureFailsClosed(t *testing.T) {
	t.Parallel()
	transport := &client{responses: []response{
		{200, `{"upload_url":"https://uploads.example/job.zip","code":"2202"}`, ""},
		{200, `{}`, ""},
		{200, `{"timestamp":"2026-09-04T12:00:00.000Z","signature":"invalid","job_complete":true}`, ""},
	}}
	adapter, _ := smileid.New(secrets{}, inputs{}, evidence{}, transport, waiter{}, func() time.Time { return fixedNow })
	request, _ := fixture(t, adapter)
	result, err := adapter.Execute(t.Context(), request)
	if err != nil || result.Failure == nil || result.Failure.Code != "invalid_response_signature" || result.Failure.Retry != providerv1.RetryNever {
		t.Fatalf("result = %+v, error = %v", result, err)
	}
}

func TestLegacyStatusSignatureFieldIsVerified(t *testing.T) {
	t.Parallel()
	timestamp := fixedNow.Format("2006-01-02T15:04:05.000Z")
	status := statusBody(timestamp, true)
	status = strings.Replace(status, `"signature"`, `"sec_key"`, 1)
	transport := &client{responses: []response{
		{200, `{"upload_url":"https://uploads.example/job.zip","code":"2202"}`, ""},
		{200, `{}`, ""},
		{200, status, ""},
	}}
	adapter, _ := smileid.New(secrets{}, inputs{}, evidence{}, transport, waiter{}, func() time.Time { return fixedNow })
	request, _ := fixture(t, adapter)
	result, err := adapter.Execute(t.Context(), request)
	if err != nil || result.Outcome != providerv1.ResultOutcomeCompleted {
		t.Fatalf("result = %+v, error = %v", result, err)
	}
}

func fixture(t *testing.T, adapter *smileid.Adapter) (providerv1.Request, providerv1.ConfigurationReference) {
	t.Helper()
	manifest, err := adapter.Manifest(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	capability := manifest.Capabilities[0]
	configuration := providerv1.ConfigurationReference{ProviderID: "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWH", SchemaDigest: manifest.Configuration.Digest, SecretReference: "secret://provider/smileid", CredentialVersion: "2026-09-04"}
	variants := []string{"selfie", "document.front", "document.back", "liveness.1", "liveness.2"}
	grants := make([]providerv1.EvidenceGrantReference, len(variants))
	for index, variant := range variants {
		character := string("123456789ABCDEFGHJKMNPQRS"[index])
		grants[index] = providerv1.EvidenceGrantReference{GrantID: "grt_" + strings.Repeat(character, 26), RedemptionID: "rdm_" + strings.Repeat(character, 26), EvidenceID: "evd_" + strings.Repeat(character, 26), Purpose: "idenqa.purpose.identity_verification", Variant: variant, ExpiresAt: fixedNow.Add(time.Hour)}
	}
	return providerv1.Request{
		Contract: providerv1.CurrentVersion, AttemptID: "atm_01K4AR9V8FQ2G7ZXCPNM5T6JWH", ProviderID: configuration.ProviderID,
		TenantID: "ten_01K4AR9V8FQ2G7ZXCPNM5T6JWH", VerificationID: "ver_01K4AR9V8FQ2G7ZXCPNM5T6JWH",
		Check: capability.Check, IdempotencyKey: "idenqa-fixture-key", Adapter: manifest.Package, Capability: capability,
		Restrictions: manifest.Restrictions, Configuration: configuration,
		Inputs: []providerv1.InputReference{
			{Name: "idenqa.input.country", Reference: "secret://input/country"},
			{Name: "idenqa.input.id_type", Reference: "secret://input/id-type"},
		}, Evidence: grants, Deadline: fixedNow.Add(5 * time.Minute),
	}, configuration
}

func statusBody(timestamp string, success bool) string {
	signature := signature("test-key", timestamp)
	return fmt.Sprintf(`{"timestamp":%q,"signature":%q,"job_complete":true,"job_success":%t,"result":{"Actions":{"Liveness_Check":"Passed","Selfie_To_ID_Card_Compare":"Completed","Document_Check":"Passed"}}}`, timestamp, signature, success)
}

func signature(key, timestamp string) string {
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte(timestamp + "085" + "sid_request"))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// TestStatusNormalisationNeverForwardsUnconfirmedExtraction proves the legacy
// v1 job_status path is explicitly fail-closed for extraction. Smile ID
// documents extracted fields only on the v3 verification webhook id_fields
// object, so undocumented status keys (id_fields, FullName, IDNumber,
// ExpirationDate, Gender) are never forwarded as an observation.
func TestStatusNormalisationNeverForwardsUnconfirmedExtraction(t *testing.T) {
	t.Parallel()
	const sentinel = "SENTINELRAWV1STATUS"
	timestamp := fixedNow.Format("2006-01-02T15:04:05.000Z")
	status := fmt.Sprintf(
		`{"code":"2302","timestamp":%q,"signature":%q,"job_complete":true,"job_success":true,"result":{"SmileJobID":"job-123","Actions":{"Liveness_Check":"Passed","Verify_Document":"Passed","Selfie_To_ID_Card_Compare":"Completed"},"id_fields":{"id_number":%q,"country":"NG","id_type":"PASSPORT"},"FullName":%q,"IDNumber":%q,"ExpirationDate":"2031-01-15","Gender":"Female"}}`,
		timestamp, signature("test-key", timestamp), sentinel, sentinel, sentinel)
	transport := &client{responses: []response{
		{200, `{"upload_url":"https://uploads.example/job.zip","code":"2202"}`, ""},
		{200, `{}`, ""},
		{200, status, ""},
	}}
	adapter, err := smileid.New(secrets{}, inputs{}, evidence{}, transport, waiter{}, func() time.Time { return fixedNow })
	if err != nil {
		t.Fatal(err)
	}
	request, _ := fixture(t, adapter)
	result, err := adapter.Execute(t.Context(), request)
	if err != nil || result.Outcome != providerv1.ResultOutcomeCompleted {
		t.Fatalf("result = %+v, error = %v", result, err)
	}
	if result.Document != nil {
		t.Fatalf("legacy status forwarded unconfirmed extraction: %+v", result.Document)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(sentinel)) {
		t.Fatalf("legacy status forwarded raw provider values: %s", encoded)
	}
}
