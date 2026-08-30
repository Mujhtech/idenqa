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

const (
	drainRetryAfter = 5 * time.Second
)

type establishedConnection interface {
	messageConnection
	Ping(context.Context) error
}

// ClientCommandHandler applies one consequential client message idempotently.
// Implementations must use the command ID and current application authority;
// transport authentication alone is insufficient for a consequential effect.
type ClientCommandHandler interface {
	HandleClientCommand(context.Context, core.Ticket, core.Message) (core.CommandAcknowledgement, error)
}

// EstablishedLoop owns connection-local client sequencing, acknowledgements,
// bounded writes, and native heartbeat after hello/welcome.
type EstablishedLoop struct {
	identifiers MessageIDGenerator
	clock       clock.Clock
	limits      core.Limits
	commands    ClientCommandHandler
	replay      core.ReplayRepository
	wakeups     core.ReplayWakeups
}

// UseReplayWakeups installs an optional latency optimisation before serving.
// Durable polling remains active when notifications are absent or lost.
func (loop *EstablishedLoop) UseReplayWakeups(wakeups core.ReplayWakeups) error {
	if loop == nil || wakeups == nil {
		return errors.New("realtime transport: replay wakeups are invalid")
	}
	loop.wakeups = wakeups
	return nil
}

// NewEstablishedLoop constructs the bounded v1 established-session loop.
func NewEstablishedLoop(
	identifiers MessageIDGenerator,
	source clock.Clock,
	limits core.Limits,
	commands ClientCommandHandler,
	replay ...core.ReplayRepository,
) (*EstablishedLoop, error) {
	if identifiers == nil || source == nil || commands == nil || limits.PingInterval() == 0 ||
		limits.PongTimeout() == 0 || limits.WriteTimeout() == 0 || len(replay) > 1 ||
		(len(replay) == 1 && replay[0] == nil) {
		return nil, errors.New("realtime transport: established loop dependencies are invalid")
	}

	loop := &EstablishedLoop{identifiers: identifiers, clock: source, limits: limits, commands: commands}
	if len(replay) == 1 {
		loop.replay = replay[0]
	}
	return loop, nil
}

// ServeEstablished runs until peer close, authority deadline, process
// cancellation, heartbeat failure, or protocol failure.
func (loop *EstablishedLoop) ServeEstablished(
	ctx context.Context,
	ticket core.Ticket,
	hello core.ClientHello,
	connection *Connection,
	drain <-chan struct{},
) error {
	return loop.serve(ctx, ticket, hello, connection, drain)
}

func (loop *EstablishedLoop) serve(
	ctx context.Context,
	ticket core.Ticket,
	hello core.ClientHello,
	connection establishedConnection,
	drain <-chan struct{},
) error {
	if loop == nil || connection == nil || drain == nil || ticket.ConnectionID().IsZero() {
		return errors.New("realtime transport: established loop is not initialised")
	}
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	queue := newServerMessageQueue(connection, loop.identifiers, loop.clock, loop.limits, ticket)
	errorsChannel := make(chan error, 6)
	activity := make(chan struct{}, 1)
	go func() {
		errorsChannel <- queue.run(runContext)
	}()
	if hello.LastAcknowledgedServerSequence > 0 && hello.LastAcknowledgedEventCursor == 0 {
		if err := queue.sendAndWait(runContext, core.ResyncRequired{Reason: "connection_sequence_reset"},
			id.Command{}, id.Message{}, id.Message{}); err != nil {
			cancel()

			return establishedErrors(err, <-errorsChannel)
		}
	}
	durableCursor := hello.LastAcknowledgedEventCursor
	if loop.replay != nil {
		var err error
		durableCursor, err = loop.replayDurable(runContext, ticket, durableCursor, queue)
		if err != nil {
			cancel()
			return establishedErrors(err, <-errorsChannel)
		}
	}

	go func() {
		errorsChannel <- loop.read(runContext, ticket, connection, queue, activity)
	}()
	go func() {
		errorsChannel <- loop.heartbeat(runContext, connection, activity)
	}()
	go func() {
		errorsChannel <- loop.waitForDrain(runContext, queue, drain)
	}()
	go func() {
		errorsChannel <- loop.waitForIdle(runContext, activity)
	}()
	go func() {
		errorsChannel <- loop.followDurable(runContext, ticket, durableCursor, queue)
	}()
	first := <-errorsChannel
	cancel()
	remaining := []error{
		<-errorsChannel, <-errorsChannel, <-errorsChannel, <-errorsChannel, <-errorsChannel,
	}

	return establishedErrors(first, remaining[0], remaining[1], remaining[2], remaining[3], remaining[4])
}

