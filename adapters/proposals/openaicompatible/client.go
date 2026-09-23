// Package openaicompatible adapts the OpenAI-compatible chat-completions
// protocol to Idenqa's owned proposal.Generator port.
package openaicompatible

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/Mujhtech/idenqa/adapters/proposals/internal/boundedhttp"
	"github.com/Mujhtech/idenqa/internal/platform/secret"
	"github.com/Mujhtech/idenqa/internal/proposal"
	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

const defaultResponseLimit = 128 * 1024

// Config contains one exact OpenAI-compatible origin and credential reference.
// The HTTP client should be destination-bound by the composition root.
type Config struct {
	Origin                string
	Credential            secret.Reference
	Secrets               secret.Resolver
	HTTPClient            *http.Client
	MaxResponseBytes      int64
	AllowInsecureLoopback bool
}

// Client uses the official OpenAI SDK behind Idenqa's provider-neutral port.
type Client struct {
	baseURL    string
	credential secret.Reference
	secrets    secret.Resolver
	http       *http.Client
}

// New validates one exact origin and returns a concrete adapter.
func New(config Config) (*Client, error) {
	origin, err := parseOrigin(config.Origin, config.AllowInsecureLoopback)
	if err != nil || config.Credential.IsZero() || config.Secrets == nil || config.HTTPClient == nil {
		return nil, errors.New("openai-compatible proposal adapter: invalid configuration")
	}
	limit := config.MaxResponseBytes
	if limit == 0 {
		limit = defaultResponseLimit
	}
	if limit < 1024 || limit > 1024*1024 {
		return nil, errors.New("openai-compatible proposal adapter: invalid response limit")
	}
	return &Client{
		baseURL: strings.TrimSuffix(origin.String(), "/") + "/v1/", credential: config.Credential,
		secrets: config.Secrets, http: boundedhttp.Client(config.HTTPClient, limit),
	}, nil
}

// Generate performs one non-streaming, strict-schema request. SDK retries are
// disabled; retry and cost policy belong to the owning application boundary.
func (client *Client) Generate(ctx context.Context, request proposal.GenerationRequest) (proposal.GenerationResponse, error) {
	if err := proposal.ValidateGenerationRequest(request); err != nil {
		return proposal.GenerationResponse{}, err
	}
	schema, err := decodeSchema(request.Schema)
	if err != nil {
		return proposal.GenerationResponse{}, proposal.ErrModelOutput
	}
	credential, err := client.secrets.Resolve(ctx, client.credential)
	if err != nil {
		return proposal.GenerationResponse{}, proposal.ErrModelUnavailable
	}
	apiKey, err := credential.Text()
	if err != nil || apiKey == "" {
		return proposal.GenerationResponse{}, proposal.ErrModelUnavailable
	}

	service := openai.NewChatCompletionService(
		option.WithBaseURL(client.baseURL),
		option.WithAPIKey(apiKey),
		option.WithHTTPClient(client.http),
		option.WithMaxRetries(0),
	)
	var rawResponse *http.Response
	decoded, err := service.New(ctx, openai.ChatCompletionNewParams{
		Model: request.Model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(request.Instructions),
			openai.UserMessage(string(request.Input)),
		},
		MaxCompletionTokens: openai.Int(int64(request.MaxOutputTokens)),
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &openai.ResponseFormatJSONSchemaParam{
				JSONSchema: openai.ResponseFormatJSONSchemaJSONSchemaParam{
					Name: "idenqa_proposal", Strict: openai.Bool(true), Schema: schema,
				},
			},
		},
	}, option.WithResponseInto(&rawResponse))
	if err != nil {
		return proposal.GenerationResponse{}, classifySDKError(ctx, err)
	}
	response := proposal.GenerationResponse{Model: decoded.Model}
	if rawResponse != nil {
		response.RequestID = rawResponse.Header.Get("X-Request-ID")
	}
	if decoded.JSON.Usage.Valid() {
		response.InputTokens = decoded.Usage.PromptTokens
		response.OutputTokens = decoded.Usage.CompletionTokens
		response.UsageReported = true
	}
	if len(decoded.Choices) != 1 || decoded.Choices[0].FinishReason != "stop" ||
		decoded.Model != request.Model || decoded.Choices[0].Message.Refusal != "" ||
		!json.Valid([]byte(decoded.Choices[0].Message.Content)) {
		return response, proposal.ErrModelOutput
	}
	response.Output = json.RawMessage(decoded.Choices[0].Message.Content)
	return response, nil
}

func decodeSchema(raw json.RawMessage) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var schema map[string]any
	if err := decoder.Decode(&schema); err != nil || schema == nil {
		return nil, errors.New("schema must be an object")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("schema must contain one value")
	}
	return schema, nil
}

func classifySDKError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, boundedhttp.ErrResponseTooLarge) {
		return proposal.ErrModelOutput
	}
	var apiError *openai.Error
	if errors.As(err, &apiError) {
		return classifyStatus(apiError.StatusCode)
	}
	return proposal.ErrModelUnavailable
}

func parseOrigin(raw string, allowInsecureLoopback bool) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("invalid origin")
	}
	if parsed.Scheme == "https" {
		return parsed, nil
	}
	if !allowInsecureLoopback || parsed.Scheme != "http" {
		return nil, errors.New("origin requires HTTPS")
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("insecure origin must be literal loopback")
	}
	return parsed, nil
}

func classifyStatus(status int) error {
	switch {
	case status >= 200 && status < 300:
		return nil
	case status == http.StatusTooManyRequests:
		return proposal.ErrRateLimited
	case status >= 400 && status < 500:
		return proposal.ErrModelRejected
	default:
		return proposal.ErrModelUnavailable
	}
}

var _ proposal.Generator = (*Client)(nil)
