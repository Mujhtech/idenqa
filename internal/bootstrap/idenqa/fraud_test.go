package idenqa_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	bootstrap "github.com/Mujhtech/idenqa/internal/bootstrap/idenqa"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
)

var fraudDigest = strings.Repeat("a", 64)

func TestFraudCLICommands(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "environment-credential")
	keyFile := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(keyFile, []byte("test-fraud-credential\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name        string
		arguments   []string
		body        string
		method      string
		path        string
		idempotency string
		status      int
		response    string
	}{
		{
			name:      "config-get",
			arguments: []string{"fraud", "config-get"},
			method:    "GET",
			path:      "/v1/fraud/configuration",
			status:    200,
			response:  `{"version":3,"digest":"` + fraudDigest + `"}`,
		},
		{
			name:        "config-put",
			arguments:   []string{"fraud", "config-put", "--idempotency-key", "config-key"},
			body:        `{"expected_version":3,"configuration":{"enabled":true,"region":"eu","window_seconds":3600,"retention_seconds":86400,"minimum_session_seconds":0,"maximum_session_seconds":3600,"thresholds":{},"sources":[],"mappings":[]}}`,
			method:      "PUT",
			path:        "/v1/fraud/configuration",
			idempotency: `"config-key"`,
			status:      200,
			response:    `{"version":4,"digest":"` + fraudDigest + `"}`,
		},
		{
			name:      "revision",
			arguments: []string{"fraud", "revision", "4"},
			method:    "GET",
			path:      "/v1/fraud/configuration/revisions/4",
			status:    200,
			response:  `{"version":4,"digest":"` + fraudDigest + `"}`,
		},
		{
			name:        "inputs",
			arguments:   []string{"fraud", "inputs", "--idempotency-key", "inputs-key"},
			body:        `{"verification_id":"ver_01ARZ3NDEKTSV4RRFFQ69G5FAV","evidence_id":"evd_01ARZ3NDEKTSV4RRFFQ69G5FAV","namespace":"crm","source_reference":"case-1","attributes":[{"kind":"subject","value":"crm-4815"}]}`,
			method:      "POST",
			path:        "/v1/fraud/inputs",
			idempotency: `"inputs-key"`,
			status:      200,
			response:    `{"version":4,"digest":"` + fraudDigest + `"}`,
		},
		{
			name:        "proposal",
			arguments:   []string{"fraud", "proposal", "--idempotency-key", "proposal-key"},
			body:        `{"receipt_digest":"` + fraudDigest + `","hypothesis":"repeated_device","signals":["device_reuse"]}`,
			method:      "POST",
			path:        "/v1/fraud/proposals",
			idempotency: `"proposal-key"`,
			status:      200,
			response:    `{"proposal":{"receipt_digest":"` + fraudDigest + `","hypothesis":"repeated_device","signals":["device_reuse"]}}`,
		},
		{
			name:      "proposal-get",
			arguments: []string{"fraud", "proposal-get", fraudDigest},
			method:    "GET",
			path:      "/v1/fraud/proposals/" + fraudDigest,
			status:    200,
			response:  `{"proposal":{"receipt_digest":"` + fraudDigest + `","hypothesis":"repeated_device","signals":["device_reuse"]}}`,
		},
		{
			name:      "receipt",
			arguments: []string{"fraud", "receipt", fraudDigest},
			method:    "GET",
			path:      "/v1/fraud/receipts/" + fraudDigest,
			status:    200,
			response:  `{"receipt":{"digest":"` + fraudDigest + `","verification_id":"ver_01ARZ3NDEKTSV4RRFFQ69G5FAV","configuration_version":4,"configuration_digest":"` + fraudDigest + `","at":"2026-09-08T12:00:00Z","findings":[]}}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requestBody []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != test.method || r.URL.Path != test.path {
					t.Errorf("request = %s %s, want %s %s", r.Method, r.URL.Path, test.method, test.path)
				}
				if r.URL.Query().Encode() != "" {
					t.Errorf("query = %q, want empty", r.URL.Query().Encode())
				}
				if got := r.Header.Get("Authorization"); got != "Bearer test-fraud-credential" {
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
			if strings.Contains(out.String()+diagnostics.String(), "test-fraud-credential") || strings.Contains(out.String()+diagnostics.String(), "environment-credential") {
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

func TestFraudCLIFailuresAndValidation(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "test-fraud-credential")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
		_, _ = fmt.Fprint(w, "private-server-response")
	}))
	defer server.Close()

	var out, diagnostics bytes.Buffer
	if code := bootstrap.Run([]string{"fraud", "config-get", "--api-url", server.URL}, &out, &diagnostics, buildinfo.Info{}); code != 1 || calls.Load() != 1 {
		t.Fatalf("runtime failure exit = %d calls = %d stderr = %s", code, calls.Load(), diagnostics.String())
	}
	if strings.Contains(diagnostics.String(), "Usage:") || strings.Contains(diagnostics.String(), "private-server-response") {
		t.Fatal("runtime failure leaked usage or response body")
	}
	if !strings.Contains(diagnostics.String(), "fraud API returned status 503") {
		t.Fatalf("runtime failure = %q", diagnostics.String())
	}

	configFile := filepath.Join(t.TempDir(), "configuration.json")
	if err := os.WriteFile(configFile, []byte(`{"expected_version":0,"configuration":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		args []string
	}{
		{"missing config-put body", []string{"fraud", "config-put", "--idempotency-key", "config-key"}},
		{"missing config-put idempotency key", []string{"fraud", "config-put", "--body-file", configFile}},
		{"zero revision", []string{"fraud", "revision", "0"}},
		{"non-numeric revision", []string{"fraud", "revision", "latest"}},
		{"missing inputs body", []string{"fraud", "inputs", "--idempotency-key", "inputs-key"}},
		{"missing inputs idempotency key", []string{"fraud", "inputs", "--body-file", configFile}},
		{"missing proposal body", []string{"fraud", "proposal", "--idempotency-key", "proposal-key"}},
		{"missing proposal idempotency key", []string{"fraud", "proposal", "--body-file", configFile}},
		{"short proposal digest", []string{"fraud", "proposal-get", "abc"}},
		{"uppercase receipt digest", []string{"fraud", "receipt", strings.ToUpper(fraudDigest)}},
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

func TestFraudCLIRequiresCredential(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = fmt.Fprint(w, `{"version":3}`)
	}))
	defer server.Close()

	var out, diagnostics bytes.Buffer
	if code := bootstrap.Run([]string{"fraud", "config-get", "--api-url", server.URL}, &out, &diagnostics, buildinfo.Info{}); code != 2 || calls.Load() != 0 {
		t.Fatalf("exit = %d calls = %d stderr = %s", code, calls.Load(), diagnostics.String())
	}
}
