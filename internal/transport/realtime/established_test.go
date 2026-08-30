package realtime

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	core "github.com/Mujhtech/idenqa/internal/realtime"
)

const unsupportedCommandCode = "not_supported"

type connectionRead struct {
	encoded []byte
	err     error
}

type establishedConnectionStub struct {
	reads  chan connectionRead
	writes chan []byte
	pings  chan struct{}
}

type rejectUnsupportedCommands struct{}

type replayRepositoryStub struct {
	mu           sync.Mutex
	event        core.DurableEvent
	acknowledged chan core.EventCursor
}

func (*replayRepositoryStub) Append(context.Context, core.EventIntent) (core.DurableEvent, error) {
	return core.DurableEvent{}, errors.New("unexpected append")
}

func (stub *replayRepositoryStub) Replay(
	_ context.Context, _ core.Ticket, after core.EventCursor, _ uint16, _ time.Time,
) (core.ReplayWindow, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	events := []core.DurableEvent(nil)
	if after == 0 {
		events = []core.DurableEvent{stub.event}
	}
	return core.NewReplayWindow(after, 1, 1, events, false)
}

func (stub *replayRepositoryStub) Acknowledge(
	_ context.Context, _ core.Ticket, cursor core.EventCursor, _ time.Time,
) error {
	stub.acknowledged <- cursor
	return nil
}

func (rejectUnsupportedCommands) HandleClientCommand(
	context.Context,
	core.Ticket,
	core.Message,
) (core.CommandAcknowledgement, error) {
	return core.CommandAcknowledgement{
		Disposition: core.CommandDispositionRejected,
		Code:        unsupportedCommandCode,
	}, nil
}

func newEstablishedConnectionStub() *establishedConnectionStub {
	return &establishedConnectionStub{
		reads: make(chan connectionRead, 64), writes: make(chan []byte, 4), pings: make(chan struct{}, 4),
	}
}

func (connection *establishedConnectionStub) Read(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-connection.reads:
		return append([]byte(nil), result.encoded...), result.err
	}
}

func (connection *establishedConnectionStub) Write(ctx context.Context, encoded []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case connection.writes <- append([]byte(nil), encoded...):
		return nil
	}
}

func (connection *establishedConnectionStub) Ping(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case connection.pings <- struct{}{}:
		return nil
	}
}

func TestEstablishedLoopRejectsUnsupportedCommandAndAcceptsItsAcknowledgement(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		loop, clientIDs, ticket, now := newEstablishedLoopFixture(t, core.DefaultLimits())
		connection := newEstablishedConnectionStub()
		result := make(chan error, 1)
		go func() {
			result <- loop.serve(t.Context(), ticket, core.ClientHello{}, connection, make(chan struct{}))
		}()
		commandID, err := clientIDs.NewCommand()
		if err != nil {
			t.Fatal(err)
		}
		command := clientMessage(t, clientIDs, ticket, now, 2, commandID, core.CaptureStepUpdate{
			State: core.StepStarted, RequirementKey: "selfie", Artefact: "idenqa.artefact.selfie",
			AcquisitionMethod: "idenqa.method.live_camera",
		})
		connection.reads <- connectionRead{encoded: command}
		rejection := decodeServerWrite(t, <-connection.writes)
		acknowledgement := rejection.Payload().(core.CommandAcknowledgement)
		if rejection.Type() != core.MessageCommandRejected || rejection.Sequence() != 2 ||
			rejection.CommandID() != commandID || acknowledgement.Code != unsupportedCommandCode {
			t.Fatalf("rejection = %#v, payload = %#v", rejection, acknowledgement)
		}
		// Let the writer record the successful send before simulating a client that
		// acknowledges the bytes it just received. The channel-backed connection can
		// otherwise expose bytes before Write has returned, unlike a real socket.
		synctest.Wait()
		connection.reads <- connectionRead{encoded: clientMessage(t, clientIDs, ticket, now, 3, id.Command{},
			core.ServerEventAck{ServerSequence: 2})}
		connection.reads <- connectionRead{err: ErrConnectionClosed}
		if err := <-result; err != nil {
			t.Fatalf("serve() error = %v", err)
		}
	})
}

