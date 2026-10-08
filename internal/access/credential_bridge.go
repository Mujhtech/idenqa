package access

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// BridgeCommand identifies one immutable command delivered by the deployment
// agent. The digest binds the identifier to its canonical request body.
type BridgeCommand struct {
	ID            string
	RequestDigest [sha256.Size]byte
}

// BridgeIssueRepository atomically persists a newly issued key, its privileged
// audit record, and the command receipt. A replay returns the original key with
// created false and must not recreate or reveal credential material.
type BridgeIssueRepository interface {
	IssueBridgeCommand(
		context.Context,
		AdminAction,
		tenant.Scope,
		BridgeCommand,
		Key,
		CredentialEnvelope,
	) (persisted Key, envelope CredentialEnvelope, created bool, err error)
	ObserveBridgeCommand(context.Context, tenant.Scope, BridgeCommand) (Key, CredentialEnvelope, error)
}

type bridgeLifecycleRepository interface {
	FindAdministrative(context.Context, tenant.Scope, id.APIKey) (Key, error)
	RotateBridgeCommand(context.Context, AdminAction, tenant.Scope, BridgeCommand, Key, int64, Key, CredentialEnvelope) (Key, CredentialEnvelope, bool, error)
	RevokeBridgeCommand(context.Context, AdminAction, tenant.Scope, BridgeCommand, Key, int64) (Key, bool, error)
}

// CredentialEnvelope is ciphertext safe for durable retry recovery.
type CredentialEnvelope struct {
	Algorithm          string `json:"algorithm"`
	EphemeralPublicKey string `json:"ephemeralPublicKey"`
	Nonce              string `json:"nonce"`
	Ciphertext         string `json:"ciphertext"`
}

// CredentialSealer encrypts display-once material to the requesting browser.
type CredentialSealer interface {
	Seal(string) (CredentialEnvelope, error)
}

// Observe returns secret-free metadata for an already committed command.
func (bridge *CredentialBridge) Observe(
	ctx context.Context,
	command BridgeCommand,
	scope tenant.Scope,
) (Key, CredentialEnvelope, error) {
	if bridge == nil {
		return Key{}, CredentialEnvelope{}, errors.New("credential bridge is not initialised")
	}
	if command.ID == "" || command.RequestDigest == ([sha256.Size]byte{}) {
		return Key{}, CredentialEnvelope{}, errors.New("credential bridge command identity is invalid")
	}
	key, envelope, err := bridge.repository.ObserveBridgeCommand(ctx, scope, command)
	if err != nil {
		return Key{}, CredentialEnvelope{}, fmt.Errorf("observe credential bridge command: %w", err)
	}

	return key, envelope, nil
}

// CredentialBridge is the narrow Core-owned application boundary consumed by
// a local deployment agent. It deliberately exposes no generic command runner.
type CredentialBridge struct {
	issuer     *Issuer
	repository BridgeIssueRepository
}

// Find returns secret-free predecessor state to fixed Core-local workflows
// that must distinguish an ordinary rotation from expiry recovery.
func (bridge *CredentialBridge) Find(ctx context.Context, scope tenant.Scope, keyID id.APIKey) (Key, error) {
	if bridge == nil || keyID.IsZero() {
		return Key{}, errors.New("credential bridge lookup is invalid")
	}
	repository, ok := bridge.repository.(bridgeLifecycleRepository)
	if !ok {
		return Key{}, errors.New("credential bridge lifecycle repository is unavailable")
	}
	key, err := repository.FindAdministrative(ctx, scope, keyID)
	if err != nil {
		return Key{}, fmt.Errorf("find credential bridge key: %w", err)
	}
	return key, nil
}

// NewCredentialBridge constructs the local credential lifecycle boundary.
func NewCredentialBridge(issuer *Issuer, repository BridgeIssueRepository) (*CredentialBridge, error) {
	if issuer == nil || repository == nil {
		return nil, errors.New("credential bridge dependencies are required")
	}

	return &CredentialBridge{issuer: issuer, repository: repository}, nil
}

// BridgeIssueResult returns display-once credential material only when this
// invocation created the key. Replays contain secret-free metadata.
type BridgeIssueResult struct {
	Key      Key
	Envelope CredentialEnvelope
	Created  bool
}

