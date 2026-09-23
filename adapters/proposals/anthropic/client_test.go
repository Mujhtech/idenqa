package anthropic_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Mujhtech/idenqa/adapters/proposals/anthropic"
	"github.com/Mujhtech/idenqa/internal/platform/secret"
	"github.com/Mujhtech/idenqa/internal/proposal"
)

type secretResolver struct{ value secret.Value }

func (resolver secretResolver) Resolve(context.Context, secret.Reference) (secret.Value, error) {
	return resolver.value, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestClientGenerate(t *testing.T) {
	t.Parallel()
	reference := mustReference(t)
	credential, _ := secret.NewValue(reference, "v1", []byte("anthropic-key"))
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v1/messages" || request.Header.Get("X-API-Key") != "anthropic-key" || request.Header.Get("Anthropic-Version") != "2023-06-01" {
			t.Errorf("unexpected request path or headers")
		}
		var body struct {
			Model      string `json:"model"`
			ToolChoice struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"tool_choice"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body.Model != "claude-snapshot" || body.ToolChoice.Type != "tool" || body.ToolChoice.Name != "submit_idenqa_proposal" {
			t.Errorf("request body = %+v", body)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Request-Id": []string{"request-2"}}, Body: io.NopCloser(strings.NewReader(`{"model":"claude-snapshot","stop_reason":"tool_use","content":[{"type":"tool_use","name":"submit_idenqa_proposal","input":{"actions":[],"reason":""}}],"usage":{"input_tokens":20,"output_tokens":8}}`))}, nil
	})}
	client, err := anthropic.New(anthropic.Config{
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
	if result.Model != "claude-snapshot" || result.RequestID != "request-2" || result.InputTokens != 20 || result.OutputTokens != 8 {
		t.Fatalf("Generate() = %+v", result)
	}
}

func TestClientFailsClosed(t *testing.T) {
	t.Parallel()
	reference := mustReference(t)
	credential, _ := secret.NewValue(reference, "v1", []byte("anthropic-key"))
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusTooManyRequests, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})}
	client, err := anthropic.New(anthropic.Config{
		Origin: "http://127.0.0.1", Credential: reference, Secrets: secretResolver{value: credential},
		HTTPClient: httpClient, AllowInsecureLoopback: true,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := client.Generate(t.Context(), generationRequest()); !errors.Is(err, proposal.ErrRateLimited) {
		t.Fatalf("Generate() error = %v", err)
	}
	if _, err := anthropic.New(anthropic.Config{Origin: "http://example.com", Credential: reference, Secrets: secretResolver{value: credential}, HTTPClient: http.DefaultClient}); err == nil {
		t.Fatal("New() accepted insecure public origin")
	}
	if _, err := anthropic.New(anthropic.Config{Origin: "https://api.anthropic.com", Credential: reference, Secrets: secretResolver{value: credential}, HTTPClient: http.DefaultClient, APIVersion: "202x-06-01"}); err == nil {
		t.Fatal("New() accepted malformed API version")
	}
}

func TestClientDisablesSDKRetriesAndBoundsResponses(t *testing.T) {
	t.Parallel()
	reference := mustReference(t)
	credential, _ := secret.NewValue(reference, "v1", []byte("anthropic-key"))
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
			client, err := anthropic.New(anthropic.Config{
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
		ModelID: "ai.review", Model: "claude-snapshot", PromptVersion: "p1", Instructions: "Return the requested tool.",
		Input: json.RawMessage(`{"context_digest":"abc"}`), Schema: json.RawMessage(`{"type":"object"}`), MaxOutputTokens: 100,
	}
}

func mustReference(t *testing.T) secret.Reference {
	t.Helper()
	reference, err := secret.ParseReference("secret://file/run/secrets/anthropic")
	if err != nil {
		t.Fatalf("ParseReference() error = %v", err)
	}
	return reference
}
