// Package realtime adapts the owned capture realtime contract to WebSockets.
package realtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	core "github.com/Mujhtech/idenqa/internal/realtime"
	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
)

const (
	connectionPath      = "/capture/socket"
	rejectedCloseReason = "connection rejected"
	failedCloseReason   = "connection failed"
	drainingCloseReason = "server draining"
	overloadCloseReason = "connection overloaded"
	idleCloseReason     = "connection idle"
)

var (
	// ErrBinaryMessage reports a binary frame on the control-only channel.
	ErrBinaryMessage = errors.New("realtime transport: binary messages are not supported")
	// ErrMessageTooLarge reports an outbound message above the protocol limit.
	ErrMessageTooLarge = errors.New("realtime transport: message is too large")
	// ErrConnectionClosed identifies an ordinary peer-initiated socket close.
	ErrConnectionClosed = errors.New("realtime transport: connection closed")
)

// TicketRedeemer atomically consumes an exactly bound connection ticket.
type TicketRedeemer interface {
	Redeem(context.Context, string, core.ClientBinding, string, string) (core.Ticket, error)
}

// SessionHandler owns protocol processing after authenticated admission.
type SessionHandler interface {
	Serve(context.Context, core.Ticket, *Connection, <-chan struct{}) error
}

// Connection is a bounded text-message transport that hides library types.
type Connection struct {
	socket              *websocket.Conn
	maximumMessageBytes int64
}

// Read receives one bounded text control message.
func (connection *Connection) Read(ctx context.Context) ([]byte, error) {
	messageType, payload, err := connection.socket.Read(ctx)
	if err != nil {
		if status := websocket.CloseStatus(err); status == websocket.StatusNormalClosure ||
			status == websocket.StatusGoingAway {
			return nil, ErrConnectionClosed
		}
		return nil, fmt.Errorf("realtime transport: read message: %w", err)
	}
	if messageType != websocket.MessageText {
		_ = connection.socket.Close(websocket.StatusUnsupportedData, "text messages required")

		return nil, ErrBinaryMessage
	}

	return payload, nil
}

// Write sends one bounded text control message.
func (connection *Connection) Write(ctx context.Context, payload []byte) error {
	if int64(len(payload)) > connection.maximumMessageBytes {
		return ErrMessageTooLarge
	}
	if err := connection.socket.Write(ctx, websocket.MessageText, payload); err != nil {
		return fmt.Errorf("realtime transport: write message: %w", err)
	}

	return nil
}

// Ping performs one native WebSocket ping/pong exchange.
func (connection *Connection) Ping(ctx context.Context) error {
	if err := connection.socket.Ping(ctx); err != nil {
		return fmt.Errorf("realtime transport: ping: %w", err)
	}

	return nil
}

// Routes owns exact browser admission and WebSocket upgrade handling.
type Routes struct {
	redeemer       TicketRedeemer
	handler        SessionHandler
	region         string
	allowedOrigins map[string]struct{}
	limits         core.Limits
	lifecycle      *ConnectionLifecycle
	logger         *slog.Logger
}

// NewRoutes constructs the authenticated capture WebSocket boundary.
func NewRoutes(
	redeemer TicketRedeemer,
	handler SessionHandler,
	region string,
	allowedOrigins []string,
	limits core.Limits,
	lifecycle *ConnectionLifecycle,
	logger *slog.Logger,
) (*Routes, error) {
	if redeemer == nil || handler == nil || lifecycle == nil || logger == nil || region == "" ||
		len(allowedOrigins) == 0 || limits.MaximumMessageBytes() == 0 || limits.ConnectionLifetime() == 0 {
		return nil, errors.New("realtime transport: route dependencies are invalid")
	}
	origins := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		if _, duplicate := origins[origin]; duplicate {
			return nil, errors.New("realtime transport: allowed origins contain duplicates")
		}
		if _, err := core.NewBrowserBinding(origin); err != nil {
			return nil, errors.New("realtime transport: allowed origin is invalid")
		}
		origins[origin] = struct{}{}
	}

	return &Routes{
		redeemer: redeemer, handler: handler, region: region,
		allowedOrigins: origins, limits: limits, lifecycle: lifecycle, logger: logger,
	}, nil
}

// Register adds the capture WebSocket endpoint. The path is relative to the
// public API version prefix applied by process composition.
func (routes *Routes) Register(router chi.Router) {
	router.Get(connectionPath, routes.connect)
}

