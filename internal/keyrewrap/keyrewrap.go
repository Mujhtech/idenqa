// Package keyrewrap orchestrates bounded, resumable, idempotent rewrap sweeps
// across every durable artifact class whose ciphertext is protected by a
// provider-managed key-encryption key. A sweep never removes an existing
// wrapping before the verified replacement is durable: each object is
// unwrapped, re-wrapped under the active provider material, round-trip
// verified, and only then replaced in place.
package keyrewrap

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

const (
	// ClassEvidenceContent covers envelope content keys stored with evidence
	// assets.
	ClassEvidenceContent Class = "evidence.content"
	// ClassWebhookEvent covers canonical catalogue event bodies.
	ClassWebhookEvent Class = "webhook.event"
	// ClassWebhookDelivery covers per-endpoint delivery bodies.
	ClassWebhookDelivery Class = "webhook.delivery"
	// ClassWebhookSecret covers endpoint signing secrets.
	ClassWebhookSecret Class = "webhook.secret"
	// ClassHMACKey covers tenant predictable-identifier HMAC key material.
	ClassHMACKey Class = "keycustody.hmac"
	// ClassIdentityLookupKey covers tenant/region identity lookup keys.
	ClassIdentityLookupKey Class = "identity.lookup"
	// ClassIdentitySubjectKey covers per-subject external-reference keys.
	ClassIdentitySubjectKey Class = "identity.subject"
	// ClassFraudCorrelationKey covers tenant/region fraud correlation keys.
	ClassFraudCorrelationKey Class = "fraud.correlation"
)

// EpochPurpose is the fixed purpose used only to observe the active provider
// wrapping identity. Its probe plaintext protects nothing.
const EpochPurpose = "idenqa.key-rewrap.epoch.v1"

// SweepBatch bounds one class pass; the worker duty processes at most one
// bounded batch per tick so a sweep never monopolises the maintenance queue.
const (
	// MinimumBatch is the smallest accepted batch.
	MinimumBatch = 1
	// MaximumBatch bounds one class pass.
	MaximumBatch = 500
	// DefaultBatch is the batch used by the worker duty.
	DefaultBatch = 64
	// DefaultRetryCap bounds sweep retry backoff.
	DefaultRetryCap = time.Hour
)

// Status values.
const (
	StatusRunning   = "running"
	StatusFailed    = "failed"
	StatusCompleted = "completed"
)

var (
	// ErrInvalid identifies malformed classes, targets, or batches.
	ErrInvalid = errors.New("key rewrap: invalid command")
	// ErrUnavailable identifies provider, persistence, or verification failure.
	ErrUnavailable = errors.New("key rewrap: unavailable")
	// ErrConflict identifies a fencing or concurrent-generation loss.
	ErrConflict = errors.New("key rewrap: concurrent sweep state changed")
)

// Class is one durable artifact class that stores a KMS wrapping.
type Class string

// String returns the canonical class name.
func (class Class) String() string { return string(class) }

// ParseClass validates one canonical class name.
func ParseClass(value string) (Class, error) {
	for _, candidate := range AllClasses() {
		if candidate.String() == value {
			return candidate, nil
		}
	}

	return "", ErrInvalid
}

// AllClasses returns the fixed class order used by full sweeps.
func AllClasses() []Class {
	return []Class{
		ClassEvidenceContent,
		ClassWebhookEvent,
		ClassWebhookDelivery,
		ClassWebhookSecret,
		ClassHMACKey,
		ClassIdentityLookupKey,
		ClassIdentitySubjectKey,
		ClassFraudCorrelationKey,
	}
}

// ClassSet returns every valid class in sweep order.
func ClassSet() map[Class]struct{} {
	set := make(map[Class]struct{}, len(AllClasses()))
	for _, class := range AllClasses() {
		set[class] = struct{}{}
	}

	return set
}

// Target identifies one durable class object within one tenant.
type Target struct {
	TenantID id.Tenant
	Class    Class
	// Object is the class-owned opaque identifier. It is the durable cursor
	// position and must be stable and totally ordered within the class.
	Object string
	// Version is the class-owned optimistic version when the class has one.
	// Classes without an aggregate version leave it zero.
	Version int64
}

// Scope returns the tenant scope for this target.
func (target Target) Scope() (tenant.Scope, error) {
	if target.TenantID.IsZero() || target.Object == "" || len(target.Object) > 512 {
		return tenant.Scope{}, ErrInvalid
	}
	return tenant.NewScope(target.TenantID)
}

