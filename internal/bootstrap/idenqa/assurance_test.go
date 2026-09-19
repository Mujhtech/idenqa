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
)

const (
	assurancePolicyID       = "pol_01ARZ3NDEKTSV4RRFFQ69G5FAV"
	assuranceVerificationID = "ver_01ARZ3NDEKTSV4RRFFQ69G5FAV"
	assuranceProfileJSON    = `{"name":"capture.example","revision":1,"requirements":[],"mappings":[]}`
)

func TestAssuranceCLICommands(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "environment-credential")
	keyFile := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(keyFile, []byte("test-assurance-credential\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name        string
		arguments   []string
		body        string
		method      string
		path        string
		query       url.Values
		idempotency string
		status      int
		response    string
	}{
		{
			name:      "capabilities",
			arguments: []string{"assurance", "capabilities"},
			method:    "GET",
			path:      "/v1/assurance-capabilities",
			status:    200,
			response:  `{"version":1,"capabilities":[],"builtin_digests":{}}`,
		},
		{
			name:      "profiles",
			arguments: []string{"assurance", "profiles", "--limit", "25", "--after", "next-cursor"},
			method:    "GET",
			path:      "/v1/assurance-profiles",
			query:     url.Values{"limit": {"25"}, "after": {"next-cursor"}},
			status:    200,
			response:  `{"profiles":[],"next_cursor":""}`,
		},
		{
			name:      "profile",
			arguments: []string{"assurance", "profile", "capture.example", "3"},
			method:    "GET",
			path:      "/v1/assurance-profiles/capture.example/revisions/3",
			status:    200,
			response:  `{"profile":` + assuranceProfileJSON + `,"digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
		},
		{
			name:        "publish",
			arguments:   []string{"assurance", "publish", "--idempotency-key", "publish-key"},
			body:        assuranceProfileJSON,
			method:      "POST",
			path:        "/v1/assurance-profiles",
			idempotency: `"publish-key"`,
			status:      200,
			response:    `{"profile":` + assuranceProfileJSON + `,"digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
		},
		{
			name:      "validate",
			arguments: []string{"assurance", "validate"},
			body:      assuranceProfileJSON,
			method:    "POST",
			path:      "/v1/assurance-profiles/validate",
			status:    200,
			response:  `{"profile":` + assuranceProfileJSON + `,"digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
		},
		{
			name:      "policy-get",
			arguments: []string{"assurance", "policy-get", assurancePolicyID},
			method:    "GET",
			path:      "/v1/policies/" + assurancePolicyID + "/assurance",
			status:    200,
			response:  `{"selection":null,"version":2}`,
		},
		{
			name:        "policy-assign",
			arguments:   []string{"assurance", "policy-assign", assurancePolicyID, "--idempotency-key", "assign-key"},
			body:        `{"expected_version":2,"selection":` + assuranceProfileJSON + `}`,
			method:      "PUT",
			path:        "/v1/policies/" + assurancePolicyID + "/assurance",
			idempotency: `"assign-key"`,
			status:      200,
			response:    `{"selection":` + assuranceProfileJSON + `,"version":3}`,
		},
		{
			name:      "verification",
			arguments: []string{"assurance", "verification", assuranceVerificationID},
			method:    "GET",
			path:      "/v1/verifications/" + assuranceVerificationID + "/assurance",
			status:    200,
			response:  `{"selection":` + assuranceProfileJSON + `}`,
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
				if got := r.Header.Get("Authorization"); got != "Bearer test-assurance-credential" {
					t.Errorf("authorization = %q", got)
				}
				if test.idempotency != "" {
					if got := r.Header.Get("Idempotency-Key"); got != test.idempotency {
						t.Errorf("idempotency key = %q, want %q", got, test.idempotency)
					}
				} else if got := r.Header.Get("Idempotency-Key"); got != "" {
					t.Errorf("unexpected idempotency key = %q", got)
				}
				requestBody, _ = io.ReadAll(r.Body)
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.response)
			}))
			defer server.Close()
			arguments := append([]string(nil), test.arguments...)
			if test.body != "" {
				bodyFile := filepath.Join(t.TempDir(), "body.json")
				if err := os.WriteFile(bodyFile, []byte(test.body), 0600); err != nil {
					t.Fatal(err)
				}
				arguments = append(arguments, "--body-file", bodyFile)
			}
			var out, diagnostics bytes.Buffer
			args := append([]string(nil), arguments...)
			args = append(args, "--api-url", server.URL, "--api-key-file", keyFile)
			if code := bootstrap.Run(args, &out, &diagnostics, buildinfo.Info{}); code != 0 {
				t.Fatalf("exit = %d, stderr = %s", code, diagnostics.String())
			}
			if strings.Contains(out.String()+diagnostics.String(), "test-assurance-credential") || strings.Contains(out.String()+diagnostics.String(), "environment-credential") {
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
			} else if len(requestBody) != 0 {
				t.Fatalf("request body = %q, want empty", requestBody)
			}
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
		})
	}
}

func TestAssuranceCLIFailuresAndValidation(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "test-assurance-credential")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
		_, _ = fmt.Fprint(w, "private-server-response")
	}))
	defer server.Close()

	var out, diagnostics bytes.Buffer
	if code := bootstrap.Run([]string{"assurance", "capabilities", "--api-url", server.URL}, &out, &diagnostics, buildinfo.Info{}); code != 1 || calls.Load() != 1 {
		t.Fatalf("runtime failure exit = %d calls = %d stderr = %s", code, calls.Load(), diagnostics.String())
	}
	if strings.Contains(diagnostics.String(), "Usage:") || strings.Contains(diagnostics.String(), "private-server-response") {
		t.Fatal("runtime failure leaked usage or response body")
	}
	if !strings.Contains(diagnostics.String(), "assurance API returned status 503") {
		t.Fatalf("runtime failure = %q", diagnostics.String())
	}

	profileFile := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(profileFile, []byte(assuranceProfileJSON), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		args []string
	}{
		{"limit below range", []string{"assurance", "profiles", "--limit", "0"}},
		{"limit above range", []string{"assurance", "profiles", "--limit", "101"}},
		{"missing profile name arguments", []string{"assurance", "profile", "capture.example"}},
		{"blank profile name", []string{"assurance", "profile", "", "1"}},
		{"zero profile revision", []string{"assurance", "profile", "capture.example", "0"}},
		{"missing publish body", []string{"assurance", "publish", "--idempotency-key", "publish-key"}},
		{"missing publish idempotency key", []string{"assurance", "publish", "--body-file", profileFile}},
		{"missing validate body", []string{"assurance", "validate"}},
		{"invalid policy id", []string{"assurance", "policy-get", "not-a-policy"}},
		{"missing assign body", []string{"assurance", "policy-assign", assurancePolicyID, "--idempotency-key", "assign-key"}},
		{"invalid verification id", []string{"assurance", "verification", "not-a-verification"}},
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

func TestAssuranceCLIRequiresCredential(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = fmt.Fprint(w, `{"version":1,"capabilities":[],"builtin_digests":{}}`)
	}))
	defer server.Close()

	var out, diagnostics bytes.Buffer
	if code := bootstrap.Run([]string{"assurance", "capabilities", "--api-url", server.URL}, &out, &diagnostics, buildinfo.Info{}); code != 2 || calls.Load() != 0 {
		t.Fatalf("exit = %d calls = %d stderr = %s", code, calls.Load(), diagnostics.String())
	}
}
