package task

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

// State is the deterministic driver's observable durable state.
type State uint8

const (
	// StateUnknown is never persisted.
	StateUnknown State = iota
	// StateScheduled waits for its scheduled time or another retry.
	StateScheduled
	// StateRunning has one active fenced lease.
	StateRunning
	// StateCompleted reached successful terminal completion.
	StateCompleted
	// StateCancelled reached deliberate terminal cancellation.
	StateCancelled
	// StateQuarantined requires inspection or explicit replay.
	StateQuarantined
)

// DriverOptions configure the deterministic task driver.
type DriverOptions struct {
	Clock      clock.Clock
	CrashLimit uint32
}

type record struct {
	intent        Intent
	fingerprint   [sha256.Size]byte
	state         State
	attempt       uint32
	crashAttempt  uint32
	fence         uint64
	leaseUntil    time.Time
	nextAttemptAt time.Time
	lastError     string
	cancel        context.CancelFunc
}

// Driver is a deterministic, in-memory execution model for state-machine and
// application tests. It is not a production queue.
type Driver struct {
	mutex       sync.Mutex
	clock       clock.Clock
	crashLimit  uint32
	records     map[string]*record
	unique      map[string]string
	order       []string
	isDraining  bool
	activeCount uint32
}

// NewDriver constructs a deterministic driver with explicit time and crash policy.
func NewDriver(options DriverOptions) (*Driver, error) {
	if options.Clock == nil || options.CrashLimit == 0 {
		return nil, fmt.Errorf("%w: deterministic driver options", ErrInvalid)
	}
	return &Driver{
		clock: options.Clock, crashLimit: options.CrashLimit,
		records: make(map[string]*record), unique: make(map[string]string),
	}, nil
}

// Enqueue atomically adds a batch. Equivalent identifier replays are harmless.
func (driver *Driver) Enqueue(ctx context.Context, intents ...Intent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	driver.mutex.Lock()
	defer driver.mutex.Unlock()
	return driver.enqueueLocked(intents)
}

func (driver *Driver) enqueueLocked(intents []Intent) error {
	if driver.isDraining {
		return ErrDraining
	}
	if len(intents) == 0 {
		return fmt.Errorf("%w: empty enqueue batch", ErrInvalid)
	}
	type pending struct {
		intent      Intent
		fingerprint [sha256.Size]byte
	}
	items := make([]pending, 0, len(intents))
	seenIDs := make(map[string][sha256.Size]byte, len(intents))
	seenUnique := make(map[string]string, len(intents))
	for _, intent := range intents {
		if err := intent.Validate(); err != nil {
			return err
		}
		fingerprint, err := intentFingerprint(intent)
		if err != nil {
			return err
		}
		identifier := intent.ID().String()
		if prior, exists := seenIDs[identifier]; exists && prior != fingerprint {
			return fmt.Errorf("%w: task id reused in batch", ErrConflict)
		}
		seenIDs[identifier] = fingerprint
		uniqueKey := scopedUniqueKey(intent)
		if prior, exists := seenUnique[uniqueKey]; exists && prior != identifier {
			return fmt.Errorf("%w: idempotency key repeated in batch", ErrDuplicate)
		}
		seenUnique[uniqueKey] = identifier
		if existing, exists := driver.records[identifier]; exists {
			if existing.fingerprint != fingerprint {
				return fmt.Errorf("%w: task id reused", ErrConflict)
			}
			continue
		}
		if existingID, exists := driver.unique[uniqueKey]; exists && existingID != identifier {
			return fmt.Errorf("%w: idempotency key belongs to %s", ErrDuplicate, existingID)
		}
		items = append(items, pending{intent: intent, fingerprint: fingerprint})
	}
	for _, item := range items {
		identifier := item.intent.ID().String()
		driver.records[identifier] = &record{
			intent: item.intent, fingerprint: item.fingerprint,
			state: StateScheduled, nextAttemptAt: item.intent.ScheduledAt(),
		}
		driver.unique[scopedUniqueKey(item.intent)] = identifier
		driver.order = append(driver.order, identifier)
	}
	return nil
}

