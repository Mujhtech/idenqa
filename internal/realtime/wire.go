package realtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

// ErrInvalidWireMessage identifies malformed or directionally invalid v1 JSON.
var ErrInvalidWireMessage = errors.New("realtime: invalid v1 wire message")

type wireEnvelope struct {
	Version        uint16          `json:"version"`
	MessageID      string          `json:"message_id"`
	VerificationID string          `json:"verification_id"`
	ConnectionID   *string         `json:"connection_id,omitempty"`
	Sequence       uint64          `json:"sequence"`
	EventCursor    EventCursor     `json:"event_cursor,omitempty"`
	CommandID      *string         `json:"command_id,omitempty"`
	CorrelationID  *string         `json:"correlation_id,omitempty"`
	CausationID    *string         `json:"causation_id,omitempty"`
	OccurredAt     string          `json:"occurred_at"`
	Type           MessageType     `json:"type"`
	Payload        json.RawMessage `json:"payload"`
}

// DecodeClientMessage strictly decodes one complete client-to-server v1 JSON object.
func DecodeClientMessage(encoded []byte) (Message, error) {
	return decodeWireMessage(encoded, directionClient)
}

// DecodeServerMessage strictly decodes one complete server-to-client v1 JSON object.
// It is primarily useful to SDK conformance tests.
func DecodeServerMessage(encoded []byte) (Message, error) {
	return decodeWireMessage(encoded, directionServer)
}

// EncodeClientMessage encodes a validated client-to-server message as v1 JSON.
func EncodeClientMessage(message Message) ([]byte, error) {
	return encodeWireMessage(message, directionClient)
}

// EncodeServerMessage encodes a validated server-to-client message as v1 JSON.
func EncodeServerMessage(message Message) ([]byte, error) {
	return encodeWireMessage(message, directionServer)
}

func decodeWireMessage(encoded []byte, expected direction) (Message, error) {
	var envelope wireEnvelope
	if err := decodeStrictJSON(encoded, &envelope); err != nil {
		return Message{}, wireError(err)
	}
	if len(bytes.TrimSpace(envelope.Payload)) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Payload), []byte("null")) ||
		bytes.TrimSpace(envelope.Payload)[0] != '{' {
		return Message{}, wireError(errors.New("payload must be an object"))
	}
	direction, err := messageDirection(envelope.Type)
	if err != nil || direction != expected {
		return Message{}, wireError(errors.New("message direction is invalid"))
	}

	messageID, err := id.ParseMessage(envelope.MessageID)
	if err != nil {
		return Message{}, wireError(errors.New("message ID is invalid"))
	}
	verificationID, err := id.ParseVerification(envelope.VerificationID)
	if err != nil {
		return Message{}, wireError(errors.New("verification ID is invalid"))
	}
	connectionID, err := parseOptionalConnection(envelope.ConnectionID)
	if err != nil {
		return Message{}, wireError(err)
	}
	commandID, err := parseOptionalCommand(envelope.CommandID)
	if err != nil {
		return Message{}, wireError(err)
	}
	correlationID, err := parseOptionalMessage(envelope.CorrelationID)
	if err != nil {
		return Message{}, wireError(err)
	}
	causationID, err := parseOptionalMessage(envelope.CausationID)
	if err != nil {
		return Message{}, wireError(err)
	}
	occurredAt, err := parseWireTime(envelope.OccurredAt)
	if err != nil {
		return Message{}, wireError(err)
	}
	payload, err := decodeWirePayload(envelope.Type, envelope.Payload)
	if err != nil {
		return Message{}, wireError(err)
	}

	message, err := NewMessage(MessageInput{
		Version: envelope.Version, ID: messageID, VerificationID: verificationID,
		ConnectionID: connectionID, Sequence: envelope.Sequence, CommandID: commandID,
		EventCursor:   envelope.EventCursor,
		CorrelationID: correlationID, CausationID: causationID, OccurredAt: occurredAt,
		Payload: payload,
	})
	if err != nil {
		return Message{}, wireError(err)
	}

	return message, nil
}