// Outcome reports one object rewrap attempt.
type Outcome struct {
	// Changed is true when the stored wrapping was replaced with a new
	// provider identity.
	Changed bool
	// Wrapping is the durable wrapping after the attempt.
	Wrapping kms.WrappedKeyRecord
}

// Adapter is the owning-boundary contract implemented once per artifact class.
// Implementations are responsible for the class transaction, its optimistic
// or identity guards, and any class-owned audit record.
type Adapter interface {
	Class() Class
	// List returns up to limit targets strictly after the opaque object key,
	// ordered by that key, inside the tenant scope.
	List(ctx context.Context, scope tenant.Scope, after string, limit int) ([]Target, error)
	// Rewrap replaces one stored wrapping only after the replacement is
	// verified. It must be idempotent: an object already on the active
	// provider material reports Changed false without rewriting.
	Rewrap(ctx context.Context, target Target) (Outcome, error)
}

// TenantSource pages tenants installation-wide in identifier order.
type TenantSource interface {
	ListTenants(ctx context.Context, after string, limit int) ([]id.Tenant, error)
}

// Epoch is the active provider wrapping identity observed from the configured
// provider. The ciphertext probe is discarded; only identity is retained.
type Epoch struct {
	Key    string
	Record kms.WrappedKeyRecord
}

// IsZero reports whether the epoch has not been observed.
func (epoch Epoch) IsZero() bool { return epoch.Key == "" }

// Cursor is the last durably completed sweep position for one class.
type Cursor struct {
	Tenant id.Tenant
	Object string
}

// IsZero reports whether the cursor is at the beginning of the sweep.
func (cursor Cursor) IsZero() bool { return cursor.Tenant.IsZero() && cursor.Object == "" }

// State is the durable class sweep state.
type State struct {
	Class               Class
	Epoch               Epoch
	Generation          int64
	Status              string
	Cursor              Cursor
	Processed           int64
	Rewrapped           int64
	Skipped             int64
	Failed              int64
	ConsecutiveFailures int
	LastError           string
	NextAttemptAt       time.Time
	StartedAt           time.Time
	CompletedAt         time.Time
	UpdatedAt           time.Time
}

// Batch is one bounded, durably committed sweep advance.
type Batch struct {
	Generation int64
	Epoch      Epoch
	From       Cursor
	To         Cursor
	Processed  int
	Rewrapped  int
	Skipped    int
	Failed     int
	Status     string
	ErrorClass string
	OccurredAt time.Time
}

// Result is the bounded outcome of one sweep pass.
type Result struct {
	Class        Class
	Generation   int64
	Processed    int
	Rewrapped    int
	Skipped      int
	Failed       int
	Status       string
	EpochChanged bool
	Completed    bool
	Backoff      bool
}

// Metrics receives bounded, low-cardinality sweep observations.
type Metrics interface {
	RecordKeyRewrapBatch(class, status string, processed, rewrapped, skipped, failed int64)
}

// Repository persists sweep state and append-only batch audit atomically.
type Repository interface {
	Load(ctx context.Context, class Class) (State, bool, error)
	// States returns every class state in fixed class order.
	States(ctx context.Context) ([]State, error)
	// BeginGeneration establishes the state for one observed epoch. It is
	// idempotent under concurrency and resets the cursor when the epoch key
	// changed.
	BeginGeneration(ctx context.Context, class Class, epoch Epoch, at time.Time) (State, error)
	// CommitBatch compare-and-swaps the expected cursor and generation and
	// appends one audit row. ErrConflict means another writer advanced state.
	CommitBatch(ctx context.Context, expected State, batch Batch) error
}

// Service orchestrates bounded sweeps over composed class adapters.
type Service struct {
	repository Repository
	tenants    TenantSource
	adapters   map[Class]Adapter
	wrapper    platformcrypto.KeyWrapper
	metrics    Metrics
	now        func() time.Time
}

