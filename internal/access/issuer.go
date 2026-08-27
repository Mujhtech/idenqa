package access

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// ExpiryIntent explicitly chooses a fixed expiry or no expiry.
type ExpiryIntent struct {
	mode      expiryMode
	expiresAt time.Time
}

type expiryMode uint8

const (
	expiryUnknown expiryMode = iota
	expiryFixed
	expiryNone
)

// ExpiringAt creates an explicit fixed-expiry intent.
func ExpiringAt(expiresAt time.Time) ExpiryIntent {
	return ExpiryIntent{mode: expiryFixed, expiresAt: expiresAt.UTC()}
}

// WithoutExpiry creates an explicit no-expiry intent.
func WithoutExpiry() ExpiryIntent {
	return ExpiryIntent{mode: expiryNone}
}

// ExpiryPolicy constrains deployment-level API-key lifetime choices.
type ExpiryPolicy struct {
	configured      bool
	allowNoExpiry   bool
	maximumLifetime *time.Duration
}

// NewExpiryPolicy constructs a policy. A nil maximum permits any future fixed
// expiry; no-expiry remains independently configurable.
func NewExpiryPolicy(allowNoExpiry bool, maximumLifetime *time.Duration) (ExpiryPolicy, error) {
	if maximumLifetime != nil && *maximumLifetime <= 0 {
		return ExpiryPolicy{}, errors.New("API key maximum lifetime must be greater than zero")
	}
	policy := ExpiryPolicy{configured: true, allowNoExpiry: allowNoExpiry}
	if maximumLifetime != nil {
		maximum := *maximumLifetime
		policy.maximumLifetime = &maximum
	}

	return policy, nil
}

// RotationPolicy bounds how long predecessor and successor keys overlap.
type RotationPolicy struct {
	maximumOverlap time.Duration
}

// NewRotationPolicy constructs a bounded-overlap policy.
func NewRotationPolicy(maximumOverlap time.Duration) (RotationPolicy, error) {
	if maximumOverlap <= 0 {
		return RotationPolicy{}, errors.New("API key maximum rotation overlap must be greater than zero")
	}

	return RotationPolicy{maximumOverlap: maximumOverlap}, nil
}

// IssuanceRepository is the three-operation persistence contract consumed by Issuer.
type IssuanceRepository interface {
	Create(context.Context, tenant.Scope, Key) error
	Find(context.Context, tenant.Scope, id.APIKey) (Key, error)
	Rotate(context.Context, tenant.Scope, Key, int64, Key) error
}

// IssuerConfig contains deployment policy and the tenant-assignable registry.
type IssuerConfig struct {
	Registry       Registry
	ExpiryPolicy   ExpiryPolicy
	RotationPolicy RotationPolicy
}

// Issuer creates and rotates display-once tenant API keys.
type Issuer struct {
	repository IssuanceRepository
	ids        *id.Generator
	secrets    *KeyGenerator
	peppers    *PepperSet
	clock      clock.Clock
	registry   Registry
	expiry     ExpiryPolicy
	rotation   RotationPolicy
}

// NewIssuer constructs an API-key issuance application service.
func NewIssuer(
	repository IssuanceRepository,
	identifiers *id.Generator,
	secrets *KeyGenerator,
	peppers *PepperSet,
	source clock.Clock,
	configuration IssuerConfig,
) (*Issuer, error) {
	if repository == nil || identifiers == nil || secrets == nil || peppers == nil || source == nil {
		return nil, errors.New("API key issuer dependencies are required")
	}
	if len(configuration.Registry.permissions) == 0 || !configuration.ExpiryPolicy.configured ||
		configuration.RotationPolicy.maximumOverlap <= 0 {
		return nil, errors.New("API key issuer configuration is invalid")
	}

	return &Issuer{
		repository: repository, ids: identifiers, secrets: secrets, peppers: peppers, clock: source,
		registry: configuration.Registry, expiry: configuration.ExpiryPolicy, rotation: configuration.RotationPolicy,
	}, nil
}

// IssueInput contains operator-selected metadata for a new API key.
type IssueInput struct {
	Label    string
	Patterns []Pattern
	Expiry   ExpiryIntent
}

// IssuedKey holds persisted metadata and its display-once credential.
type IssuedKey struct {
	key        Key
	credential PresentedKey
}

// Key returns the persisted secret-free aggregate.
func (issued IssuedKey) Key() Key { return issued.key }

// Credential returns the redacting display-once credential.
func (issued IssuedKey) Credential() PresentedKey { return issued.credential }

