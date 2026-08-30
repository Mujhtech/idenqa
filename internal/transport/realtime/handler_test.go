package realtime

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	core "github.com/Mujhtech/idenqa/internal/realtime"
)

type handlerClock struct{ now time.Time }

func (source handlerClock) Now() time.Time { return source.now }

type authoritySourceStub struct {
	authority core.SessionAuthority
	err       error
	ticket    core.Ticket
}

func (source *authoritySourceStub) LoadSessionAuthority(
	_ context.Context,
	ticket core.Ticket,
	_ time.Time,
) (core.SessionAuthority, error) {
	source.ticket = ticket

	return source.authority, source.err
}

type wireConnectionStub struct {
	read    []byte
	written []byte
	err     error
}

func (connection *wireConnectionStub) Read(context.Context) ([]byte, error) {
	return append([]byte(nil), connection.read...), connection.err
}

func (connection *wireConnectionStub) Write(_ context.Context, encoded []byte) error {
	connection.written = append([]byte(nil), encoded...)

	return connection.err
}

type establishedStub struct{}

func (establishedStub) ServeEstablished(
	context.Context,
	core.Ticket,
	core.ClientHello,
	*Connection,
	<-chan struct{},
) error {
	return nil
}

func TestHelloHandlerExchangesStrictBoundWelcome(t *testing.T) {
	t.Parallel()

	handler, connection, ticket, authority := newHelloHandlerFixture(t)
	hello, loadedAuthority, err := handler.exchange(t.Context(), ticket, connection)
	if err != nil {
		t.Fatal(err)
	}
	if hello.SDKVersion != "typescript-0.1.0" || loadedAuthority.Version() != authority.Version() {
		t.Fatalf("exchange returned hello %#v and authority version %d", hello, loadedAuthority.Version())
	}
	welcome, err := core.DecodeServerMessage(connection.written)
	if err != nil {
		t.Fatalf("DecodeServerMessage() error = %v; JSON = %s", err, connection.written)
	}
	payload := welcome.Payload().(core.ServerWelcome)
	if welcome.Type() != core.MessageServerWelcome || welcome.Sequence() != 1 ||
		welcome.VerificationID() != ticket.VerificationID() || welcome.ConnectionID() != ticket.ConnectionID() ||
		payload.SessionVersion != authority.Version() || payload.PingInterval != core.DefaultPingInterval ||
		payload.PongTimeout != core.DefaultPongTimeout || payload.IdleTimeout != core.DefaultIdleTimeout {
		t.Fatalf("server welcome = %#v, payload = %#v", welcome, payload)
	}
}

func TestHelloHandlerRejectsWrongBindingsBeforeAuthorityLookup(t *testing.T) {
	t.Parallel()

	handler, connection, ticket, _ := newHelloHandlerFixture(t)
	message, err := core.DecodeClientMessage(connection.read)
	if err != nil {
		t.Fatal(err)
	}
	wrongVerification, err := id.ParseVerification("ver_01ARZ3NDEKTSV4RRFFQ69G5FAZ")
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := core.NewMessage(core.MessageInput{
		Version: core.ProtocolVersionV1, ID: message.ID(), VerificationID: wrongVerification,
		Sequence: 1, OccurredAt: message.OccurredAt(), Payload: message.Payload(),
	})
	if err != nil {
		t.Fatal(err)
	}
	connection.read, err = core.EncodeClientMessage(wrong)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := handler.exchange(t.Context(), ticket, connection); !errors.Is(err, ErrProtocolViolation) {
		t.Fatalf("exchange() error = %v", err)
	}
	if !handler.authority.(*authoritySourceStub).ticket.ID().IsZero() {
		t.Fatal("authority source was called for a mismatched hello")
	}
}

func TestHelloHandlerCollapsesUnavailableOrExpiredAuthority(t *testing.T) {
	t.Parallel()

	t.Run("source unavailable", func(t *testing.T) {
		handler, connection, ticket, _ := newHelloHandlerFixture(t)
		handler.authority.(*authoritySourceStub).err = core.ErrSessionUnavailable
		if _, _, err := handler.exchange(t.Context(), ticket, connection); !errors.Is(err, ErrSessionUnavailable) {
			t.Fatalf("exchange() error = %v", err)
		}
	})
	t.Run("already expired", func(t *testing.T) {
		handler, connection, ticket, _ := newHelloHandlerFixture(t)
		expired, err := core.NewSessionAuthority(2, handler.clock.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		handler.authority.(*authoritySourceStub).authority = expired
		if _, _, err := handler.exchange(t.Context(), ticket, connection); !errors.Is(err, ErrSessionUnavailable) {
			t.Fatalf("exchange() error = %v", err)
		}
	})
}

func newHelloHandlerFixture(
	t *testing.T,
) (*HelloHandler, *wireConnectionStub, core.Ticket, core.SessionAuthority) {
	t.Helper()
	now := time.Date(2026, time.August, 30, 12, 0, 2, 0, time.UTC)
	identifiers, err := id.NewGenerator(handlerClock{now: now}, bytes.NewReader(bytes.Repeat([]byte{9}, 256)))
	if err != nil {
		t.Fatal(err)
	}
	messageID, err := identifiers.NewMessage()
	if err != nil {
		t.Fatal(err)
	}
	ticket := redeemedTicket(t)
	hello, err := core.NewMessage(core.MessageInput{
		Version: core.ProtocolVersionV1, ID: messageID, VerificationID: ticket.VerificationID(),
		Sequence: 1, OccurredAt: now, Payload: core.ClientHello{
			SDKVersion: "typescript-0.1.0", SupportedVersions: []uint16{core.ProtocolVersionV1},
			Capabilities: []string{"camera"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := core.EncodeClientMessage(hello)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := core.NewSessionAuthority(2, now.Add(20*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	source := &authoritySourceStub{authority: authority}
	handler, err := NewHelloHandler(source, identifiers, handlerClock{now: now}, core.DefaultLimits(), establishedStub{})
	if err != nil {
		t.Fatal(err)
	}

	return handler, &wireConnectionStub{read: encoded}, ticket, authority
}
