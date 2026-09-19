package idenqa_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	bootstrap "github.com/Mujhtech/idenqa/internal/bootstrap/idenqa"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
)

const acceptedCommandID = "acc_01ARZ3NDEKTSV4RRFFQ69G5FAV"

func TestProposalExecuteCLI(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "environment-credential")
	var calls atomic.Int32
	var requestBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/accepted-commands/"+acceptedCommandID+"/execute" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Encode() != "" {
			t.Errorf("query = %q, want empty", r.URL.Query().Encode())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer environment-credential" {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "" {
			t.Errorf("unexpected idempotency key = %q", got)
		}
		requestBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	var out, diagnostics bytes.Buffer
	code := bootstrap.Run([]string{"proposal", "execute", acceptedCommandID, "--api-url", server.URL}, &out, &diagnostics, buildinfo.Info{})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, diagnostics.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
	if len(requestBody) != 0 {
		t.Fatalf("request body = %q, want empty", requestBody)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if strings.Contains(out.String()+diagnostics.String(), "environment-credential") {
		t.Fatal("credential leaked")
	}
}

func TestProposalExecuteCLIFailuresAndValidation(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "test-proposal-credential")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
		_, _ = fmt.Fprint(w, "private-server-response")
	}))
	defer server.Close()

	var out, diagnostics bytes.Buffer
	if code := bootstrap.Run([]string{"proposal", "execute", acceptedCommandID, "--api-url", server.URL}, &out, &diagnostics, buildinfo.Info{}); code != 1 || calls.Load() != 1 {
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
		{"missing command id", []string{"proposal", "execute"}},
		{"invalid command id", []string{"proposal", "execute", "not-a-command"}},
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

func TestProposalExecuteCLIRequiresCredential(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	var out, diagnostics bytes.Buffer
	if code := bootstrap.Run([]string{"proposal", "execute", acceptedCommandID, "--api-url", server.URL}, &out, &diagnostics, buildinfo.Info{}); code != 2 || calls.Load() != 0 {
		t.Fatalf("exit = %d calls = %d stderr = %s", code, calls.Load(), diagnostics.String())
	}
}