// NewService composes the sweep over explicit adapters.
func NewService(
	repository Repository,
	tenants TenantSource,
	adapters []Adapter,
	wrapper platformcrypto.KeyWrapper,
	metrics Metrics,
	now func() time.Time,
) (*Service, error) {
	if repository == nil || tenants == nil || wrapper == nil || now == nil || len(adapters) == 0 {
		return nil, ErrInvalid
	}
	composed := make(map[Class]Adapter, len(adapters))
	for _, adapter := range adapters {
		if adapter == nil {
			return nil, ErrInvalid
		}
		class := adapter.Class()
		if _, err := ParseClass(class.String()); err != nil {
			return nil, ErrInvalid
		}
		if _, exists := composed[class]; exists {
			return nil, ErrInvalid
		}
		composed[class] = adapter
	}

	return &Service{repository: repository, tenants: tenants, adapters: composed, wrapper: wrapper, metrics: metrics, now: now}, nil
}

// ObserveEpoch wraps one fixed probe to discover the active provider identity
// without exposing provider-specific configuration.
func ObserveEpoch(ctx context.Context, wrapper platformcrypto.KeyWrapper) (Epoch, error) {
	if wrapper == nil || ctx == nil {
		return Epoch{}, ErrUnavailable
	}
	purpose, err := kms.NewPurpose(EpochPurpose)
	if err != nil {
		return Epoch{}, ErrInvalid
	}
	probe := []byte(EpochPurpose)
	wrapped, err := wrapper.Wrap(ctx, purpose, probe, probe)
	if err != nil {
		return Epoch{}, ErrUnavailable
	}
	record := wrapped.Record()
	record.Ciphertext = nil

	return EpochFromRecord(record)
}

// EpochFromRecord canonicalises one wrapping identity.
func EpochFromRecord(record kms.WrappedKeyRecord) (Epoch, error) {
	record.Ciphertext = nil
	canonical, err := json.Marshal(struct {
		Provider  string
		Reference string
		Version   string
		Algorithm string
	}{record.Provider, record.Reference, record.Version, record.Algorithm})
	if err != nil {
		return Epoch{}, ErrInvalid
	}
	sum := sha256.Sum256(canonical)

	return Epoch{Key: hex.EncodeToString(sum[:]), Record: record}, nil
}

// Statuses returns every durable class sweep state in fixed class order.
func (service *Service) Statuses(ctx context.Context) ([]State, error) {
	if service == nil || ctx == nil {
		return nil, ErrInvalid
	}

	return service.repository.States(ctx)
}