func (loop *EstablishedLoop) read(
	ctx context.Context,
	ticket core.Ticket,
	connection establishedConnection,
	queue *serverMessageQueue,
	activity chan<- struct{},
) error {
	expectedSequence := uint64(2)
	for {
		encoded, err := connection.Read(ctx)
		if err != nil {
			return err
		}
		markActivity(activity)
		message, err := core.DecodeClientMessage(encoded)
		if err != nil {
			return fmt.Errorf("%w: decode established client message", ErrProtocolViolation)
		}
		if message.VerificationID() != ticket.VerificationID() ||
			message.ConnectionID() != ticket.ConnectionID() || message.Sequence() != expectedSequence {
			protocolErr := fmt.Errorf("%w: established message binding or sequence", ErrProtocolViolation)
			if writeErr := queue.sendAndWait(ctx, core.ResyncRequired{Reason: "connection_sequence_gap"},
				id.Command{}, message.ID(), message.ID()); writeErr != nil {
				return errors.Join(protocolErr, writeErr)
			}

			return protocolErr
		}
		expectedSequence++

		switch payload := message.Payload().(type) {
		case core.ServerEventAck:
			acknowledgedCursor, valid := queue.acknowledge(payload.ServerSequence, payload.EventCursor)
			if !message.CommandID().IsZero() || !valid {
				return fmt.Errorf("%w: server acknowledgement is invalid", ErrProtocolViolation)
			}
			if acknowledgedCursor > 0 && loop.replay != nil {
				if err := loop.replay.Acknowledge(ctx, ticket, acknowledgedCursor, loop.clock.Now().UTC()); err != nil {
					return fmt.Errorf("persist realtime event acknowledgement: %w", err)
				}
			}
		case core.CaptureStepUpdate, core.CaptureCommandResult, core.ChallengeUpdate:
			acknowledgement, err := loop.commands.HandleClientCommand(ctx, ticket, message)
			if err != nil {
				return fmt.Errorf("handle realtime client command: %w", err)
			}
			correlationID := message.CorrelationID()
			if correlationID.IsZero() {
				correlationID = message.ID()
			}
			if err := queue.enqueue(
				ctx,
				acknowledgement,
				message.CommandID(),
				correlationID,
				message.ID(),
				nil,
			); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: message is not valid after hello", ErrProtocolViolation)
		}
	}
}

func (loop *EstablishedLoop) replayDurable(
	ctx context.Context,
	ticket core.Ticket,
	after core.EventCursor,
	queue *serverMessageQueue,
) (core.EventCursor, error) {
	for {
		window, err := loop.replay.Replay(ctx, ticket, after, 256, loop.clock.Now().UTC())
		if errors.Is(err, core.ErrReplayCursorAhead) {
			return 0, queue.sendAndWait(ctx, core.ResyncRequired{Reason: "event_cursor_ahead"},
				id.Command{}, id.Message{}, id.Message{})
		}
		if err != nil {
			return after, fmt.Errorf("replay durable realtime events: %w", err)
		}
		if window.Gap() {
			return window.Latest(), queue.sendAndWait(
				ctx, core.ResyncRequired{Reason: "event_retention_gap"},
				id.Command{}, id.Message{}, id.Message{},
			)
		}
		for _, event := range window.Events() {
			if err := queue.sendEventAndWait(ctx, event); err != nil {
				return after, err
			}
			after = event.Cursor()
		}
		if !window.HasMore() {
			return after, nil
		}
	}
}

func (loop *EstablishedLoop) followDurable(
	ctx context.Context,
	ticket core.Ticket,
	after core.EventCursor,
	queue *serverMessageQueue,
) error {
	if loop.replay == nil {
		<-ctx.Done()
		return ctx.Err()
	}
	for {
		waitContext, cancel := context.WithCancel(ctx)
		wakeup := make(chan error, 1)
		if loop.wakeups != nil {
			go func() { wakeup <- loop.wakeups.Wait(waitContext, ticket) }()
		}
		timer := time.NewTimer(500 * time.Millisecond)
		woke := false
		select {
		case <-ctx.Done():
			cancel()
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
			cancel()
		case wakeErr := <-wakeup:
			woke = true
			if wakeErr == nil {
				cancel()
				if !timer.Stop() {
					<-timer.C
				}
			} else {
				select {
				case <-ctx.Done():
					cancel()
					if !timer.Stop() {
						<-timer.C
					}
					return ctx.Err()
				case <-timer.C:
					cancel()
				}
			}
		}
		if loop.wakeups != nil && !woke {
			<-wakeup
		}
		var err error
		after, err = loop.replayDurable(ctx, ticket, after, queue)
		if err != nil {
			return err
		}
	}
}

func (*EstablishedLoop) waitForDrain(
	ctx context.Context,
	queue *serverMessageQueue,
	drain <-chan struct{},
) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-drain:
		if err := queue.sendAndWait(
			ctx,
			core.ServerDraining{RetryAfter: drainRetryAfter},
			id.Command{},
			id.Message{},
			id.Message{},
		); err != nil {
			return err
		}

		return ErrServerDraining
	}
}

func (loop *EstablishedLoop) heartbeat(
	ctx context.Context,
	connection establishedConnection,
	activity chan<- struct{},
) error {
	ticker := time.NewTicker(loop.limits.PingInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			pingContext, cancel := context.WithTimeout(ctx, loop.limits.PongTimeout())
			err := connection.Ping(pingContext)
			cancel()
			if err != nil {
				return err
			}
			markActivity(activity)
		}
	}
}

func (loop *EstablishedLoop) waitForIdle(ctx context.Context, activity <-chan struct{}) error {
	timer := time.NewTimer(loop.limits.IdleTimeout())
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-activity:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(loop.limits.IdleTimeout())
		case <-timer.C:
			return ErrConnectionIdle
		}
	}
}

func markActivity(activity chan<- struct{}) {
	select {
	case activity <- struct{}{}:
	default:
	}
}

func establishedErrors(found ...error) error {
	for _, err := range found {
		if meaningfulEstablishedError(err) {
			return err
		}
	}

	return nil
}

func meaningfulEstablishedError(err error) bool {
	return err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) &&
		!errors.Is(err, ErrConnectionClosed)
}

var _ EstablishedSessionHandler = (*EstablishedLoop)(nil)
