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
	subjectID             = "sub_01ARZ3NDEKTSV4RRFFQ69G5FAV"
	subjectVerificationID = "ver_01ARZ3NDEKTSV4RRFFQ69G5FAV"
	subjectJSON           = `{"id":"` + subjectID + `","region":"eu","state":"active","version":2}`
)

func TestSubjectCLICommands(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "environment-credential")
	keyFile := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(keyFile, []byte("test-subject-credential\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name        string
		arguments   []string
		body        string
		inlineBody  bool
		method      string
		path        string
		query       url.Values
		idempotency string
		status      int
		response    string
	}{
		{
			name:        "create",
			arguments:   []string{"subject", "create", "--idempotency-key", "create-key"},
			body:        `{"external_reference":"crm-4815"}`,
			method:      "POST",
			path:        "/v1/subjects",
			idempotency: `"create-key"`,
			status:      201,
			response:    `{"subject":` + subjectJSON + `,"replayed":false}`,
		},
		{
			name:      "get",
			arguments: []string{"subject", "get", subjectID, "--reveal"},
			method:    "GET",
			path:      "/v1/subjects/" + subjectID,
			query:     url.Values{"reveal": {"true"}},
			status:    200,
			response:  `{"subject":` + subjectJSON + `}`,
		},
		{
			name:        "update",
			arguments:   []string{"subject", "update", subjectID, "--idempotency-key", "update-key"},
			body:        `{"expected_version":2,"state":"suspended"}`,
			method:      "PUT",
			path:        "/v1/subjects/" + subjectID,
			idempotency: `"update-key"`,
			status:      200,
			response:    `{"subject":` + subjectJSON + `}`,
		},
		{
			name:        "delete",
			arguments:   []string{"subject", "delete", subjectID, "--expected-version", "2", "--confirm", "--idempotency-key", "delete-key"},
			method:      "DELETE",
			path:        "/v1/subjects/" + subjectID,
			query:       url.Values{"expected_version": {"2"}},
			idempotency: `"delete-key"`,
			status:      202,
			response:    `{"deletion_id":"del_01ARZ3NDEKTSV4RRFFQ69G5FAV"}`,
		},
		{
			name:      "lookup",
			arguments: []string{"subject", "lookup"},
			body:      `{"external_reference":"crm-4815","limit":25}`,
			method:    "POST",
			path:      "/v1/subjects/lookup",
			status:    200,
			response:  `{"subjects":[` + subjectJSON + `],"next_cursor":""}`,
		},
		{
			name:        "records",
			arguments:   []string{"subject", "records", subjectID, "--idempotency-key", "record-key"},
			body:        `{"expected_version":2,"record":{"kind":"fact","name":"identity.name","source_record_ids":["obs_01ARZ3NDEKTSV4RRFFQ69G5FAV"],"normalization":"identity.exact.v1","collected_at":"2026-09-08T12:00:00Z","observed_at":"2026-09-08T12:00:00Z","valid_from":"2026-09-08T12:00:00Z","valid_until":"2027-09-08T12:00:00Z","retain_until":"2026-10-08T12:00:00Z"}}`,
			method:      "POST",
			path:        "/v1/subjects/" + subjectID + "/records",
			idempotency: `"record-key"`,
			status:      201,
			response:    `{"record_id":"fct_01ARZ3NDEKTSV4RRFFQ69G5FAV"}`,
		},
		{
			name:        "attach-verification",
			arguments:   []string{"subject", "attach-verification", subjectID, subjectVerificationID, "--expected-version", "2", "--idempotency-key", "attach-key"},
			body:        `{"expected_version":2}`,
			inlineBody:  true,
			method:      "PUT",
			path:        "/v1/subjects/" + subjectID + "/verifications/" + subjectVerificationID,
			idempotency: `"attach-key"`,
			status:      200,
			response:    subjectJSON,
		},
		{
			name:        "projection-rebuild",
			arguments:   []string{"subject", "projection-rebuild", subjectID, "--expected-version", "2", "--idempotency-key", "rebuild-key"},
			body:        `{"expected_version":2}`,
			inlineBody:  true,
			method:      "POST",
			path:        "/v1/subjects/" + subjectID + "/projection/rebuild",
			idempotency: `"rebuild-key"`,
			status:      200,
			response:    subjectJSON,
		},
		{
			name:      "identifier-lookup",
			arguments: []string{"subject", "identifier-lookup"},
			body:      `{"identifier":{"kind":"identifier","namespace":"crm","issuer":"tenant","value_type":"string","value":"4815"}}`,
			method:    "POST",
			path:      "/v1/identity/identifiers/lookup",
			status:    200,
			response:  `{"subjects":[` + subjectJSON + `],"next_cursor":""}`,
		},
		{
			name:        "configuration",
			arguments:   []string{"subject", "configuration", "--idempotency-key", "configure-key"},
			body:        `{"expected_version":0,"configuration":{"region":"eu","version":1}}`,
			method:      "PUT",
			path:        "/v1/identity/configuration",
			idempotency: `"configure-key"`,
			status:      200,
			response:    `{"configuration":{"region":"eu","version":1}}`,
		},
		{
			name:      "configuration-get",
			arguments: []string{"subject", "configuration-get"},
			method:    "GET",
			path:      "/v1/identity/configuration",
			status:    200,
			response:  `{"configuration":{"region":"eu","version":1}}`,
		},
		{
			name:      "configuration-revision",
			arguments: []string{"subject", "configuration-revision", "4"},
			method:    "GET",
			path:      "/v1/identity/configuration/revisions/4",
			status:    200,
			response:  `{"configuration":{"region":"eu","version":4}}`,
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
				if got := r.Header.Get("Authorization"); got != "Bearer test-subject-credential" {
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
			if test.body != "" && !test.inlineBody {
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
			if strings.Contains(out.String()+diagnostics.String(), "test-subject-credential") || strings.Contains(out.String()+diagnostics.String(), "environment-credential") {
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

func TestSubjectCLIFailuresAndValidation(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "test-subject-credential")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
		_, _ = fmt.Fprint(w, "private-server-response")
	}))
	defer server.Close()

	var out, diagnostics bytes.Buffer
	if code := bootstrap.Run([]string{"subject", "get", subjectID, "--api-url", server.URL}, &out, &diagnostics, buildinfo.Info{}); code != 1 || calls.Load() != 1 {
		t.Fatalf("runtime failure exit = %d calls = %d stderr = %s", code, calls.Load(), diagnostics.String())
	}
	if strings.Contains(diagnostics.String(), "Usage:") || strings.Contains(diagnostics.String(), "private-server-response") {
		t.Fatal("runtime failure leaked usage or response body")
	}
	if !strings.Contains(diagnostics.String(), "subject API returned status 503") {
		t.Fatalf("runtime failure = %q", diagnostics.String())
	}

	bodyFile := filepath.Join(t.TempDir(), "body.json")
	if err := os.WriteFile(bodyFile, []byte(`{"external_reference":"crm-4815"}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		args []string
	}{
		{"invalid subject id", []string{"subject", "get", "not-a-subject"}},
		{"missing create body", []string{"subject", "create", "--idempotency-key", "create-key"}},
		{"missing create idempotency key", []string{"subject", "create", "--body-file", bodyFile}},
		{"missing update body", []string{"subject", "update", subjectID, "--idempotency-key", "update-key"}},
		{"missing delete confirmation", []string{"subject", "delete", subjectID, "--expected-version", "2", "--idempotency-key", "delete-key"}},
		{"missing delete version", []string{"subject", "delete", subjectID, "--confirm", "--idempotency-key", "delete-key"}},
		{"invalid attach verification", []string{"subject", "attach-verification", subjectID, "not-a-verification", "--expected-version", "2", "--idempotency-key", "attach-key"}},
		{"missing attach version", []string{"subject", "attach-verification", subjectID, subjectVerificationID, "--idempotency-key", "attach-key"}},
		{"missing rebuild version", []string{"subject", "projection-rebuild", subjectID, "--idempotency-key", "rebuild-key"}},
		{"missing record body", []string{"subject", "records", subjectID, "--idempotency-key", "record-key"}},
		{"missing configuration body", []string{"subject", "configuration", "--idempotency-key", "configure-key"}},
		{"invalid configuration revision", []string{"subject", "configuration-revision", "0"}},
		{"missing lookup body", []string{"subject", "lookup"}},
		{"missing identifier body", []string{"subject", "identifier-lookup"}},
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

func TestSubjectCLIRequiresCredential(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = fmt.Fprint(w, subjectJSON)
	}))
	defer server.Close()

	var out, diagnostics bytes.Buffer
	if code := bootstrap.Run([]string{"subject", "get", subjectID, "--api-url", server.URL}, &out, &diagnostics, buildinfo.Info{}); code != 2 || calls.Load() != 0 {
		t.Fatalf("exit = %d calls = %d stderr = %s", code, calls.Load(), diagnostics.String())
	}
}

func TestSubjectCLIBodyFileValidation(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "test-subject-credential")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = fmt.Fprint(w, subjectJSON)
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
		code int
	}{
		{"invalid json", invalid, 2},
		{"oversized body", oversized, 2},
		{"missing body file", filepath.Join(t.TempDir(), "absent.json"), 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out, diagnostics bytes.Buffer
			args := []string{"subject", "create", "--idempotency-key", "create-key", "--body-file", test.file, "--api-url", server.URL}
			if code := bootstrap.Run(args, &out, &diagnostics, buildinfo.Info{}); code != test.code {
				t.Fatalf("exit = %d, want %d; stderr = %s", code, test.code, diagnostics.String())
			}
			if calls.Load() != 0 {
				t.Fatal("invalid body file reached the API")
			}
			if test.code == 2 && !strings.Contains(diagnostics.String(), "Usage:") {
				t.Fatalf("usage error did not print usage: %s", diagnostics.String())
			}
		})
	}
}
