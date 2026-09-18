package realtime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	core "github.com/Mujhtech/idenqa/internal/realtime"
	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
)

const allowedOrigin = "https://capture.example"

// versionPrefix mirrors the public API version prefix applied by process
// composition.
const versionPrefix = "/v1"

type redeemerStub struct {
	mu         sync.Mutex
	ticket     core.Ticket
	err        error
	credential string
	binding    core.ClientBinding
	region     string
	protocol   string
	calls      atomic.Int64
}

func (stub *redeemerStub) Redeem(
	_ context.Context,
	credential string,
	binding core.ClientBinding,
	region string,
	protocol string,
) (core.Ticket, error) {
	stub.calls.Add(1)
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.credential = credential
	stub.binding = binding
	stub.region = region
	stub.protocol = protocol

	return stub.ticket, stub.err
}

type sessionHandlerFunc func(context.Context, core.Ticket, *Connection, <-chan struct{}) error

func (handler sessionHandlerFunc) Serve(
	ctx context.Context,
	ticket core.Ticket,
	connection *Connection,
	drain <-chan struct{},
) error {
	return handler(ctx, ticket, connection, drain)
}

func TestRoutesUpgradeAndRedeemExactBrowserBinding(t *testing.T) {
	t.Parallel()

	ticket := redeemedTicket(t)
	redeemer := &redeemerStub{ticket: ticket}
	handled := make(chan core.Ticket, 1)
	routes := newTestRoutes(t, redeemer, sessionHandlerFunc(func(
		ctx context.Context,
		accepted core.Ticket,
		connection *Connection,
		_ <-chan struct{},
	) error {
		handled <- accepted

		return connection.Write(ctx, []byte("accepted"))
	}))
	server := newTestServer(t, routes)
	connection, response, err := dial(t, server, "?ticket=display-once", allowedOrigin, core.SubprotocolV1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.CloseNow() }()
	if response.StatusCode != http.StatusSwitchingProtocols || connection.Subprotocol() != core.SubprotocolV1 {
		t.Fatalf("upgrade = %d, subprotocol = %q", response.StatusCode, connection.Subprotocol())
	}
	if extension := response.Header.Get("Sec-WebSocket-Extensions"); extension != "" {
		t.Fatalf("compression negotiated: %q", extension)
	}
	messageType, payload, err := connection.Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if messageType != websocket.MessageText || string(payload) != "accepted" {
		t.Fatalf("message = %v %q", messageType, payload)
	}
	if accepted := <-handled; accepted.ConnectionID() != ticket.ConnectionID() {
		t.Fatalf("accepted ticket = %#v", accepted)
	}
	redeemer.mu.Lock()
	defer redeemer.mu.Unlock()
	if redeemer.calls.Load() != 1 || redeemer.credential != "display-once" ||
		redeemer.binding.Identity() != allowedOrigin || redeemer.region != "lagos-1" ||
		redeemer.protocol != core.SubprotocolV1 {
		t.Fatalf("redemption = %d %q %#v %q %q", redeemer.calls.Load(), redeemer.credential,
			redeemer.binding, redeemer.region, redeemer.protocol)
	}
}

func TestRoutesRejectHandshakeBeforeRedemption(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		origin      string
		subprotocol string
		status      int
	}{
		{name: "missing origin", subprotocol: core.SubprotocolV1, status: http.StatusForbidden},
		{name: "wrong origin", origin: "https://attacker.example", subprotocol: core.SubprotocolV1, status: http.StatusForbidden},
		{name: "missing subprotocol", origin: allowedOrigin, status: http.StatusBadRequest},
		{name: "wrong subprotocol", origin: allowedOrigin, subprotocol: "idenqa.capture.v2", status: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			redeemer := &redeemerStub{ticket: redeemedTicket(t)}
			routes := newTestRoutes(t, redeemer, sessionHandlerFunc(func(
				context.Context,
				core.Ticket,
				*Connection,
				<-chan struct{},
			) error {
				t.Fatal("handler called")

				return nil
			}))
			server := newTestServer(t, routes)
			connection, response, err := dial(t, server, "?ticket=secret", test.origin, test.subprotocol)
			if connection != nil {
				_ = connection.CloseNow()
			}
			if err == nil || response == nil || response.StatusCode != test.status {
				t.Fatalf("Dial() response = %#v, error = %v", response, err)
			}
			if redeemer.calls.Load() != 0 {
				t.Fatalf("redemption calls = %d", redeemer.calls.Load())
			}
		})
	}
}

