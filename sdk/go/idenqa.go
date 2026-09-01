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
	if base.Scheme != "https" && base.Hostname() != "localhost" && base.Hostname() != "127.0.0.1" {
		return nil, errors.New("idenqa: HTTPS is required")
	}
	return &Client{base: base, token: token, http: client}, nil
}

// DoJSON performs one authenticated JSON operation with bounded decoding.
func (client *Client) DoJSON(ctx context.Context, method, path string, input, output any) error {
	if client == nil || strings.Contains(path, "..") {
		return errors.New("idenqa: invalid request")
	}
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	reference, err := url.Parse(path)
	if err != nil {
		return fmt.Errorf("parse path: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.base.ResolveReference(reference).String(), body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+client.token)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.http.Do(request)
	if err != nil {
		return fmt.Errorf("perform request: %w", err)
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, maximumResponseBytes+1)
	encoded, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if len(encoded) > maximumResponseBytes {
		return errors.New("idenqa: response exceeds limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("idenqa: API status %d", response.StatusCode)
	}
	if output != nil && len(encoded) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(output); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
