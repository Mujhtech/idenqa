package boundedhttp_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Mujhtech/idenqa/adapters/proposals/internal/boundedhttp"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestClientBoundsResponsesWithoutMutatingSource(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		status     int
		body       string
		wantErr    error
		wantLength int
	}{
		{name: "bounded success", status: http.StatusOK, body: strings.Repeat("a", 1025), wantErr: boundedhttp.ErrResponseTooLarge},
		{name: "bounded error diagnostic", status: http.StatusBadGateway, body: strings.Repeat("b", 5000), wantLength: 1024},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			original := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: test.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(test.body)), Request: request}, nil
			})
			source := &http.Client{Transport: original}
			client := boundedhttp.Client(source, 1024)
			response, err := client.Do(mustRequest(t))
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Do() error = %v, want %v", err, test.wantErr)
			}
			if response != nil {
				defer func() { _ = response.Body.Close() }()
				raw, readErr := io.ReadAll(response.Body)
				if readErr != nil || len(raw) != test.wantLength {
					t.Fatalf("response body length/error = %d / %v, want %d", len(raw), readErr, test.wantLength)
				}
			}
		})
	}
}

func mustRequest(t *testing.T) *http.Request {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext() error = %v", err)
	}
	return request
}
