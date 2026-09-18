package webhook

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Event is one canonical reference-only catalogue event.
type Event struct {
	ID            string
	Type          Type
	SchemaVersion string
	CreatedAt     time.Time
	TenantID      string
	Region        string
	Data          json.RawMessage
}

// NewEvent validates and snapshots one catalogue event.
func NewEvent(identifier, tenantID, region string, eventType Type, schemaVersion string, createdAt time.Time, data json.RawMessage) (Event, error) {
	event := Event{
		ID:            identifier,
		Type:          eventType,
		SchemaVersion: schemaVersion,
		CreatedAt:     time.Unix(createdAt.UTC().Unix(), 0).UTC(),
		TenantID:      tenantID,
		Region:        region,
		Data:          append(json.RawMessage(nil), data...),
	}
	if err := event.Validate(); err != nil {
		return Event{}, err
	}

	return event, nil
}

// Parse decodes one canonical envelope and validates it against the catalogue.
func Parse(encoded []byte) (Event, error) {
	if len(encoded) == 0 || len(encoded) > MaximumEventBytes {
		return Event{}, ErrInvalidEvent
	}
	var payload struct {
		ID            string          `json:"id"`
		Type          string          `json:"type"`
		SchemaVersion string          `json:"schema_version"`
		CreatedAt     time.Time       `json:"created_at"`
		TenantID      string          `json:"tenant_id"`
		Region        string          `json:"region"`
		Data          json.RawMessage `json:"data"`
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return Event{}, ErrInvalidEvent
	}
	if err := expectEOF(decoder); err != nil {
		return Event{}, err
	}

	return NewEvent(payload.ID, payload.TenantID, payload.Region, Type(payload.Type), payload.SchemaVersion, payload.CreatedAt, payload.Data)
}

// Validate checks the closed envelope and its catalogue data contract.
func (event Event) Validate() error {
	definition, exists := Lookup(event.Type)
	if !exists || definition.SchemaVersion != event.SchemaVersion {
		return fmt.Errorf("%w: unknown event type or version", ErrInvalidEvent)
	}
	if !validIdentifier("evt", event.ID) || !validIdentifier("ten", event.TenantID) || event.CreatedAt.IsZero() || event.CreatedAt.Location() != time.UTC ||
		len(event.Region) == 0 || len(event.Region) > MaximumRegionBytes || len(event.Data) == 0 || len(event.Data) > MaximumDataBytes {
		return fmt.Errorf("%w: envelope identity, region, or bounds", ErrInvalidEvent)
	}
	var data map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(event.Data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&data); err != nil {
		return fmt.Errorf("%w: data is not an object", ErrInvalidEvent)
	}
	if err := expectEOF(decoder); err != nil {
		return err
	}

	return definition.Accepts(data)
}

// Canonical returns the deterministic compact envelope bytes.
func (event Event) Canonical() ([]byte, error) {
	var compact bytes.Buffer
	if err := json.Compact(&compact, event.Data); err != nil {
		return nil, fmt.Errorf("%w: compact data", ErrInvalidEvent)
	}
	encoded, err := json.Marshal(struct {
		ID            string          `json:"id"`
		Type          Type            `json:"type"`
		SchemaVersion string          `json:"schema_version"`
		CreatedAt     string          `json:"created_at"`
		TenantID      string          `json:"tenant_id"`
		Region        string          `json:"region"`
		Data          json.RawMessage `json:"data"`
	}{event.ID, event.Type, event.SchemaVersion, event.CreatedAt.UTC().Format(time.RFC3339), event.TenantID, event.Region, compact.Bytes()})
	if err != nil {
		return nil, fmt.Errorf("%w: encode envelope", ErrInvalidEvent)
	}
	if len(encoded) > MaximumEventBytes {
		return nil, fmt.Errorf("%w: envelope exceeds %d bytes", ErrInvalidEvent, MaximumEventBytes)
	}

	return encoded, nil
}

// deterministicAlphabet is Crockford base32 without the ambiguous I, L, O and U.
const deterministicAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// DeterministicEventID derives a stable event identifier from a stable seed, so
// retries of the same domain transition cannot create a second event. The seed
// must identify the transition exactly and must never contain secret material.
func DeterministicEventID(seed string) string {
	digest := sha256.Sum256([]byte(seed))
	value := make([]byte, 26)
	// The first character carries only 128 ULID bits, so it is masked to 0-7.
	value[0] = deterministicAlphabet[int(digest[0])&0x07]
	for index := 1; index < len(value); index++ {
		value[index] = deterministicAlphabet[int(digest[index])%len(deterministicAlphabet)]
	}

	return "evt_" + string(value)
}

// Digest returns the lowercase SHA-256 digest of the canonical envelope.
func (event Event) Digest() (string, error) {
	encoded, err := event.Canonical()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)

	return hex.EncodeToString(digest[:]), nil
}

func expectEOF(decoder *json.Decoder) error {
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing content", ErrInvalidEvent)
	}

	return nil
}

func validIdentifier(prefix, value string) bool {
	if !strings.HasPrefix(value, prefix+"_") || len(value) != len(prefix)+1+26 {
		return false
	}
	for _, character := range value[len(prefix)+1:] {
		if (character < '0' || character > '9') && (character < 'A' || character > 'H') && (character < 'J' || character > 'K') && (character < 'M' || character > 'N') && (character < 'P' || character > 'T') && (character < 'V' || character > 'Z') {
			return false
		}
	}

	return true
}
