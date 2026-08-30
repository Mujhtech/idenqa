package realtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	core "github.com/Mujhtech/idenqa/internal/realtime"
)

var (
	// ErrProtocolViolation identifies a client message that violates the admitted v1 session.
	ErrProtocolViolation = errors.New("realtime transport: protocol violation")
	// ErrSessionUnavailable identifies authority that became unavailable after ticket redemption.
	ErrSessionUnavailable = errors.New("realtime transport: session unavailable")
)

type messageConnection interface {
	Read(context.Context) ([]byte, error)
	Write(context.Context, []byte) error
}

// SessionAuthoritySource reloads current capture-session authority after ticket redemption.
type SessionAuthoritySource interface {
	LoadSessionAuthority(context.Context, core.Ticket, time.Time) (core.SessionAuthority, error)
}

// MessageIDGenerator creates server message identities behind an owned seam.
type MessageIDGenerator interface {
	NewMessage() (id.Message, error)
}

// EstablishedSessionHandler owns the connection after v1 hello/welcome succeeds.
type EstablishedSessionHandler interface {
	ServeEstablished(context.Context, core.Ticket, core.ClientHello, *Connection, <-chan struct{}) error
}

// HelloHandler performs strict v1 hello/welcome admission before delegating an established session.
type HelloHandler struct {
	authority   SessionAuthoritySource
	identifiers MessageIDGenerator
	clock       clock.Clock
	limits      core.Limits
	established EstablishedSessionHandler
}

// NewHelloHandler constructs the v1 protocol admission handler.
func NewHelloHandler(
	authority SessionAuthoritySource,
	identifiers MessageIDGenerator,
	source clock.Clock,
	limits core.Limits,
	established EstablishedSessionHandler,
) (*HelloHandler, error) {
	if authority == nil || identifiers == nil || source == nil || established == nil ||
		limits.HelloTimeout() == 0 || limits.WriteTimeout() == 0 {
		return nil, errors.New("realtime transport: hello handler dependencies are invalid")
	}

	return &HelloHandler{authority: authority, identifiers: identifiers, clock: source,
		limits: limits, established: established}, nil
}

// Serve validates client.hello, emits server.welcome, and delegates the bounded session.
func (handler *HelloHandler) Serve(
	ctx context.Context,
	ticket core.Ticket,
	connection *Connection,
	drain <-chan struct{},
) error {
	hello, authority, err := handler.exchange(ctx, ticket, connection)
	if err != nil {
		return err
	}
	sessionContext, cancel := context.WithDeadline(ctx, authority.ExpiresAt())
	defer cancel()

	return handler.established.ServeEstablished(sessionContext, ticket, hello, connection, drain)
}

func (handler *HelloHandler) exchange(
	ctx context.Context,
	ticket core.Ticket,
	connection messageConnection,
) (core.ClientHello, core.SessionAuthority, error) {
	if handler == nil || connection == nil || ticket.ConnectionID().IsZero() {
		return core.ClientHello{}, core.SessionAuthority{}, errors.New("realtime transport: hello handler is not initialised")
	}
	helloContext, cancelHello := context.WithTimeout(ctx, handler.limits.HelloTimeout())
	encoded, err := connection.Read(helloContext)
	cancelHello()
	if err != nil {
		return core.ClientHello{}, core.SessionAuthority{}, err
	}
	message, err := core.DecodeClientMessage(encoded)
	if err != nil {
		return core.ClientHello{}, core.SessionAuthority{}, fmt.Errorf("%w: decode client hello", ErrProtocolViolation)
	}
	hello, ok := message.Payload().(core.ClientHello)
	if !ok || message.Type() != core.MessageClientHello || message.Sequence() != 1 ||
		message.VerificationID() != ticket.VerificationID() || !message.ConnectionID().IsZero() ||
		!message.CommandID().IsZero() || !message.CorrelationID().IsZero() || !message.CausationID().IsZero() {
		return core.ClientHello{}, core.SessionAuthority{}, fmt.Errorf("%w: invalid client hello binding", ErrProtocolViolation)
	}
	now := handler.clock.Now().UTC()
	authority, err := handler.authority.LoadSessionAuthority(ctx, ticket, now)
	if errors.Is(err, core.ErrSessionUnavailable) {
		return core.ClientHello{}, core.SessionAuthority{}, ErrSessionUnavailable
	}
	if err != nil {
		return core.ClientHello{}, core.SessionAuthority{}, fmt.Errorf("load realtime session authority: %w", err)
	}
	if !now.Before(authority.ExpiresAt()) {
		return core.ClientHello{}, core.SessionAuthority{}, ErrSessionUnavailable
	}
	messageID, err := handler.identifiers.NewMessage()
	if err != nil {
		return core.ClientHello{}, core.SessionAuthority{}, fmt.Errorf("generate welcome message ID: %w", err)
	}
	welcome, err := core.NewMessage(core.MessageInput{
		Version: core.ProtocolVersionV1, ID: messageID, VerificationID: ticket.VerificationID(),
		ConnectionID: ticket.ConnectionID(), Sequence: 1, OccurredAt: now,
		Payload: core.ServerWelcome{SelectedVersion: core.ProtocolVersionV1,
			SessionVersion: authority.Version(), PingInterval: handler.limits.PingInterval(),
			PongTimeout: handler.limits.PongTimeout(), IdleTimeout: handler.limits.IdleTimeout()},
	})
	if err != nil {
		return core.ClientHello{}, core.SessionAuthority{}, fmt.Errorf("construct server welcome: %w", err)
	}
	encodedWelcome, err := core.EncodeServerMessage(welcome)
	if err != nil {
		return core.ClientHello{}, core.SessionAuthority{}, fmt.Errorf("encode server welcome: %w", err)
	}
	writeContext, cancelWrite := context.WithTimeout(ctx, handler.limits.WriteTimeout())
	err = connection.Write(writeContext, encodedWelcome)
	cancelWrite()
	if err != nil {
		return core.ClientHello{}, core.SessionAuthority{}, err
	}

	return hello, authority, nil
}

var _ SessionHandler = (*HelloHandler)(nil)
