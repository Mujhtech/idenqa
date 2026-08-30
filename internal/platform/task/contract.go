// Package task owns Idenqa's background-execution contract.
//
// Queue-library types must not cross this boundary. Payloads contain opaque
// identifiers and safe control metadata, never raw evidence or credentials.
package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/postgres"
)

const (
	maximumPayloadBytes  = 64 * 1024
	maximumMetadataBytes = 512
	maximumAttempts      = 100
	maximumBackoff       = 24 * time.Hour
	maximumRetention     = 365 * 24 * time.Hour
)

var (
	// ErrInvalid means an owned task value failed boundary validation.
	ErrInvalid = errors.New("task: invalid")
	// ErrConflict means an identifier or idempotency key was reused with different content.
	ErrConflict = errors.New("task: conflict")
	// ErrDuplicate means equivalent work is already durable.
	ErrDuplicate = errors.New("task: duplicate")
	// ErrNotFound means the addressed task does not exist.
	ErrNotFound = errors.New("task: not found")
	// ErrLeaseLost means a worker no longer owns the fenced attempt.
	ErrLeaseLost = errors.New("task: lease lost")
	// ErrDraining means the driver is not accepting or claiming more work.
	ErrDraining = errors.New("task: draining")
	// ErrUnavailable means durable execution storage is temporarily unavailable.
	ErrUnavailable = errors.New("task: unavailable")
	// ErrBackpressure means an owned queue has reached its admission bound.
	ErrBackpressure = errors.New("task: backpressure")
)

// Name is a stable task dispatch name and part of durable wire state.
type Name string

// NewName validates a durable task name.
func NewName(value string) (Name, error) {
	if !validName(value, 128) {
		return "", fmt.Errorf("%w: task name", ErrInvalid)
	}
	return Name(value), nil
}

// String returns the durable task name.
func (name Name) String() string { return string(name) }

// Key identifies one version of one task payload.
type Key struct {
	Name    Name
	Version uint32
}

// Validate checks that the task key is usable for enqueue and dispatch.
func (key Key) Validate() error {
	if _, err := NewName(key.Name.String()); err != nil || key.Version == 0 {
		return fmt.Errorf("%w: task key", ErrInvalid)
	}
	return nil
}

// RetryClass is a stable operational reason for another attempt.
type RetryClass uint8

const (
	// RetryClassUnknown is never a valid retry classification.
	RetryClassUnknown RetryClass = iota
	// RetryClassTransient covers temporary local or dependency failures.
	RetryClassTransient
	// RetryClassConflict covers optimistic-concurrency conflicts worth reloading.
	RetryClassConflict
	// RetryClassRateLimited covers explicit upstream throttling.
	RetryClassRateLimited
	// RetryClassUnavailable covers temporarily unavailable infrastructure.
	RetryClassUnavailable
)

// Valid reports whether the retry class has defined scheduling semantics.
func (class RetryClass) Valid() bool {
	switch class {
	case RetryClassTransient, RetryClassConflict, RetryClassRateLimited, RetryClassUnavailable:
		return true
	default:
		return false
	}
}

// RetryPolicy bounds retries and computes deterministic exponential backoff.
type RetryPolicy struct {
	MaxAttempts    uint32
	InitialBackoff time.Duration
	MaximumBackoff time.Duration
	JitterPercent  uint8
}

// Validate rejects retry policies that can create unbounded or ambiguous work.
func (policy RetryPolicy) Validate() error {
	if policy.MaxAttempts == 0 || policy.MaxAttempts > maximumAttempts || policy.InitialBackoff < time.Millisecond ||
		policy.MaximumBackoff < policy.InitialBackoff || policy.MaximumBackoff > maximumBackoff ||
		policy.JitterPercent > 100 {
		return fmt.Errorf("%w: retry policy", ErrInvalid)
	}
	return nil
}

// IntentSpec is the serialisable input used to construct an immutable Intent.
type IntentSpec struct {
	ID             id.Task
	TenantID       id.Tenant
	Key            Key
	Queue          string
	PartitionKey   string
	IdempotencyKey string
	Payload        any
	ScheduledAt    time.Time
	Deadline       time.Time
	Retry          RetryPolicy
	Retention      time.Duration
	CorrelationID  string
	CausationID    string
	Traceparent    string
	Tracestate     string
}

// Intent is validated durable task meaning independent of any queue library.
type Intent struct {
	id             id.Task
	tenantID       id.Tenant
	key            Key
	queue          string
	partitionKey   string
	idempotencyKey string
	payload        json.RawMessage
	scheduledAt    time.Time
	deadline       time.Time
	retry          RetryPolicy
	retention      time.Duration
	correlationID  string
	causationID    string
	traceparent    string
	tracestate     string
}