// Transaction stages deterministic enqueue operations until Commit.
type Transaction struct {
	driver   *Driver
	intents  []Intent
	isClosed bool
}

// Begin starts a deterministic transaction used to prove rollback semantics.
func (driver *Driver) Begin() *Transaction { return &Transaction{driver: driver} }

// Enqueue stages work without making it visible.
func (transaction *Transaction) Enqueue(intents ...Intent) error {
	if transaction == nil || transaction.driver == nil || transaction.isClosed || len(intents) == 0 {
		return fmt.Errorf("%w: deterministic transaction", ErrInvalid)
	}
	transaction.intents = append(transaction.intents, intents...)
	return nil
}

// Commit atomically publishes all staged work.
func (transaction *Transaction) Commit(ctx context.Context) error {
	if transaction == nil || transaction.driver == nil || transaction.isClosed {
		return fmt.Errorf("%w: deterministic transaction", ErrInvalid)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	transaction.driver.mutex.Lock()
	defer transaction.driver.mutex.Unlock()
	if err := transaction.driver.enqueueLocked(transaction.intents); err != nil {
		return err
	}
	transaction.isClosed = true
	transaction.intents = nil
	return nil
}

// Rollback discards every staged intent.
func (transaction *Transaction) Rollback() error {
	if transaction == nil || transaction.driver == nil || transaction.isClosed {
		return fmt.Errorf("%w: deterministic transaction", ErrInvalid)
	}
	transaction.isClosed = true
	transaction.intents = nil
	return nil
}

// Claim selects the next due task and creates a monotonically fenced lease.
func (driver *Driver) Claim(ctx context.Context, lease time.Duration) (Delivery, error) {
	if err := ctx.Err(); err != nil {
		return Delivery{}, err
	}
	if lease <= 0 {
		return Delivery{}, fmt.Errorf("%w: lease duration", ErrInvalid)
	}
	driver.mutex.Lock()
	defer driver.mutex.Unlock()
	if driver.isDraining {
		return Delivery{}, ErrDraining
	}
	now := driver.clock.Now().UTC()
	driver.reclaimExpiredLocked(now)
	var selected *record
	for _, identifier := range driver.order {
		candidate := driver.records[identifier]
		if candidate.state != StateScheduled || candidate.nextAttemptAt.After(now) {
			continue
		}
		if !candidate.intent.Deadline().IsZero() && !candidate.intent.Deadline().After(now) {
			candidate.state = StateCancelled
			candidate.lastError = "deadline exceeded before execution"
			continue
		}
		if selected == nil || candidate.nextAttemptAt.Before(selected.nextAttemptAt) {
			selected = candidate
		}
	}
	if selected == nil {
		return Delivery{}, ErrNotFound
	}
	selected.state = StateRunning
	selected.attempt++
	selected.fence++
	selected.leaseUntil = now.Add(lease)
	driver.activeCount++
	return deliveryOf(selected), nil
}

// Heartbeat renews only the current fenced lease.
func (driver *Driver) Heartbeat(ctx context.Context, taskID id.Task, fence uint64, lease time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if taskID.IsZero() || fence == 0 || lease <= 0 {
		return fmt.Errorf("%w: heartbeat", ErrInvalid)
	}
	driver.mutex.Lock()
	defer driver.mutex.Unlock()
	record, exists := driver.records[taskID.String()]
	if !exists {
		return ErrNotFound
	}
	now := driver.clock.Now().UTC()
	if record.state != StateRunning || record.fence != fence || !record.leaseUntil.After(now) {
		return ErrLeaseLost
	}
	record.leaseUntil = now.Add(lease)
	return nil
}

// Resolve applies a handler result only for the current fenced attempt.
func (driver *Driver) Resolve(ctx context.Context, delivery Delivery, result Result) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if result.Validate() != nil {
		result = Quarantine(errors.New("invalid handler result"))
	}
	driver.mutex.Lock()
	defer driver.mutex.Unlock()
	record, exists := driver.records[delivery.Intent.ID().String()]
	if !exists {
		return ErrNotFound
	}
	if record.state != StateRunning || record.fence != delivery.Fence ||
		!record.leaseUntil.After(driver.clock.Now().UTC()) {
		return ErrLeaseLost
	}
	record.cancel = nil
	if driver.activeCount > 0 {
		driver.activeCount--
	}
	switch result.Outcome {
	case OutcomeComplete:
		record.state = StateCompleted
	case OutcomeRetry:
		record.lastError = result.Err.Error()
		if record.attempt >= record.intent.Retry().MaxAttempts {
			record.state = StateQuarantined
			break
		}
		record.state = StateScheduled
		record.nextAttemptAt = driver.clock.Now().UTC().Add(
			record.intent.Retry().Backoff(record.attempt, record.intent.ID().String()),
		)
	case OutcomeCancel:
		record.state = StateCancelled
		record.lastError = result.Err.Error()
	case OutcomeQuarantine:
		record.state = StateQuarantined
		record.lastError = result.Err.Error()
	}
	return nil
}

