package idenqa_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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
	"github.com/Mujhtech/idenqa/internal/policy"
)

const (
	policySimulationCLIID    = "pol_01ARZ3NDEKTSV4RRFFQ69G5FAV"
	policyComputationCLIBody = `{"synthetic":true}`
)

func TestPolicyComputationCLICommands(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "environment-credential")
	keyFile := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(keyFile, []byte("test-policy-computation-credential\n"), 0600); err != nil {
		t.Fatal(err)
	}
	simulationResponse := marshalPolicyCLI(t, policy.SimulationReport{
		SchemaMajor: 1, SchemaMinor: 0, PolicyID: policySimulationCLIID, PolicyRevision: 3,
		Directive: policy.DirectiveCompleteVerified, Outcome: policy.OutcomeVerified,
		AuthorisesCompletion: true, EvaluationDigest: strings.Repeat("a", 64), BundleDigest: strings.Repeat("b", 64),
	})
	regressionResponse := marshalPolicyCLI(t, policy.ScenarioSuiteReport{
		SchemaMajor: 1, SchemaMinor: 0, Passed: true, Digest: strings.Repeat("c", 64),
		Cases: []policy.ScenarioCaseReport{{
			Name: "verified", Matches: true, ExpectedEvaluationDigest: strings.Repeat("a", 64),
			Actual: policy.SimulationReport{EvaluationDigest: strings.Repeat("a", 64)},
		}},
	})
	regressionFailure := marshalPolicyCLI(t, policy.ScenarioSuiteReport{
		SchemaMajor: 1, SchemaMinor: 0, Passed: false, Digest: strings.Repeat("d", 64),
		Cases: []policy.ScenarioCaseReport{{
			Name: "verified", Matches: false, ExpectedEvaluationDigest: strings.Repeat("e", 64),
			Actual: policy.SimulationReport{EvaluationDigest: strings.Repeat("a", 64)},
		}},
	})
	diffResponse := marshalPolicyCLI(t, policy.RevisionDiff{
		SchemaMajor: 1, SchemaMinor: 0, PolicyID: policySimulationCLIID,
		From:      policy.RevisionDiffRevision{Revision: 1, Digest: strings.Repeat("a", 64)},
		To:        policy.RevisionDiffRevision{Revision: 2, Digest: strings.Repeat("b", 64)},
		Identical: false, ChangeCount: 1, Digest: strings.Repeat("c", 64),
		Changes: []policy.RevisionDiffChange{{
			Path: "/verified_assurance", Kind: policy.RevisionDiffChanged,
			Old: json.RawMessage(`"synthetic.first"`), New: json.RawMessage(`"synthetic.second"`),
		}},
	})
	for _, test := range []struct {
		name     string
		args     []string
		body     string
		method   string
		path     string
		query    url.Values
		response string
		wantCode int
		contains []string
		wantJSON bool
	}{
		{
			name: "simulate summary", args: []string{"policy", "simulate", "--body-file", policyComputationCLIBodyFile(t)},
			body: policyComputationCLIBody, method: "POST", path: "/v1/policy-simulations", response: simulationResponse,
			contains: []string{"policy_simulation", "directive=complete_verified", "outcome=verified", "authorises_completion=true"},
		},
		{
			name: "simulate json", args: []string{"policy", "simulate", "--body-file", policyComputationCLIBodyFile(t), "--json"},
			body: policyComputationCLIBody, method: "POST", path: "/v1/policy-simulations", response: simulationResponse, wantJSON: true,
		},
		{
			name: "regression pass", args: []string{"policy", "regression", "--body-file", policyComputationCLIBodyFile(t)},
			body: policyComputationCLIBody, method: "POST", path: "/v1/policy-regressions", response: regressionResponse,
			contains: []string{"policy_regression_case name=verified matches=true", "policy_regression passed=true cases=1"},
		},
		{
			name: "regression mismatch summary prints and exits nonzero", args: []string{"policy", "regression", "--body-file", policyComputationCLIBodyFile(t)},
			body: policyComputationCLIBody, method: "POST", path: "/v1/policy-regressions", response: regressionFailure, wantCode: 1,
			contains: []string{"policy_regression passed=false cases=1"},
		},
		{
			name: "regression mismatch json exits nonzero after printing", args: []string{"policy", "regression", "--body-file", policyComputationCLIBodyFile(t), "--json"},
			body: policyComputationCLIBody, method: "POST", path: "/v1/policy-regressions", response: regressionFailure, wantCode: 1,
		},
		{
			name: "diff summary", args: []string{"policy", "diff", "--policy-id", policySimulationCLIID, "--from", "1", "--to", "2"},
			method: "GET", path: "/v1/policies/" + policySimulationCLIID + "/diff",
			query: url.Values{"from_revision": {"1"}, "to_revision": {"2"}}, response: diffResponse,
			contains: []string{"policy_diff policy_id=" + policySimulationCLIID, "change_count=1", "policy_diff_change path=/verified_assurance kind=changed"},
		},
		{
			name: "diff json", args: []string{"policy", "diff", "--policy-id", policySimulationCLIID, "--from", "1", "--to", "2", "--json"},
			method: "GET", path: "/v1/policies/" + policySimulationCLIID + "/diff",
			query: url.Values{"from_revision": {"1"}, "to_revision": {"2"}}, response: diffResponse, wantJSON: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requestBody []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != test.method || r.URL.Path != test.path {
					t.Errorf("request = %s %s, want %s %s", r.Method, r.URL.Path, test.method, test.path)
				}
				if got, want := r.URL.Query().Encode(), test.query.Encode(); got != want {
					t.Errorf("query = %q, want %q", got, want)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer test-policy-computation-credential" {
					t.Errorf("authorization = %q", got)
				}
				requestBody, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, test.response)
			}))
			defer server.Close()
			args := append(append([]string(nil), test.args...), "--api-url", server.URL, "--api-key-file", keyFile)
			var out, diagnostics bytes.Buffer
			code := bootstrap.Run(args, &out, &diagnostics, buildinfo.Info{})
			if code != test.wantCode {
				t.Fatalf("exit = %d, want %d, stderr = %s", code, test.wantCode, diagnostics.String())
			}
			if strings.Contains(out.String()+diagnostics.String(), "test-policy-computation-credential") ||
				strings.Contains(out.String()+diagnostics.String(), "environment-credential") {
				t.Fatal("credential leaked")
			}
			if test.body != "" {
				var got, want any
				if err := json.Unmarshal(requestBody, &got); err != nil {
					t.Fatalf("request body = %q: %v", requestBody, err)
				}
				if err := json.Unmarshal([]byte(test.body), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("request body = %q, want %q", requestBody, test.body)
				}
			}
			if test.wantJSON {
				var got, want any
				if err := json.Unmarshal(out.Bytes(), &got); err != nil {
					t.Fatalf("stdout = %q: %v", out.String(), err)
				}
				if err := json.Unmarshal([]byte(test.response), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("stdout = %s, want %s", out.String(), test.response)
				}
				return
			}
			for _, expected := range test.contains {
				if !strings.Contains(out.String(), expected) {
					t.Fatalf("stdout = %q, want %q", out.String(), expected)
				}
			}
		})
	}
}