func TestRoutesCollapseInvalidTicketShapesAfterUpgrade(t *testing.T) {
	t.Parallel()

	for _, query := range []string{"", "?ticket=", "?ticket=one&ticket=two", "?ticket=one&extra=value"} {
		t.Run(query, func(t *testing.T) {
			redeemer := &redeemerStub{err: core.ErrInvalidTicket}
			routes := newTestRoutes(t, redeemer, sessionHandlerFunc(func(
				context.Context,
				core.Ticket,
				*Connection,
				<-chan struct{},
			) error {
				t.Fatal("handler called")

				return nil
			}))
			server := newTestServer(t, routes)
			connection, _, err := dial(t, server, query, allowedOrigin, core.SubprotocolV1)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = connection.CloseNow() }()
			_, _, err = connection.Read(t.Context())
			if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
				t.Fatalf("close error = %v", err)
			}
			if redeemer.calls.Load() != 1 {
				t.Fatalf("redemption calls = %d", redeemer.calls.Load())
			}
		})
	}
}

func TestConnectionEnforcesTextAndSizeLimits(t *testing.T) {
	t.Parallel()

	readError := make(chan error, 1)
	routes := newTestRoutes(t, &redeemerStub{ticket: redeemedTicket(t)}, sessionHandlerFunc(func(
		ctx context.Context,
		_ core.Ticket,
		connection *Connection,
		_ <-chan struct{},
	) error {
		if err := connection.Write(ctx, bytes.Repeat([]byte{'x'}, int(core.DefaultMaximumMessageBytes)+1)); !errors.Is(err, ErrMessageTooLarge) {
			return errors.New("oversized outbound message was accepted")
		}
		_, err := connection.Read(ctx)
		readError <- err

		return nil
	}))
	server := newTestServer(t, routes)
	connection, _, err := dial(t, server, "?ticket=secret", allowedOrigin, core.SubprotocolV1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.CloseNow() }()
	if err := connection.Write(t.Context(), websocket.MessageBinary, []byte("not control JSON")); err != nil {
		t.Fatal(err)
	}
	if err := <-readError; !errors.Is(err, ErrBinaryMessage) {
		t.Fatalf("Read(binary) error = %v", err)
	}
}

func TestConnectionRejectsOversizedInboundMessage(t *testing.T) {
	t.Parallel()

	readError := make(chan error, 1)
	routes := newTestRoutes(t, &redeemerStub{ticket: redeemedTicket(t)}, sessionHandlerFunc(func(
		ctx context.Context,
		_ core.Ticket,
		connection *Connection,
		_ <-chan struct{},
	) error {
		_, err := connection.Read(ctx)
		readError <- err

		return nil
	}))
	server := newTestServer(t, routes)
	connection, _, err := dial(t, server, "?ticket=secret", allowedOrigin, core.SubprotocolV1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.CloseNow() }()
	_ = connection.Write(
		t.Context(),
		websocket.MessageText,
		bytes.Repeat([]byte{'x'}, int(core.DefaultMaximumMessageBytes)+1),
	)
	if err := <-readError; !errors.Is(err, websocket.ErrMessageTooBig) {
		t.Fatalf("Read(oversized) error = %v", err)
	}
}

func TestRoutesClearInheritedHTTPDeadlinesBeforeUpgrade(t *testing.T) {
	const httpTimeout = 25 * time.Millisecond

	upgraded := make(chan struct{}, 1)
	routes := newTestRoutes(t, &redeemerStub{ticket: redeemedTicket(t)}, sessionHandlerFunc(func(
		ctx context.Context,
		_ core.Ticket,
		connection *Connection,
		_ <-chan struct{},
	) error {
		upgraded <- struct{}{}
		payload, err := connection.Read(ctx)
		if err != nil {
			return err
		}

		return connection.Write(ctx, payload)
	}))
	router := chi.NewRouter()
	router.Route(versionPrefix, func(versioned chi.Router) {
		routes.Register(versioned)
	})
	server := httptest.NewUnstartedServer(router)
	server.Config.ReadTimeout = httpTimeout
	server.Config.WriteTimeout = httpTimeout
	server.Start()
	t.Cleanup(server.Close)

	connection, _, err := dial(t, server, "?ticket=secret", allowedOrigin, core.SubprotocolV1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.CloseNow() }()
	<-upgraded
	time.Sleep(4 * httpTimeout)
	if err := connection.Write(t.Context(), websocket.MessageText, []byte("after-http-deadline")); err != nil {
		t.Fatalf("late client write: %v", err)
	}
	_, payload, err := connection.Read(t.Context())
	if err != nil {
		t.Fatalf("late server write: %v", err)
	}
	if string(payload) != "after-http-deadline" {
		t.Fatalf("late payload = %q", payload)
	}
}

