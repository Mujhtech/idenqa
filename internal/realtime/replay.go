package realtime

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

const maximumReplayPage = 256

var (
	// ErrInvalidDurableEvent identifies malformed or unsafe replay state.
	ErrInvalidDurableEvent = errors.New("realtime: invalid durable event")
	// ErrReplayCursorAhead identifies an acknowledgement or replay cursor that
	// claims state beyond the durable stream.
	ErrReplayCursorAhead = errors.New("realtime: replay cursor is ahead")
	// ErrDurableEventConflict identifies reuse of an event ID for different
	// immutable content.
	ErrDurableEventConflict = errors.New("realtime: durable event conflict")
)

// EventCursor is a verification-local, monotonically increasing durable
// server-event position. Zero means that no durable event has been observed.
type EventCursor uint64

// Uint64 returns the cursor's storage and wire representation.
func (cursor EventCursor) Uint64() uint64 { return uint64(cursor) }

// EventIntent is one immutable capture-visible event before its stream cursor
// is assigned by the authoritative repository.
type EventIntent struct {
	id             id.Event
	tenantID       id.Tenant
	verificationID id.Verification
	commandID      id.Command
	correlationID  id.Message
	causationID    id.Message
	occurredAt     time.Time
	expiresAt      time.Time
	payload        Payload
}

// NewEventIntent validates one safe event suitable for durable replay. Raw
// evidence and transport-only messages have no representation here.
func NewEventIntent(
	identifier id.Event,
	tenantID id.Tenant,
	verificationID id.Verification,
	commandID id.Command,
	correlationID id.Message,
	causationID id.Message,
	occurredAt time.Time,
	expiresAt time.Time,
	payload Payload,
) (EventIntent, error) {
	if identifier.IsZero() || tenantID.IsZero() || verificationID.IsZero() ||
		occurredAt.IsZero() || occurredAt.Location() != time.UTC || expiresAt.IsZero() ||
		expiresAt.Location() != time.UTC || !expiresAt.After(occurredAt) || payload == nil {
		return EventIntent{}, ErrInvalidDurableEvent
	}
	cloned, err := clonePayload(payload)
	if err != nil || cloned.validate() != nil || !durableServerPayload(cloned) {
		return EventIntent{}, ErrInvalidDurableEvent
	}
	if consequentialMessage(cloned.messageType()) == commandID.IsZero() {
		return EventIntent{}, ErrInvalidDurableEvent
	}

	return EventIntent{
		id: identifier, tenantID: tenantID, verificationID: verificationID,
		commandID: commandID, correlationID: correlationID, causationID: causationID,
		occurredAt: occurredAt, expiresAt: expiresAt, payload: cloned,
	}, nil
}

// ID returns the stable durable event identity.
func (event EventIntent) ID() id.Event { return event.id }

// TenantID returns the owning tenant.
func (event EventIntent) TenantID() id.Tenant { return event.tenantID }

// VerificationID returns the owning capture verification.
func (event EventIntent) VerificationID() id.Verification { return event.verificationID }

// CommandID returns the consequential server command identity, when present.
func (event EventIntent) CommandID() id.Command { return event.commandID }

// CorrelationID returns the optional correlation identity.
func (event EventIntent) CorrelationID() id.Message { return event.correlationID }

// CausationID returns the optional causation identity.
func (event EventIntent) CausationID() id.Message { return event.causationID }

// Type returns the closed v1 payload type.
func (event EventIntent) Type() MessageType { return event.payload.messageType() }

// OccurredAt returns the authoritative UTC occurrence time.
func (event EventIntent) OccurredAt() time.Time { return event.occurredAt }

// ExpiresAt returns the exclusive replay-retention boundary.
func (event EventIntent) ExpiresAt() time.Time { return event.expiresAt }

// Payload returns a defensive payload copy.
func (event EventIntent) Payload() Payload {
	cloned, err := clonePayload(event.payload)
	if err != nil {
		panic("realtime: validated durable payload became invalid")
	}

	return cloned
}

// DurableEvent is an event assigned to one verification-local stream cursor.
type DurableEvent struct {
	intent EventIntent
	cursor EventCursor
}

// RestoreDurableEvent validates persisted replay state.
func RestoreDurableEvent(intent EventIntent, cursor EventCursor) (DurableEvent, error) {
	if intent.id.IsZero() || cursor == 0 {
		return DurableEvent{}, ErrInvalidDurableEvent
	}

	return DurableEvent{intent: intent, cursor: cursor}, nil
}

// Intent returns the immutable event metadata and payload.
func (event DurableEvent) Intent() EventIntent { return event.intent }

