package evidence

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/objectstore"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

const (
	// DefaultReconciliationClaimTimeout fences one recovery attempt.
	DefaultReconciliationClaimTimeout = 5 * time.Minute
	// DefaultReconciliationRetryDelay avoids hot-looping unavailable dependencies.
	DefaultReconciliationRetryDelay = time.Minute
)

var (
	// ErrReconciliationNotFound deliberately also covers cross-tenant misses.
	ErrReconciliationNotFound = errors.New("evidence: reconciliation obligation not found")
	// ErrReconciliationConflict identifies an invalid or stale lifecycle transition.
	ErrReconciliationConflict = errors.New("evidence: reconciliation obligation conflict")
	// ErrNoReconciliationReady means no tenant-scoped obligation can currently be claimed.
	ErrNoReconciliationReady = errors.New("evidence: no reconciliation obligation ready")
)

// ReconciliationState is the durable disposition of one exact staged object.
type ReconciliationState string

const (
	// ReconciliationPending awaits authoritative recovery after its upload lease.
	ReconciliationPending ReconciliationState = "pending"
	// ReconciliationClaimed is owned by one fenced recovery lease.
	ReconciliationClaimed ReconciliationState = "claimed"
	// ReconciliationRetained proves acceptance references the exact object.
	ReconciliationRetained ReconciliationState = "retained"
	// ReconciliationDeleted proves the exact unaccepted object was deleted.
	ReconciliationDeleted ReconciliationState = "deleted"
)

// ReconciliationRecord is the secret-free durable write-ahead object obligation.
type ReconciliationRecord struct {
	TenantID       id.Tenant
	UploadID       id.Upload
	EvidenceID     id.Evidence
	UploadAttempt  uint32
	Object         objectstore.ObjectRecord
	State          ReconciliationState
	Version        int64
	Claim          uint32
	AvailableAt    time.Time
	LeaseExpiresAt *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ResolvedAt     *time.Time
}

// ObjectReconciliation protects one exact immutable ciphertext version from
// being retained or deleted without an authoritative durable disposition.
type ObjectReconciliation struct{ record ReconciliationRecord }

// NewObjectReconciliation creates the write-ahead obligation for a staged object.
func NewObjectReconciliation(upload Upload, prepared PreparedEvidence, createdAt time.Time) (ObjectReconciliation, error) {
	uploadRecord := upload.Record()
	asset := prepared.Asset()
	object := asset.Content().Object()
	if asset.ID() != uploadRecord.EvidenceID {
		return ObjectReconciliation{}, ErrReconciliationConflict
	}

	return NewObjectReconciliationForObject(upload, object, createdAt)
}

