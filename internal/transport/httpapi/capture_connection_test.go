package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/realtime"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/go-chi/chi/v5"
)

type captureConnectionIssuerStub struct {
	input realtime.IssueInput
	err   error
	calls int
}

func (stub *captureConnectionIssuerStub) Issue(
	_ context.Context,
	input realtime.IssueInput,
) (realtime.IssuedTicket, error) {
	stub.calls++
	stub.input = input
	if stub.err != nil {
		return realtime.IssuedTicket{}, stub.err
	}
	ticketID, _ := id.ParseWebSocketTicket("wst_01ARZ3NDEKTSV4RRFFQ69G5FB4")
	generator, err := realtime.NewTicketGenerator(bytes.NewReader(bytes.Repeat([]byte{9}, 32)))
	if err != nil {
		return realtime.IssuedTicket{}, err
	}
	credential, err := generator.Generate(input.Scope.ID(), ticketID)
	if err != nil {
		return realtime.IssuedTicket{}, err
	}
	digest, err := realtime.DigestTicket(credential)
	if err != nil {
		return realtime.IssuedTicket{}, err
	}
	issuedAt := time.Date(2026, time.August, 30, 12, 1, 0, 0, time.UTC)
	record, err := realtime.NewTicket(
		ticketID, input.Scope.ID(), input.VerificationID, input.CaptureTokenID, digest,
		input.Binding, input.Region, realtime.SubprotocolV1, issuedAt, issuedAt.Add(30*time.Second),
	)
	if err != nil {
		return realtime.IssuedTicket{}, err
	}

	return realtime.IssuedTicket{Ticket: record, Credential: credential}, nil
}

func TestCaptureConnectionRoutesIssueExactOriginBoundDisplayOnceURL(t *testing.T) {
	t.Parallel()

	fixture := newEvidenceUploadHTTPFixture(t)
	issuer := &captureConnectionIssuerStub{}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	routes, err := NewCaptureConnectionRoutes(
		fixture.capture, issuer, "wss://core.example/v1/capture/socket", "eu-west-1",
		[]string{"https://capture.example"}, logger,
	)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	routes.Register(router)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/capture/connections", nil)
	request.Host = "attacker.invalid"
	request.Header.Set("Authorization", "Bearer "+fixture.token)
	request.Header.Set("Origin", "https://capture.example")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusCreated, response.Body)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	var connection openapiv1.CaptureConnection
	if err := json.Unmarshal(response.Body.Bytes(), &connection); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(connection.WebsocketURL)
	if err != nil {
		t.Fatal(err)
	}
	credential := parsed.Query().Get("ticket")
	if parsed.Scheme != "wss" || parsed.Host != "core.example" || parsed.Path != "/v1/capture/socket" ||
		len(parsed.Query()) != 1 || credential == "" {
		t.Fatalf("websocket URL = %q", connection.WebsocketURL)
	}
	if _, err := realtime.ParsePresentedTicket(credential); err != nil {
		t.Fatalf("parse response ticket: %v", err)
	}
	if connection.Protocol != realtime.SubprotocolV1 ||
		!connection.ExpiresAt.Equal(time.Date(2026, time.August, 30, 12, 1, 30, 0, time.UTC)) {
		t.Fatalf("connection response = %+v", connection)
	}
	record := fixture.upload.Record()
	if issuer.calls != 1 || issuer.input.Scope.ID() != record.TenantID ||
		issuer.input.VerificationID != record.VerificationID || issuer.input.CaptureTokenID != record.CaptureTokenID ||
		issuer.input.Binding.Kind() != realtime.ClientKindBrowser ||
		issuer.input.Binding.Identity() != "https://capture.example" || issuer.input.Region != "eu-west-1" {
		t.Fatalf("issue input = %+v", issuer.input)
	}
	if logs.Len() != 0 || bytes.Contains(logs.Bytes(), []byte(credential)) {
		t.Fatalf("logs contain display-once connection material: %s", logs.Bytes())
	}
}

func TestCaptureConnectionRoutesRejectInvalidOriginsBeforeIssuance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		origins []string
	}{
		{name: "missing"},
		{name: "disallowed", origins: []string{"https://other.example"}},
		{name: "duplicated", origins: []string{"https://capture.example", "https://capture.example"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newEvidenceUploadHTTPFixture(t)
			issuer := &captureConnectionIssuerStub{}
			router := newCaptureConnectionTestRouter(t, fixture, issuer)
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/capture/connections", nil)
			request.Header.Set("Authorization", "Bearer "+fixture.token)
			for _, origin := range test.origins {
				request.Header.Add("Origin", origin)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
			}
			var problem respond.Problem
			if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if problem.Code != "CAPTURE_ORIGIN_NOT_ALLOWED" || issuer.calls != 0 {
				t.Fatalf("problem = %+v; issuer calls = %d", problem, issuer.calls)
			}
		})
	}
}

func TestCaptureConnectionRoutesCollapseUnavailableTicketToCaptureAuthentication(t *testing.T) {
	t.Parallel()

	fixture := newEvidenceUploadHTTPFixture(t)
	issuer := &captureConnectionIssuerStub{err: realtime.ErrTicketUnavailable}
	router := newCaptureConnectionTestRouter(t, fixture, issuer)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/capture/connections", nil)
	request.Header.Set("Authorization", "Bearer "+fixture.token)
	request.Header.Set("Origin", "https://capture.example")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("status = %d challenge = %q", response.Code, response.Header().Get("WWW-Authenticate"))
	}
}

func TestNewCaptureConnectionRoutesRejectsUnsafeEndpointAndOrigins(t *testing.T) {
	t.Parallel()

	fixture := newEvidenceUploadHTTPFixture(t)
	issuer := &captureConnectionIssuerStub{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tests := []struct {
		name     string
		endpoint string
		region   string
		origins  []string
	}{
		{name: "http endpoint", endpoint: "https://core.example/v1/capture/socket", region: "local", origins: []string{"https://capture.example"}},
		{name: "endpoint query", endpoint: "wss://core.example/v1/capture/socket?ticket=x", region: "local", origins: []string{"https://capture.example"}},
		{name: "missing region", endpoint: "wss://core.example/v1/capture/socket", origins: []string{"https://capture.example"}},
		{name: "missing origins", endpoint: "wss://core.example/v1/capture/socket", region: "local"},
		{name: "invalid origin", endpoint: "wss://core.example/v1/capture/socket", region: "local", origins: []string{"https://capture.example/path"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewCaptureConnectionRoutes(
				fixture.capture, issuer, test.endpoint, test.region, test.origins, logger,
			); err == nil {
				t.Fatal("NewCaptureConnectionRoutes() error = nil")
			}
		})
	}
}

func newCaptureConnectionTestRouter(
	t *testing.T,
	fixture evidenceUploadHTTPFixture,
	issuer *captureConnectionIssuerStub,
) http.Handler {
	t.Helper()
	routes, err := NewCaptureConnectionRoutes(
		fixture.capture, issuer, "wss://core.example/v1/capture/socket", "eu-west-1",
		[]string{"https://capture.example"}, fixture.logger,
	)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	routes.Register(router)

	return router
}

var _ CaptureConnectionIssuer = (*captureConnectionIssuerStub)(nil)
