package evidence_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/evidence"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestRewrapperChangesOnlyVerifiedWrappedKeyMetadata(t *testing.T) {
	t.Parallel()

	fixture := newProtectionFixture(t)
	plaintext := bytes.Repeat([]byte("private evidence\n"), 70_000)
	asset, err := fixture.protector.Protect(
		context.Background(), fixture.scope, fixture.input(bytes.NewReader(plaintext)),
	)
	if err != nil {
		t.Fatalf("Protect() error = %v", err)
	}
	before := asset.Record()
	if _, err := fixture.keyring.Rotate(context.Background()); err != nil {
		t.Fatalf("Rotate() error = %v", err)
	}
	store := &keyRewrapStoreStub{asset: asset}
	rewrapper, err := evidence.NewRewrapper(store, store, fixture.keyring, fixture.keyring)
	if err != nil {
		t.Fatalf("NewRewrapper() error = %v", err)
	}
	now := before.UpdatedAt.Add(time.Minute)
	rewrapped, err := rewrapper.Rewrap(
		context.Background(), fixture.scope, asset.ID(), asset.Version(), validRewrapAttribution(), now,
	)
	if err != nil {
		t.Fatalf("Rewrap() error = %v", err)
	}
	after := rewrapped.Record()
	if after.Version != before.Version+1 || after.UpdatedAt != now.UTC() {
		t.Fatalf("rewrapped lifecycle version=%d updated=%s", after.Version, after.UpdatedAt)
	}
	if after.Content.Object != before.Content.Object || after.ContentRevision != before.ContentRevision ||
		after.State != before.State || after.Integrity != before.Integrity {
		t.Fatal("rewrap changed ciphertext identity, content revision, or lifecycle")
	}
	oldEnvelope := before.Content.Envelope
	newEnvelope := after.Content.Envelope
	if oldEnvelope.FormatVersion != newEnvelope.FormatVersion ||
		oldEnvelope.ContentAlgorithm != newEnvelope.ContentAlgorithm ||
		oldEnvelope.Purpose != newEnvelope.Purpose ||
		oldEnvelope.ContextSchemaVersion != newEnvelope.ContextSchemaVersion ||
		oldEnvelope.ContextDigest != newEnvelope.ContextDigest {
		t.Fatal("rewrap changed immutable envelope metadata")
	}
	if oldEnvelope.WrappedKey.Version != "v1" || newEnvelope.WrappedKey.Version != "v2" {
		t.Fatalf("wrapped key versions old=%q new=%q", oldEnvelope.WrappedKey.Version, newEnvelope.WrappedKey.Version)
	}
	if store.persisted.PreviousKey().Record().Version != "v1" ||
		store.attribution != validRewrapAttribution() {
		t.Fatal("rewrap persistence did not receive exact prior key and attribution")
	}

	reader, err := fixture.objects.Open(context.Background(), rewrapped.Content().Object())
	if err != nil {
		t.Fatalf("objects.Open() error = %v", err)
	}
	defer func() { _ = reader.Close() }()
	var opened bytes.Buffer
	authenticatedContext, err := evidence.AuthenticatedContext(after)
	if err != nil {
		t.Fatalf("AuthenticatedContext() error = %v", err)
	}
	if err := fixture.streaming.Open(
		context.Background(), &opened, reader, rewrapped.Content().Envelope(), authenticatedContext,
	); err != nil {
		t.Fatalf("streaming.Open(rewrapped) error = %v", err)
	}
	if !bytes.Equal(opened.Bytes(), plaintext) {
		t.Fatal("rewrapped envelope did not open the unchanged ciphertext")
	}
}

func TestRewrapperFailsBeforePersistenceWhenTargetCannotBeVerified(t *testing.T) {
	t.Parallel()

	fixture := newProtectionFixture(t)
	asset, err := fixture.protector.Protect(
		context.Background(), fixture.scope, fixture.input(bytes.NewReader([]byte("private evidence"))),
	)
	if err != nil {
		t.Fatalf("Protect() error = %v", err)
	}
	store := &keyRewrapStoreStub{asset: asset}
	provider := &mismatchedRewrapProvider{previous: asset.Content().Envelope().Record().WrappedKey}
	rewrapper, err := evidence.NewRewrapper(store, store, provider, provider)
	if err != nil {
		t.Fatalf("NewRewrapper() error = %v", err)
	}
	_, err = rewrapper.Rewrap(
		context.Background(), fixture.scope, asset.ID(), asset.Version(), validRewrapAttribution(),
		asset.Record().UpdatedAt.Add(time.Minute),
	)
	if !errors.Is(err, evidence.ErrKeyRewrapFailed) || !store.persisted.Asset().ID().IsZero() {
		t.Fatalf("Rewrap() error=%v persisted=%+v", err, store.persisted.Asset().Record())
	}
}

