// Package idenqa provides the dependency-light public Go client.
package idenqa

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const maximumResponseBytes = 1 << 20

// Client calls the public Idenqa API with an explicit HTTP client.
type Client struct {
	base  *url.URL
	token string
	http  *http.Client
}

// NewClient constructs a client without changing global HTTP state.
func NewClient(baseURL, token string, client *http.Client) (*Client, error) {
	base, err := url.Parse(baseURL)
	if err != nil || base.Scheme == "" || base.Host == "" || token == "" || client == nil {
		return nil, errors.New("idenqa: base URL, token, and HTTP client are required")
	}
	localHTTP := base.Scheme == "http" && (base.Hostname() == "localhost" || base.Hostname() == "127.0.0.1" || base.Hostname() == "::1")
	if base.Scheme != "https" && !localHTTP {
		return nil, errors.New("idenqa: HTTPS is required")
	}
	if base.User != nil || base.RawQuery != "" || base.Fragment != "" || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("idenqa: invalid base URL or credential")
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/"
	// Copy configuration so credential-bearing redirects cannot leave the API and
	// caller-owned HTTP client settings are never mutated.
	transport := *client
	transport.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{base: base, token: token, http: &transport}, nil
}

// DoJSON performs one authenticated JSON operation with bounded decoding.
func (client *Client) DoJSON(ctx context.Context, method, path string, input, output any) error {
	_, err := client.doJSON(ctx, method, path, input, output, nil)
	return err
}

func (client *Client) doJSON(
	ctx context.Context, method, path string, input, output any, headers http.Header,
) (ResponseMetadata, error) {
	metadata := ResponseMetadata{}
	if client == nil || client.base == nil || client.http == nil {
		return metadata, errors.New("idenqa: invalid client")
	}
	reference, err := url.Parse(path)
	if err != nil {
		return metadata, fmt.Errorf("parse path: %w", err)
	}
	if reference.IsAbs() || reference.Host != "" || reference.Fragment != "" {
		return metadata, errors.New("idenqa: invalid request path")
	}
	for _, segment := range strings.Split(reference.Path, "/") {
		if segment == ".." {
			return metadata, errors.New("idenqa: invalid request path")
		}
	}
	reference.Path = strings.TrimLeft(reference.Path, "/")
	reference.RawPath = strings.TrimLeft(reference.RawPath, "/")
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return metadata, fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.base.ResolveReference(reference).String(), body)
	if err != nil {
		return metadata, fmt.Errorf("build request: %w", err)
	}
	request.Header = headers.Clone()
	if request.Header == nil {
		request.Header = make(http.Header)
	}
	request.Header.Set("Authorization", "Bearer "+client.token)
	request.Header.Set("Accept", "application/json, application/problem+json")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.http.Do(request)
	if err != nil {
		return metadata, fmt.Errorf("perform request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	metadata = ResponseMetadata{
		RequestID: response.Header.Get("X-Request-ID"), ETag: response.Header.Get("ETag"),
		Location: response.Header.Get("Location"), StatusCode: response.StatusCode,
	}
	limited := io.LimitReader(response.Body, maximumResponseBytes+1)
	encoded, err := io.ReadAll(limited)
	if err != nil {
		return metadata, fmt.Errorf("read response: %w", err)
	}
	if len(encoded) > maximumResponseBytes {
		return metadata, errors.New("idenqa: response exceeds limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		problem := Problem{}
		_ = json.Unmarshal(encoded, &problem)
		return metadata, &APIError{StatusCode: response.StatusCode, RequestID: metadata.RequestID, Problem: problem}
	}
	if output != nil && len(encoded) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		if err := decoder.Decode(output); err != nil {
			return metadata, fmt.Errorf("decode response: %w", err)
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return metadata, errors.New("idenqa: trailing response data")
		}
	}
	return metadata, nil
}

// ResponseMetadata preserves server request correlation and resource version metadata.
type ResponseMetadata struct {
	RequestID  string
	ETag       string
	Location   string
	StatusCode int
}

// Response contains a typed public resource and its HTTP metadata.
type Response[T any] struct {
	Data T
	ResponseMetadata
}

// APIError exposes the stable public problem code without including response bodies in logs.
type APIError struct {
	StatusCode int
	RequestID  string
	Problem    Problem
}

func (err *APIError) Error() string {
	return fmt.Sprintf("idenqa: API status %d", err.StatusCode)
}
