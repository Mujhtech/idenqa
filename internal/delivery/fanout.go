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
	SchemaVersion  string
	Body           []byte
	BodyWrapping   *kms.WrappedKey
	State          EventState
	Cursor         string
	DeliveredCount int
	CreatedAt      time.Time
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