// NewIntent validates and defensively copies an object-shaped safe payload.
func NewIntent(spec IntentSpec) (Intent, error) {
	if spec.ID.IsZero() || spec.TenantID.IsZero() || spec.Key.Validate() != nil ||
		!validName(spec.Queue, 64) || !validMetadata(spec.PartitionKey, false) ||
		!validMetadata(spec.IdempotencyKey, true) || spec.ScheduledAt.IsZero() ||
		spec.Retry.Validate() != nil || spec.Retention < time.Millisecond || spec.Retention > maximumRetention ||
		!validMetadata(spec.CorrelationID, false) ||
		!validMetadata(spec.CausationID, false) ||
		!validMetadata(spec.Traceparent, false) ||
		!validMetadata(spec.Tracestate, false) {
		return Intent{}, fmt.Errorf("%w: intent metadata", ErrInvalid)
	}
	scheduledAt := spec.ScheduledAt.UTC()
	deadline := spec.Deadline.UTC()
	if !spec.Deadline.IsZero() && !deadline.After(scheduledAt) {
		return Intent{}, fmt.Errorf("%w: deadline must follow scheduled time", ErrInvalid)
	}
	payload, err := encodePayload(spec.Payload)
	if err != nil {
		return Intent{}, err
	}

	return Intent{
		id: spec.ID, tenantID: spec.TenantID, key: spec.Key, queue: spec.Queue,
		partitionKey: spec.PartitionKey, idempotencyKey: spec.IdempotencyKey,
		payload: payload, scheduledAt: scheduledAt, deadline: deadline, retry: spec.Retry,
		retention: spec.Retention, correlationID: spec.CorrelationID, causationID: spec.CausationID,
		traceparent: spec.Traceparent, tracestate: spec.Tracestate,
	}, nil
}

// Validate checks a constructed intent. It also makes zero-value and forged
// values fail closed at adapter boundaries.
func (intent Intent) Validate() error {
	if intent.id.IsZero() || intent.tenantID.IsZero() || intent.key.Validate() != nil ||
		!validName(intent.queue, 64) || !validMetadata(intent.partitionKey, false) ||
		!validMetadata(intent.idempotencyKey, true) || intent.scheduledAt.IsZero() ||
		intent.retry.Validate() != nil || intent.retention < time.Millisecond ||
		intent.retention > maximumRetention ||
		!validMetadata(intent.correlationID, false) ||
		!validMetadata(intent.causationID, false) ||
		!validMetadata(intent.traceparent, false) ||
		!validMetadata(intent.tracestate, false) {
		return fmt.Errorf("%w: intent metadata", ErrInvalid)
	}
	if !intent.deadline.IsZero() && !intent.deadline.After(intent.scheduledAt) {
		return fmt.Errorf("%w: deadline must follow scheduled time", ErrInvalid)
	}
	var object map[string]json.RawMessage
	if len(intent.payload) == 0 || len(intent.payload) > maximumPayloadBytes ||
		json.Unmarshal(intent.payload, &object) != nil || object == nil {
		return fmt.Errorf("%w: intent payload", ErrInvalid)
	}
	return nil
}

// ID returns the opaque Idenqa task identifier.
func (intent Intent) ID() id.Task { return intent.id }

// TenantID returns the tenant that owns this work.
func (intent Intent) TenantID() id.Tenant { return intent.tenantID }

// Key returns the task name and payload version.
func (intent Intent) Key() Key { return intent.key }

// Queue returns the owned logical queue name.
func (intent Intent) Queue() string { return intent.queue }

// PartitionKey returns the safe fairness and isolation key.
func (intent Intent) PartitionKey() string { return intent.partitionKey }

// IdempotencyKey returns the caller-owned semantic work key.
func (intent Intent) IdempotencyKey() string { return intent.idempotencyKey }

// Payload returns a defensive copy of the opaque JSON object.
func (intent Intent) Payload() json.RawMessage {
	return append(json.RawMessage(nil), intent.payload...)
}

// ScheduledAt returns the earliest execution time.
func (intent Intent) ScheduledAt() time.Time { return intent.scheduledAt }

// Deadline returns the optional absolute operation deadline.
func (intent Intent) Deadline() time.Time { return intent.deadline }

// Retry returns the bounded retry policy.
func (intent Intent) Retry() RetryPolicy { return intent.retry }

// Retention returns how long successful queue metadata may remain.
func (intent Intent) Retention() time.Duration { return intent.retention }

// CorrelationID returns safe cross-operation correlation metadata.
func (intent Intent) CorrelationID() string { return intent.correlationID }

// CausationID returns the safe identifier of the operation that produced the task.
func (intent Intent) CausationID() string { return intent.causationID }

// Traceparent returns an optional W3C traceparent value.
func (intent Intent) Traceparent() string { return intent.traceparent }