func TestRoutesCloseAdmittedConnectionForServerDrain(t *testing.T) {
	lifecycle := NewConnectionLifecycle()
	routes := newTestRoutesWithLifecycle(t, &redeemerStub{ticket: redeemedTicket(t)}, sessionHandlerFunc(func(
		ctx context.Context,
		_ core.Ticket,
		_ *Connection,
		drain <-chan struct{},
	) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-drain:
			return ErrServerDraining
		}
	}), lifecycle)
	server := newTestServer(t, routes)
	connection, _, err := dial(t, server, "?ticket=secret", allowedOrigin, core.SubprotocolV1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.CloseNow() }()
	drained := make(chan error, 1)
	go func() {
		drained <- lifecycle.Drain(t.Context())
	}()
	_, _, err = connection.Read(t.Context())
	if websocket.CloseStatus(err) != websocket.StatusServiceRestart {
		t.Fatalf("drain close error = %v", err)
	}
	if err := <-drained; err != nil {
		t.Fatalf("Drain() error = %v", err)
	}
}

func TestRoutesRejectNewAdmissionDuringServerDrain(t *testing.T) {
	t.Parallel()

	lifecycle := NewConnectionLifecycle()
	if err := lifecycle.Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	redeemer := &redeemerStub{ticket: redeemedTicket(t)}
	routes := newTestRoutesWithLifecycle(t, redeemer, sessionHandlerFunc(func(
		context.Context,
		core.Ticket,
		*Connection,
		<-chan struct{},
	) error {
		t.Fatal("handler called during drain")

		return nil
	}), lifecycle)
	server := newTestServer(t, routes)
	connection, response, err := dial(t, server, "?ticket=secret", allowedOrigin, core.SubprotocolV1)
	if connection != nil {
		_ = connection.CloseNow()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusServiceUnavailable ||
		response.Header.Get("Retry-After") != "5" {
		t.Fatalf("Dial() response = %#v, error = %v", response, err)
	}
	if redeemer.calls.Load() != 0 {
		t.Fatalf("redemption calls = %d", redeemer.calls.Load())
	}
}

func TestRoutesCloseSlowConsumerWithRetryableOverloadStatus(t *testing.T) {
	t.Parallel()

	routes := newTestRoutes(t, &redeemerStub{ticket: redeemedTicket(t)}, sessionHandlerFunc(func(
		context.Context,
		core.Ticket,
		*Connection,
		<-chan struct{},
	) error {
		return ErrSlowConsumer
	}))
	server := newTestServer(t, routes)
	connection, _, err := dial(t, server, "?ticket=secret", allowedOrigin, core.SubprotocolV1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.CloseNow() }()
	_, _, err = connection.Read(t.Context())
	if websocket.CloseStatus(err) != websocket.StatusTryAgainLater {
		t.Fatalf("slow-consumer close error = %v", err)
	}
}

func TestRoutesCloseIdleConnectionWithRetryableGoingAwayStatus(t *testing.T) {
	t.Parallel()

	routes := newTestRoutes(t, &redeemerStub{ticket: redeemedTicket(t)}, sessionHandlerFunc(func(
		context.Context,
		core.Ticket,
		*Connection,
		<-chan struct{},
	) error {
		return ErrConnectionIdle
	}))
	server := newTestServer(t, routes)
	connection, _, err := dial(t, server, "?ticket=secret", allowedOrigin, core.SubprotocolV1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.CloseNow() }()
	_, _, err = connection.Read(t.Context())
	if websocket.CloseStatus(err) != websocket.StatusGoingAway {
		t.Fatalf("idle close error = %v", err)
	}
}

func TestRoutesHandleBoundedConcurrentAdmissions(t *testing.T) {
	t.Parallel()

	const connections = 32
	redeemer := &redeemerStub{ticket: redeemedTicket(t)}
	routes := newTestRoutes(t, redeemer, sessionHandlerFunc(func(
		ctx context.Context,
		_ core.Ticket,
		connection *Connection,
		_ <-chan struct{},
	) error {
		return connection.Write(ctx, []byte("ok"))
	}))
	server := newTestServer(t, routes)
	errorsFound := make(chan error, connections)
	var wait sync.WaitGroup
	for index := 0; index < connections; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			connection, _, err := dialWithContext(t.Context(), server, "?ticket=secret", allowedOrigin, core.SubprotocolV1)
			if err != nil {
				errorsFound <- err

				return
			}
			defer func() { _ = connection.CloseNow() }()
			_, payload, err := connection.Read(t.Context())
			if err != nil {
				errorsFound <- err

				return
			}
			if string(payload) != "ok" {
				errorsFound <- errors.New("unexpected admission response")
			}
		}()
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
	}
	if redeemer.calls.Load() != connections {
		t.Fatalf("redemption calls = %d, want %d", redeemer.calls.Load(), connections)
	}
}