func (routes *Routes) connect(writer http.ResponseWriter, request *http.Request) {
	connectionBase := context.WithoutCancel(request.Context())
	binding, ok := routes.browserBinding(request.Header.Values("Origin"))
	if !ok {
		http.Error(writer, http.StatusText(http.StatusForbidden), http.StatusForbidden)

		return
	}
	if !offersSubprotocol(request.Header.Values("Sec-WebSocket-Protocol"), core.SubprotocolV1) {
		http.Error(writer, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)

		return
	}
	admission, ok := routes.lifecycle.admit()
	if !ok {
		writer.Header().Set("Retry-After", fmt.Sprintf("%d", int(drainRetryAfter/time.Second)))
		http.Error(writer, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)

		return
	}
	defer admission.done()
	if err := clearSocketDeadlines(writer); err != nil {
		http.Error(writer, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)

		return
	}

	socket, err := websocket.Accept(writer, request, &websocket.AcceptOptions{
		Subprotocols:       []string{core.SubprotocolV1},
		InsecureSkipVerify: true, // Exact Origin was checked above; patterns are deliberately not used.
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	defer func() { _ = socket.CloseNow() }()
	socket.SetReadLimit(routes.limits.MaximumMessageBytes())

	admissionContext, cancelAdmission := context.WithTimeout(connectionBase, routes.limits.HelloTimeout())
	ticket, err := routes.redeemer.Redeem(
		admissionContext,
		ticketCredential(request.URL),
		binding,
		routes.region,
		core.SubprotocolV1,
	)
	cancelAdmission()
	if err != nil {
		status := websocket.StatusInternalError
		if errors.Is(err, core.ErrInvalidTicket) {
			status = websocket.StatusPolicyViolation
		}
		_ = socket.Close(status, rejectedCloseReason)

		return
	}
	if socket.Subprotocol() != core.SubprotocolV1 {
		_ = socket.Close(websocket.StatusProtocolError, rejectedCloseReason)

		return
	}

	connectionContext, cancelConnection := context.WithTimeout(
		connectionBase,
		routes.limits.ConnectionLifetime(),
	)
	defer cancelConnection()
	connection := &Connection{socket: socket, maximumMessageBytes: routes.limits.MaximumMessageBytes()}
	if err := routes.handler.Serve(connectionContext, ticket, connection, admission.drain); err != nil {
		if errors.Is(err, ErrServerDraining) {
			_ = socket.Close(websocket.StatusServiceRestart, drainingCloseReason)

			return
		}
		if errors.Is(err, ErrConnectionIdle) {
			_ = socket.Close(websocket.StatusGoingAway, idleCloseReason)

			return
		}
		routes.logger.WarnContext(
			connectionContext,
			"realtime connection handler stopped",
			"connection_id", ticket.ConnectionID().String(),
		)
		status := websocket.StatusInternalError
		if errors.Is(err, ErrSlowConsumer) {
			status = websocket.StatusTryAgainLater
		} else if errors.Is(err, ErrProtocolViolation) || errors.Is(err, ErrSessionUnavailable) {
			status = websocket.StatusPolicyViolation
		}
		reason := failedCloseReason
		if errors.Is(err, ErrSlowConsumer) {
			reason = overloadCloseReason
		}
		_ = socket.Close(status, reason)

		return
	}
	_ = socket.Close(websocket.StatusNormalClosure, "")
}

func clearSocketDeadlines(writer http.ResponseWriter) error {
	controller := http.NewResponseController(writer)
	if err := controller.SetReadDeadline(time.Time{}); err != nil {
		return fmt.Errorf("realtime transport: clear inherited read deadline: %w", err)
	}
	if err := controller.SetWriteDeadline(time.Time{}); err != nil {
		return fmt.Errorf("realtime transport: clear inherited write deadline: %w", err)
	}

	return nil
}

func (routes *Routes) browserBinding(values []string) (core.ClientBinding, bool) {
	if len(values) != 1 {
		return core.ClientBinding{}, false
	}
	if _, allowed := routes.allowedOrigins[values[0]]; !allowed {
		return core.ClientBinding{}, false
	}
	binding, err := core.NewBrowserBinding(values[0])

	return binding, err == nil
}

func offersSubprotocol(values []string, required string) bool {
	found := false
	for _, value := range values {
		for _, offered := range strings.Split(value, ",") {
			offered = strings.TrimSpace(offered)
			if offered == "" {
				return false
			}
			if offered == required {
				if found {
					return false
				}
				found = true
			}
		}
	}

	return found
}

func ticketCredential(requestURL *url.URL) string {
	values, err := url.ParseQuery(requestURL.RawQuery)
	if err != nil || len(values) != 1 {
		return ""
	}
	tickets, ok := values["ticket"]
	if !ok || len(tickets) != 1 {
		return ""
	}

	return tickets[0]
}