func TestEstablishedLoopReplaysAndPersistsDurableCursor(t *testing.T) {
	t.Parallel()

	loop, clientIDs, ticket, now := newEstablishedLoopFixture(t, core.DefaultLimits())
	eventID, err := clientIDs.NewEvent()
	if err != nil {
		t.Fatal(err)
	}
	intent, err := core.NewEventIntent(
		eventID, ticket.TenantID(), ticket.VerificationID(), id.Command{}, id.Message{}, id.Message{},
		now, now.Add(time.Minute), core.CaptureProgress{CompletedSteps: 1, TotalSteps: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	durable, err := core.RestoreDurableEvent(intent, 1)
	if err != nil {
		t.Fatal(err)
	}
	repository := &replayRepositoryStub{event: durable, acknowledged: make(chan core.EventCursor, 1)}
	loop.replay = repository
	connection := newEstablishedConnectionStub()
	result := make(chan error, 1)
	go func() {
		result <- loop.serve(t.Context(), ticket, core.ClientHello{}, connection, make(chan struct{}))
	}()
	replayed := decodeServerWrite(t, <-connection.writes)
	if replayed.EventCursor() != 1 || replayed.Type() != core.MessageCaptureProgress || replayed.Sequence() != 2 {
		t.Fatalf("replayed message = %#v", replayed)
	}
	connection.reads <- connectionRead{encoded: clientMessage(
		t, clientIDs, ticket, now, 2, id.Command{},
		core.ServerEventAck{ServerSequence: 2, EventCursor: 1},
	)}
	if acknowledged := <-repository.acknowledged; acknowledged != 1 {
		t.Fatalf("acknowledged cursor = %d", acknowledged)
	}
	connection.reads <- connectionRead{err: ErrConnectionClosed}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestEstablishedLoopEmitsResyncForPriorConnectionCursor(t *testing.T) {
	t.Parallel()

	loop, _, ticket, _ := newEstablishedLoopFixture(t, core.DefaultLimits())
	connection := newEstablishedConnectionStub()
	connection.reads <- connectionRead{err: ErrConnectionClosed}
	if err := loop.serve(t.Context(), ticket,
		core.ClientHello{LastAcknowledgedServerSequence: 9}, connection, make(chan struct{})); err != nil {
		t.Fatal(err)
	}
	resync := decodeServerWrite(t, <-connection.writes)
	if resync.Type() != core.MessageResyncRequired || resync.Sequence() != 2 ||
		resync.Payload().(core.ResyncRequired).Reason != "connection_sequence_reset" {
		t.Fatalf("resync = %#v", resync)
	}
}

func TestEstablishedLoopEmitsResyncAndStopsOnSequenceGap(t *testing.T) {
	t.Parallel()

	loop, clientIDs, ticket, now := newEstablishedLoopFixture(t, core.DefaultLimits())
	connection := newEstablishedConnectionStub()
	connection.reads <- connectionRead{encoded: clientMessage(t, clientIDs, ticket, now, 3, id.Command{},
		core.ServerEventAck{ServerSequence: 1})}
	result := make(chan error, 1)
	go func() {
		result <- loop.serve(t.Context(), ticket, core.ClientHello{}, connection, make(chan struct{}))
	}()
	resync := decodeServerWrite(t, <-connection.writes)
	if resync.Type() != core.MessageResyncRequired ||
		resync.Payload().(core.ResyncRequired).Reason != "connection_sequence_gap" {
		t.Fatalf("resync = %#v", resync)
	}
	if err := <-result; !errors.Is(err, ErrProtocolViolation) {
		t.Fatalf("serve() error = %v", err)
	}
}

func TestEstablishedLoopHeartbeatUsesNativeBoundedPing(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		config := core.DefaultLimitConfig()
		config.PingInterval = 5 * time.Second
		config.PongTimeout = 5 * time.Second
		config.IdleTimeout = 15 * time.Second
		limits, err := core.NewLimits(config)
		if err != nil {
			t.Fatal(err)
		}
		loop, _, _, _ := newEstablishedLoopFixture(t, limits)
		connection := newEstablishedConnectionStub()
		ctx, cancel := context.WithCancel(t.Context())
		result := make(chan error, 1)
		activity := make(chan struct{}, 1)
		go func() {
			result <- loop.heartbeat(ctx, connection, activity)
		}()
		time.Sleep(5 * time.Second)
		synctest.Wait()
		select {
		case <-connection.pings:
		default:
			t.Fatal("heartbeat did not ping")
		}
		select {
		case <-activity:
		default:
			t.Fatal("successful ping did not mark transport activity")
		}
		cancel()
		synctest.Wait()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("heartbeat() error = %v", err)
		}
	})
}