// RunOne claims and dispatches one due task through an exact registry entry.
func (driver *Driver) RunOne(ctx context.Context, registry *Registry, lease time.Duration) error {
	delivery, err := driver.Claim(ctx, lease)
	if err != nil {
		return err
	}
	handler, err := registry.Resolve(delivery.Intent.Key())
	if err != nil {
		return driver.Resolve(ctx, delivery, Quarantine(err))
	}
	var handlerContext context.Context
	var cancel context.CancelFunc
	if !delivery.Intent.Deadline().IsZero() {
		handlerContext, cancel = context.WithDeadline(ctx, delivery.Intent.Deadline())
	} else {
		handlerContext, cancel = context.WithCancel(ctx)
	}
	driver.setCancel(delivery, cancel)
	defer cancel()
	return driver.Resolve(context.WithoutCancel(ctx), delivery, handler.Handle(handlerContext, delivery))
}

func (driver *Driver) setCancel(delivery Delivery, cancel context.CancelFunc) {
	driver.mutex.Lock()
	defer driver.mutex.Unlock()
	record := driver.records[delivery.Intent.ID().String()]
	if record != nil && record.state == StateRunning && record.fence == delivery.Fence {
		record.cancel = cancel
	}
}

// Cancel stops pending work and invalidates a running fence. RunOne handlers
// also receive context cancellation.
func (driver *Driver) Cancel(ctx context.Context, taskID id.Task) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	driver.mutex.Lock()
	defer driver.mutex.Unlock()
	record, exists := driver.records[taskID.String()]
	if !exists {
		return ErrNotFound
	}
	if terminal(record.state) {
		return nil
	}
	if record.cancel != nil {
		record.cancel()
	}
	if record.state == StateRunning && driver.activeCount > 0 {
		driver.activeCount--
	}
	record.state = StateCancelled
	record.fence++
	record.lastError = "cancelled"
	return nil
}

// Crash leaves the lease unresolved so deterministic time can exercise reclaim.
func (driver *Driver) Crash(ctx context.Context, delivery Delivery) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	driver.mutex.Lock()
	defer driver.mutex.Unlock()
	record, exists := driver.records[delivery.Intent.ID().String()]
	if !exists {
		return ErrNotFound
	}
	if record.state != StateRunning || record.fence != delivery.Fence {
		return ErrLeaseLost
	}
	if record.cancel != nil {
		record.cancel()
		record.cancel = nil
	}
	return nil
}

// BeginDrain stops enqueue and claim while allowing active attempts to finish.
func (driver *Driver) BeginDrain() {
	driver.mutex.Lock()
	defer driver.mutex.Unlock()
	driver.isDraining = true
}

