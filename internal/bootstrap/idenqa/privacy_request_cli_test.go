package idenqa_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	bootstrap "github.com/Mujhtech/idenqa/internal/bootstrap/idenqa"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
)

const (
	privacyRequestID      = "prq_01ARZ3NDEKTSV4RRFFQ69G5FAV"
	privacyRestrictionID  = "prs_01ARZ3NDEKTSV4RRFFQ69G5FAV"
	privacyProcessorID    = "prc_01ARZ3NDEKTSV4RRFFQ69G5FAV"
	privacySubjectID      = "sub_01ARZ3NDEKTSV4RRFFQ69G5FAV"
	privacyRequestJSON    = `{"id":"` + privacyRequestID + `","type":"access","state":"approved","channel":"tenant_api","subject_id":"` + privacySubjectID + `","region":"ng-1","expires_at":"2026-10-20T12:00:00Z","requested_at":"2026-09-20T12:00:00Z","updated_at":"2026-09-20T12:05:00Z","version":3,"decisions":[]}`
	privacyDisclosureJSON = `{"id":"pdc_01ARZ3NDEKTSV4RRFFQ69G5FAV","request_id":"` + privacyRequestID + `","recipient":"legal.counsel","purpose":"access.request","data_class":"subject_export","legal_basis":"controller.contract","region":"ng-1","reference":"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","disclosed_at":"2026-09-20T12:05:00Z","version":1}`
	privacyProcessorJSON  = `{"id":"` + privacyProcessorID + `","name":"Example KYC","role":"processor","purpose":"identity.verification","data_classes":["raw_evidence"],"regions":["ng-1"],"transfer_mechanism":"standard.contractual_clauses","version":2,"created_at":"2026-09-20T12:00:00Z","updated_at":"2026-09-20T13:00:00Z"}`
)