// Tracestate returns optional W3C vendor trace state.
func (intent Intent) Tracestate() string { return intent.tracestate }

// Outcome describes what the execution system must do after a handler returns.
type Outcome uint8

const (
	// OutcomeUnknown is never a valid handler result.
	OutcomeUnknown Outcome = iota
	// OutcomeComplete commits successful completion.
	OutcomeComplete
	// OutcomeRetry schedules another bounded attempt.
	OutcomeRetry
	// OutcomeCancel records deliberate cancellation.
	OutcomeCancel
	// OutcomeQuarantine isolates poison or incompatible work.
	OutcomeQuarantine
)

// Result is the stable handler outcome. Technical errors remain internal.
type Result struct {
	Outcome Outcome
	Class   RetryClass
	Err     error
}

// Complete returns a successful handler result.
func Complete() Result { return Result{Outcome: OutcomeComplete} }

// Retry returns a retryable result with an explicit stable class.
func Retry(class RetryClass, err error) Result {
	return Result{Outcome: OutcomeRetry, Class: class, Err: err}
}

// Cancel returns a deliberate cancellation result.
func Cancel(err error) Result { return Result{Outcome: OutcomeCancel, Err: err} }

// Quarantine returns a poison-work result that requires inspection or replay.
func Quarantine(err error) Result { return Result{Outcome: OutcomeQuarantine, Err: err} }

// Validate prevents handlers from returning ambiguous results.
func (result Result) Validate() error {
	switch result.Outcome {
	case OutcomeComplete:
		if result.Err != nil || result.Class != RetryClassUnknown {
			return fmt.Errorf("%w: successful result metadata", ErrInvalid)
		}
	case OutcomeRetry:
		if result.Err == nil || !result.Class.Valid() {
			return fmt.Errorf("%w: retry result metadata", ErrInvalid)
		}
	case OutcomeCancel, OutcomeQuarantine:
		if result.Err == nil || result.Class != RetryClassUnknown {
			return fmt.Errorf("%w: terminal result metadata", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: handler outcome", ErrInvalid)
	}
	return nil
}

// Delivery is one fenced attempt passed to a task handler.
type Delivery struct {
	Intent       Intent
	Attempt      uint32
	CrashAttempt uint32
	Fence        uint64
	LeaseUntil   time.Time
}

// Handler executes one attempt. It must stop when ctx is cancelled and must
// present Delivery.Fence when committing consequential effects.
type Handler interface {
	Handle(context.Context, Delivery) Result
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(context.Context, Delivery) Result

// Handle executes the wrapped function.
func (handler HandlerFunc) Handle(ctx context.Context, delivery Delivery) Result {
	return handler(ctx, delivery)
}

// TransactionWork is a bounded application effect that must commit with the
// current queue lease. External I/O must finish before this work is created.
type TransactionWork func(context.Context, postgres.Transaction) Result

// TransactionalHandler prepares slow or external work outside a database
// transaction, then returns only the bounded effect that must be fenced and
// committed atomically with task completion.
type TransactionalHandler interface {
	Handler
	Prepare(context.Context, Delivery) (TransactionWork, Result)
}

// Enqueuer is the narrow producer contract consumed by application services.
type Enqueuer interface {
	Enqueue(context.Context, ...Intent) error
}

// Dispatcher moves already-durable owned intents into execution storage.
type Dispatcher interface {
	Dispatch(context.Context, int) (int, error)
}

// Effect identifies an application-owned idempotent consequence of one task.
type Effect struct {
	TaskID id.Task
	Key    string
	Fence  uint64
}

// Deduplicator is the application inbox boundary. Claim and the consequential
// effect must share the consumer's transaction.
type Deduplicator interface {
	Claim(context.Context, Effect) (bool, error)
}

func encodePayload(value any) (json.RawMessage, error) {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) == 0 || len(encoded) > maximumPayloadBytes {
		return nil, fmt.Errorf("%w: payload must be serialisable and bounded", ErrInvalid)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil || object == nil {
		return nil, fmt.Errorf("%w: payload must be a JSON object", ErrInvalid)
	}
	return append(json.RawMessage(nil), encoded...), nil
}

func validName(value string, maximum int) bool {
	if value == "" || len(value) > maximum || strings.TrimSpace(value) != value {
		return false
	}
	for index := range len(value) {
		character := value[index]
		isLetter := character >= 'a' && character <= 'z'
		isDigit := character >= '0' && character <= '9'
		isSeparator := strings.ContainsRune("_.:/-", rune(character))
		if (index == 0 && !isLetter) || (index > 0 && !isLetter && !isDigit && !isSeparator) {
			return false
		}
	}
	return true
}

func validMetadata(value string, required bool) bool {
	if required && value == "" {
		return false
	}
	if len(value) > maximumMetadataBytes || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}