// Cursor returns the verification-local durable position.
func (event DurableEvent) Cursor() EventCursor { return event.cursor }

// ReplayWindow is one bounded, ordered read after a client cursor.
type ReplayWindow struct {
	after        EventCursor
	latest       EventCursor
	retainedFrom EventCursor
	events       []DurableEvent
	hasMore      bool
	gap          bool
}

// NewReplayWindow validates repository output before transport consumption.
func NewReplayWindow(
	after EventCursor,
	latest EventCursor,
	retainedFrom EventCursor,
	events []DurableEvent,
	hasMore bool,
) (ReplayWindow, error) {
	if retainedFrom == 0 || after > latest {
		return ReplayWindow{}, ErrReplayCursorAhead
	}
	gap := after+1 < retainedFrom
	if gap && len(events) != 0 {
		return ReplayWindow{}, ErrInvalidDurableEvent
	}
	if len(events) > maximumReplayPage {
		return ReplayWindow{}, ErrInvalidDurableEvent
	}
	copyOfEvents := append([]DurableEvent(nil), events...)
	if !gap {
		expected := after + 1
		for _, event := range copyOfEvents {
			if event.Cursor() != expected || event.Cursor() > latest {
				return ReplayWindow{}, ErrInvalidDurableEvent
			}
			expected++
		}
		if hasMore && (len(copyOfEvents) == 0 || copyOfEvents[len(copyOfEvents)-1].Cursor() >= latest) {
			return ReplayWindow{}, ErrInvalidDurableEvent
		}
		if !hasMore && expected-1 != latest {
			return ReplayWindow{}, ErrInvalidDurableEvent
		}
	}

	return ReplayWindow{
		after: after, latest: latest, retainedFrom: retainedFrom,
		events: copyOfEvents, hasMore: hasMore, gap: gap,
	}, nil
}

// After returns the requested exclusive lower bound.
func (window ReplayWindow) After() EventCursor { return window.after }

// Latest returns the stream's current durable high-water mark.
func (window ReplayWindow) Latest() EventCursor { return window.latest }

// RetainedFrom returns the earliest cursor that can still be replayed.
func (window ReplayWindow) RetainedFrom() EventCursor { return window.retainedFrom }

// Events returns a defensive ordered event copy.
func (window ReplayWindow) Events() []DurableEvent {
	return slices.Clone(window.events)
}

// HasMore reports whether another bounded page exists.
func (window ReplayWindow) HasMore() bool { return window.hasMore }

// Gap reports that the requested cursor predates retained history.
func (window ReplayWindow) Gap() bool { return window.gap }

// ReplayRepository owns durable append, ordered replay, and monotonic client
// acknowledgement. Implementations must recheck tenant and capture authority.
type ReplayRepository interface {
	Append(context.Context, EventIntent) (DurableEvent, error)
	Replay(context.Context, Ticket, EventCursor, uint16, time.Time) (ReplayWindow, error)
	Acknowledge(context.Context, Ticket, EventCursor, time.Time) error
}

// ReplayWakeups is an optional latency optimisation. Notifications may be
// duplicated, delayed, or dropped; callers must always reread ReplayRepository.
type ReplayWakeups interface {
	Wait(context.Context, Ticket) error
}

func durableServerPayload(payload Payload) bool {
	switch value := payload.(type) {
	case CaptureProgress, VerificationCheckProgress, SessionStateChanged, CaptureCommand:
		return true
	case ChallengeUpdate:
		return value.Action == ChallengeRequested || value.Action == ChallengeCancelled
	default:
		return false
	}
}

// EncodeDurablePayload returns the canonical v1 JSON object stored for a
// previously validated durable server payload.
func EncodeDurablePayload(payload Payload) ([]byte, error) {
	if payload == nil || payload.validate() != nil || !durableServerPayload(payload) {
		return nil, ErrInvalidDurableEvent
	}
	encoded, err := encodeWirePayload(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: encode durable payload", ErrInvalidDurableEvent)
	}

	return encoded, nil
}

// DecodeDurablePayload strictly restores one closed durable payload shape.
func DecodeDurablePayload(messageType MessageType, encoded []byte) (Payload, error) {
	payload, err := decodeWirePayload(messageType, encoded)
	if err != nil || payload.validate() != nil || !durableServerPayload(payload) {
		return nil, ErrInvalidDurableEvent
	}

	return payload, nil
}

// ValidateReplayLimit bounds repository and transport memory independently of
// deployment configuration.
func ValidateReplayLimit(limit uint16) error {
	if limit == 0 || limit > maximumReplayPage {
		return fmt.Errorf("realtime: replay limit must be between 1 and %d", maximumReplayPage)
	}

	return nil
}
