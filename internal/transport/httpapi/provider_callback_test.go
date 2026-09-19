package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/provider"
)

//nolint:gosec // test-only opaque callback reference, not a credential.
const routeCallbackToken = "pcb_01K4AR9V8FQ2G7ZXCPNM5T6JWH"

type providerCallbackServiceStub struct {
	calls    int
	token    string
	envelope providerv1.CallbackEnvelope
	outcome  provider.CallbackOutcome
	err      error
}

func (stub *providerCallbackServiceStub) Handle(_ context.Context, token string, envelope providerv1.CallbackEnvelope) (provider.CallbackOutcome, error) {
	stub.calls++
	stub.token = token
	stub.envelope = envelope
	return stub.outcome, stub.err
}

func providerCallbackRouteFixture(t *testing.T, stub *providerCallbackServiceStub, logs *bytes.Buffer) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	routes, err := NewProviderCallbackRoutes(stub, logger)
	if err != nil {
		t.Fatal(err)
	}
	return versionedRouter(t, routes)
}

func TestProviderCallbackRouteMapsOutcomes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		stub   *providerCallbackServiceStub
		status int
		body   string
		token  string
	}{
		{name: "accepted", stub: &providerCallbackServiceStub{outcome: provider.CallbackOutcome{Status: provider.CallbackAccepted}}, status: http.StatusOK, body: `"status":"accepted"`, token: routeCallbackToken},
		{name: "pending", stub: &providerCallbackServiceStub{outcome: provider.CallbackOutcome{Status: provider.CallbackPending}}, status: http.StatusAccepted, body: `"status":"pending"`, token: routeCallbackToken},
		{name: "duplicate", stub: &providerCallbackServiceStub{outcome: provider.CallbackOutcome{Status: provider.CallbackDuplicate}}, status: http.StatusOK, body: `"status":"duplicate"`, token: routeCallbackToken},
		{name: "unknown reference", stub: &providerCallbackServiceStub{err: provider.ErrCallbackUnavailable}, status: http.StatusNotFound, token: routeCallbackToken},
		{name: "malformed reference", stub: &providerCallbackServiceStub{}, status: http.StatusNotFound, token: "not-a-token"},
		{name: "malformed callback", stub: &providerCallbackServiceStub{err: provider.ErrCallbackInvalid}, status: http.StatusBadRequest, token: routeCallbackToken},
		{name: "rejected callback", stub: &providerCallbackServiceStub{err: provider.ErrCallbackRejected}, status: http.StatusForbidden, token: routeCallbackToken},
		{name: "conflicting callback", stub: &providerCallbackServiceStub{err: provider.ErrCallbackConflict}, status: http.StatusConflict, token: routeCallbackToken},
		{name: "unavailable runner", stub: &providerCallbackServiceStub{err: fmt.Errorf("runner down")}, status: http.StatusServiceUnavailable, token: routeCallbackToken},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			router := providerCallbackRouteFixture(t, test.stub, &logs)
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/provider-callbacks/"+test.token, strings.NewReader(`{"status":"clear"}`))
			request.Header.Set("Response-Signature", "signature")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
			if test.body != "" && !strings.Contains(response.Body.String(), test.body) {
				t.Fatalf("body=%s", response.Body)
			}
			if test.name == "malformed reference" && test.stub.calls != 0 {
				t.Fatal("service was called for a malformed reference")
			}
		})
	}
}

func TestProviderCallbackRouteBoundsBodyHeadersAndRate(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	stub := &providerCallbackServiceStub{outcome: provider.CallbackOutcome{Status: provider.CallbackAccepted}}
	router := providerCallbackRouteFixture(t, stub, &logs)

	oversized := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/provider-callbacks/"+routeCallbackToken, strings.NewReader(strings.Repeat("x", providerv1.MaxCallbackBodyBytes+1)))
	oversizedResponse := httptest.NewRecorder()
	router.ServeHTTP(oversizedResponse, oversized)
	if oversizedResponse.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status=%d", oversizedResponse.Code)
	}

	excessive := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/provider-callbacks/"+routeCallbackToken, strings.NewReader(`{}`))
	for index := 0; index <= providerv1.MaxCallbackHeaders; index++ {
		excessive.Header.Set(fmt.Sprintf("X-Callback-%d", index), "value")
	}
	excessiveResponse := httptest.NewRecorder()
	router.ServeHTTP(excessiveResponse, excessive)
	if excessiveResponse.Code != http.StatusBadRequest {
		t.Fatalf("excessive headers status=%d", excessiveResponse.Code)
	}

	limited := 0
	for index := 0; index <= callbackLimiterBurst; index++ {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/provider-callbacks/"+routeCallbackToken, strings.NewReader(`{"status":"clear"}`))
		request.RemoteAddr = "192.0.2.10:1234"
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("rate limit never triggered")
	}
}

func TestProviderCallbackRouteNeverLogsRawBody(t *testing.T) {
	t.Parallel()
	sentinel := "raw-provider-callback-body-sentinel"
	var logs bytes.Buffer
	stub := &providerCallbackServiceStub{err: provider.ErrCallbackConflict}
	router := providerCallbackRouteFixture(t, stub, &logs)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/provider-callbacks/"+routeCallbackToken, strings.NewReader(sentinel))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("status=%d", response.Code)
	}
	if bytes.Contains(logs.Bytes(), []byte(sentinel)) {
		t.Fatal("raw callback body was logged")
	}
	if !strings.Contains(string(stub.envelope.Body), sentinel) {
		t.Fatal("service did not receive the bounded callback body")
	}
}