func encodeWireMessage(message Message, expected direction) ([]byte, error) {
	direction, err := messageDirection(message.Type())
	if err != nil || direction != expected {
		return nil, wireError(errors.New("message direction is invalid"))
	}
	payload, err := encodeWirePayload(message.Payload())
	if err != nil {
		return nil, wireError(err)
	}
	envelope := wireEnvelope{
		Version: message.Version(), MessageID: message.ID().String(),
		VerificationID: message.VerificationID().String(), Sequence: message.Sequence(),
		EventCursor: message.EventCursor(),
		OccurredAt:  message.OccurredAt().UTC().Format(time.RFC3339Nano), Type: message.Type(),
		Payload: payload,
	}
	envelope.ConnectionID = optionalString(message.ConnectionID().String())
	envelope.CommandID = optionalString(message.CommandID().String())
	envelope.CorrelationID = optionalString(message.CorrelationID().String())
	envelope.CausationID = optionalString(message.CausationID().String())
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return nil, wireError(fmt.Errorf("encode envelope: %w", err))
	}

	return encoded, nil
}

func decodeWirePayload(messageType MessageType, encoded []byte) (Payload, error) {
	switch messageType {
	case MessageClientHello:
		var value struct {
			SDKVersion        string      `json:"sdk_version"`
			SupportedVersions []uint16    `json:"supported_versions"`
			LastAck           uint64      `json:"last_acknowledged_server_sequence,omitempty"`
			LastEventCursor   EventCursor `json:"last_acknowledged_event_cursor,omitempty"`
			Capabilities      []string    `json:"capabilities,omitempty"`
		}
		if err := decodeStrictJSON(encoded, &value); err != nil {
			return nil, err
		}
		return ClientHello{SDKVersion: value.SDKVersion, SupportedVersions: value.SupportedVersions,
			LastAcknowledgedServerSequence: value.LastAck,
			LastAcknowledgedEventCursor:    value.LastEventCursor, Capabilities: value.Capabilities}, nil
	case MessageServerWelcome:
		var value struct {
			SelectedVersion uint16 `json:"selected_version"`
			SessionVersion  int64  `json:"session_version"`
			PingIntervalMS  int64  `json:"ping_interval_ms"`
			PongTimeoutMS   int64  `json:"pong_timeout_ms"`
			IdleTimeoutMS   int64  `json:"idle_timeout_ms"`
		}
		if err := decodeStrictJSON(encoded, &value); err != nil {
			return nil, err
		}
		return ServerWelcome{SelectedVersion: value.SelectedVersion, SessionVersion: value.SessionVersion,
			PingInterval: milliseconds(value.PingIntervalMS), PongTimeout: milliseconds(value.PongTimeoutMS),
			IdleTimeout: milliseconds(value.IdleTimeoutMS)}, nil
	case MessageServerEventAck:
		var value struct {
			ServerSequence uint64      `json:"server_sequence"`
			EventCursor    EventCursor `json:"event_cursor,omitempty"`
		}
		if err := decodeStrictJSON(encoded, &value); err != nil {
			return nil, err
		}
		return ServerEventAck{ServerSequence: value.ServerSequence, EventCursor: value.EventCursor}, nil
	case MessageStepStarted, MessageStepFailed, MessageStepCancelled:
		var value struct {
			RequirementKey    string  `json:"requirement_key"`
			Artefact          string  `json:"artefact"`
			AcquisitionMethod string  `json:"acquisition_method"`
			Code              *string `json:"code,omitempty"`
		}
		if err := decodeStrictJSON(encoded, &value); err != nil {
			return nil, err
		}
		state := map[MessageType]StepState{MessageStepStarted: StepStarted, MessageStepFailed: StepFailed,
			MessageStepCancelled: StepCancelled}[messageType]
		if (state == StepStarted && value.Code != nil) || (state != StepStarted && value.Code == nil) {
			return nil, errors.New("step result code presence is invalid")
		}
		return CaptureStepUpdate{State: state, RequirementKey: value.RequirementKey, Artefact: value.Artefact,
			AcquisitionMethod: value.AcquisitionMethod, Code: stringValue(value.Code)}, nil
	case MessageCaptureCommand:
		var value struct {
			Action            CommandAction `json:"action"`
			RequirementKey    string        `json:"requirement_key"`
			Artefact          string        `json:"artefact"`
			AcquisitionMethod *string       `json:"acquisition_method,omitempty"`
		}
		if err := decodeStrictJSON(encoded, &value); err != nil {
			return nil, err
		}
		return CaptureCommand{Action: value.Action, RequirementKey: value.RequirementKey,
			Artefact: value.Artefact, AcquisitionMethod: stringValue(value.AcquisitionMethod)}, nil
	case MessageCommandResult:
		var value struct {
			Outcome CommandOutcome `json:"outcome"`
			Code    *string        `json:"code,omitempty"`
		}
		if err := decodeStrictJSON(encoded, &value); err != nil {
			return nil, err
		}
		if (value.Outcome == CommandCompleted && value.Code != nil) ||
			(value.Outcome != CommandCompleted && value.Code == nil) {
			return nil, errors.New("command result code presence is invalid")
		}
		return CaptureCommandResult{Outcome: value.Outcome, Code: stringValue(value.Code)}, nil
	case MessageChallengeRequest:
		var value struct {
			ChallengeID string `json:"challenge_id"`
			Kind        string `json:"kind"`
			ExpiresAt   string `json:"expires_at"`
		}
		if err := decodeStrictJSON(encoded, &value); err != nil {
			return nil, err
		}
		challengeID, err := id.ParseChallenge(value.ChallengeID)
		if err != nil {
			return nil, errors.New("challenge ID is invalid")
		}
		expiresAt, err := parseWireTime(value.ExpiresAt)
		if err != nil {
			return nil, err
		}
		return ChallengeUpdate{Action: ChallengeRequested, ChallengeID: challengeID, Kind: value.Kind,
			ExpiresAt: expiresAt}, nil
	case MessageChallengeResponse:
		var value struct {
			ChallengeID string  `json:"challenge_id"`
			Outcome     string  `json:"outcome"`
			Code        *string `json:"code,omitempty"`
		}
		if err := decodeStrictJSON(encoded, &value); err != nil {
			return nil, err
		}
		challengeID, err := id.ParseChallenge(value.ChallengeID)
		if err != nil {
			return nil, errors.New("challenge ID is invalid")
		}
		if (value.Outcome == "completed" && value.Code != nil) ||
			(value.Outcome != "completed" && value.Code == nil) {
			return nil, errors.New("challenge response code presence is invalid")
		}
		return ChallengeUpdate{Action: ChallengeResponded, ChallengeID: challengeID,
			Outcome: value.Outcome, Code: stringValue(value.Code)}, nil
	case MessageChallengeCancel:
		var value struct {
			ChallengeID string `json:"challenge_id"`
			Code        string `json:"code"`
		}
		if err := decodeStrictJSON(encoded, &value); err != nil {
			return nil, err
		}
		challengeID, err := id.ParseChallenge(value.ChallengeID)
		if err != nil {
			return nil, errors.New("challenge ID is invalid")
		}
		return ChallengeUpdate{Action: ChallengeCancelled, ChallengeID: challengeID, Code: value.Code}, nil
	case MessageCaptureProgress:
		var value struct {
			CompletedSteps uint32 `json:"completed_steps"`
			TotalSteps     uint32 `json:"total_steps"`
		}
		if err := decodeStrictJSON(encoded, &value); err != nil {
			return nil, err
		}
		return CaptureProgress{CompletedSteps: value.CompletedSteps, TotalSteps: value.TotalSteps}, nil
	case MessageVerificationCheckProgress:
		var value struct {
			CheckID      string `json:"check_id"`
			State        string `json:"state"`
			CheckVersion int64  `json:"check_version"`
		}
		if err := decodeStrictJSON(encoded, &value); err != nil {
			return nil, err
		}
		checkID, err := id.ParseCheck(value.CheckID)
		if err != nil {
			return nil, errors.New("check ID is invalid")
		}
		return VerificationCheckProgress{
			CheckID: checkID, State: value.State, CheckVersion: value.CheckVersion,
		}, nil
	case MessageSessionState:
		var value struct {
			State          string `json:"state"`
			SessionVersion int64  `json:"session_version"`
		}
		if err := decodeStrictJSON(encoded, &value); err != nil {
			return nil, err
		}
		return SessionStateChanged{State: value.State, SessionVersion: value.SessionVersion}, nil
	case MessageResyncRequired:
		var value struct {
			Reason string `json:"reason"`
		}
		if err := decodeStrictJSON(encoded, &value); err != nil {
			return nil, err
		}
		return ResyncRequired{Reason: value.Reason}, nil
	case MessageServerDraining:
		var value struct {
			RetryAfterMS int64 `json:"retry_after_ms"`
		}
		if err := decodeStrictJSON(encoded, &value); err != nil {
			return nil, err
		}
		return ServerDraining{RetryAfter: milliseconds(value.RetryAfterMS)}, nil
	case MessageCommandAccepted, MessageCommandRejected:
		var value struct {
			Code *string `json:"code,omitempty"`
		}
		if err := decodeStrictJSON(encoded, &value); err != nil {
			return nil, err
		}
		if (messageType == MessageCommandAccepted && value.Code != nil) ||
			(messageType == MessageCommandRejected && value.Code == nil) {
			return nil, errors.New("command acknowledgement code presence is invalid")
		}
		disposition := CommandDispositionAccepted
		if messageType == MessageCommandRejected {
			disposition = CommandDispositionRejected
		}
		return CommandAcknowledgement{Disposition: disposition, Code: stringValue(value.Code)}, nil
	default:
		return nil, errors.New("message type is unsupported")
	}
}