func TestPolicyComputationCLIValidationAndFailures(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "test-policy-computation-credential")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprint(w, "private-server-response")
	}))
	defer server.Close()
	bodyFile := policyComputationCLIBodyFile(t)
	invalidBodyFile := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(invalidBodyFile, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	oversizedBodyFile := filepath.Join(t.TempDir(), "oversized.json")
	if err := os.WriteFile(oversizedBodyFile, bytes.Repeat([]byte{'x'}, policy.MaximumSimulationInputBytes+1), 0600); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name string
		args []string
		code int
	}{
		{"missing body file", []string{"policy", "simulate"}, 2},
		{"invalid body json", []string{"policy", "simulate", "--body-file", invalidBodyFile}, 2},
		{"oversized body", []string{"policy", "simulate", "--body-file", oversizedBodyFile}, 2},
		{"invalid policy identifier", []string{"policy", "diff", "--policy-id", "not-a-policy", "--from", "1", "--to", "2"}, 2},
		{"zero from revision", []string{"policy", "diff", "--policy-id", policySimulationCLIID, "--from", "0", "--to", "2"}, 2},
		{"zero to revision", []string{"policy", "diff", "--policy-id", policySimulationCLIID, "--from", "1", "--to", "0"}, 2},
		{"runtime failure", []string{"policy", "simulate", "--body-file", bodyFile}, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := calls.Load()
			args := append(append([]string(nil), test.args...), "--api-url", server.URL)
			var out, diagnostics bytes.Buffer
			if code := bootstrap.Run(args, &out, &diagnostics, buildinfo.Info{}); code != test.code {
				t.Fatalf("exit = %d, want %d, stderr = %s", code, test.code, diagnostics.String())
			}
			if test.code == 2 && calls.Load() != before {
				t.Fatal("invalid input reached the API")
			}
			if test.code == 1 {
				if calls.Load() != before+1 {
					t.Fatalf("runtime failure calls = %d", calls.Load()-before)
				}
				if strings.Contains(diagnostics.String(), "private-server-response") || !strings.Contains(diagnostics.String(), "status 503") {
					t.Fatalf("runtime failure diagnostics = %q", diagnostics.String())
				}
			}
		})
	}
}

func marshalPolicyCLI(t testing.TB, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func policyComputationCLIBodyFile(t testing.TB) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "body.json")
	if err := os.WriteFile(path, []byte(policyComputationCLIBody), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
