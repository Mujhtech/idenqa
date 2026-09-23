// Package boundedhttp constrains response bodies read by provider SDKs while
// preserving the destination-pinned HTTP client supplied by composition.
package boundedhttp

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
)

const errorResponseLimit = 4 * 1024

// ErrResponseTooLarge reports a successful provider response over the owned
// adapter limit. Error responses are truncated before the SDK classifies their
// status so an untrusted diagnostic body cannot cause an unbounded read.
var ErrResponseTooLarge = errors.New("provider response exceeds limit")

// Client shallow-copies source and wraps its transport. The caller's client is
// never mutated, so independently configured routes can safely share it.
func Client(source *http.Client, successLimit int64) *http.Client {
	result := *source
	transport := source.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	result.Transport = &responseLimitTransport{next: transport, successLimit: successLimit}
	return &result
}

type responseLimitTransport struct {
	next         http.RoundTripper
	successLimit int64
}

func (transport *responseLimitTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.next.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	if response == nil || response.Body == nil {
		return response, nil
	}

	limit := transport.successLimit
	success := response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices
	if !success && limit > errorResponseLimit {
		limit = errorResponseLimit
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
	closeErr := response.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read provider response: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close provider response: %w", closeErr)
	}
	if int64(len(raw)) > limit {
		if success {
			clear(raw)
			return nil, ErrResponseTooLarge
		}
		raw = raw[:limit]
	}
	response.Body = io.NopCloser(bytes.NewReader(raw))
	response.ContentLength = int64(len(raw))
	response.Header.Del("Content-Length")
	if success && response.Header.Get("Content-Type") == "" {
		response.Header.Set("Content-Type", "application/json")
	}
	return response, nil
}
