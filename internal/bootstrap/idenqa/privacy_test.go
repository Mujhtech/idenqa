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

const (
	privacyDeletionID       = "del_01ARZ3NDEKTSV4RRFFQ69G5FAV"
	privacyAggregateID      = "ver_01ARZ3NDEKTSV4RRFFQ69G5FAV"
	privacyDeletionJSON     = `{"id":"` + privacyDeletionID + `","aggregate_id":"` + privacyAggregateID + `","region":"ng-1","state":"failed","target_count":2,"backup_expires_at":"2026-10-11T00:00:00Z","failure_class":"target_unavailable","version":3,"requested_at":"2026-09-06T00:00:00Z","updated_at":"2026-09-06T00:05:00Z","targets":[{"kind":"raw_evidence","reference":"3f2a0c9d1b4e6a8c0d2f4b6a","state":"deleted"},{"kind":"derived_evidence","reference":"9c1b7e5a2d4f6a8c0e2b4d6f","state":"failed","failure_class":"target_unavailable"}],"holds":[]}`
	privacyResolutionJSON   = `{"aggregate_id":"` + privacyAggregateID + `","records":[{"id":"evd_01ARZ3NDEKTSV4RRFFQ69G5FAV","data_class":"raw_evidence","region":"ng-1","duration_seconds":2592000,"expires_at":"2026-10-06T00:00:00Z"}],"holds":[]}`
	privacyDeletionListJSON = `{"data":[` + privacyDeletionJSON + `],"page":{"has_more":false}}`
)

func TestPrivacyCLICommands(t *testing.T) {
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
		status    int
		response  string
	}{
		{
			name:      "deletion status",
			arguments: []string{"privacy", "deletion-status", "--limit", "10", "--cursor", "next-cursor", "--aggregate-id", privacyAggregateID},
			method:    "GET",
			path:      "/v1/deletions",
			query:     url.Values{"limit": {"10"}, "cursor": {"next-cursor"}, "aggregate_id": {privacyAggregateID}},
			status:    200,
			response:  privacyDeletionListJSON,
		},
		{
			name:      "deletion status default limit",
			arguments: []string{"privacy", "deletion-status"},
			method:    "GET",
			path:      "/v1/deletions",
			query:     url.Values{"limit": {"25"}},
			status:    200,
			response:  privacyDeletionListJSON,
		},
		{
			name:      "deletion get",
			arguments: []string{"privacy", "deletion", privacyDeletionID},
			method:    "GET",
			path:      "/v1/deletions/" + privacyDeletionID,
			status:    200,
			response:  privacyDeletionJSON,
		},
		{
			name:      "deletion retry",
			arguments: []string{"privacy", "deletion-retry", privacyDeletionID},
			method:    "POST",
			path:      "/v1/deletions/" + privacyDeletionID + "/run",
			status:    200,
			response:  privacyDeletionJSON,
		},
		{
			name:      "retention resolution",
			arguments: []string{"privacy", "retention", "--aggregate-id", privacyAggregateID},
			method:    "GET",
			path:      "/v1/retention/resolutions",
			query:     url.Values{"aggregate_id": {privacyAggregateID}},
			status:    200,
			response:  privacyResolutionJSON,
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
				if got := r.Header.Get("Authorization"); got != "Bearer test-privacy-credential" {
					t.Errorf("authorization = %q", got)
				}
				requestBody, _ = io.ReadAll(r.Body)
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
			if len(requestBody) != 0 {
				t.Fatalf("request body = %q, want empty", requestBody)
			}
			if strings.Contains(out.String()+diagnostics.String(), "test-privacy-credential") || strings.Contains(out.String()+diagnostics.String(), "environment-credential") {
				t.Fatal("credential leaked")
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

func TestPrivacyCLIFailuresAndValidation(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "test-privacy-credential")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
		_, _ = w.Write([]byte("private-server-response"))
	}))
	defer server.Close()

	var out, diagnostics bytes.Buffer
	if code := bootstrap.Run([]string{"privacy", "deletion", privacyDeletionID, "--api-url", server.URL}, &out, &diagnostics, buildinfo.Info{}); code != 1 || calls.Load() != 1 {
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
		{"invalid deletion id", []string{"privacy", "deletion", "not-a-deletion"}},
		{"invalid retry deletion id", []string{"privacy", "deletion-retry", "not-a-deletion"}},
		{"limit below range", []string{"privacy", "deletion-status", "--limit", "0"}},
		{"limit above range", []string{"privacy", "deletion-status", "--limit", "101"}},
		{"missing retention aggregate", []string{"privacy", "retention"}},
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

func TestPrivacyCLIRequiresCredential(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(privacyDeletionListJSON))
	}))
	defer server.Close()

	var out, diagnostics bytes.Buffer
	if code := bootstrap.Run([]string{"privacy", "deletion-status", "--api-url", server.URL}, &out, &diagnostics, buildinfo.Info{}); code != 2 || calls.Load() != 0 {
		t.Fatalf("exit = %d calls = %d stderr = %s", code, calls.Load(), diagnostics.String())
	}
}
