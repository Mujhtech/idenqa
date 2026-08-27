package access

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

const maxKeyLabelLength = 100

var (
	// ErrKeyNotFound intentionally covers absent and cross-tenant key records.
	ErrKeyNotFound = errors.New("access: API key not found")
	// ErrKeyConflict means an optimistic lifecycle write could not be applied.
	ErrKeyConflict = errors.New("access: API key lifecycle conflict")
	// ErrKeyTerminal means an irreversible terminal state was already reached.
	ErrKeyTerminal = errors.New("access: API key is terminal")
	// ErrTenantInactive means new credentials cannot be issued for the tenant.
	ErrTenantInactive = errors.New("access: tenant is not active")
)

// KeyState is the lifecycle state effective at a particular instant.
type KeyState string

const (
	// KeyStateActive permits authentication.
	KeyStateActive KeyState = "active"
	// KeyStateExpired means the fixed expiry has passed.
	KeyStateExpired KeyState = "expired"
	// KeyStateRevoked is an irreversible explicit invalidation.
	KeyStateRevoked KeyState = "revoked"
	// KeyStateRetired is an irreversible rotation or planned replacement state.
	KeyStateRetired KeyState = "retired"
)

// Key is the durable API-key aggregate. It never contains the presented secret.
type Key struct {
	id            id.APIKey
	tenantID      id.Tenant
	label         string
	digest        Digest
	pepperVersion PepperVersion
	grant         Grant
	version       int64
	createdAt     time.Time
	updatedAt     time.Time
	expiresAt     *time.Time
	revokedAt     *time.Time
	retiredAt     *time.Time
	replacesID    id.APIKey
}

// KeyRecord contains the complete durable representation used to restore a Key.
type KeyRecord struct {
	ID            id.APIKey
	TenantID      id.Tenant
	Label         string
	Digest        Digest
	PepperVersion PepperVersion
	Grant         Grant
	Version       int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
	ExpiresAt     *time.Time
	RevokedAt     *time.Time
	RetiredAt     *time.Time
	ReplacesID    id.APIKey
}

// RestoreKey validates and reconstructs a key from durable state.
func RestoreKey(record KeyRecord) (Key, error) {
	record.CreatedAt = record.CreatedAt.UTC()
	record.UpdatedAt = record.UpdatedAt.UTC()
	record.ExpiresAt = utcCopy(record.ExpiresAt)
	record.RevokedAt = utcCopy(record.RevokedAt)
	record.RetiredAt = utcCopy(record.RetiredAt)

	if record.ID.IsZero() || record.TenantID.IsZero() || record.Digest.IsZero() || record.PepperVersion == 0 ||
		record.Version < 1 || record.CreatedAt.IsZero() || record.UpdatedAt.Before(record.CreatedAt) {
		return Key{}, errors.New("API key state is invalid")
	}
	if err := validateKeyLabel(record.Label); err != nil {
		return Key{}, err
	}
	if len(record.Grant.patterns) == 0 || len(record.Grant.permissions) == 0 {
		return Key{}, errors.New("API key grant is required")
	}
	if record.ExpiresAt != nil && !record.ExpiresAt.After(record.CreatedAt) {
		return Key{}, errors.New("API key expiry must be after creation")
	}
	if !record.ReplacesID.IsZero() && record.ReplacesID.String() == record.ID.String() {
		return Key{}, errors.New("API key cannot replace itself")
	}
	if record.RevokedAt != nil && (record.RevokedAt.Before(record.CreatedAt) || !record.RevokedAt.Equal(record.UpdatedAt)) {
		return Key{}, errors.New("API key revocation time is invalid")
	}
	if record.RetiredAt != nil && record.RetiredAt.Before(record.UpdatedAt) {
		return Key{}, errors.New("API key retirement time is invalid")
	}

	return Key{
		id: record.ID, tenantID: record.TenantID, label: record.Label,
		digest: record.Digest, pepperVersion: record.PepperVersion, grant: record.Grant,
		version: record.Version, createdAt: record.CreatedAt, updatedAt: record.UpdatedAt,
		expiresAt: record.ExpiresAt, revokedAt: record.RevokedAt, retiredAt: record.RetiredAt,
		replacesID: record.ReplacesID,
	}, nil
}

// ID returns the non-secret key record identifier.
func (key Key) ID() id.APIKey { return key.id }

// TenantID returns the owning tenant identifier.
func (key Key) TenantID() id.Tenant { return key.tenantID }

// Label returns the operator-facing key label.
func (key Key) Label() string { return key.label }

// Digest returns the redacting verification digest.
func (key Key) Digest() Digest { return key.digest }