func TestAssetRewrapRejectsSameKeyIdentityAndStaleVersion(t *testing.T) {
	t.Parallel()

	fixture := newProtectionFixture(t)
	asset, err := fixture.protector.Protect(
		context.Background(), fixture.scope, fixture.input(bytes.NewReader([]byte("private evidence"))),
	)
	if err != nil {
		t.Fatalf("Protect() error = %v", err)
	}
	wrapped, err := kms.NewWrappedKey(asset.Content().Envelope().Record().WrappedKey)
	if err != nil {
		t.Fatalf("NewWrappedKey() error = %v", err)
	}
	now := asset.Record().UpdatedAt.Add(time.Minute)
	if _, err := asset.RewrapKey(asset.Version(), wrapped, now); !errors.Is(err, evidence.ErrConflict) {
		t.Fatalf("RewrapKey(same identity) error = %v, want ErrConflict", err)
	}
	if _, err := asset.RewrapKey(asset.Version()+1, wrapped, now); !errors.Is(err, evidence.ErrVersionConflict) {
		t.Fatalf("RewrapKey(stale) error = %v, want ErrVersionConflict", err)
	}
}

func TestRewrapperPreservesQuarantine(t *testing.T) {
	t.Parallel()

	fixture := newProtectionFixture(t)
	asset, err := fixture.protector.Protect(
		context.Background(), fixture.scope, fixture.input(bytes.NewReader([]byte("private evidence"))),
	)
	if err != nil {
		t.Fatalf("Protect() error = %v", err)
	}
	quarantined, err := asset.FailIntegrity(
		asset.Version(), "evidence.integrity.synthetic", asset.Record().UpdatedAt.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("FailIntegrity() error = %v", err)
	}
	if _, err := fixture.keyring.Rotate(context.Background()); err != nil {
		t.Fatalf("Rotate() error = %v", err)
	}
	store := &keyRewrapStoreStub{asset: quarantined}
	rewrapper, err := evidence.NewRewrapper(store, store, fixture.keyring, fixture.keyring)
	if err != nil {
		t.Fatalf("NewRewrapper() error = %v", err)
	}
	rewrapped, err := rewrapper.Rewrap(
		context.Background(), fixture.scope, quarantined.ID(), quarantined.Version(),
		validRewrapAttribution(), quarantined.Record().UpdatedAt.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("Rewrap() error = %v", err)
	}
	if rewrapped.Record().State != evidence.StateQuarantined || rewrapped.CanRead() ||
		rewrapped.Record().QuarantineReason != quarantined.Record().QuarantineReason {
		t.Fatalf("rewrapped quarantine = %+v", rewrapped.Record())
	}
}

type keyRewrapStoreStub struct {
	asset       evidence.Asset
	persisted   evidence.KeyRewrap
	attribution evidence.CommandAttribution
}

func (store *keyRewrapStoreStub) Find(
	_ context.Context,
	_ tenant.Scope,
	_ id.Evidence,
) (evidence.Asset, error) {
	return store.asset, nil
}

func (store *keyRewrapStoreStub) PersistKeyRewrap(
	_ context.Context,
	_ tenant.Scope,
	change evidence.KeyRewrap,
	attribution evidence.CommandAttribution,
) error {
	store.persisted = change
	store.attribution = attribution
	return nil
}

type mismatchedRewrapProvider struct {
	previous kms.WrappedKeyRecord
	wraps    int
}

func (provider *mismatchedRewrapProvider) Wrap(
	_ context.Context,
	_ kms.Purpose,
	_ []byte,
	_ []byte,
) (kms.WrappedKey, error) {
	provider.wraps++
	record := provider.previous
	record.Version = "v2"
	record.Ciphertext = []byte("new wrapped key")
	return kms.NewWrappedKey(record)
}

func (provider *mismatchedRewrapProvider) Unwrap(
	_ context.Context,
	_ kms.Purpose,
	_ kms.WrappedKey,
	_ []byte,
) ([]byte, error) {
	if provider.wraps == 0 {
		return []byte("original keyset"), nil
	}
	return []byte("different keyset"), nil
}

func validRewrapAttribution() evidence.CommandAttribution {
	return evidence.CommandAttribution{
		Principal:   evidence.Actor{Type: "idenqa.actor.operator", ID: "operator-1"},
		TenantActor: evidence.Actor{Type: "idenqa.actor.tenant_admin", ID: "tenant-admin-1"},
		Reason:      "scheduled evidence key rotation",
	}
}

var _ platformcrypto.KeyWrapper = (*mismatchedRewrapProvider)(nil)
var _ platformcrypto.KeyUnwrapper = (*mismatchedRewrapProvider)(nil)
