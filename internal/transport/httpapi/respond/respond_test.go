package respond_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/transport/httpapi/apierror"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
)

func TestWriteProblemSetsProtocolHeaders(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	failure := apierror.New(
		http.StatusTooManyRequests,
		"RATE_LIMITED",
		"Rate limited",
		"Try again later.",
		nil,
	).WithRetryAfter(1_500 * time.Millisecond).WithChallenge("Bearer realm=\"idenqa\"")

	if err := respond.WriteProblem(response, request, failure, "req_01M11HEQG00000000000000000"); err != nil {
		t.Fatalf("WriteProblem() error = %v", err)
	}
	if got, want := response.Header().Get("Retry-After"), "2"; got != want {
		t.Errorf("Retry-After = %q, want %q", got, want)
	}
	if got, want := response.Header().Get("WWW-Authenticate"), "Bearer realm=\"idenqa\""; got != want {
		t.Errorf("WWW-Authenticate = %q, want %q", got, want)
	}
	if got, want := response.Header().Get("Content-Type"), "application/problem+json; charset=utf-8"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}

	var problem respond.Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if problem.Code != "RATE_LIMITED" || problem.RequestID == "" {
		t.Fatalf("problem = %+v", problem)
	}
}

func TestJSONReportsEncodingFailureBeforeWriting(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	if err := respond.JSON(response, request, http.StatusOK, make(chan int)); err == nil {
		t.Fatal("JSON() error = nil")
	}
	if response.Code != http.StatusOK || response.Body.Len() != 0 {
		t.Fatalf("response changed after encoding failure: status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestCanonicalJSONWritesExactBytesWithoutANewline(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	payload := []byte(`{"schema_major":1,"bundle_digest":"abc"}`)
	if err := respond.CanonicalJSON(
		response,
		request,
		http.StatusOK,
		"application/vnd.idenqa.decision-bundle.v1+json",
		payload,
	); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(response.Body.Bytes(), payload) {
		t.Fatalf("body = %q, want exact %q", response.Body.Bytes(), payload)
	}
	if response.Header().Get("Content-Type") != "application/vnd.idenqa.decision-bundle.v1+json" {
		t.Fatalf("Content-Type = %q", response.Header().Get("Content-Type"))
	}
}

func TestCanonicalJSONRejectsInvalidInputBeforeWriting(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	if err := respond.CanonicalJSON(response, request, http.StatusOK, "application/json", []byte("{")); err == nil {
		t.Fatal("CanonicalJSON() error = nil")
	}
	if response.Body.Len() != 0 || response.Header().Get("Content-Type") != "" {
		t.Fatalf("response changed after validation failure: headers=%v body=%q", response.Header(), response.Body)
	}
}
