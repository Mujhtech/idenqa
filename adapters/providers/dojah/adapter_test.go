package dojah_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/adapters/providers/dojah"
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
	status  int
	body    string
	retry   string
	request *http.Request
}

func (value *client) Do(request *http.Request) (*http.Response, error) {
	value.request = request
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
