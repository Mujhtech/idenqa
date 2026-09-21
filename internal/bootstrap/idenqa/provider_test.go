package idenqa_test

import (
	"bytes"
	"encoding/json"
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

const providerRegistrationJSON = `{"id":"pvr_01M11HEQG00000000000000000","adapter_id":"dojah","region":"africa","configuration":{"provider_id":"pvd_01M11HEQG00000000000000000","schema_digest":"sha256:284a4418399974a7d1ce691f96ea534e1cd97ee1a9bc0d09de25d4a3b9d48969","secret_reference":"secret://provider/dojah/tenant","credential_version":"v1"},"enabled":false,"version":1,"actor_id":"key_01M11HEQG00000000000000000","created_at":"2026-09-20T00:00:00Z","updated_at":"2026-09-20T00:00:00Z"}`

const providerWriteJSON = `{"adapter_id":"dojah","region":"africa","configuration":{"provider_id":"pvd_01M11HEQG00000000000000000","schema_digest":"sha256:284a4418399974a7d1ce691f96ea534e1cd97ee1a9bc0d09de25d4a3b9d48969","secret_reference":"secret://provider/dojah/tenant","credential_version":"v1"}}`

func TestProviderCLICommands(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "environment-credential")
	keyFile := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(keyFile, []byte("test-provider-credential\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name        string
		arguments   []string
		body        string
		wantBody    string
		method      string
		path        string
		query       url.Values
		idempotency string
		status      int
		response    string
	}{
		{
			name:      "provider list",
			arguments: []string{"provider", "list", "--limit", "10", "--cursor", "opaque"},
			method:    "GET",
			path:      "/v1/providers",
			query:     url.Values{"limit": {"10"}, "cursor": {"opaque"}},
			status:    200,
			response:  `{"data":[],"page":{"has_more":false}}`,
		},
		{
			name:      "provider get",
			arguments: []string{"provider", "get", "pvr_01M11HEQG00000000000000000"},
			method:    "GET",
			path:      "/v1/providers/pvr_01M11HEQG00000000000000000",
			status:    200,
			response:  providerRegistrationJSON,
		},
		{
			name:        "provider register",
			arguments:   []string{"provider", "register", "pvr_01M11HEQG00000000000000000", "--reason", "onboarding", "--idempotency-key", "register-key"},
			body:        providerWriteJSON,
			wantBody:    `{"reason":"onboarding","registration":` + providerWriteJSON + `}`,
			method:      "POST",
			path:        "/v1/providers",
			idempotency: `"register-key"`,
			status:      200,
			response:    `{"registration":` + providerRegistrationJSON + `,"operation":"create","reason":"onboarding","replayed":false}`,
		},
		{
			name:      "provider update",
			arguments: []string{"provider", "update", "pvr_01M11HEQG00000000000000000", "--reason", "rotation", "--expected-version", "1"},
			body:      providerWriteJSON,
			wantBody:  `{"expected_version":1,"reason":"rotation","registration":` + providerWriteJSON + `}`,
			method:    "PUT",
			path:      "/v1/providers/pvr_01M11HEQG00000000000000000",
			status:    200,
			response:  `{"registration":` + providerRegistrationJSON + `,"operation":"update","reason":"rotation","replayed":false}`,
		},
		{
			name:      "provider validate",
			arguments: []string{"provider", "validate", "pvr_01M11HEQG00000000000000000"},
			body:      providerWriteJSON,
			wantBody:  providerWriteJSON,
			method:    "POST",
			path:      "/v1/providers/pvr_01M11HEQG00000000000000000/validate",
			status:    200,
			response:  `{"accepted":true,"reason_codes":["accepted"]}`,
		},
		{
			name:      "provider enable",
			arguments: []string{"provider", "enable", "pvr_01M11HEQG00000000000000000", "--reason", "enable", "--expected-version", "1"},
			wantBody:  `{"expected_version":1,"reason":"enable"}`,
			method:    "POST",
			path:      "/v1/providers/pvr_01M11HEQG00000000000000000/enable",
			status:    200,
			response:  `{"registration":` + providerRegistrationJSON + `,"operation":"enable","reason":"enable","replayed":false}`,
		},
		{
			name:      "provider disable",
			arguments: []string{"provider", "disable", "pvr_01M11HEQG00000000000000000", "--reason", "disable", "--expected-version", "2"},
			wantBody:  `{"expected_version":2,"reason":"disable"}`,
			method:    "POST",
			path:      "/v1/providers/pvr_01M11HEQG00000000000000000/disable",
			status:    200,
			response:  `{"registration":` + providerRegistrationJSON + `,"operation":"disable","reason":"disable","replayed":false}`,
		},
		{
			name:      "provider rotate credential",
			arguments: []string{"provider", "rotate-credential", "pvr_01M11HEQG00000000000000000", "--reason", "rotation", "--expected-version", "3", "--secret-reference", "secret://aws/prod/dojah/tenant", "--credential-version", "v2"},
			wantBody:  `{"expected_version":3,"reason":"rotation","credential":{"secret_reference":"secret://aws/prod/dojah/tenant","credential_version":"v2"}}`,
			method:    "POST",
			path:      "/v1/providers/pvr_01M11HEQG00000000000000000/rotate-credential",
			status:    200,
			response:  `{"registration":` + providerRegistrationJSON + `,"operation":"rotate-credential","reason":"rotation","replayed":false}`,
		},
		{
			name:      "provider health",
			arguments: []string{"provider", "health", "pvr_01M11HEQG00000000000000000"},
			method:    "GET",
			path:      "/v1/providers/pvr_01M11HEQG00000000000000000/health",
			status:    200,
			response:  `{"registration_id":"pvr_01M11HEQG00000000000000000","adapter_id":"dojah","requests":0,"pending_dispatches":0,"completed_dispatches":0,"failed_dispatches":0}`,
		},
		{
			name:      "provider simulate failure",
			arguments: []string{"provider", "simulate-failure", "pvr_01M11HEQG00000000000000000"},
			body:      `{"class":"unavailable","code":"provider_unavailable"}`,
			wantBody:  `{"class":"unavailable","code":"provider_unavailable"}`,
			method:    "POST",
			path:      "/v1/providers/pvr_01M11HEQG00000000000000000/failure-simulations",
			status:    200,
			response:  `{"class":"unavailable","code":"provider_unavailable","retry":"backoff","retry_after_seconds":1,"attempt_state":"failed","check_state":"failed","produces_identity_outcome":false}`,
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
				if got := r.Header.Get("Authorization"); got != "Bearer test-provider-credential" {
					t.Errorf("authorization = %q", got)
				}
				if got, want := r.Header.Get("Idempotency-Key"), test.idempotency; got != want {
					t.Errorf("idempotency key = %q, want %q", got, want)
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
			args := append(append([]string(nil), arguments...), "--api-url", server.URL, "--api-key-file", keyFile)
			if code := bootstrap.Run(args, &out, &diagnostics, buildinfo.Info{}); code != 0 {
				t.Fatalf("exit = %d, stderr = %s", code, diagnostics.String())
			}
			if strings.Contains(out.String()+diagnostics.String(), "test-provider-credential") || strings.Contains(out.String()+diagnostics.String(), "environment-credential") {
				t.Fatal("credential leaked")
			}
			if test.wantBody != "" {
				var got, want any
				if err := json.Unmarshal(requestBody, &got); err != nil {
					t.Fatalf("request body = %q: %v", requestBody, err)
				}
				if err := json.Unmarshal([]byte(test.wantBody), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("request body = %q, want %q", requestBody, test.wantBody)
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

func TestProviderCLIFailuresAndValidation(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "test-provider-credential")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
		_, _ = io.WriteString(w, "private-server-response")
	}))
	defer server.Close()

	var out, diagnostics bytes.Buffer
	if code := bootstrap.Run([]string{"provider", "health", "pvr_01M11HEQG00000000000000000", "--api-url", server.URL}, &out, &diagnostics, buildinfo.Info{}); code != 1 || calls.Load() != 1 {
		t.Fatalf("runtime failure exit = %d calls = %d stderr = %s", code, calls.Load(), diagnostics.String())
	}
	if !strings.Contains(diagnostics.String(), "status 503") || strings.Contains(diagnostics.String(), "private-server-response") {
		t.Fatalf("runtime failure = %q", diagnostics.String())
	}

	bodyFile := filepath.Join(t.TempDir(), "body.json")
	if err := os.WriteFile(bodyFile, []byte(providerWriteJSON), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		args []string
	}{
		{"invalid provider id", []string{"provider", "get", "pvd_01M11HEQG00000000000000000"}},
		{"register without key", []string{"provider", "register", "pvr_01M11HEQG00000000000000000", "--reason", "onboarding", "--body-file", bodyFile}},
		{"register without reason", []string{"provider", "register", "pvr_01M11HEQG00000000000000000", "--idempotency-key", "register-key", "--body-file", bodyFile}},
		{"register without body", []string{"provider", "register", "pvr_01M11HEQG00000000000000000", "--reason", "onboarding", "--idempotency-key", "register-key"}},
		{"update without version", []string{"provider", "update", "pvr_01M11HEQG00000000000000000", "--reason", "rotation", "--body-file", bodyFile}},
		{"enable without version", []string{"provider", "enable", "pvr_01M11HEQG00000000000000000", "--reason", "enable"}},
		{"disable without reason", []string{"provider", "disable", "pvr_01M11HEQG00000000000000000", "--expected-version", "1"}},
		{"simulate without body", []string{"provider", "simulate-failure", "pvr_01M11HEQG00000000000000000"}},
		{"list limit above range", []string{"provider", "list", "--limit", "101"}},
		{"list limit below range", []string{"provider", "list", "--limit", "0"}},
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