func TestPrivacyRequestCLICommands(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "environment-credential")
	keyFile := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(keyFile, []byte("test-privacy-credential\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		arguments []string
		method    string
		path      string
		query     url.Values
		body      map[string]any
		status    int
		response  string
	}{
		{
			name:      "request create",
			arguments: []string{"privacy", "request", "create", "--type", "access", "--subject-id", privacySubjectID, "--region", "ng-1"},
			method:    "POST", path: "/v1/privacy-requests",
			body:   map[string]any{"type": "access", "subject_id": privacySubjectID, "region": "ng-1"},
			status: 202, response: privacyRequestJSON,
		},
		{
			name:      "request list",
			arguments: []string{"privacy", "request", "list", "--limit", "10", "--cursor", "next-cursor", "--type", "access", "--subject-id", privacySubjectID},
			method:    "GET", path: "/v1/privacy-requests",
			query:  url.Values{"limit": {"10"}, "cursor": {"next-cursor"}, "type": {"access"}, "subject_id": {privacySubjectID}},
			status: 200, response: `{"data":[` + privacyRequestJSON + `],"page":{"has_more":false}}`,
		},
		{
			name:      "request get",
			arguments: []string{"privacy", "request", "get", privacyRequestID},
			method:    "GET", path: "/v1/privacy-requests/" + privacyRequestID,
			status: 200, response: privacyRequestJSON,
		},
		{
			name:      "request approve",
			arguments: []string{"privacy", "request", "approve", privacyRequestID, "--expected-version", "2", "--reason-code", "access_approved"},
			method:    "POST", path: "/v1/privacy-requests/" + privacyRequestID + "/approve",
			body:   map[string]any{"expected_version": float64(2), "reason_code": "access_approved", "outcome": "approved"},
			status: 200, response: privacyRequestJSON,
		},
		{
			name:      "request deny",
			arguments: []string{"privacy", "request", "deny", privacyRequestID, "--expected-version", "3", "--reason-code", "identity_unverified"},
			method:    "POST", path: "/v1/privacy-requests/" + privacyRequestID + "/deny",
			body:   map[string]any{"expected_version": float64(3), "reason_code": "identity_unverified"},
			status: 200, response: privacyRequestJSON,
		},
		{
			name:      "request withdraw",
			arguments: []string{"privacy", "request", "withdraw", privacyRequestID, "--expected-version", "2"},
			method:    "POST", path: "/v1/privacy-requests/" + privacyRequestID + "/withdraw",
			body:   map[string]any{"expected_version": float64(2)},
			status: 200, response: privacyRequestJSON,
		},
		{
			name:      "request execute",
			arguments: []string{"privacy", "request", "execute", privacyRequestID, "--expected-version", "3"},
			method:    "POST", path: "/v1/privacy-requests/" + privacyRequestID + "/execute",
			body:   map[string]any{"expected_version": float64(3)},
			status: 200, response: privacyRequestJSON,
		},
		{
			name:      "restriction lift",
			arguments: []string{"privacy", "restriction", "lift", privacyRestrictionID, "--expected-version", "1", "--reason-code", "lifted_by_tenant"},
			method:    "POST", path: "/v1/privacy-restrictions/" + privacyRestrictionID + "/lift",
			body:   map[string]any{"expected_version": float64(1), "reason_code": "lifted_by_tenant"},
			status: 200, response: `{"id":"` + privacyRestrictionID + `","request_id":"` + privacyRequestID + `","subject_id":"` + privacySubjectID + `","scope":"subject","reason_code":"subject_request","region":"ng-1","state":"lifted","starts_at":"2026-09-20T12:05:00Z","version":2}`,
		},
		{
			name:      "disclosure create",
			arguments: []string{"privacy", "disclosure", "create", "--request-id", privacyRequestID, "--recipient", "legal.counsel", "--purpose", "access.request", "--data-class", "subject_export", "--legal-basis", "controller.contract", "--region", "ng-1", "--reference", "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
			method:    "POST", path: "/v1/privacy-disclosures",
			body:   map[string]any{"request_id": privacyRequestID, "recipient": "legal.counsel", "purpose": "access.request", "data_class": "subject_export", "legal_basis": "controller.contract", "region": "ng-1", "reference": "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
			status: 201, response: privacyDisclosureJSON,
		},
		{
			name:      "disclosure list",
			arguments: []string{"privacy", "disclosure", "list", "--request-id", privacyRequestID, "--limit", "5"},
			method:    "GET", path: "/v1/privacy-disclosures",
			query:  url.Values{"limit": {"5"}, "request_id": {privacyRequestID}},
			status: 200, response: `{"data":[` + privacyDisclosureJSON + `],"page":{"has_more":false}}`,
		},
		{
			name:      "processor create",
			arguments: []string{"privacy", "processor", "put", "--name", "Example KYC", "--role", "processor", "--purpose", "identity.verification", "--data-classes", "raw_evidence,derived_evidence", "--regions", "ng-1", "--transfer-mechanism", "standard.contractual_clauses", "--expected-version", "0"},
			method:    "POST", path: "/v1/privacy-processors",
			body:   map[string]any{"name": "Example KYC", "role": "processor", "purpose": "identity.verification", "data_classes": []any{"raw_evidence", "derived_evidence"}, "regions": []any{"ng-1"}, "transfer_mechanism": "standard.contractual_clauses", "expected_version": float64(0)},
			status: 201, response: privacyProcessorJSON,
		},
		{
			name:      "processor update",
			arguments: []string{"privacy", "processor", "put", privacyProcessorID, "--name", "Example KYC", "--role", "processor", "--purpose", "identity.verification", "--data-classes", "raw_evidence", "--regions", "ng-1", "--transfer-mechanism", "standard.contractual_clauses", "--expected-version", "2"},
			method:    "PUT", path: "/v1/privacy-processors/" + privacyProcessorID,
			body:   map[string]any{"name": "Example KYC", "role": "processor", "purpose": "identity.verification", "data_classes": []any{"raw_evidence"}, "regions": []any{"ng-1"}, "transfer_mechanism": "standard.contractual_clauses", "expected_version": float64(2)},
			status: 200, response: privacyProcessorJSON,
		},
		{
			name:      "processor list",
			arguments: []string{"privacy", "processor", "list", "--limit", "10"},
			method:    "GET", path: "/v1/privacy-processors",
			query:  url.Values{"limit": {"10"}},
			status: 200, response: `{"data":[` + privacyProcessorJSON + `],"page":{"has_more":false}}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requestBody map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != test.method || r.URL.Path != test.path {
					t.Errorf("request = %s %s, want %s %s", r.Method, r.URL.Path, test.method, test.path)
				}
				if got, want := r.URL.Query().Encode(), test.query.Encode(); got != want {
					t.Errorf("query = %q, want %q", got, want)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer test-privacy-credential" {
					t.Errorf("authorization = %q", got)
				}
				requestBody = nil
				if r.Body != nil {
					_ = json.NewDecoder(r.Body).Decode(&requestBody)
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.response))
			}))
			defer server.Close()
			arguments := append([]string(nil), test.arguments...)
			arguments = append(arguments, "--api-url", server.URL, "--api-key-file", keyFile)
			var out, diagnostics bytes.Buffer
			if code := bootstrap.Run(arguments, &out, &diagnostics, buildinfo.Info{}); code != 0 {
				t.Fatalf("exit = %d, stderr = %s", code, diagnostics.String())
			}
			if !reflect.DeepEqual(requestBody, test.body) {
				t.Fatalf("request body = %#v, want %#v", requestBody, test.body)
			}
			if strings.Contains(out.String()+diagnostics.String(), "test-privacy-credential") || strings.Contains(out.String()+diagnostics.String(), "environment-credential") {
				t.Fatal("credential leaked")
			}
		})
	}
}

func TestPrivacyRequestCLIValidation(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "test-privacy-credential")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(500)
	}))
	defer server.Close()

	for _, test := range []struct {
		name string
		args []string
	}{
		{"create missing type", []string{"privacy", "request", "create", "--region", "ng-1"}},
		{"create missing region", []string{"privacy", "request", "create", "--type", "access"}},
		{"get invalid id", []string{"privacy", "request", "get", "not-a-request"}},
		{"approve missing reason", []string{"privacy", "request", "approve", privacyRequestID, "--expected-version", "2"}},
		{"approve missing version", []string{"privacy", "request", "approve", privacyRequestID, "--reason-code", "access_approved"}},
		{"withdraw missing version", []string{"privacy", "request", "withdraw", privacyRequestID}},
		{"lift invalid id", []string{"privacy", "restriction", "lift", "not-a-restriction", "--expected-version", "1", "--reason-code", "lifted_by_tenant"}},
		{"disclosure missing reference", []string{"privacy", "disclosure", "create", "--request-id", privacyRequestID, "--recipient", "legal.counsel", "--purpose", "access.request", "--data-class", "subject_export", "--legal-basis", "controller.contract", "--region", "ng-1"}},
		{"processor missing name", []string{"privacy", "processor", "put", "--role", "processor", "--purpose", "identity.verification", "--data-classes", "raw_evidence", "--regions", "ng-1", "--transfer-mechanism", "standard.contractual_clauses"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := calls.Load()
			args := append(append([]string(nil), test.args...), "--api-url", server.URL)
			var out, diagnostics bytes.Buffer
			if code := bootstrap.Run(args, &out, &diagnostics, buildinfo.Info{}); code != 2 {
				t.Fatalf("exit = %d, stderr = %s", code, diagnostics.String())
			}
			if calls.Load() != before {
				t.Fatal("invalid input reached the API")
			}
			if !strings.Contains(diagnostics.String(), "Usage:") {
				t.Fatalf("usage error did not print usage: %s", diagnostics.String())
			}
		})
	}
}