// SweepClass processes at most one bounded batch for one class. ErrConflict
// signals that another worker advanced the durable state first; callers retry
// on the next tick.
func (service *Service) SweepClass(ctx context.Context, class Class, batchSize int) (Result, error) {
	if service == nil || ctx == nil || batchSize < MinimumBatch || batchSize > MaximumBatch {
		return Result{}, ErrInvalid
	}
	adapter, ok := service.adapters[class]
	if !ok {
		return Result{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("sweep %s: %w", class, err)
	}
	observedAt := service.now().UTC().Truncate(time.Microsecond)
	epoch, err := ObserveEpoch(ctx, service.wrapper)
	if err != nil {
		return Result{}, err
	}
	previous, exists, err := service.repository.Load(ctx, class)
	if err != nil {
		return Result{}, err
	}
	epochChanged := false
	if !exists || previous.Epoch.Key != epoch.Key {
		epochChanged = exists
		previous, err = service.repository.BeginGeneration(ctx, class, epoch, observedAt)
		if err != nil {
			return Result{}, err
		}
	}
	if previous.Status == StatusCompleted {
		return Result{
			Class: class, Generation: previous.Generation, Status: StatusCompleted,
			Completed: true, EpochChanged: epochChanged,
		}, nil
	}
	if !previous.NextAttemptAt.IsZero() && previous.NextAttemptAt.After(observedAt) {
		return Result{
			Class: class, Generation: previous.Generation, Status: previous.Status,
			Backoff: true, EpochChanged: epochChanged,
		}, nil
	}
	batch, result, err := service.process(ctx, adapter, previous, batchSize, observedAt)
	if err != nil {
		return Result{}, err
	}
	if err := service.repository.CommitBatch(ctx, previous, batch); err != nil {
		return Result{}, err
	}
	if service.metrics != nil {
		service.metrics.RecordKeyRewrapBatch(class.String(), batch.Status,
			int64(batch.Processed), int64(batch.Rewrapped), int64(batch.Skipped), int64(batch.Failed))
	}
	result.EpochChanged = epochChanged

	return result, nil
}

// SweepAll processes one bounded batch per class in fixed class order. A class
// failure is reported but does not stop the remaining classes.
func (service *Service) SweepAll(ctx context.Context, batchSize int) ([]Result, error) {
	if service == nil || ctx == nil || batchSize < MinimumBatch || batchSize > MaximumBatch {
		return nil, ErrInvalid
	}
	results := make([]Result, 0, len(AllClasses()))
	for _, class := range AllClasses() {
		if _, ok := service.adapters[class]; !ok {
			continue
		}
		result, err := service.SweepClass(ctx, class, batchSize)
		if err != nil {
			results = append(results, Result{Class: class, Status: StatusFailed, Failed: 1})
			continue
		}
		results = append(results, result)
	}

	return results, nil
}

func (service *Service) process(
	ctx context.Context,
	adapter Adapter,
	state State,
	batchSize int,
	observedAt time.Time,
) (Batch, Result, error) {
	batch := Batch{
		Generation: state.Generation, Epoch: state.Epoch, From: state.Cursor,
		To: state.Cursor, Status: StatusRunning, OccurredAt: observedAt,
	}
	cursor := state.Cursor
	fail := func(class string) (Batch, Result, error) {
		batch.Failed++
		batch.Status = StatusFailed
		batch.ErrorClass = class
		batch.To = cursor

		return batch, resultOf(batch, adapter.Class()), nil
	}
	for batch.Processed+batch.Failed < batchSize {
		if err := ctx.Err(); err != nil {
			return Batch{}, Result{}, fmt.Errorf("sweep %s: %w", adapter.Class(), err)
		}
		scope, current, done, err := service.resolveTenant(ctx, cursor)
		if err != nil {
			return fail("tenant_discovery_failed")
		}
		if done {
			batch.Status = StatusCompleted
			batch.To = cursor

			return batch, resultOf(batch, adapter.Class()), nil
		}
		cursor.Tenant = current
		remaining := batchSize - batch.Processed - batch.Failed
		targets, err := adapter.List(ctx, scope, cursor.Object, remaining)
		if err != nil {
			return fail("target_list_failed")
		}
		if len(targets) == 0 {
			// The tenant has no further objects in this pass. Record the
			// exhausted tenant and continue with its successor.
			cursor.Tenant, cursor.Object = current, ""
			continue
		}
		for _, target := range targets {
			if target.TenantID != current {
				return fail("target_scope_mismatch")
			}
			outcome, err := adapter.Rewrap(ctx, target)
			if err != nil {
				return fail("rewrap_failed")
			}
			batch.Processed++
			if outcome.Changed {
				batch.Rewrapped++
			} else {
				batch.Skipped++
			}
			cursor.Object = target.Object
		}
	}
	batch.To = cursor

	return batch, resultOf(batch, adapter.Class()), nil
}

func resultOf(batch Batch, class Class) Result {
	return Result{
		Class: class, Generation: batch.Generation,
		Processed: batch.Processed, Rewrapped: batch.Rewrapped, Skipped: batch.Skipped,
		Failed: batch.Failed, Status: batch.Status, Completed: batch.Status == StatusCompleted,
	}
}

// resolveTenant returns the tenant scope to continue in. A cursor with a
// non-empty object continues inside its tenant; otherwise the next tenant in
// identifier order is selected. done reports that no tenant remains.
func (service *Service) resolveTenant(ctx context.Context, cursor Cursor) (tenant.Scope, id.Tenant, bool, error) {
	if !cursor.Tenant.IsZero() && cursor.Object != "" {
		scope, err := tenant.NewScope(cursor.Tenant)
		if err != nil {
			return tenant.Scope{}, id.Tenant{}, false, ErrInvalid
		}

		return scope, cursor.Tenant, false, nil
	}
	tenants, err := service.tenants.ListTenants(ctx, cursor.Tenant.String(), 1)
	if err != nil {
		return tenant.Scope{}, id.Tenant{}, false, err
	}
	if len(tenants) == 0 {
		return tenant.Scope{}, id.Tenant{}, true, nil
	}
	scope, err := tenant.NewScope(tenants[0])
	if err != nil {
		return tenant.Scope{}, id.Tenant{}, false, ErrInvalid
	}

	return scope, tenants[0], false, nil
}

// RewrapRecord unwraps one stored wrapping, protects the identical plaintext
// with the active provider material, and verifies the round trip. The caller
// persists the replacement only after this returns. Changed is false when the
// stored wrapping already carries the active provider identity.
func RewrapRecord(
	ctx context.Context,
	wrapper platformcrypto.KeyWrapper,
	unwrapper platformcrypto.KeyUnwrapper,
	purpose kms.Purpose,
	previous kms.WrappedKeyRecord,
	authenticatedContext []byte,
) (kms.WrappedKeyRecord, bool, error) {
	if wrapper == nil || unwrapper == nil || len(previous.Ciphertext) == 0 || len(authenticatedContext) == 0 {
		return kms.WrappedKeyRecord{}, false, ErrInvalid
	}
	stored, err := kms.NewWrappedKey(previous)
	if err != nil {
		return kms.WrappedKeyRecord{}, false, ErrInvalid
	}
	plaintext, err := unwrapper.Unwrap(ctx, purpose, stored, authenticatedContext)
	if err != nil {
		clear(plaintext)
		return kms.WrappedKeyRecord{}, false, ErrUnavailable
	}
	defer clear(plaintext)
	replacement, err := wrapper.Wrap(ctx, purpose, plaintext, authenticatedContext)
	if err != nil {
		return kms.WrappedKeyRecord{}, false, ErrUnavailable
	}
	verified, err := unwrapper.Unwrap(ctx, purpose, replacement, authenticatedContext)
	if err != nil {
		clear(verified)
		return kms.WrappedKeyRecord{}, false, ErrUnavailable
	}
	equal := len(verified) == len(plaintext) && subtle.ConstantTimeCompare(verified, plaintext) == 1
	clear(verified)
	if !equal {
		return kms.WrappedKeyRecord{}, false, ErrUnavailable
	}
	next := replacement.Record()
	changed := previous.Provider != next.Provider || previous.Reference != next.Reference ||
		previous.Version != next.Version || previous.Algorithm != next.Algorithm

	return next, changed, nil
}

// SameIdentity reports whether two wrapping records share the provider
// identity that determines whether a rewrap is required.
func SameIdentity(first, second kms.WrappedKeyRecord) bool {
	return first.Provider == second.Provider && first.Reference == second.Reference &&
		first.Version == second.Version && first.Algorithm == second.Algorithm
}

// RewrapTenantClass rewraps up to limit stale objects of one class for one
// tenant. It is the bounded executor consumed by a recovery ceremony.
func (service *Service) RewrapTenantClass(ctx context.Context, class string, tenantID id.Tenant, limit int) (int, error) {
	if service == nil || ctx == nil || tenantID.IsZero() || limit < 1 || limit > 100000 {
		return 0, ErrInvalid
	}
	parsed, err := ParseClass(class)
	if err != nil {
		return 0, err
	}
	adapter, ok := service.adapters[parsed]
	if !ok {
		return 0, ErrInvalid
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		return 0, ErrInvalid
	}
	rewrapped := 0
	after := ""
	for rewrapped < limit {
		batch := min(limit-rewrapped, DefaultBatch)
		targets, err := adapter.List(ctx, scope, after, batch)
		if err != nil {
			return rewrapped, err
		}
		if len(targets) == 0 {
			return rewrapped, nil
		}
		for _, target := range targets {
			outcome, err := adapter.Rewrap(ctx, target)
			if err != nil {
				return rewrapped, err
			}
			if outcome.Changed {
				rewrapped++
			}
			after = target.Object
		}
	}

	return rewrapped, nil
}

// AuthorizeEpoch verifies the requested wrapping identity against the active
// provider material and begins a new sweep generation for the class.
func (service *Service) AuthorizeEpoch(ctx context.Context, class, provider, reference, version, algorithm string) (int64, error) {
	if service == nil || ctx == nil {
		return 0, ErrInvalid
	}
	parsed, err := ParseClass(class)
	if err != nil {
		return 0, err
	}
	target := kms.WrappedKeyRecord{Provider: provider, Reference: reference, Version: version, Algorithm: algorithm, Ciphertext: []byte{1}}
	expected, err := EpochFromRecord(target)
	if err != nil {
		return 0, ErrInvalid
	}
	observed, err := ObserveEpoch(ctx, service.wrapper)
	if err != nil {
		return 0, err
	}
	if observed.Key != expected.Key {
		return 0, ErrConflict
	}
	state, err := service.repository.BeginGeneration(ctx, parsed, observed, service.now().UTC().Truncate(time.Microsecond))
	if err != nil {
		return 0, err
	}

	return state.Generation, nil
}