func encodeWirePayload(payload Payload) (json.RawMessage, error) {
	var value any
	switch payload := payload.(type) {
	case ClientHello:
		value = struct {
			SDKVersion        string      `json:"sdk_version"`
			SupportedVersions []uint16    `json:"supported_versions"`
			LastAck           uint64      `json:"last_acknowledged_server_sequence,omitempty"`
			LastEventCursor   EventCursor `json:"last_acknowledged_event_cursor,omitempty"`
			Capabilities      []string    `json:"capabilities,omitempty"`
		}{payload.SDKVersion, payload.SupportedVersions, payload.LastAcknowledgedServerSequence,
			payload.LastAcknowledgedEventCursor, payload.Capabilities}
	case ServerWelcome:
		value = struct {
			SelectedVersion uint16 `json:"selected_version"`
			SessionVersion  int64  `json:"session_version"`
			PingIntervalMS  int64  `json:"ping_interval_ms"`
			PongTimeoutMS   int64  `json:"pong_timeout_ms"`
			IdleTimeoutMS   int64  `json:"idle_timeout_ms"`
		}{payload.SelectedVersion, payload.SessionVersion, payload.PingInterval.Milliseconds(),
			payload.PongTimeout.Milliseconds(), payload.IdleTimeout.Milliseconds()}
	case ServerEventAck:
		value = struct {
			ServerSequence uint64      `json:"server_sequence"`
			EventCursor    EventCursor `json:"event_cursor,omitempty"`
		}{payload.ServerSequence, payload.EventCursor}
	case CaptureStepUpdate:
		value = struct {
			RequirementKey    string `json:"requirement_key"`
			Artefact          string `json:"artefact"`
			AcquisitionMethod string `json:"acquisition_method"`
			Code              string `json:"code,omitempty"`
		}{payload.RequirementKey, payload.Artefact, payload.AcquisitionMethod, payload.Code}
	case CaptureCommand:
		value = struct {
			Action            CommandAction `json:"action"`
			RequirementKey    string        `json:"requirement_key"`
			Artefact          string        `json:"artefact"`
			AcquisitionMethod string        `json:"acquisition_method,omitempty"`
		}{payload.Action, payload.RequirementKey, payload.Artefact, payload.AcquisitionMethod}
	case CaptureCommandResult:
		value = struct {
			Outcome CommandOutcome `json:"outcome"`
			Code    string         `json:"code,omitempty"`
		}{payload.Outcome, payload.Code}
	case ChallengeUpdate:
		value = struct {
			ChallengeID string `json:"challenge_id"`
			Kind        string `json:"kind,omitempty"`
			Outcome     string `json:"outcome,omitempty"`
			Code        string `json:"code,omitempty"`
			ExpiresAt   string `json:"expires_at,omitempty"`
		}{ChallengeID: payload.ChallengeID.String(), Kind: payload.Kind, Outcome: payload.Outcome, Code: payload.Code}
		if !payload.ExpiresAt.IsZero() {
			valueAsChallenge := value.(struct {
				ChallengeID string `json:"challenge_id"`
				Kind        string `json:"kind,omitempty"`
				Outcome     string `json:"outcome,omitempty"`
				Code        string `json:"code,omitempty"`
				ExpiresAt   string `json:"expires_at,omitempty"`
			})
			valueAsChallenge.ExpiresAt = payload.ExpiresAt.UTC().Format(time.RFC3339Nano)
			value = valueAsChallenge
		}
	case CaptureProgress:
		value = struct {
			CompletedSteps uint32 `json:"completed_steps"`
			TotalSteps     uint32 `json:"total_steps"`
		}{payload.CompletedSteps, payload.TotalSteps}
	case VerificationCheckProgress:
		value = struct {
			CheckID      string `json:"check_id"`
			State        string `json:"state"`
			CheckVersion int64  `json:"check_version"`
		}{payload.CheckID.String(), payload.State, payload.CheckVersion}
	case SessionStateChanged:
		value = struct {
			State          string `json:"state"`
			SessionVersion int64  `json:"session_version"`
		}{payload.State, payload.SessionVersion}
	case ResyncRequired:
		value = struct {
			Reason string `json:"reason"`
		}{payload.Reason}
	case ServerDraining:
		value = struct {
			RetryAfterMS int64 `json:"retry_after_ms"`
		}{payload.RetryAfter.Milliseconds()}
	case CommandAcknowledgement:
		value = struct {
			Code string `json:"code,omitempty"`
		}{payload.Code}
	default:
		return nil, errors.New("payload is unsupported")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode payload: %w", err)
	}

	return encoded, nil
}