// Rotate idempotently creates a successor and schedules the predecessor's retirement.
func (bridge *CredentialBridge) Rotate(ctx context.Context, command BridgeCommand, action AdminAction, scope tenant.Scope, input RotateInput, sealer CredentialSealer) (BridgeIssueResult, error) {
	if bridge == nil || sealer == nil || command.ID == "" || command.RequestDigest == ([sha256.Size]byte{}) {
		return BridgeIssueResult{}, errors.New("credential bridge rotation is invalid")
	}
	if key, envelope, err := bridge.repository.ObserveBridgeCommand(ctx, scope, command); err == nil {
		return BridgeIssueResult{Key: key, Envelope: envelope, Created: false}, nil
	} else if !errors.Is(err, ErrKeyNotFound) {
		return BridgeIssueResult{}, err
	}
	now := bridge.issuer.clock.Now().UTC()
	repository, ok := bridge.repository.(bridgeLifecycleRepository)
	if !ok {
		return BridgeIssueResult{}, errors.New("credential bridge lifecycle repository is unavailable")
	}
	prepared, err := prepareAdminAction(action, now)
	if err != nil {
		return BridgeIssueResult{}, err
	}
	predecessor, err := repository.FindAdministrative(ctx, scope, input.KeyID)
	if err != nil {
		return BridgeIssueResult{}, fmt.Errorf("find credential bridge predecessor: %w", err)
	}
	if input.Overlap <= 0 || input.Overlap > bridge.issuer.rotation.maximumOverlap || !predecessor.UsableAt(now) || predecessor.RetiredAt() != nil {
		return BridgeIssueResult{}, ErrKeyTerminal
	}
	expiresAt, err := bridge.issuer.expiry.resolve(now, input.Expiry)
	if err != nil {
		return BridgeIssueResult{}, err
	}
	retirementAt := now.Add(input.Overlap)
	if predecessorExpiry := predecessor.ExpiresAt(); predecessorExpiry != nil && predecessorExpiry.Before(retirementAt) {
		retirementAt = *predecessorExpiry
	}
	if expiresAt != nil && !expiresAt.After(retirementAt) {
		return BridgeIssueResult{}, errors.New("successor API key must outlive rotation overlap")
	}
	grant := predecessor.Grant()
	if len(input.Patterns) > 0 {
		grant, err = bridge.issuer.registry.Resolve(input.Patterns...)
		if err != nil {
			return BridgeIssueResult{}, fmt.Errorf("resolve rotated API key grant: %w", err)
		}
	}
	successor, err := bridge.issuer.build(predecessor.TenantID(), predecessor.Label(), grant, expiresAt, predecessor.ID(), now)
	if err != nil {
		return BridgeIssueResult{}, err
	}
	expected := predecessor.Version()
	if err := predecessor.ScheduleRetirement(now, retirementAt); err != nil {
		return BridgeIssueResult{}, err
	}
	envelope, err := sealer.Seal(successor.Credential().Reveal())
	if err != nil {
		return BridgeIssueResult{}, fmt.Errorf("seal credential bridge rotation: %w", err)
	}
	persisted, persistedEnvelope, created, err := repository.RotateBridgeCommand(ctx, prepared, scope, command, predecessor, expected, successor.Key(), envelope)
	if err != nil {
		return BridgeIssueResult{}, fmt.Errorf("persist credential bridge rotation: %w", err)
	}
	return BridgeIssueResult{Key: persisted, Envelope: persistedEnvelope, Created: created}, nil
}

// Revoke idempotently applies an irreversible expected-version transition.
func (bridge *CredentialBridge) Revoke(ctx context.Context, command BridgeCommand, action AdminAction, scope tenant.Scope, identifier id.APIKey, expectedVersion int64) (Key, bool, error) {
	if bridge == nil || command.ID == "" || command.RequestDigest == ([sha256.Size]byte{}) || identifier.IsZero() || expectedVersion < 1 {
		return Key{}, false, errors.New("credential bridge revocation is invalid")
	}
	if key, _, err := bridge.repository.ObserveBridgeCommand(ctx, scope, command); err == nil {
		return key, false, nil
	} else if !errors.Is(err, ErrKeyNotFound) {
		return Key{}, false, err
	}
	now := bridge.issuer.clock.Now().UTC()
	repository, ok := bridge.repository.(bridgeLifecycleRepository)
	if !ok {
		return Key{}, false, errors.New("credential bridge lifecycle repository is unavailable")
	}
	prepared, err := prepareAdminAction(action, now)
	if err != nil {
		return Key{}, false, err
	}
	key, err := repository.FindAdministrative(ctx, scope, identifier)
	if err != nil {
		return Key{}, false, err
	}
	if key.Version() != expectedVersion {
		return Key{}, false, ErrKeyConflict
	}
	if err := key.Revoke(now); err != nil {
		return Key{}, false, err
	}
	persisted, created, err := repository.RevokeBridgeCommand(ctx, prepared, scope, command, key, expectedVersion)
	if err != nil {
		return Key{}, false, fmt.Errorf("persist credential bridge revocation: %w", err)
	}
	return persisted, created, nil
}

// Issue idempotently issues a tenant key for one immutable bridge command.
func (bridge *CredentialBridge) Issue(
	ctx context.Context,
	command BridgeCommand,
	action AdminAction,
	scope tenant.Scope,
	input IssueInput,
	sealer CredentialSealer,
) (BridgeIssueResult, error) {
	if bridge == nil || sealer == nil {
		return BridgeIssueResult{}, errors.New("credential bridge is not initialised")
	}
	if command.ID == "" || command.RequestDigest == ([sha256.Size]byte{}) {
		return BridgeIssueResult{}, errors.New("credential bridge command identity is invalid")
	}
	now := bridge.issuer.clock.Now().UTC()
	preparedAction, err := prepareAdminAction(action, now)
	if err != nil {
		return BridgeIssueResult{}, fmt.Errorf("validate credential bridge action: %w", err)
	}
	expiresAt, err := bridge.issuer.expiry.resolve(now, input.Expiry)
	if err != nil {
		return BridgeIssueResult{}, err
	}
	grant, err := bridge.issuer.registry.Resolve(input.Patterns...)
	if err != nil {
		return BridgeIssueResult{}, fmt.Errorf("resolve API key grant: %w", err)
	}
	issued, err := bridge.issuer.build(scope.ID(), input.Label, grant, expiresAt, id.APIKey{}, now)
	if err != nil {
		return BridgeIssueResult{}, err
	}
	envelope, err := sealer.Seal(issued.credential.Reveal())
	if err != nil {
		return BridgeIssueResult{}, fmt.Errorf("seal credential bridge result: %w", err)
	}
	persisted, persistedEnvelope, created, err := bridge.repository.IssueBridgeCommand(
		ctx, preparedAction, scope, command, issued.key, envelope,
	)
	if err != nil {
		return BridgeIssueResult{}, fmt.Errorf("persist credential bridge issue command: %w", err)
	}
	return BridgeIssueResult{Key: persisted, Envelope: persistedEnvelope, Created: created}, nil
}
