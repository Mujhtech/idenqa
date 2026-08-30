package realtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	core "github.com/Mujhtech/idenqa/internal/realtime"
)

// ErrSlowConsumer identifies a connection that exhausted its bounded outbound
// queue or unacknowledged-command allowance.
var ErrSlowConsumer = errors.New("realtime transport: slow consumer")

type outboundMessage struct {
	encoded   []byte
	sequence  uint64
	completed chan<- error
}

type serverMessageQueue struct {
	mu                sync.Mutex
	connection        establishedConnection
	identifiers       MessageIDGenerator
	clock             clock.Clock
	limits            core.Limits
	ticket            core.Ticket
	queued            chan outboundMessage
	nextSequence      uint64
	highestSent       uint64
	highestAck        uint64
	pendingCommands   []uint64
	durableBySequence map[uint64]core.EventCursor
	stopped           error
}

func newServerMessageQueue(
	connection establishedConnection,
	identifiers MessageIDGenerator,
	source clock.Clock,
	limits core.Limits,
	ticket core.Ticket,
) *serverMessageQueue {
	return &serverMessageQueue{
		connection: connection, identifiers: identifiers, clock: source, limits: limits, ticket: ticket,
		queued: make(chan outboundMessage, limits.OutboundQueueDepth()), nextSequence: 2, highestSent: 1,
		pendingCommands:   make([]uint64, 0, limits.UnacknowledgedLimit()),
		durableBySequence: make(map[uint64]core.EventCursor),
	}
}

func (queue *serverMessageQueue) enqueue(
	ctx context.Context,
	payload core.Payload,
	commandID id.Command,
	correlationID id.Message,
	causationID id.Message,
	completed chan<- error,
) error {
	return queue.enqueueEvent(ctx, payload, commandID, correlationID, causationID, 0, time.Time{}, completed)
}

func (queue *serverMessageQueue) enqueueEvent(
	ctx context.Context,
	payload core.Payload,
	commandID id.Command,
	correlationID id.Message,
	causationID id.Message,
	eventCursor core.EventCursor,
	occurredAt time.Time,
	completed chan<- error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if queue.stopped != nil {
		return queue.stopped
	}
	if !commandID.IsZero() && len(queue.pendingCommands) >= queue.limits.UnacknowledgedLimit() {
		return ErrSlowConsumer
	}
	if len(queue.queued) >= cap(queue.queued) {
		return ErrSlowConsumer
	}
	messageID, err := queue.identifiers.NewMessage()
	if err != nil {
		return fmt.Errorf("generate realtime server message ID: %w", err)
	}
	if occurredAt.IsZero() {
		occurredAt = queue.clock.Now().UTC()
	}
	message, err := core.NewMessage(core.MessageInput{
		Version: core.ProtocolVersionV1, ID: messageID, VerificationID: queue.ticket.VerificationID(),
		ConnectionID: queue.ticket.ConnectionID(), Sequence: queue.nextSequence, CommandID: commandID,
		CorrelationID: correlationID, CausationID: causationID, EventCursor: eventCursor,
		OccurredAt: occurredAt.UTC(), Payload: payload,
	})
	if err != nil {
		return fmt.Errorf("construct realtime server message: %w", err)
	}
	encoded, err := core.EncodeServerMessage(message)
	if err != nil {
		return fmt.Errorf("encode realtime server message: %w", err)
	}
	queue.queued <- outboundMessage{encoded: encoded, sequence: queue.nextSequence, completed: completed}
	if !commandID.IsZero() {
		queue.pendingCommands = append(queue.pendingCommands, queue.nextSequence)
	}
	if eventCursor > 0 {
		queue.durableBySequence[queue.nextSequence] = eventCursor
	}
	queue.nextSequence++

	return nil
}

func (queue *serverMessageQueue) sendEventAndWait(
	ctx context.Context,
	event core.DurableEvent,
) error {
	completed := make(chan error, 1)
	intent := event.Intent()
	if err := queue.enqueueEvent(
		ctx, intent.Payload(), intent.CommandID(), intent.CorrelationID(), intent.CausationID(),
		event.Cursor(), intent.OccurredAt(), completed,
	); err != nil {
		return err
	}
	select {
	case err := <-completed:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (queue *serverMessageQueue) sendAndWait(
	ctx context.Context,
	payload core.Payload,
	commandID id.Command,
	correlationID id.Message,
	causationID id.Message,
) error {
	completed := make(chan error, 1)
	if err := queue.enqueue(ctx, payload, commandID, correlationID, causationID, completed); err != nil {
		return err
	}
	select {
	case err := <-completed:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (queue *serverMessageQueue) run(ctx context.Context) (runErr error) {
	defer func() { queue.stop(runErr) }()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case message := <-queue.queued:
			writeContext, cancel := context.WithTimeout(ctx, queue.limits.WriteTimeout())
			err := queue.connection.Write(writeContext, message.encoded)
			cancel()
			if err == nil {
				queue.markSent(message.sequence)
			}
			completeOutbound(message, err)
			if err != nil {
				return err
			}
		}
	}
}

func (queue *serverMessageQueue) acknowledge(
	sequence uint64,
	eventCursor core.EventCursor,
) (core.EventCursor, bool) {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if sequence == 0 || sequence > queue.highestSent || sequence < queue.highestAck {
		return 0, false
	}
	highestDurable := core.EventCursor(0)
	for candidateSequence, candidateCursor := range queue.durableBySequence {
		if candidateSequence <= sequence && candidateCursor > highestDurable {
			highestDurable = candidateCursor
		}
	}
	if eventCursor != highestDurable {
		return 0, false
	}
	queue.highestAck = sequence
	firstPending := 0
	for firstPending < len(queue.pendingCommands) && queue.pendingCommands[firstPending] <= sequence {
		firstPending++
	}
	queue.pendingCommands = queue.pendingCommands[firstPending:]
	for candidateSequence := range queue.durableBySequence {
		if candidateSequence <= sequence {
			delete(queue.durableBySequence, candidateSequence)
		}
	}

	return highestDurable, true
}

func (queue *serverMessageQueue) markSent(sequence uint64) {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	queue.highestSent = sequence
}

func (queue *serverMessageQueue) stop(err error) {
	if err == nil {
		err = context.Canceled
	}
	queue.mu.Lock()
	queue.stopped = err
	queue.mu.Unlock()
	for {
		select {
		case message := <-queue.queued:
			completeOutbound(message, err)
		default:
			return
		}
	}
}

func completeOutbound(message outboundMessage, err error) {
	if message.completed != nil {
		message.completed <- err
	}
}