// Drained reports whether drain was requested and no attempt remains active.
func (driver *Driver) Drained() bool {
	driver.mutex.Lock()
	defer driver.mutex.Unlock()
	driver.reclaimExpiredLocked(driver.clock.Now().UTC())
	return driver.isDraining && driver.activeCount == 0
}

// Snapshot is an immutable observation for tests and operational assertions.
type Snapshot struct {
	Intent        Intent
	State         State
	Attempt       uint32
	CrashAttempt  uint32
	Fence         uint64
	LeaseUntil    time.Time
	NextAttemptAt time.Time
	LastError     string
}

// Snapshot returns the current state of one task.
func (driver *Driver) Snapshot(taskID id.Task) (Snapshot, error) {
	driver.mutex.Lock()
	defer driver.mutex.Unlock()
	record, exists := driver.records[taskID.String()]
	if !exists {
		return Snapshot{}, ErrNotFound
	}
	return snapshotOf(record), nil
}

// Snapshots returns insertion-ordered defensive observations.
func (driver *Driver) Snapshots() []Snapshot {
	driver.mutex.Lock()
	defer driver.mutex.Unlock()
	result := make([]Snapshot, 0, len(driver.order))
	for _, identifier := range driver.order {
		result = append(result, snapshotOf(driver.records[identifier]))
	}
	return result
}

func (driver *Driver) reclaimExpiredLocked(now time.Time) {
	for _, record := range driver.records {
		if record.state != StateRunning || record.leaseUntil.After(now) {
			continue
		}
		if record.cancel != nil {
			record.cancel()
			record.cancel = nil
		}
		if driver.activeCount > 0 {
			driver.activeCount--
		}
		record.crashAttempt++
		record.fence++
		if record.crashAttempt >= driver.crashLimit {
			record.state = StateQuarantined
			record.lastError = "crash limit exhausted"
			continue
		}
		record.state = StateScheduled
		record.nextAttemptAt = now
	}
}

func deliveryOf(record *record) Delivery {
	return Delivery{
		Intent: record.intent, Attempt: record.attempt, CrashAttempt: record.crashAttempt,
		Fence: record.fence, LeaseUntil: record.leaseUntil,
	}
}

func snapshotOf(record *record) Snapshot {
	return Snapshot{
		Intent: record.intent, State: record.state, Attempt: record.attempt,
		CrashAttempt: record.crashAttempt, Fence: record.fence, LeaseUntil: record.leaseUntil,
		NextAttemptAt: record.nextAttemptAt, LastError: record.lastError,
	}
}

func terminal(state State) bool {
	return slices.Contains([]State{StateCompleted, StateCancelled, StateQuarantined}, state)
}

func scopedUniqueKey(intent Intent) string {
	return intent.TenantID().String() + "\x00" + intent.Key().Name.String() + "\x00" + intent.IdempotencyKey()
}

func intentFingerprint(intent Intent) ([sha256.Size]byte, error) {
	hash := sha256.New()
	writePart := func(value string) {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(value))
	}
	writePart(intent.TenantID().String())
	writePart(intent.Key().Name.String())
	writePart(fmt.Sprint(intent.Key().Version))
	writePart(intent.Queue())
	writePart(intent.PartitionKey())
	writePart(intent.IdempotencyKey())
	writePart(string(intent.Payload()))
	writePart(intent.ScheduledAt().Format(time.RFC3339Nano))
	writePart(intent.Deadline().Format(time.RFC3339Nano))
	writePart(fmt.Sprint(intent.Retry()))
	writePart(intent.Retention().String())
	writePart(intent.CorrelationID())
	writePart(intent.CausationID())
	writePart(intent.Traceparent())
	writePart(intent.Tracestate())
	var result [sha256.Size]byte
	if copy(result[:], hash.Sum(nil)) != sha256.Size {
		return result, errors.New("task: compute intent fingerprint")
	}
	return result, nil
}
