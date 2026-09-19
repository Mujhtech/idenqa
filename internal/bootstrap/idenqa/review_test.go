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
	reviewCaseID     = "rvc_01ARZ3NDEKTSV4RRFFQ69G5FAV"
	reviewAppealID   = "apl_01ARZ3NDEKTSV4RRFFQ69G5FAV"
	reviewGrantID    = "grt_01ARZ3NDEKTSV4RRFFQ69G5FAV"
	reviewDecisionID = "dec_01ARZ3NDEKTSV4RRFFQ69G5FAV"
	reviewTokenID    = "ctk_01ARZ3NDEKTSV4RRFFQ69G5FAV" //nolint:gosec // deterministic test fixture identifier
	reviewCaseJSON   = `{"id":"` + reviewCaseID + `","verification_id":"ver_01ARZ3NDEKTSV4RRFFQ69G5FAV","region":"eu","oversight":"single","state":"open","finding_count":0,"version":2}`
)

func TestReviewCLICommands(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "environment-credential")
	keyFile := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(keyFile, []byte("test-review-credential\n"), 0600); err != nil {
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
		binary      bool
	}{
		{
			name:        "case open",
			arguments:   []string{"review", "case", "open", "--decision-id", reviewDecisionID, "--idempotency-key", "open-key"},
			method:      "POST",
			path:        "/v1/decisions/" + reviewDecisionID + "/review-cases",
			idempotency: `"open-key"`,
			status:      201,
			response:    `{"case_id":"` + reviewCaseID + `","version":1}`,
		},
		{
			name:      "case list",
			arguments: []string{"review", "case", "list", "--limit", "10", "--cursor", "next-cursor"},
			method:    "GET",
			path:      "/v1/review-cases",
			query:     url.Values{"limit": {"10"}, "cursor": {"next-cursor"}},
			status:    200,
			response:  `{"items":[],"has_more":false}`,
		},
		{
			name:      "case get",
			arguments: []string{"review", "case", "get", reviewCaseID},
			method:    "GET",
			path:      "/v1/review-cases/" + reviewCaseID,
			status:    200,
			response:  reviewCaseJSON,
		},
		{
			name:       "case claim",
			arguments:  []string{"review", "case", "claim", reviewCaseID, "--expected-version", "3"},
			body:       `{"expected_version":3}`,
			inlineBody: true,
			method:     "POST",
			path:       "/v1/review-cases/" + reviewCaseID + "/claim",
			status:     200,
			response:   reviewCaseJSON,
		},
		{
			name:      "case findings",
			arguments: []string{"review", "case", "findings", reviewCaseID},
			body:      `{"expected_version":2,"resolution":"satisfy","reason_code":"document_match","grant_ids":["` + reviewGrantID + `"]}`,
			method:    "POST",
			path:      "/v1/review-cases/" + reviewCaseID + "/findings",
			status:    200,
			response:  reviewCaseJSON,
		},
		{
			name:      "case evidence",
			arguments: []string{"review", "case", "evidence", reviewCaseID, "--expected-version", "2"},
			method:    "GET",
			path:      "/v1/review-cases/" + reviewCaseID + "/evidence",
			query:     url.Values{"expected_version": {"2"}},
			status:    200,
			response:  `{"items":[]}`,
		},
		{
			name:        "case grant",
			arguments:   []string{"review", "case", "grant", reviewCaseID, "--idempotency-key", "grant-key"},
			body:        `{"expected_version":2,"evidence_id":"evd_01ARZ3NDEKTSV4RRFFQ69G5FAV"}`,
			method:      "POST",
			path:        "/v1/review-cases/" + reviewCaseID + "/evidence-grants",
			idempotency: `"grant-key"`,
			status:      201,
			response:    `{"grant_id":"` + reviewGrantID + `","case_id":"` + reviewCaseID + `","evidence_id":"evd_01ARZ3NDEKTSV4RRFFQ69G5FAV","case_version":2,"expires_at":"2026-09-08T12:00:00Z"}`,
		},
		{
			name:      "case grant-content",
			arguments: []string{"review", "case", "grant-content", reviewGrantID},
			method:    "POST",
			path:      "/v1/review-evidence-grants/" + reviewGrantID + "/content",
			status:    200,
			response:  "\x89PNG\r\n\x1a\nreview-display",
			binary:    true,
		},
		{
			name:      "case recaptures",
			arguments: []string{"review", "case", "recaptures", reviewCaseID},
			method:    "GET",
			path:      "/v1/review-cases/" + reviewCaseID + "/recaptures",
			status:    200,
			response:  `{"items":[]}`,
		},
		{
			name:        "case recapture",
			arguments:   []string{"review", "case", "recapture", reviewCaseID, "--idempotency-key", "recapture-key"},
			body:        `{"expected_version":2}`,
			method:      "POST",
			path:        "/v1/review-cases/" + reviewCaseID + "/recaptures",
			idempotency: `"recapture-key"`,
			status:      201,
			response:    `{"case_id":"` + reviewCaseID + `","verification_id":"ver_01ARZ3NDEKTSV4RRFFQ69G5FAV","capture_token":"idq_cap_v1_example","capture_token_id":"` + reviewTokenID + `","capture_token_expires_at":"2026-09-08T12:00:00Z","outcome_token":"idq_out_v1_example","outcome_token_expires_at":"2026-09-09T12:00:00Z","verification_expires_at":"2026-09-08T12:00:00Z"}`,
		},
		{
			name:        "case recapture-renew",
			arguments:   []string{"review", "case", "recapture-renew", reviewCaseID, "--idempotency-key", "renew-key"},
			body:        `{"expected_version":2,"expected_capture_token_id":"` + reviewTokenID + `"}`,
			method:      "POST",
			path:        "/v1/review-cases/" + reviewCaseID + "/recaptures/renew",
			idempotency: `"renew-key"`,
			status:      200,
			response:    `{"case_id":"` + reviewCaseID + `","verification_id":"ver_01ARZ3NDEKTSV4RRFFQ69G5FAV","capture_token":"idq_cap_v1_renewed","capture_token_id":"` + reviewTokenID + `","capture_token_expires_at":"2026-09-08T12:00:00Z","outcome_token":"idq_out_v1_example","outcome_token_expires_at":"2026-09-09T12:00:00Z","verification_expires_at":"2026-09-08T12:00:00Z"}`,
		},
		{
			name:        "case recapture-ack",
			arguments:   []string{"review", "case", "recapture-ack", reviewCaseID, "--idempotency-key", "ack-key"},
			body:        `{"expected_version":3,"decision_id":"` + reviewDecisionID + `"}`,
			method:      "POST",
			path:        "/v1/review-cases/" + reviewCaseID + "/recaptures/acknowledgements",
			idempotency: `"ack-key"`,
			status:      200,
			response:    `{"case_id":"` + reviewCaseID + `","case_version":3,"verification_id":"ver_01ARZ3NDEKTSV4RRFFQ69G5FAV","decision_id":"` + reviewDecisionID + `","actor_key_id":"key_example","reviewer_id":"reviewer.alex","acknowledged_at":"2026-09-08T12:00:00Z"}`,
		},
		{
			name:        "case recapture-reevaluate",
			arguments:   []string{"review", "case", "recapture-reevaluate", reviewCaseID, "--idempotency-key", "reevaluate-key"},
			body:        `{"expected_version":3,"decision_id":"` + reviewDecisionID + `"}`,
			method:      "POST",
			path:        "/v1/review-cases/" + reviewCaseID + "/recaptures/reevaluations",
			idempotency: `"reevaluate-key"`,
			status:      202,
			response:    `{"case_id":"` + reviewCaseID + `","case_version":4,"source_version":3,"decision_id":"` + reviewDecisionID + `","actor_key_id":"key_example","reviewer_id":"reviewer.alex","requested_at":"2026-09-08T12:00:00Z"}`,
		},
		{
			name:      "case correction",
			arguments: []string{"review", "case", "correction", reviewCaseID},
			body:      `{"expected_version":3,"superseding_decision_id":"` + reviewDecisionID + `"}`,
			method:    "POST",
			path:      "/v1/review-cases/" + reviewCaseID + "/corrections",
			status:    200,
			response:  reviewCaseJSON,
		},
		{
			name:        "case correction-evaluate",
			arguments:   []string{"review", "case", "correction-evaluate", reviewCaseID, "--idempotency-key", "evaluate-key"},
			body:        `{"expected_version":3}`,
			method:      "POST",
			path:        "/v1/review-cases/" + reviewCaseID + "/corrections/evaluate",
			idempotency: `"evaluate-key"`,
			status:      200,
			response:    `{"case_id":"` + reviewCaseID + `","version":4}`,
		},
		{
			name:        "case arbitration",
			arguments:   []string{"review", "case", "arbitration", reviewCaseID, "--idempotency-key", "arbitration-key"},
			body:        `{"expected_version":3,"resolution":"not_satisfy","reason_code":"document_mismatch"}`,
			method:      "POST",
			path:        "/v1/review-cases/" + reviewCaseID + "/arbitrations",
			idempotency: `"arbitration-key"`,
			status:      200,
			response:    `{"case_id":"` + reviewCaseID + `","version":4}`,
		},
		{
			name:        "appeal open",
			arguments:   []string{"review", "appeal", "open", reviewCaseID, "--idempotency-key", "appeal-key"},
			body:        `{"deadline":"2026-09-08T12:00:00Z"}`,
			method:      "POST",
			path:        "/v1/review-cases/" + reviewCaseID + "/appeals",
			idempotency: `"appeal-key"`,
			status:      201,
			response:    `{"id":"` + reviewAppealID + `","case_id":"` + reviewCaseID + `","state":"open","version":1,"deadline":"2026-09-08T12:00:00Z"}`,
		},
		{
			name:      "appeal get",
			arguments: []string{"review", "appeal", "get", reviewAppealID},
			method:    "GET",
			path:      "/v1/appeals/" + reviewAppealID,
			status:    200,
			response:  `{"id":"` + reviewAppealID + `","case_id":"` + reviewCaseID + `","state":"open","version":1,"deadline":"2026-09-08T12:00:00Z"}`,
		},
		{
			name:      "appeal assign",
			arguments: []string{"review", "appeal", "assign", reviewAppealID},
			body:      `{"expected_version":1}`,
			method:    "POST",
			path:      "/v1/appeals/" + reviewAppealID + "/assign",
			status:    200,
			response:  `{"id":"` + reviewAppealID + `","case_id":"` + reviewCaseID + `","state":"assigned","version":2,"deadline":"2026-09-08T12:00:00Z"}`,
		},
		{
			name:      "appeal resolve",
			arguments: []string{"review", "appeal", "resolve", reviewAppealID},
			body:      `{"expected_version":2,"outcome":"overturned","reason_code":"document_match","superseding_decision_id":"` + reviewDecisionID + `"}`,
			method:    "POST",
			path:      "/v1/appeals/" + reviewAppealID + "/resolve",
			status:    200,
			response:  `{"id":"` + reviewAppealID + `","case_id":"` + reviewCaseID + `","state":"resolved","outcome":"overturned","version":3,"deadline":"2026-09-08T12:00:00Z"}`,
		},
		{
			name:      "appeal withdraw",
			arguments: []string{"review", "appeal", "withdraw", reviewAppealID},
			body:      `{"expected_version":2}`,
			method:    "POST",
			path:      "/v1/appeals/" + reviewAppealID + "/withdraw",
			status:    200,
			response:  `{"id":"` + reviewAppealID + `","case_id":"` + reviewCaseID + `","state":"withdrawn","version":3,"deadline":"2026-09-08T12:00:00Z"}`,
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
				if got := r.Header.Get("Authorization"); got != "Bearer test-review-credential" {
					t.Errorf("authorization = %q", got)
				}
				if test.idempotency != "" {
					if got := r.Header.Get("Idempotency-Key"); got != test.idempotency {
						t.Errorf("idempotency key = %q, want %q", got, test.idempotency)
					}
				}
				requestBody, _ = io.ReadAll(r.Body)
				if test.binary {
					w.Header().Set("Content-Type", "image/png")
				}
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
			if strings.Contains(out.String()+diagnostics.String(), "test-review-credential") || strings.Contains(out.String()+diagnostics.String(), "environment-credential") {
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
			if test.binary {
				if out.String() != test.response {
					t.Fatalf("stdout = %q, want %q", out.String(), test.response)
				}
				return
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

func TestReviewCLIFailuresAndValidation(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "test-review-credential")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
		_, _ = fmt.Fprint(w, "private-server-response")
	}))
	defer server.Close()

	var out, diagnostics bytes.Buffer
	if code := bootstrap.Run([]string{"review", "case", "get", reviewCaseID, "--api-url", server.URL}, &out, &diagnostics, buildinfo.Info{}); code != 1 || calls.Load() != 1 {
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
		{"missing claim version", []string{"review", "case", "claim", reviewCaseID}},
		{"zero claim version", []string{"review", "case", "claim", reviewCaseID, "--expected-version", "0"}},
		{"missing evidence version", []string{"review", "case", "evidence", reviewCaseID}},
		{"missing findings body", []string{"review", "case", "findings", reviewCaseID}},
		{"missing grant body", []string{"review", "case", "grant", reviewCaseID, "--idempotency-key", "grant-key"}},
		{"missing open idempotency key", []string{"review", "case", "open", "--decision-id", reviewDecisionID}},
		{"invalid open decision id", []string{"review", "case", "open", "--decision-id", "not-a-decision", "--idempotency-key", "open-key"}},
		{"invalid case id", []string{"review", "case", "get", "not-a-case"}},
		{"invalid grant id", []string{"review", "case", "grant-content", "not-a-grant"}},
		{"invalid appeal id", []string{"review", "appeal", "get", "not-an-appeal"}},
		{"limit below range", []string{"review", "case", "list", "--limit", "0"}},
		{"limit above range", []string{"review", "case", "list", "--limit", "101"}},
		{"missing appeal body", []string{"review", "appeal", "open", reviewCaseID, "--idempotency-key", "appeal-key"}},
		{"missing appeal assign body", []string{"review", "appeal", "assign", reviewAppealID}},
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

func TestReviewCLIRequiresCredential(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = fmt.Fprint(w, `{"items":[]}`)
	}))
	defer server.Close()

	var out, diagnostics bytes.Buffer
	if code := bootstrap.Run([]string{"review", "case", "list", "--api-url", server.URL}, &out, &diagnostics, buildinfo.Info{}); code != 2 || calls.Load() != 0 {
		t.Fatalf("exit = %d calls = %d stderr = %s", code, calls.Load(), diagnostics.String())
	}
}

func TestReviewCLIBodyFileValidation(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "test-review-credential")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = fmt.Fprint(w, reviewCaseJSON)
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
			args := []string{"review", "case", "findings", reviewCaseID, "--body-file", test.file, "--api-url", server.URL}
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

func TestReviewCLIReadsBodyFromStdin(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "test-review-credential")
	var requestBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestBody, _ = io.ReadAll(r.Body)
		_, _ = fmt.Fprint(w, reviewCaseJSON)
	}))
	defer server.Close()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	if _, err := writer.WriteString(`{"expected_version":2}`); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	previous := os.Stdin
	os.Stdin = reader
	defer func() { os.Stdin = previous }()

	var out, diagnostics bytes.Buffer
	code := bootstrap.Run([]string{"review", "case", "findings", reviewCaseID, "--body-file", "-", "--api-url", server.URL}, &out, &diagnostics, buildinfo.Info{})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, diagnostics.String())
	}
	if string(requestBody) != `{"expected_version":2}` {
		t.Fatalf("request body = %q", requestBody)
	}
}
