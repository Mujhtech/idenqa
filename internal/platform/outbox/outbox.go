// Package outbox owns durable event-intent metadata. Dispatch and delivery
// semantics are intentionally outside the C-04 creation transaction.
package outbox

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

const maximumPayloadBytes = 64 * 1024

// Intent is a validated, unpublished durable event intent.
type Intent struct {
	ID               id.Event
	AggregateType    string
	AggregateID      string
	AggregateVersion int64
	EventType        string
	SchemaVersion    uint32
	Payload          json.RawMessage
	OccurredAt       time.Time
}

// NewIntent validates metadata and serialises an object-shaped safe payload.
func NewIntent(
	identifier id.Event,
	aggregateType string,
	aggregateID string,
	aggregateVersion int64,
	eventType string,
	schemaVersion uint32,
	payload any,
	occurredAt time.Time,
) (Intent, error) {
	if identifier.IsZero() || !validName(aggregateType, 100) ||
		!validAggregateID(aggregateID) || aggregateVersion < 1 ||
		!validName(eventType, 200) || schemaVersion == 0 || occurredAt.IsZero() {
		return Intent{}, errors.New("outbox: valid event identity and metadata are required")
	}
	encoded, err := json.Marshal(payload)
	if err != nil || len(encoded) == 0 || len(encoded) > maximumPayloadBytes {
		return Intent{}, errors.New("outbox: payload must be serialisable and bounded")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil || object == nil {
		return Intent{}, errors.New("outbox: payload must be a JSON object")
	}

	return Intent{
		ID:               identifier,
		AggregateType:    aggregateType,
		AggregateID:      aggregateID,
		AggregateVersion: aggregateVersion,
		EventType:        eventType,
		SchemaVersion:    schemaVersion,
		Payload:          append(json.RawMessage(nil), encoded...),
		OccurredAt:       occurredAt.UTC(),
	}, nil
}

func validName(value string, maximum int) bool {
	if value == "" || len(value) > maximum || strings.TrimSpace(value) != value {
		return false
	}
	for index, character := range value {
		isLetter := character >= 'a' && character <= 'z'
		isDigit := character >= '0' && character <= '9'
		isSeparator := character == '_' || character == '.' || character == ':' || character == '-'
		if (index == 0 && !isLetter) || (index > 0 && !isLetter && !isDigit && !isSeparator) {
			return false
		}
	}

	return true
}

func validAggregateID(value string) bool {
	if value == "" || len(value) > 100 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}

	return true
}
