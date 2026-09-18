package callback

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/delivery"
)

// Client performs one DNS-pinned HTTPS webhook attempt.
type Client struct {
	Resolver Resolver
	Dialer   *net.Dialer
	Timeout  time.Duration
}

// Send posts exact bytes and returns bounded response metadata with a sanitised excerpt.
func (client Client) Send(ctx context.Context, targetURL string, signature delivery.Signature, body []byte) (delivery.SafeDiagnostic, bool, bool, error) {
	if client.Resolver == nil || client.Dialer == nil || client.Timeout <= 0 || client.Timeout > time.Minute {
		return delivery.SafeDiagnostic{}, false, false, errors.New("callback: invalid client")
	}
	target, err := ResolveTarget(ctx, client.Resolver, targetURL)
	if err != nil {
		diagnostic, _ := delivery.NewSafeDiagnostic(0, "unsafe_target", 0)
		return diagnostic, false, false, err
	}
	transport := &http.Transport{
		Proxy:             nil,
		DialContext:       target.DialContext(client.Dialer),
		TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12, ServerName: target.URL.Hostname()},
		ForceAttemptHTTP2: true,
	}
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{
		Transport:     transport,
		Timeout:       client.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.URL.String(), bytes.NewReader(body))
	if err != nil {
		return delivery.SafeDiagnostic{}, false, false, fmt.Errorf("build callback request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idenqa-Signature", signature.Value)
	request.Header.Set("Idenqa-Timestamp", signature.Timestamp)
	request.Header.Set("Idenqa-Event-ID", signature.EventID)
	response, err := httpClient.Do(request)
	if err != nil {
		diagnostic, _ := delivery.NewSafeDiagnostic(0, "transport", 0)
		return diagnostic, false, true, err
	}
	defer func() { _ = response.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, int64(delivery.MaximumResponseExcerptBytes)+1))
	retryAfter := parseRetryAfter(response.Header.Get("Retry-After"))
	succeeded := response.StatusCode >= 200 && response.StatusCode < 300
	retry := response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooEarly || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
	class := "rejected"
	if succeeded {
		class = "delivered"
	} else if retry {
		class = "retryable_status"
	}
	diagnostic, err := delivery.NewSafeDiagnostic(response.StatusCode, class, retryAfter)
	if err != nil {
		return diagnostic, succeeded, retry, err
	}
	return diagnostic.WithResponse(raw), succeeded, retry, nil
}

func parseRetryAfter(value string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || seconds < 0 || seconds > 86400 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}
