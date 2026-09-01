package delivery

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
)

var (
	// ErrInvalid means delivery state violates the closed contract.
	ErrInvalid = errors.New("delivery: invalid")
	// ErrNotFound means a tenant-scoped endpoint or delivery is absent.
	ErrNotFound = errors.New("delivery: not found")
	// ErrConflict means replay or optimistic state changed meaning.
	ErrConflict = errors.New("delivery: conflict")
	// ErrDisabled means the selected endpoint cannot accept new delivery.
	ErrDisabled = errors.New("delivery: endpoint disabled")
	// ErrUnauthorized means application authorization denied an administrative action.
	ErrUnauthorized = errors.New("delivery: unauthorized")
)

// Secret is one immutable KMS-wrapped webhook signing secret version.
type Secret struct {
	Version   int64
	Wrapped   kms.WrappedKey
	CreatedAt time.Time
}

// Endpoint is tenant-owned configuration with a bounded rotation overlap.
type Endpoint struct {
	ID                 id.WebhookEndpoint
	URL                string
	Active             Secret
	Previous           *Secret
	PreviousValidUntil time.Time
	DisabledAt         time.Time
	DisabledReason     string
	Version            int64
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// Validate checks durable endpoint state without resolving DNS.
func (endpoint Endpoint) Validate() error {
	parsed, err := url.Parse(endpoint.URL)
	if err != nil || endpoint.ID.IsZero() || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" || (parsed.Port() != "" && parsed.Port() != "443") || endpoint.Version <= 0 || endpoint.Active.Version <= 0 || endpoint.Active.Wrapped.IsZero() || endpoint.CreatedAt.IsZero() || endpoint.UpdatedAt.Before(endpoint.CreatedAt) {
		return ErrInvalid
	}
	if (endpoint.Previous == nil) != endpoint.PreviousValidUntil.IsZero() || (endpoint.DisabledAt.IsZero() != (endpoint.DisabledReason == "")) {
		return ErrInvalid
	}
	if endpoint.Previous != nil && (endpoint.Previous.Version <= 0 || endpoint.Previous.Version >= endpoint.Active.Version || endpoint.Previous.Wrapped.IsZero() || !endpoint.PreviousValidUntil.After(endpoint.UpdatedAt)) {
		return ErrInvalid
	}
	return nil
}

// State is the closed delivery lifecycle.
type State string

const (
	// StatePending remains eligible for a bounded attempt.
	StatePending State = "pending"
	// StateDelivered records a successful endpoint response.
	StateDelivered State = "delivered"
	// StateExhausted records terminal retry exhaustion or rejection.
	StateExhausted State = "exhausted"
	// StateCancelled records deliberate cancellation.
	StateCancelled State = "cancelled"
)

// Intent is one immutable event payload plus mutable bounded delivery state.
type Intent struct {
	ID            id.Delivery
	EndpointID    id.WebhookEndpoint
	EventID       id.Event
	EventType     string
	Body          []byte
	BodyDigest    string
	State         State
	AttemptCount  int32
	MaxAttempts   int32
	NextAttemptAt time.Time
	DeliveredAt   time.Time
	ReplayOf      id.Delivery
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// NewIntent creates exact delivery meaning; event IDs are the tenant deduplication key.
func NewIntent(identifier id.Delivery, endpoint id.WebhookEndpoint, event id.Event, eventType string, body []byte, maxAttempts int32, at time.Time) (Intent, error) {
	if identifier.IsZero() || endpoint.IsZero() || event.IsZero() || !validToken(eventType) || len(body) == 0 || len(body) > 1<<20 || maxAttempts == 0 || maxAttempts > 20 || at.IsZero() || at.Location() != time.UTC {
		return Intent{}, ErrInvalid
	}
	digest := sha256.Sum256(body)
	return Intent{ID: identifier, EndpointID: endpoint, EventID: event, EventType: eventType, Body: slices.Clone(body), BodyDigest: hex.EncodeToString(digest[:]), State: StatePending, MaxAttempts: maxAttempts, NextAttemptAt: at, CreatedAt: at, UpdatedAt: at}, nil
}

// Validate checks exact persisted delivery state and body integrity.
func (intent Intent) Validate() error {
	if intent.ID.IsZero() || intent.EndpointID.IsZero() || intent.EventID.IsZero() || !validToken(intent.EventType) || len(intent.Body) == 0 || len(intent.Body) > 1<<20 || intent.MaxAttempts == 0 || intent.MaxAttempts > 20 || intent.AttemptCount > intent.MaxAttempts || intent.CreatedAt.IsZero() || intent.UpdatedAt.Before(intent.CreatedAt) {
		return ErrInvalid
	}
	digest := sha256.Sum256(intent.Body)
	if intent.BodyDigest != hex.EncodeToString(digest[:]) {
		return ErrInvalid
	}
	switch intent.State {
	case StatePending:
		if !intent.DeliveredAt.IsZero() || intent.NextAttemptAt.IsZero() {
			return ErrInvalid
		}
	case StateDelivered:
		if intent.DeliveredAt.IsZero() {
			return ErrInvalid
		}
	case StateExhausted, StateCancelled:
		if !intent.DeliveredAt.IsZero() {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

// Attempt is append-only safe response metadata. Response bodies are absent by construction.
type Attempt struct {
	Number             int32
	SecretVersion      int64
	SignatureTimestamp int64
	Diagnostic         SafeDiagnostic
	CompletedAt        time.Time
}

func validToken(value string) bool {
	if len(value) == 0 || len(value) > 128 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && !strings.ContainsRune("._:-", character) {
			return false
		}
	}
	return true
}
