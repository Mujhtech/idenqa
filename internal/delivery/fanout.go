package delivery

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	webhookv1 "github.com/Mujhtech/idenqa/contracts/webhook/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
)

// EventState is the closed fanout lifecycle of one durable catalogue event.
type EventState string

const (
	// EventPending remains eligible for bounded fanout batches.
	EventPending EventState = "pending"
	// EventCompleted was fanned out to every subscribed endpoint.
	EventCompleted EventState = "completed"
)

// FanoutEvent is the durable canonical event awaiting bounded fanout.
type FanoutEvent struct {
	ID             id.Event
	TenantID       id.Tenant
	Type           webhookv1.Type
	Body           []byte
	BodyWrapping   *kms.WrappedKey
	State          EventState
	Cursor         string
	DeliveredCount int
	CreatedAt      time.Time
}

// ParseFanoutEvent validates one stored canonical body against the catalogue.
func ParseFanoutEvent(identifier id.Event, tenantID id.Tenant, body []byte, state, cursor string, delivered int, createdAt time.Time) (FanoutEvent, error) {
	parsed, err := webhookv1.Parse(body)
	if err != nil || parsed.ID != identifier.String() || parsed.TenantID != tenantID.String() {
		return FanoutEvent{}, ErrInvalid
	}
	if state != string(EventPending) && state != string(EventCompleted) {
		return FanoutEvent{}, ErrInvalid
	}
	if delivered < 0 || createdAt.IsZero() || len(cursor) > 64 || identifier.IsZero() || tenantID.IsZero() {
		return FanoutEvent{}, ErrInvalid
	}

	return FanoutEvent{
		ID:             identifier,
		TenantID:       tenantID,
		Type:           parsed.Type,
		Body:           append([]byte(nil), body...),
		State:          EventState(state),
		Cursor:         cursor,
		DeliveredCount: delivered,
		CreatedAt:      createdAt.UTC(),
	}, nil
}

// EventData marshals canonical, sorted reference-only event data.
func EventData(fields map[string]any) (json.RawMessage, error) {
	encoded, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("delivery: event data: %w", err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, encoded); err != nil {
		return nil, fmt.Errorf("delivery: event data: %w", err)
	}

	return compact.Bytes(), nil
}