// PepperVersion returns the HMAC pepper version stored with this key.
func (key Key) PepperVersion() PepperVersion { return key.pepperVersion }

// Grant returns the immutable scope snapshot.
func (key Key) Grant() Grant {
	return Grant{
		patterns:    slices.Clone(key.grant.patterns),
		permissions: slices.Clone(key.grant.permissions),
	}
}

// Version returns the optimistic lifecycle version.
func (key Key) Version() int64 { return key.version }

// CreatedAt returns the creation time.
func (key Key) CreatedAt() time.Time { return key.createdAt }

// UpdatedAt returns the latest persisted lifecycle transition time.
func (key Key) UpdatedAt() time.Time { return key.updatedAt }

// ExpiresAt returns a defensive copy of the optional fixed expiry.
func (key Key) ExpiresAt() *time.Time { return utcCopy(key.expiresAt) }

// RevokedAt returns a defensive copy of the irreversible revocation time.
func (key Key) RevokedAt() *time.Time { return utcCopy(key.revokedAt) }

// RetiredAt returns a defensive copy of the irreversible retirement time.
func (key Key) RetiredAt() *time.Time { return utcCopy(key.retiredAt) }

// ReplacesID returns the predecessor key in a rotation, if any.
func (key Key) ReplacesID() id.APIKey { return key.replacesID }

// StateAt returns the effective state at now. Terminal states take precedence over expiry.
func (key Key) StateAt(now time.Time) KeyState {
	if key.revokedAt != nil {
		return KeyStateRevoked
	}
	if key.retiredAt != nil && !now.UTC().Before(*key.retiredAt) {
		return KeyStateRetired
	}
	if key.expiresAt != nil && !now.UTC().Before(*key.expiresAt) {
		return KeyStateExpired
	}

	return KeyStateActive
}

// UsableAt reports whether the key may authenticate at now.
func (key Key) UsableAt(now time.Time) bool { return key.StateAt(now) == KeyStateActive }

// Revoke irreversibly marks the key revoked and advances its version.
func (key *Key) Revoke(now time.Time) error {
	if key == nil || key.id.IsZero() {
		return errors.New("API key is not initialised")
	}
	if key.revokedAt != nil || (key.retiredAt != nil && !now.UTC().Before(*key.retiredAt)) {
		return ErrKeyTerminal
	}
	now = now.UTC()
	if now.Before(key.updatedAt) {
		return errors.New("API key transition time precedes its current state")
	}
	key.revokedAt = &now
	key.updatedAt = now
	key.version++

	return nil
}

// Retire irreversibly marks the key retired and advances its version.
func (key *Key) Retire(now time.Time) error {
	return key.ScheduleRetirement(now, now)
}

// ScheduleRetirement irreversibly chooses when a key stops authenticating.
// A future effective time provides a bounded rotation overlap.
func (key *Key) ScheduleRetirement(scheduledAt, effectiveAt time.Time) error {
	if key == nil || key.id.IsZero() {
		return errors.New("API key is not initialised")
	}
	if key.revokedAt != nil || key.retiredAt != nil {
		return ErrKeyTerminal
	}
	scheduledAt = scheduledAt.UTC()
	effectiveAt = effectiveAt.UTC()
	if scheduledAt.Before(key.updatedAt) {
		return errors.New("API key transition time precedes its current state")
	}
	if effectiveAt.Before(scheduledAt) {
		return errors.New("API key retirement precedes its scheduling time")
	}
	key.retiredAt = &effectiveAt
	key.updatedAt = scheduledAt
	key.version++

	return nil
}

// Repository is the tenant-scoped API-key persistence boundary.
type Repository interface {
	Create(context.Context, tenant.Scope, Key) error
	Find(context.Context, tenant.Scope, id.APIKey) (Key, error)
	SaveLifecycle(context.Context, tenant.Scope, Key, int64) error
	Rotate(context.Context, tenant.Scope, Key, int64, Key) error
}

// IsZero reports whether no verification digest has been initialised.
func (digest Digest) IsZero() bool {
	return digest == Digest{}
}

func validateKeyLabel(label string) error {
	if strings.TrimSpace(label) != label || label == "" || !utf8.ValidString(label) ||
		utf8.RuneCountInString(label) > maxKeyLabelLength {
		return errors.New("API key label is invalid")
	}
	for _, character := range label {
		if character < 0x20 || character == 0x7f {
			return errors.New("API key label contains a control character")
		}
	}

	return nil
}

func utcCopy(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	timestamp := value.UTC()

	return &timestamp
}