func decodeStrictJSON(encoded []byte, target any) error {
	if err := validateJSONTokens(encoded); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON contains more than one value")
		}
		return fmt.Errorf("finish JSON: %w", err)
	}

	return nil
}

func validateJSONTokens(encoded []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	var walk func() error
	walk = func() error {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("read JSON token: %w", err)
		}
		if token == nil {
			return errors.New("JSON null values are not permitted")
		}
		delimiter, compound := token.(json.Delim)
		if !compound {
			return nil
		}
		switch delimiter {
		case '{':
			keys := make(map[string]struct{})
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return fmt.Errorf("read JSON object key: %w", err)
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("JSON object key is invalid")
				}
				if _, duplicate := keys[key]; duplicate {
					return fmt.Errorf("JSON object contains duplicate key %q", key)
				}
				keys[key] = struct{}{}
				if err := walk(); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := walk(); err != nil {
					return err
				}
			}
		default:
			return errors.New("JSON delimiter is invalid")
		}
		closing, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("read JSON closing delimiter: %w", err)
		}
		expected := json.Delim('}')
		if delimiter == '[' {
			expected = ']'
		}
		if closing != expected {
			return errors.New("JSON closing delimiter is invalid")
		}

		return nil
	}

	return walk()
}

func parseWireTime(value string) (time.Time, error) {
	if !strings.HasSuffix(value, "Z") {
		return time.Time{}, errors.New("timestamp must use UTC Z notation")
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, errors.New("timestamp is invalid")
	}

	return parsed.UTC(), nil
}

func parseOptionalConnection(value *string) (id.Connection, error) {
	if value == nil {
		return id.Connection{}, nil
	}
	parsed, err := id.ParseConnection(*value)
	if err != nil {
		return id.Connection{}, errors.New("connection ID is invalid")
	}

	return parsed, nil
}

func parseOptionalCommand(value *string) (id.Command, error) {
	if value == nil {
		return id.Command{}, nil
	}
	parsed, err := id.ParseCommand(*value)
	if err != nil {
		return id.Command{}, errors.New("command ID is invalid")
	}

	return parsed, nil
}

func parseOptionalMessage(value *string) (id.Message, error) {
	if value == nil {
		return id.Message{}, nil
	}
	parsed, err := id.ParseMessage(*value)
	if err != nil {
		return id.Message{}, errors.New("message reference is invalid")
	}

	return parsed, nil
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}

	return &value
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}

func milliseconds(value int64) time.Duration {
	if value > int64(^uint64(0)>>1)/int64(time.Millisecond) || value < 0 {
		return 0
	}

	return time.Duration(value) * time.Millisecond
}

func wireError(err error) error {
	return fmt.Errorf("%w: %w", ErrInvalidWireMessage, err)
}