func TestEstablishedLoopIdleDeadlineResetsOnTransportActivity(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		config := core.DefaultLimitConfig()
		config.PingInterval = 5 * time.Second
		config.PongTimeout = 5 * time.Second
		config.IdleTimeout = 15 * time.Second
		limits, err := core.NewLimits(config)
		if err != nil {
			t.Fatal(err)
		}
		loop, _, _, _ := newEstablishedLoopFixture(t, limits)
		activity := make(chan struct{}, 1)
		result := make(chan error, 1)
		go func() {
			result <- loop.waitForIdle(t.Context(), activity)
		}()
		time.Sleep(14 * time.Second)
		activity <- struct{}{}
		synctest.Wait()
		time.Sleep(14 * time.Second)
		synctest.Wait()
		select {
		case err := <-result:
			t.Fatalf("idle deadline fired after activity: %v", err)
		default:
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if err := <-result; !errors.Is(err, ErrConnectionIdle) {
			t.Fatalf("waitForIdle() error = %v, want ErrConnectionIdle", err)
		}
	})
}

func TestEstablishedLoopFlushesDrainNoticeBeforeStopping(t *testing.T) {
	t.Parallel()

	loop, _, ticket, _ := newEstablishedLoopFixture(t, core.DefaultLimits())
	connection := newEstablishedConnectionStub()
	drain := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- loop.serve(t.Context(), ticket, core.ClientHello{}, connection, drain)
	}()
	close(drain)
	message := decodeServerWrite(t, <-connection.writes)
	payload := message.Payload().(core.ServerDraining)
	if message.Type() != core.MessageServerDraining || message.Sequence() != 2 ||
		payload.RetryAfter != drainRetryAfter {
		t.Fatalf("drain message = %#v, payload = %#v", message, payload)
	}
	if err := <-result; !errors.Is(err, ErrServerDraining) {
		t.Fatalf("serve() error = %v, want ErrServerDraining", err)
	}
}

func TestEstablishedLoopStopsWhenUnacknowledgedCommandsReachBound(t *testing.T) {
	t.Parallel()

	loop, clientIDs, ticket, now := newEstablishedLoopFixture(t, core.DefaultLimits())
	connection := newEstablishedConnectionStub()
	for sequence := uint64(2); sequence <= uint64(core.DefaultUnacknowledgedLimit+2); sequence++ {
		commandID, err := clientIDs.NewCommand()
		if err != nil {
			t.Fatal(err)
		}
		connection.reads <- connectionRead{encoded: clientMessage(
			t,
			clientIDs,
			ticket,
			now,
			sequence,
			commandID,
			core.CaptureStepUpdate{
				State: core.StepStarted, RequirementKey: "selfie", Artefact: "idenqa.artefact.selfie",
				AcquisitionMethod: "idenqa.method.live_camera",
			},
		)}
	}
	if err := loop.serve(
		t.Context(),
		ticket,
		core.ClientHello{},
		connection,
		make(chan struct{}),
	); !errors.Is(err, ErrSlowConsumer) {
		t.Fatalf("serve() error = %v, want ErrSlowConsumer", err)
	}
}

func newEstablishedLoopFixture(
	t *testing.T,
	limits core.Limits,
) (*EstablishedLoop, *id.Generator, core.Ticket, time.Time) {
	t.Helper()
	now := time.Date(2026, time.August, 30, 12, 0, 2, 0, time.UTC)
	serverIDs, err := id.NewGenerator(handlerClock{now: now}, bytes.NewReader(bytes.Repeat([]byte{3}, 512)))
	if err != nil {
		t.Fatal(err)
	}
	clientIDs, err := id.NewGenerator(handlerClock{now: now}, bytes.NewReader(bytes.Repeat([]byte{4}, 512)))
	if err != nil {
		t.Fatal(err)
	}
	loop, err := NewEstablishedLoop(serverIDs, handlerClock{now: now}, limits, rejectUnsupportedCommands{})
	if err != nil {
		t.Fatal(err)
	}

	return loop, clientIDs, redeemedTicket(t), now
}

func clientMessage(
	t *testing.T,
	identifiers *id.Generator,
	ticket core.Ticket,
	now time.Time,
	sequence uint64,
	commandID id.Command,
	payload core.Payload,
) []byte {
	t.Helper()
	messageID, err := identifiers.NewMessage()
	if err != nil {
		t.Fatal(err)
	}
	message, err := core.NewMessage(core.MessageInput{
		Version: core.ProtocolVersionV1, ID: messageID, VerificationID: ticket.VerificationID(),
		ConnectionID: ticket.ConnectionID(), Sequence: sequence, CommandID: commandID,
		OccurredAt: now, Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := core.EncodeClientMessage(message)
	if err != nil {
		t.Fatal(err)
	}

	return encoded
}

func decodeServerWrite(t *testing.T, encoded []byte) core.Message {
	t.Helper()
	message, err := core.DecodeServerMessage(encoded)
	if err != nil {
		t.Fatalf("DecodeServerMessage() error = %v; JSON = %s", err, encoded)
	}

	return message
}