// Issue creates and persists one API key. Credential material is returned only
// after persistence succeeds.
func (issuer *Issuer) Issue(ctx context.Context, scope tenant.Scope, input IssueInput) (IssuedKey, error) {
	if issuer == nil {
		return IssuedKey{}, errors.New("API key issuer is not initialised")
	}
	now := issuer.clock.Now().UTC()
	expiresAt, err := issuer.expiry.resolve(now, input.Expiry)
	if err != nil {
		return IssuedKey{}, err
	}
	grant, err := issuer.registry.Resolve(input.Patterns...)
	if err != nil {
		return IssuedKey{}, fmt.Errorf("resolve API key grant: %w", err)
	}
	issued, err := issuer.build(scope.ID(), input.Label, grant, expiresAt, id.APIKey{}, now)
	if err != nil {
		return IssuedKey{}, err
	}
	if err := issuer.repository.Create(ctx, scope, issued.key); err != nil {
		return IssuedKey{}, fmt.Errorf("persist API key: %w", err)
	}

	return issued, nil
}

// RotateInput selects the predecessor, successor expiry, and bounded overlap.
type RotateInput struct {
	KeyID   id.APIKey
	Expiry  ExpiryIntent
	Overlap time.Duration
}

// Rotate atomically creates a same-scope successor and schedules predecessor retirement.
func (issuer *Issuer) Rotate(ctx context.Context, scope tenant.Scope, input RotateInput) (IssuedKey, error) {
	if issuer == nil {
		return IssuedKey{}, errors.New("API key issuer is not initialised")
	}
	if input.KeyID.IsZero() {
		return IssuedKey{}, errors.New("API key rotation predecessor is required")
	}
	if input.Overlap <= 0 || input.Overlap > issuer.rotation.maximumOverlap {
		return IssuedKey{}, errors.New("API key rotation overlap is outside deployment policy")
	}
	now := issuer.clock.Now().UTC()
	predecessor, err := issuer.repository.Find(ctx, scope, input.KeyID)
	if err != nil {
		return IssuedKey{}, fmt.Errorf("find API key rotation predecessor: %w", err)
	}
	if !predecessor.UsableAt(now) || predecessor.RetiredAt() != nil {
		return IssuedKey{}, ErrKeyTerminal
	}
	expiresAt, err := issuer.expiry.resolve(now, input.Expiry)
	if err != nil {
		return IssuedKey{}, err
	}
	retirementAt := now.Add(input.Overlap)
	if expiresAt != nil && !expiresAt.After(retirementAt) {
		return IssuedKey{}, errors.New("successor API key must outlive the rotation overlap")
	}
	successor, err := issuer.build(
		predecessor.TenantID(), predecessor.Label(), predecessor.Grant(), expiresAt, predecessor.ID(), now,
	)
	if err != nil {
		return IssuedKey{}, err
	}
	expectedVersion := predecessor.Version()
	if err := predecessor.ScheduleRetirement(now, retirementAt); err != nil {
		return IssuedKey{}, err
	}
	if err := issuer.repository.Rotate(ctx, scope, predecessor, expectedVersion, successor.key); err != nil {
		return IssuedKey{}, fmt.Errorf("persist API key rotation: %w", err)
	}

	return successor, nil
}

func (issuer *Issuer) build(
	tenantID id.Tenant,
	label string,
	grant Grant,
	expiresAt *time.Time,
	replacesID id.APIKey,
	now time.Time,
) (IssuedKey, error) {
	identifier, err := issuer.ids.NewAPIKey()
	if err != nil {
		return IssuedKey{}, fmt.Errorf("generate API key id: %w", err)
	}
	credential, err := issuer.secrets.Generate(tenantID, identifier)
	if err != nil {
		return IssuedKey{}, err
	}
	digest, pepperVersion, err := issuer.peppers.Digest(credential)
	if err != nil {
		return IssuedKey{}, err
	}
	key, err := RestoreKey(KeyRecord{
		ID: identifier, TenantID: tenantID, Label: label, Digest: digest,
		PepperVersion: pepperVersion, Grant: grant, Version: 1,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: expiresAt, ReplacesID: replacesID,
	})
	if err != nil {
		return IssuedKey{}, err
	}

	return IssuedKey{key: key, credential: credential}, nil
}

func (policy ExpiryPolicy) resolve(createdAt time.Time, intent ExpiryIntent) (*time.Time, error) {
	if !policy.configured {
		return nil, errors.New("API key expiry policy is not configured")
	}
	switch intent.mode {
	case expiryFixed:
		expiresAt := intent.expiresAt.UTC()
		if !expiresAt.After(createdAt) {
			return nil, errors.New("API key expiry must be after creation")
		}
		if policy.maximumLifetime != nil && expiresAt.Sub(createdAt) > *policy.maximumLifetime {
			return nil, errors.New("API key expiry exceeds deployment policy")
		}

		return &expiresAt, nil
	case expiryNone:
		if !policy.allowNoExpiry {
			return nil, errors.New("API keys without expiry are disabled by deployment policy")
		}

		return nil, nil
	default:
		return nil, errors.New("API key expiry choice must be explicit")
	}
}
