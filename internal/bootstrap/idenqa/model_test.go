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

const modelRegistryJSON = `{"name":"pad","version":3,"latest_model_revision":1,"latest_threshold_revision":1,"active":{"model_revision":1,"threshold_revision":1,"region":"ng"},"updated_at":"2026-09-06T00:00:00Z"}`

func TestModelCLICommands(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "environment-credential")
	keyFile := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(keyFile, []byte("test-model-credential\n"), 0600); err != nil {
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
			name:      "model get",
			arguments: []string{"model", "get", "pad"},
			method:    "GET",
			path:      "/v1/models/pad",
			status:    200,
			response:  modelRegistryJSON,
		},
		{
			name:      "model revision",
			arguments: []string{"model", "revision", "pad", "model", "1"},
			method:    "GET",
			path:      "/v1/models/pad/revisions/model/1",
			status:    200,
			response:  `{"kind":"model","revision":1,"digest":"sha256:` + strings.Repeat("a", 64) + `","created_at":"2026-09-06T00:00:00Z"}`,
		},
		{
			name:      "model history",
			arguments: []string{"model", "history", "pad", "--before", "4", "--limit", "10"},
			method:    "GET",
			path:      "/v1/models/pad/history",
			query:     url.Values{"before": {"4"}, "limit": {"10"}},
			status:    200,
			response:  `[]`,
		},
		{
			name:        "model register",
			arguments:   []string{"model", "register", "pad", "--idempotency-key", "register-key"},
			body:        `{"expected_version":0,"reason":"evaluation","registration":{"owner":"fixture"}}`,
			method:      "POST",
			path:        "/v1/models/pad/register",
			idempotency: `"register-key"`,
			status:      200,
			response:    `{"state":` + modelRegistryJSON + `,"actor_id":"key_example","operation":"register","reason":"evaluation","replayed":false}`,
		},
		{
			name:        "model threshold",
			arguments:   []string{"model", "threshold", "pad", "--idempotency-key", "threshold-key"},
			body:        `{"expected_version":1,"reason":"evaluation","thresholds":{"score_name":"real_score"}}`,
			method:      "POST",
			path:        "/v1/models/pad/threshold",
			idempotency: `"threshold-key"`,
			status:      200,
			response:    `{"state":` + modelRegistryJSON + `,"actor_id":"key_example","operation":"threshold","reason":"evaluation","replayed":false}`,
		},
		{
			name:        "model activate",
			arguments:   []string{"model", "activate", "pad", "--idempotency-key", "activate-key"},
			body:        `{"expected_version":2,"reason":"evaluation","deployment":{"model_revision":1,"threshold_revision":1,"region":"ng"}}`,
			method:      "POST",
			path:        "/v1/models/pad/activate",
			idempotency: `"activate-key"`,
			status:      200,
			response:    `{"state":` + modelRegistryJSON + `,"actor_id":"key_example","operation":"activate","reason":"evaluation","replayed":true}`,
		},
		{
			name:        "model rollback",
			arguments:   []string{"model", "rollback", "pad", "--idempotency-key", "rollback-key"},
			body:        `{"expected_version":4,"reason":"evaluation","deployment":{"model_revision":1,"threshold_revision":1,"region":"ng"}}`,
			method:      "POST",
			path:        "/v1/models/pad/rollback",
			idempotency: `"rollback-key"`,
			status:      200,
			response:    `{"state":` + modelRegistryJSON + `,"actor_id":"key_example","operation":"rollback","reason":"evaluation","replayed":false}`,
		},
		{
			name:        "model retire",
			arguments:   []string{"model", "retire", "pad", "--confirm", "--idempotency-key", "retire-key"},
			body:        `{"expected_version":5,"reason":"evaluation"}`,
			method:      "POST",
			path:        "/v1/models/pad/retire",
			idempotency: `"retire-key"`,
			status:      200,
			response:    `{"state":{"name":"pad","version":6,"latest_model_revision":1,"latest_threshold_revision":1,"updated_at":"2026-09-06T00:00:00Z"},"actor_id":"key_example","operation":"retire","reason":"evaluation","replayed":false}`,
		},
		{
			name:      "model validate",
			arguments: []string{"model", "validate", "pad"},
			body:      `{"operation":"register","expected_version":0,"reason":"evaluation","registration":{"owner":"fixture"}}`,
			method:    "POST",
			path:      "/v1/models/pad/validate",
			status:    200,
			response:  `{"accepted":true,"operation":"register","reason_codes":[]}`,
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
				if got := r.Header.Get("Authorization"); got != "Bearer test-model-credential" {
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
			args := append([]string(nil), arguments...)
			args = append(args, "--api-url", server.URL, "--api-key-file", keyFile)
			if code := bootstrap.Run(args, &out, &diagnostics, buildinfo.Info{}); code != 0 {
				t.Fatalf("exit = %d, stderr = %s", code, diagnostics.String())
			}
			if strings.Contains(out.String()+diagnostics.String(), "test-model-credential") || strings.Contains(out.String()+diagnostics.String(), "environment-credential") {
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

func TestModelCLIFailuresAndValidation(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "test-model-credential")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
		_, _ = fmt.Fprint(w, "private-server-response")
	}))
	defer server.Close()

	var out, diagnostics bytes.Buffer
	if code := bootstrap.Run([]string{"model", "get", "pad", "--api-url", server.URL}, &out, &diagnostics, buildinfo.Info{}); code != 1 || calls.Load() != 1 {
		t.Fatalf("runtime failure exit = %d calls = %d stderr = %s", code, calls.Load(), diagnostics.String())
	}
	if strings.Contains(diagnostics.String(), "Usage:") || strings.Contains(diagnostics.String(), "private-server-response") {
		t.Fatal("runtime failure leaked usage or response body")
	}
	if !strings.Contains(diagnostics.String(), "status 503") {
		t.Fatalf("runtime failure = %q", diagnostics.String())
	}

	for _, test := range []struct {
		name string
		args []string
	}{
		{"retire without confirmation", []string{"model", "retire", "pad", "--idempotency-key", "retire-key"}},
		{"missing idempotency key", []string{"model", "register", "pad"}},
		{"missing body", []string{"model", "threshold", "pad", "--idempotency-key", "threshold-key"}},
		{"missing validate body", []string{"model", "validate", "pad"}},
		{"invalid model name", []string{"model", "get", "Pad"}},
		{"invalid revision kind", []string{"model", "revision", "pad", "weights", "1"}},
		{"zero revision", []string{"model", "revision", "pad", "model", "0"}},
		{"negative before", []string{"model", "history", "pad", "--before", "-1"}},
		{"limit below range", []string{"model", "history", "pad", "--limit", "0"}},
		{"limit above range", []string{"model", "history", "pad", "--limit", "101"}},
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

func TestModelCLIBodyFileValidation(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "test-model-credential")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = fmt.Fprint(w, modelRegistryJSON)
	}))
	defer server.Close()

	invalid := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(invalid, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	oversized := filepath.Join(t.TempDir(), "oversized.json")
	if err := os.WriteFile(oversized, bytes.Repeat([]byte{'x'}, 1<<20+1), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		file string
	}{
		{"invalid json", invalid},
		{"oversized body", oversized},
		{"missing body file", filepath.Join(t.TempDir(), "absent.json")},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out, diagnostics bytes.Buffer
			args := []string{"model", "register", "pad", "--idempotency-key", "register-key", "--body-file", test.file, "--api-url", server.URL}
			code := bootstrap.Run(args, &out, &diagnostics, buildinfo.Info{})
			if code == 2 && !strings.Contains(diagnostics.String(), "Usage:") {
				t.Fatalf("usage error did not print usage: %s", diagnostics.String())
			}
			if test.name == "missing body file" && (code != 1 || calls.Load() != 0) {
				t.Fatalf("unreadable body file exit = %d calls = %d stderr = %s", code, calls.Load(), diagnostics.String())
			}
			if test.name != "missing body file" && (code != 2 || calls.Load() != 0) {
				t.Fatalf("invalid body exit = %d calls = %d stderr = %s", code, calls.Load(), diagnostics.String())
			}
		})
	}
}
