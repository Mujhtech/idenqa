package realtime

import (
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

const (
	// ProtocolVersionV1 is the numeric version encoded in v1 envelopes.
	ProtocolVersionV1 uint16 = 1
	// SubprotocolV1 is the exact WebSocket subprotocol for v1.
	SubprotocolV1 = "idenqa.capture.v1"
)

// ErrInvalidMessage identifies an invalid v1 envelope or payload.
var ErrInvalidMessage = errors.New("realtime: invalid message")

// MessageType identifies one closed v1 payload shape.
type MessageType string

const (
	// MessageClientHello begins the closed v1 message-type catalogue.
	MessageClientHello MessageType = "client.hello"
	// MessageServerWelcome confirms protocol and heartbeat settings.
	MessageServerWelcome MessageType = "server.welcome"
	// MessageServerEventAck acknowledges a server sequence.
	MessageServerEventAck MessageType = "server.event_ack"
	// MessageStepStarted reports that capture began.
	MessageStepStarted MessageType = "capture.step.started"
	// MessageStepFailed reports capture failure.
	MessageStepFailed MessageType = "capture.step.failed"
	// MessageStepCancelled reports capture cancellation.
	MessageStepCancelled MessageType = "capture.step.cancelled"
	// MessageCaptureCommand requests a capture action.
	MessageCaptureCommand MessageType = "capture.command"
	// MessageCommandResult reports a capture command result.
	MessageCommandResult MessageType = "capture.command_result"
	// MessageChallengeRequest starts a challenge.
	MessageChallengeRequest MessageType = "challenge.request"
	// MessageChallengeResponse reports a challenge response.
	MessageChallengeResponse MessageType = "challenge.response"
	// MessageChallengeCancel cancels a challenge.
	MessageChallengeCancel MessageType = "challenge.cancelled"
	// MessageCaptureProgress reports safe aggregate progress.
	MessageCaptureProgress MessageType = "capture.progress"
	// MessageVerificationCheckProgress reports one safe verification-check transition.
	MessageVerificationCheckProgress MessageType = "verification.check.progress"
	// MessageSessionState reports authoritative session state.
	MessageSessionState MessageType = "session.state_changed"
	// MessageResyncRequired requires authoritative REST recovery.
	MessageResyncRequired MessageType = "session.resync_required"
	// MessageServerDraining requests a reconnect during shutdown.
	MessageServerDraining MessageType = "server.draining"
	// MessageCommandAccepted acknowledges command acceptance.
	MessageCommandAccepted MessageType = "command.accepted"
	// MessageCommandRejected acknowledges command rejection.
	MessageCommandRejected MessageType = "command.rejected"
)

// Payload is sealed to the reviewed v1 catalogue.
type Payload interface {
	messageType() MessageType
	validate() error
}

// MessageInput contains the transport-independent protocol envelope.
type MessageInput struct {
	Version        uint16
	ID             id.Message
	VerificationID id.Verification
	ConnectionID   id.Connection
	Sequence       uint64
	EventCursor    EventCursor
	CommandID      id.Command
	CorrelationID  id.Message
	CausationID    id.Message
	OccurredAt     time.Time
	Payload        Payload
}

// Message is a validated immutable v1 control-channel message.
type Message struct {
	input       MessageInput
	messageType MessageType
}

// NewMessage validates an owned envelope and its typed payload.
func NewMessage(input MessageInput) (Message, error) {
	if input.Version != ProtocolVersionV1 || input.ID.IsZero() || input.VerificationID.IsZero() ||
		input.Sequence == 0 || input.OccurredAt.IsZero() || input.OccurredAt.Location() != time.UTC ||
		input.Payload == nil {
		return Message{}, ErrInvalidMessage
	}
	payload, err := clonePayload(input.Payload)
	if err != nil {
		return Message{}, err
	}
	messageType := payload.messageType()
	if err := payload.validate(); err != nil {
		return Message{}, fmt.Errorf("%w: %w", ErrInvalidMessage, err)
	}
	direction, err := messageDirection(messageType)
	if err != nil {
		return Message{}, err
	}
	if direction == directionServer && input.ConnectionID.IsZero() {
		return Message{}, fmt.Errorf("%w: server message requires connection ID", ErrInvalidMessage)
	}
	if messageType != MessageClientHello && input.ConnectionID.IsZero() {
		return Message{}, fmt.Errorf("%w: established message requires connection ID", ErrInvalidMessage)
	}
	requiresCommand := consequentialMessage(messageType)
	if requiresCommand == input.CommandID.IsZero() {
		return Message{}, fmt.Errorf("%w: command binding is invalid", ErrInvalidMessage)
	}
	if input.EventCursor > 0 && (direction != directionServer || !durableMessageType(messageType)) {
		return Message{}, fmt.Errorf("%w: durable event cursor binding is invalid", ErrInvalidMessage)
	}
	input.OccurredAt = input.OccurredAt.UTC()
	input.Payload = payload

	return Message{input: input, messageType: messageType}, nil
}

// Type returns the closed catalogue type.
func (message Message) Type() MessageType { return message.messageType }

// Version returns the numeric protocol version.
func (message Message) Version() uint16 { return message.input.Version }

// ID returns the unique message identifier.
func (message Message) ID() id.Message { return message.input.ID }

// VerificationID returns the capture verification bound to the message.
func (message Message) VerificationID() id.Verification { return message.input.VerificationID }

// ConnectionID returns the connection bound to the message, if established.
func (message Message) ConnectionID() id.Connection { return message.input.ConnectionID }

// Sequence returns the connection-local sender sequence.
func (message Message) Sequence() uint64 { return message.input.Sequence }

// EventCursor returns the verification-local durable position, when present.
func (message Message) EventCursor() EventCursor { return message.input.EventCursor }

// CommandID returns the consequential command identifier, when required.
func (message Message) CommandID() id.Command { return message.input.CommandID }

// CorrelationID returns the optional message correlation identifier.
func (message Message) CorrelationID() id.Message { return message.input.CorrelationID }

// CausationID returns the optional causing-message identifier.
func (message Message) CausationID() id.Message { return message.input.CausationID }

// OccurredAt returns the UTC message occurrence time.
func (message Message) OccurredAt() time.Time { return message.input.OccurredAt }

// Payload returns a defensive copy of the typed payload.
func (message Message) Payload() Payload {
	payload, err := clonePayload(message.input.Payload)
	if err != nil {
		panic("realtime: validated payload became invalid")
	}

	return payload
}

func durableMessageType(messageType MessageType) bool {
	switch messageType {
	case MessageCaptureCommand, MessageChallengeRequest, MessageChallengeCancel,
		MessageCaptureProgress, MessageVerificationCheckProgress, MessageSessionState:
		return true
	default:
		return false
	}
}

func clonePayload(payload Payload) (Payload, error) {
	switch value := payload.(type) {
	case ClientHello:
		value.SupportedVersions = append([]uint16(nil), value.SupportedVersions...)
		value.Capabilities = append([]string(nil), value.Capabilities...)
		return value, nil
	case ServerWelcome, ServerEventAck, CaptureStepUpdate, CaptureCommand,
		CaptureCommandResult, ChallengeUpdate, CaptureProgress, VerificationCheckProgress, SessionStateChanged,
		ResyncRequired, ServerDraining, CommandAcknowledgement:
		return value, nil
	default:
		return nil, fmt.Errorf("%w: payload must be a v1 value", ErrInvalidMessage)
	}
}

type direction uint8

const (
	directionClient direction = iota + 1
	directionServer
)

func messageDirection(messageType MessageType) (direction, error) {
	switch messageType {
	case MessageClientHello, MessageServerEventAck, MessageStepStarted, MessageStepFailed,
		MessageStepCancelled, MessageCommandResult, MessageChallengeResponse:
		return directionClient, nil
	case MessageServerWelcome, MessageCaptureCommand, MessageChallengeRequest, MessageChallengeCancel,
		MessageCaptureProgress, MessageVerificationCheckProgress, MessageSessionState,
		MessageResyncRequired, MessageServerDraining,
		MessageCommandAccepted, MessageCommandRejected:
		return directionServer, nil
	default:
		return 0, fmt.Errorf("%w: unsupported message type", ErrInvalidMessage)
	}
}

func consequentialMessage(messageType MessageType) bool {
	switch messageType {
	case MessageStepStarted, MessageStepFailed, MessageStepCancelled, MessageCaptureCommand,
		MessageCommandResult, MessageChallengeRequest, MessageChallengeResponse, MessageChallengeCancel,
		MessageCommandAccepted, MessageCommandRejected:
		return true
	default:
		return false
	}
}
