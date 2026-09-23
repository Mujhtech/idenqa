package openaicompatible_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Mujhtech/idenqa/adapters/proposals/openaicompatible"
	"github.com/Mujhtech/idenqa/internal/platform/secret"
	"github.com/Mujhtech/idenqa/internal/proposal"
)

type secretResolver struct {
	value secret.Value
	err   error
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func (resolver secretResolver) Resolve(context.Context, secret.Reference) (secret.Value, error) {
	return resolver.value, resolver.err
}

func TestClientGenerate(t *testing.T) {
	t.Parallel()
	reference := mustReference(t)
	credential, err := secret.NewValue(reference, "v1", []byte("test-api-key"))
	if err != nil {
		t.Fatalf("NewValue() error = %v", err)
	}
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v1/chat/completions" || request.Header.Get("Authorization") != "Bearer test-api-key" {
			t.Errorf("request path/header = %s / %q", request.URL.Path, request.Header.Get("Authorization"))
		}
		var body struct {
			Model          string `json:"model"`
			ResponseFormat struct {
				Type       string `json:"type"`
				JSONSchema struct {
					Strict bool `json:"strict"`
				} `json:"json_schema"`
			} `json:"response_format"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body.Model != "model-2026-09" || body.ResponseFormat.Type != "json_schema" || !body.ResponseFormat.JSONSchema.Strict {
			t.Errorf("request body = %+v", body)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Request-Id": []string{"request-1"}}, Body: io.NopCloser(strings.NewReader(`{"model":"model-2026-09","choices":[{"message":{"content":"{\"actions\":[],\"reason\":\"\"}","refusal":null},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":5}}`))}, nil
	})}
	client, err := openaicompatible.New(openaicompatible.Config{
		Origin: "http://127.0.0.1", Credential: reference, Secrets: secretResolver{value: credential},
		HTTPClient: httpClient, AllowInsecureLoopback: true,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := client.Generate(t.Context(), generationRequest())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if result.Model != "model-2026-09" || result.RequestID != "request-1" || result.InputTokens != 12 || result.OutputTokens != 5 {
		t.Fatalf("Generate() = %+v", result)
	}
}

func TestClientFailsClosed(t *testing.T) {
	t.Parallel()
	reference := mustReference(t)
	credential, _ := secret.NewValue(reference, "v1", []byte("test-api-key"))
	tests := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{name: "rate limited", status: http.StatusTooManyRequests, body: `{}`, want: proposal.ErrRateLimited},
		{name: "rejected", status: http.StatusBadRequest, body: `{}`, want: proposal.ErrModelRejected},
		{name: "model changed", status: http.StatusOK, body: `{"model":"other","choices":[{"message":{"content":"{}"},"finish_reason":"stop"}]}`, want: proposal.ErrModelOutput},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: test.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(test.body))}, nil
			})}
			client, err := openaicompatible.New(openaicompatible.Config{
				Origin: "http://127.0.0.1", Credential: reference, Secrets: secretResolver{value: credential},
				HTTPClient: httpClient, AllowInsecureLoopback: true,
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if _, err := client.Generate(t.Context(), generationRequest()); !errors.Is(err, test.want) {
				t.Fatalf("Generate() error = %v, want %v", err, test.want)
			}
		})
	}
	if _, err := openaicompatible.New(openaicompatible.Config{Origin: "http://example.com", Credential: reference, Secrets: secretResolver{value: credential}, HTTPClient: http.DefaultClient}); err == nil {
		t.Fatal("New() accepted insecure public origin")
	}
}

func TestClientDisablesSDKRetriesAndBoundsResponses(t *testing.T) {
	t.Parallel()
	reference := mustReference(t)
	credential, _ := secret.NewValue(reference, "v1", []byte("test-api-key"))
	tests := []struct {
		name      string
		status    int
		body      string
		limit     int64
		want      error
		wantCalls int
	}{
		{name: "no SDK retry", status: http.StatusInternalServerError, body: `{}`, want: proposal.ErrModelUnavailable, wantCalls: 1},
		{name: "oversized success", status: http.StatusOK, body: strings.Repeat("x", 1025), limit: 1024, want: proposal.ErrModelOutput, wantCalls: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: test.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(test.body))}, nil
			})}
			client, err := openaicompatible.New(openaicompatible.Config{
				Origin: "http://127.0.0.1", Credential: reference, Secrets: secretResolver{value: credential},
				HTTPClient: httpClient, MaxResponseBytes: test.limit, AllowInsecureLoopback: true,
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if _, err := client.Generate(t.Context(), generationRequest()); !errors.Is(err, test.want) {
				t.Fatalf("Generate() error = %v, want %v", err, test.want)
			}
			if calls != test.wantCalls {
				t.Fatalf("provider calls = %d, want %d", calls, test.wantCalls)
			}
		})
	}
}

func generationRequest() proposal.GenerationRequest {
	return proposal.GenerationRequest{
		ModelID: "ai.review", Model: "model-2026-09", PromptVersion: "p1", Instructions: "Return JSON.",
		Input: json.RawMessage(`{"context_digest":"abc"}`), Schema: json.RawMessage(`{"type":"object"}`), MaxOutputTokens: 100,
	}
}

func mustReference(t *testing.T) secret.Reference {
	t.Helper()
	reference, err := secret.ParseReference("secret://file/run/secrets/ai")
	if err != nil {
		t.Fatalf("ParseReference() error = %v", err)
	}
	return reference
}
