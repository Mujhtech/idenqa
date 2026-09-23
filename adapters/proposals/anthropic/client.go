// Package anthropic adapts Anthropic's Messages tool-use protocol to Idenqa's
// owned proposal.Generator port.
package anthropic

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
	"unicode"

	"github.com/Mujhtech/idenqa/adapters/proposals/internal/boundedhttp"
	"github.com/Mujhtech/idenqa/internal/platform/secret"
	"github.com/Mujhtech/idenqa/internal/proposal"
	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const (
	defaultResponseLimit = 128 * 1024
	defaultAPIVersion    = "2023-06-01"
	proposalToolName     = "submit_idenqa_proposal"
)

// Config contains one exact Anthropic origin and credential reference.
type Config struct {
	Origin                string
	Credential            secret.Reference
	Secrets               secret.Resolver
	HTTPClient            *http.Client
	APIVersion            string
	MaxResponseBytes      int64
	AllowInsecureLoopback bool
}

// Client uses Anthropic's official SDK behind Idenqa's provider-neutral port.
type Client struct {
	baseURL    string
	credential secret.Reference
	secrets    secret.Resolver
	http       *http.Client
	apiVersion string
}

// New validates one exact origin and returns a concrete adapter.
func New(config Config) (*Client, error) {
	origin, err := parseOrigin(config.Origin, config.AllowInsecureLoopback)
	if err != nil || config.Credential.IsZero() || config.Secrets == nil || config.HTTPClient == nil {
		return nil, errors.New("anthropic proposal adapter: invalid configuration")
	}
	version := config.APIVersion
	if version == "" {
		version = defaultAPIVersion
	}
	if !validAPIVersion(version) {
		return nil, errors.New("anthropic proposal adapter: invalid API version")
	}
	limit := config.MaxResponseBytes
	if limit == 0 {
		limit = defaultResponseLimit
	}
	if limit < 1024 || limit > 1024*1024 {
		return nil, errors.New("anthropic proposal adapter: invalid response limit")
	}
	return &Client{
		baseURL: strings.TrimSuffix(origin.String(), "/") + "/", credential: config.Credential,
		secrets: config.Secrets, http: boundedhttp.Client(config.HTTPClient, limit), apiVersion: version,
	}, nil
}

// Generate performs one forced-tool request so provider output must enter the
// supplied proposal schema. It does not execute the tool and SDK retries are
// disabled.
func (client *Client) Generate(ctx context.Context, request proposal.GenerationRequest) (proposal.GenerationResponse, error) {
	if err := proposal.ValidateGenerationRequest(request); err != nil {
		return proposal.GenerationResponse{}, err
	}
	schema, err := decodeToolSchema(request.Schema)
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

	sdk := anthropicsdk.NewClient(
		option.WithoutEnvironmentDefaults(),
		option.WithBaseURL(client.baseURL),
		option.WithAPIKey(apiKey),
		option.WithHTTPClient(client.http),
		option.WithMaxRetries(0),
		option.WithHeader("Anthropic-Version", client.apiVersion),
	)
	tool := anthropicsdk.ToolUnionParamOfTool(schema, proposalToolName)
	tool.OfTool.Description = anthropicsdk.String("Return the bounded non-authoritative Idenqa proposal.")
	tool.OfTool.Strict = anthropicsdk.Bool(true)
	var rawResponse *http.Response
	decoded, err := sdk.Messages.New(ctx, anthropicsdk.MessageNewParams{
		Model:      request.Model,
		MaxTokens:  int64(request.MaxOutputTokens),
		System:     []anthropicsdk.TextBlockParam{{Text: request.Instructions}},
		Messages:   []anthropicsdk.MessageParam{anthropicsdk.NewUserMessage(anthropicsdk.NewTextBlock(string(request.Input)))},
		Tools:      []anthropicsdk.ToolUnionParam{tool},
		ToolChoice: anthropicsdk.ToolChoiceParamOfTool(proposalToolName),
	}, option.WithResponseInto(&rawResponse))
	if err != nil {
		return proposal.GenerationResponse{}, classifySDKError(ctx, err)
	}
	response := proposal.GenerationResponse{Model: decoded.Model}
	if rawResponse != nil {
		response.RequestID = rawResponse.Header.Get("Request-ID")
	}
	if decoded.JSON.Usage.Valid() {
		response.InputTokens = decoded.Usage.InputTokens
		response.OutputTokens = decoded.Usage.OutputTokens
		response.UsageReported = true
	}
	if decoded.Model != request.Model || decoded.StopReason != anthropicsdk.StopReasonToolUse {
		return response, proposal.ErrModelOutput
	}
	var output json.RawMessage
	for _, content := range decoded.Content {
		if content.Type != "tool_use" || content.Name != proposalToolName {
			continue
		}
		if output != nil || !json.Valid(content.Input) {
			return response, proposal.ErrModelOutput
		}
		output = append(json.RawMessage(nil), content.Input...)
	}
	if output == nil {
		return response, proposal.ErrModelOutput
	}
	response.Output = output
	return response, nil
}

func decodeToolSchema(raw json.RawMessage) (anthropicsdk.ToolInputSchemaParam, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var fields map[string]any
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		return anthropicsdk.ToolInputSchemaParam{}, errors.New("tool schema must be an object")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return anthropicsdk.ToolInputSchemaParam{}, errors.New("tool schema must contain one value")
	}
	if schemaType, ok := fields["type"]; ok && schemaType != "object" {
		return anthropicsdk.ToolInputSchemaParam{}, errors.New("tool schema type must be object")
	}
	delete(fields, "type")
	result := anthropicsdk.ToolInputSchemaParam{ExtraFields: fields}
	if properties, ok := fields["properties"]; ok {
		result.Properties = properties
		delete(fields, "properties")
	}
	if required, ok := fields["required"].([]any); ok {
		result.Required = make([]string, 0, len(required))
		for _, item := range required {
			name, stringOK := item.(string)
			if !stringOK {
				return anthropicsdk.ToolInputSchemaParam{}, errors.New("tool schema required entries must be strings")
			}
			result.Required = append(result.Required, name)
		}
		delete(fields, "required")
	} else if _, exists := fields["required"]; exists {
		return anthropicsdk.ToolInputSchemaParam{}, errors.New("tool schema required must be an array")
	}
	return result, nil
}

func classifySDKError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, boundedhttp.ErrResponseTooLarge) {
		return proposal.ErrModelOutput
	}
	var apiError *anthropicsdk.Error
	if errors.As(err, &apiError) {
		return classifyStatus(apiError.StatusCode)
	}
	return proposal.ErrModelUnavailable
}

func validAPIVersion(version string) bool {
	if len(version) != 10 || version[4] != '-' || version[7] != '-' {
		return false
	}
	for index, character := range version {
		if index == 4 || index == 7 {
			continue
		}
		if !unicode.IsDigit(character) {
			return false
		}
	}
	return true
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
	ip := net.ParseIP(parsed.Hostname())
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