func newTestRoutes(
	t *testing.T,
	redeemer TicketRedeemer,
	handler SessionHandler,
) *Routes {
	t.Helper()

	return newTestRoutesWithLifecycle(t, redeemer, handler, NewConnectionLifecycle())
}

func newTestRoutesWithLifecycle(
	t *testing.T,
	redeemer TicketRedeemer,
	handler SessionHandler,
	lifecycle *ConnectionLifecycle,
) *Routes {
	t.Helper()
	routes, err := NewRoutes(
		redeemer,
		handler,
		"lagos-1",
		[]string{allowedOrigin},
		core.DefaultLimits(),
		lifecycle,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}

	return routes
}

func newTestServer(t *testing.T, routes *Routes) *httptest.Server {
	t.Helper()
	router := chi.NewRouter()
	router.Route(versionPrefix, func(versioned chi.Router) {
		routes.Register(versioned)
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	return server
}

func dial(
	t *testing.T,
	server *httptest.Server,
	query string,
	origin string,
	subprotocol string,
) (*websocket.Conn, *http.Response, error) {
	t.Helper()

	return dialWithContext(t.Context(), server, query, origin, subprotocol)
}

func dialWithContext(
	ctx context.Context,
	server *httptest.Server,
	query string,
	origin string,
	subprotocol string,
) (*websocket.Conn, *http.Response, error) {
	headers := make(http.Header)
	if origin != "" {
		headers.Set("Origin", origin)
	}
	options := &websocket.DialOptions{
		HTTPHeader:      headers,
		CompressionMode: websocket.CompressionContextTakeover,
	}
	if subprotocol != "" {
		options.Subprotocols = []string{subprotocol}
	}
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + versionPrefix + connectionPath + query

	return websocket.Dial(ctx, endpoint, options)
}

func redeemedTicket(t *testing.T) core.Ticket {
	t.Helper()
	ticketID, _ := id.ParseWebSocketTicket("wst_01ARZ3NDEKTSV4RRFFQ69G5FB4")
	tenantID, _ := id.ParseTenant("ten_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	verificationID, _ := id.ParseVerification("ver_01ARZ3NDEKTSV4RRFFQ69G5FAW")
	captureTokenID, _ := id.ParseCaptureToken("ctk_01ARZ3NDEKTSV4RRFFQ69G5FAX")
	connectionID, _ := id.ParseConnection("con_01ARZ3NDEKTSV4RRFFQ69G5FAY")
	digest, err := core.ParseTicketDigest(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	binding, err := core.NewBrowserBinding(allowedOrigin)
	if err != nil {
		t.Fatal(err)
	}
	issuedAt := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	redeemedAt := issuedAt.Add(time.Second)
	ticket, err := core.RestoreTicket(
		ticketID,
		tenantID,
		verificationID,
		captureTokenID,
		digest,
		binding,
		"lagos-1",
		core.SubprotocolV1,
		issuedAt,
		issuedAt.Add(30*time.Second),
		&redeemedAt,
		connectionID,
	)
	if err != nil {
		t.Fatal(err)
	}

	return ticket
}