// NewObjectReconciliationForObject creates an obligation when protection
// produced an exact object but failed before a PreparedEvidence value existed.
func NewObjectReconciliationForObject(
	upload Upload,
	object objectstore.Object,
	createdAt time.Time,
) (ObjectReconciliation, error) {
	uploadRecord := upload.Record()
	if uploadRecord.State != UploadStateUploading || uploadRecord.Attempt == 0 ||
		uploadRecord.LeaseExpiresAt == nil || object.IsZero() {
		return ObjectReconciliation{}, ErrReconciliationConflict
	}
	record := ReconciliationRecord{
		TenantID: uploadRecord.TenantID, UploadID: uploadRecord.ID,
		EvidenceID: uploadRecord.EvidenceID, UploadAttempt: uploadRecord.Attempt,
		Object: object.Record(), State: ReconciliationPending, Version: 1,
		AvailableAt: *uploadRecord.LeaseExpiresAt, CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	if record.AvailableAt.Before(createdAt) {
		record.AvailableAt = createdAt
	}

	return RestoreObjectReconciliation(record)
}

// RestoreObjectReconciliation validates one durable obligation.
func RestoreObjectReconciliation(record ReconciliationRecord) (ObjectReconciliation, error) {
	record.AvailableAt = record.AvailableAt.UTC()
	record.CreatedAt = record.CreatedAt.UTC()
	record.UpdatedAt = record.UpdatedAt.UTC()
	if record.LeaseExpiresAt != nil {
		value := record.LeaseExpiresAt.UTC()
		record.LeaseExpiresAt = &value
	}
	if record.ResolvedAt != nil {
		value := record.ResolvedAt.UTC()
		record.ResolvedAt = &value
	}
	if record.TenantID.IsZero() || record.UploadID.IsZero() || record.EvidenceID.IsZero() ||
		record.UploadAttempt == 0 || record.UploadAttempt > math.MaxInt32 || record.Claim > math.MaxInt32 ||
		record.Version < 1 || record.CreatedAt.IsZero() ||
		record.UpdatedAt.Before(record.CreatedAt) || record.AvailableAt.Before(record.CreatedAt) {
		return ObjectReconciliation{}, ErrReconciliationConflict
	}
	if _, err := objectstore.NewObject(record.Object); err != nil {
		return ObjectReconciliation{}, fmt.Errorf("restore reconciliation object: %w", err)
	}
	switch record.State {
	case ReconciliationPending:
		if record.LeaseExpiresAt != nil || record.ResolvedAt != nil {
			return ObjectReconciliation{}, ErrReconciliationConflict
		}
	case ReconciliationClaimed:
		if record.Claim == 0 || record.LeaseExpiresAt == nil ||
			!record.LeaseExpiresAt.After(record.UpdatedAt) || record.ResolvedAt != nil {
			return ObjectReconciliation{}, ErrReconciliationConflict
		}
	case ReconciliationRetained, ReconciliationDeleted:
		if record.LeaseExpiresAt != nil || record.ResolvedAt == nil ||
			!record.ResolvedAt.Equal(record.UpdatedAt) {
			return ObjectReconciliation{}, ErrReconciliationConflict
		}
	default:
		return ObjectReconciliation{}, ErrReconciliationConflict
	}

	return ObjectReconciliation{record: record}, nil
}

// Record returns a defensive durable copy.
func (obligation ObjectReconciliation) Record() ReconciliationRecord {
	record := obligation.record
	if record.LeaseExpiresAt != nil {
		value := *record.LeaseExpiresAt
		record.LeaseExpiresAt = &value
	}
	if record.ResolvedAt != nil {
		value := *record.ResolvedAt
		record.ResolvedAt = &value
	}

	return record
}

// Object returns the exact immutable ciphertext reference.
func (obligation ObjectReconciliation) Object() objectstore.Object {
	object, _ := objectstore.NewObject(obligation.record.Object)
	return object
}

// ClaimForRecovery fences an available or expired recovery attempt.
func (obligation ObjectReconciliation) ClaimForRecovery(now time.Time, timeout time.Duration) (ObjectReconciliation, error) {
	now = now.UTC()
	if now.IsZero() || timeout <= 0 || timeout > 15*time.Minute || obligation.record.Claim >= math.MaxInt32 {
		return ObjectReconciliation{}, ErrReconciliationConflict
	}
	ready := obligation.record.State == ReconciliationPending && !now.Before(obligation.record.AvailableAt)
	expired := obligation.record.State == ReconciliationClaimed && obligation.record.LeaseExpiresAt != nil &&
		!now.Before(*obligation.record.LeaseExpiresAt)
	if !ready && !expired {
		return ObjectReconciliation{}, ErrReconciliationConflict
	}
	record := obligation.record
	record.State = ReconciliationClaimed
	record.Version++
	record.Claim++
	record.UpdatedAt = now
	lease := now.Add(timeout)
	record.LeaseExpiresAt = &lease

	return RestoreObjectReconciliation(record)
}

// Retry releases a claimed obligation for a later fenced attempt.
func (obligation ObjectReconciliation) Retry(now time.Time, delay time.Duration) (ObjectReconciliation, error) {
	now = now.UTC()
	if obligation.record.State != ReconciliationClaimed || now.IsZero() || delay <= 0 {
		return ObjectReconciliation{}, ErrReconciliationConflict
	}
	record := obligation.record
	record.State = ReconciliationPending
	record.Version++
	record.AvailableAt = now.Add(delay)
	record.LeaseExpiresAt = nil
	record.UpdatedAt = now

	return RestoreObjectReconciliation(record)
}

// Resolve records the authoritative terminal disposition of a pending or claimed object.
func (obligation ObjectReconciliation) Resolve(state ReconciliationState, now time.Time) (ObjectReconciliation, error) {
	now = now.UTC()
	if (obligation.record.State != ReconciliationPending && obligation.record.State != ReconciliationClaimed) ||
		(state != ReconciliationRetained && state != ReconciliationDeleted) || now.IsZero() {
		return ObjectReconciliation{}, ErrReconciliationConflict
	}
	record := obligation.record
	record.State = state
	record.Version++
	record.LeaseExpiresAt = nil
	record.UpdatedAt = now
	record.ResolvedAt = &now

	return RestoreObjectReconciliation(record)
}

// ObjectReconciliationRecorder owns write-ahead creation and synchronous resolution.
type ObjectReconciliationRecorder interface {
	CreateObjectReconciliation(context.Context, tenant.Scope, Upload, PreparedEvidence, time.Time) error
	CreateObjectReconciliationForObject(
		context.Context, tenant.Scope, Upload, objectstore.Object, time.Time,
	) error
	ResolveObjectReconciliation(
		context.Context, tenant.Scope, id.Upload, uint32, objectstore.Object,
		ReconciliationState, time.Time,
	) error
}

// ObjectReconciliationQueue owns fenced background recovery transitions.
type ObjectReconciliationQueue interface {
	ClaimObjectReconciliation(context.Context, tenant.Scope, time.Time, time.Duration) (ObjectReconciliation, error)
	RetryObjectReconciliation(context.Context, tenant.Scope, ObjectReconciliation, time.Time, time.Duration) error
	CompleteObjectReconciliation(context.Context, tenant.Scope, ObjectReconciliation, ReconciliationState, time.Time) error
}
