package evidence

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// ErrKeyRewrapFailed is the stable fail-closed result for an unavailable,
// invalid, or unverifiable key rewrap operation.
var ErrKeyRewrapFailed = errors.New("evidence: key rewrap failed")

// KeyRewrap is one validated aggregate transition that changes only the
// wrapped content key and lifecycle metadata used for optimistic concurrency.
type KeyRewrap struct {
	asset       Asset
	previousKey kms.WrappedKey
}

// Asset returns the evidence aggregate containing the newly wrapped key.
func (rewrap KeyRewrap) Asset() Asset { return rewrap.asset }

// PreviousKey returns the exact immutable key metadata replaced by this transition.
func (rewrap KeyRewrap) PreviousKey() kms.WrappedKey { return rewrap.previousKey }

// RewrapKey creates an optimistic transition without changing ciphertext,
// object identity, content revision, lifecycle state, or integrity state.
func (asset Asset) RewrapKey(
	expectedVersion int64,
	wrappedKey kms.WrappedKey,
	now time.Time,
) (KeyRewrap, error) {
	if asset.record.ID.IsZero() || expectedVersion != asset.record.Version {
		return KeyRewrap{}, ErrVersionConflict
	}
	if wrappedKey.IsZero() || now.IsZero() || !now.UTC().After(asset.record.UpdatedAt) {
		return KeyRewrap{}, ErrConflict
	}
	previousRecord := asset.content.Envelope().Record().WrappedKey
	previousKey, err := kms.NewWrappedKey(previousRecord)
	if err != nil {
		return KeyRewrap{}, ErrKeyRewrapFailed
	}
	if sameKeyIdentity(previousRecord, wrappedKey.Record()) {
		return KeyRewrap{}, ErrConflict
	}

	record := asset.Record()
	record.Content.Envelope.WrappedKey = wrappedKey.Record()
	record.Version++
	record.UpdatedAt = now.UTC()
	authenticatedContext, err := AuthenticatedContext(record)
	if err != nil {
		return KeyRewrap{}, ErrKeyRewrapFailed
	}
	content, err := NewContent(record.Content, authenticatedContext)
	if err != nil {
		return KeyRewrap{}, ErrKeyRewrapFailed
	}
	record.Content = content.Record()

	return KeyRewrap{
		asset: Asset{record: record, content: content}, previousKey: previousKey,
	}, nil
}

func sameKeyIdentity(first, second kms.WrappedKeyRecord) bool {
	return first.Provider == second.Provider && first.Reference == second.Reference &&
		first.Version == second.Version && first.Algorithm == second.Algorithm
}

// Rewrapper verifies and persists one exact evidence-key transition.
type Rewrapper struct {
	finder    AssetFinder
	persister KeyRewrapPersister
	wrapper   platformcrypto.KeyWrapper
	unwrapper platformcrypto.KeyUnwrapper
}

// NewRewrapper constructs a provider-neutral evidence rewrap workflow.
func NewRewrapper(
	finder AssetFinder,
	persister KeyRewrapPersister,
	wrapper platformcrypto.KeyWrapper,
	unwrapper platformcrypto.KeyUnwrapper,
) (*Rewrapper, error) {
	if finder == nil || persister == nil || wrapper == nil || unwrapper == nil {
		return nil, errors.New("evidence: key rewrap dependencies are required")
	}

	return &Rewrapper{
		finder: finder, persister: persister, wrapper: wrapper, unwrapper: unwrapper,
	}, nil
}

// Rewrap replaces only the wrapped content key after proving that the target
// wrapping releases the same keyset under the same purpose and context.
func (rewrapper *Rewrapper) Rewrap(
	ctx context.Context,
	scope tenant.Scope,
	identifier id.Evidence,
	expectedVersion int64,
	attribution CommandAttribution,
	now time.Time,
) (Asset, error) {
	if ctx == nil || scope.ID().IsZero() || identifier.IsZero() || expectedVersion < 1 ||
		!attribution.Valid() || now.IsZero() {
		return Asset{}, ErrKeyRewrapFailed
	}
	if err := ctx.Err(); err != nil {
		return Asset{}, fmt.Errorf("rewrap evidence key: %w", err)
	}
	asset, err := rewrapper.finder.Find(ctx, scope, identifier)
	if err != nil {
		return Asset{}, fmt.Errorf("find evidence for key rewrap: %w", err)
	}
	if asset.Version() != expectedVersion {
		return Asset{}, ErrVersionConflict
	}

	record := asset.Record()
	authenticatedContext, err := AuthenticatedContext(record)
	if err != nil {
		return Asset{}, ErrKeyRewrapFailed
	}
	purpose, err := kms.NewPurpose(record.Content.Envelope.Purpose)
	if err != nil {
		return Asset{}, ErrKeyRewrapFailed
	}
	previousKey, err := kms.NewWrappedKey(record.Content.Envelope.WrappedKey)
	if err != nil {
		return Asset{}, ErrKeyRewrapFailed
	}
	contextData := authenticatedContext.Data()
	defer clear(contextData)
	plaintextKey, err := rewrapper.unwrapper.Unwrap(ctx, purpose, previousKey, contextData)
	if err != nil {
		clear(plaintextKey)
		return Asset{}, ErrKeyRewrapFailed
	}
	defer clear(plaintextKey)
	newKey, err := rewrapper.wrapper.Wrap(ctx, purpose, plaintextKey, contextData)
	if err != nil {
		return Asset{}, ErrKeyRewrapFailed
	}
	verifiedKey, err := rewrapper.unwrapper.Unwrap(ctx, purpose, newKey, contextData)
	if err != nil {
		clear(verifiedKey)
		return Asset{}, ErrKeyRewrapFailed
	}
	verified := len(verifiedKey) == len(plaintextKey) &&
		subtle.ConstantTimeCompare(verifiedKey, plaintextKey) == 1
	clear(verifiedKey)
	if !verified {
		return Asset{}, ErrKeyRewrapFailed
	}
	if err := ctx.Err(); err != nil {
		return Asset{}, fmt.Errorf("rewrap evidence key: %w", err)
	}

	change, err := asset.RewrapKey(expectedVersion, newKey, now)
	if err != nil {
		return Asset{}, err
	}
	if err := rewrapper.persister.PersistKeyRewrap(ctx, scope, change, attribution); err != nil {
		return Asset{}, fmt.Errorf("persist evidence key rewrap: %w", err)
	}

	return change.Asset(), nil
}
