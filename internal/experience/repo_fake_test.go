package experience

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"sort"
	"time"

	contract "github.com/Mujhtech/idenqa/contracts/experience/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type fakeExperience struct {
	aggregate Experience
	revisions map[uint32]Revision
	changes   []Change
}

type fakeTenant struct {
	experiences map[string]*fakeExperience
	pins        map[string]Pin
}

type fakeRepository struct {
	tenants map[string]*fakeTenant
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{tenants: map[string]*fakeTenant{}}
}

func (repository *fakeRepository) tenant(scope tenant.Scope) *fakeTenant {
	key := scope.ID().String()
	if repository.tenants[key] == nil {
		repository.tenants[key] = &fakeTenant{experiences: map[string]*fakeExperience{}, pins: map[string]Pin{}}
	}
	return repository.tenants[key]
}

func (repository *fakeRepository) Create(_ context.Context, scope tenant.Scope, mutation CreateMutation) (Experience, error) {
	state := repository.tenant(scope)
	if _, exists := state.experiences[mutation.Document.ExperienceID]; exists {
		return Experience{}, ErrConflict
	}
	current := Experience{
		ID: mustExperienceID(mutation.Document.ExperienceID), TenantID: scope.ID(), State: StateDraft, Revision: 1,
		LatestVersion: mutation.Document.Version, Document: mutation.Document, CreatedAt: mutation.At, UpdatedAt: mutation.At,
	}
	record := &fakeExperience{aggregate: current, revisions: map[uint32]Revision{
		mutation.Document.Version: {ExperienceID: current.ID, Version: mutation.Document.Version, State: RevisionDraft, Manifest: mutation.Manifest, CreatedAt: mutation.At},
	}}
	record.changes = append(record.changes, Change{
		Sequence: 1, Operation: string(OperationCreate), FromState: "", ToState: StateDraft,
		Version: mutation.Document.Version, TargetVersion: mutation.Document.Version, Actor: mutation.Actor,
		Digest: mutation.Manifest.Digest, OccurredAt: mutation.At,
	})
	state.experiences[current.ID.String()] = record
	return current, nil
}

func (repository *fakeRepository) Find(_ context.Context, scope tenant.Scope, identifier id.Experience) (Experience, error) {
	record := repository.tenant(scope).experiences[identifier.String()]
	if record == nil {
		return Experience{}, ErrNotFound
	}
	return record.aggregate, nil
}

func (repository *fakeRepository) List(_ context.Context, scope tenant.Scope, position *Position, limit int) (Page, error) {
	state := repository.tenant(scope)
	keys := make([]string, 0, len(state.experiences))
	for key := range state.experiences {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	page := Page{Experiences: make([]Experience, 0, limit)}
	for _, key := range keys {
		if position != nil && key <= position.Before.String() {
			continue
		}
		if len(page.Experiences) == limit {
			next := Position{Before: mustExperienceID(key)}
			page.Next = &next
			return page, nil
		}
		page.Experiences = append(page.Experiences, state.experiences[key].aggregate)
	}
	return page, nil
}

func (repository *fakeRepository) ApplyRevision(_ context.Context, scope tenant.Scope, mutation RevisionMutation) (Experience, error) {
	record := repository.tenant(scope).experiences[mutation.ExperienceID.String()]
	if record == nil {
		return Experience{}, ErrNotFound
	}
	if record.aggregate.Revision != mutation.ExpectedRevision || record.aggregate.State == StateRevoked {
		return Experience{}, ErrConflict
	}
	plan, applied, err := PlanTransition(record.aggregate, OperationUpdate, mutation.Document.Version)
	if err != nil {
		return Experience{}, err
	}
	if !applied {
		return record.aggregate, nil
	}
	record.aggregate.State = plan.State
	record.aggregate.LatestVersion = plan.LatestVersion
	record.aggregate.ApprovedVersion = plan.ApprovedVersion
	record.aggregate.Document = mutation.Document
	record.aggregate.Revision++
	record.aggregate.UpdatedAt = mutation.At
	record.revisions[plan.TargetVersion] = Revision{
		ExperienceID: mutation.ExperienceID, Version: plan.TargetVersion, State: plan.TargetRevision,
		Manifest: mutation.Manifest, CreatedAt: mutation.At,
	}
	record.changes = append(record.changes, Change{
		Sequence: uint64(len(record.changes) + 1), Operation: string(OperationUpdate), FromState: StateDraft, ToState: plan.State,
		Version: plan.TargetVersion, TargetVersion: plan.TargetVersion, Actor: mutation.Actor, Reason: mutation.Reason,
		Digest: mutation.Manifest.Digest, OccurredAt: mutation.At,
	})
	return record.aggregate, nil
}

func (repository *fakeRepository) ApplyTransition(_ context.Context, scope tenant.Scope, mutation TransitionMutation) (Experience, error) {
	record := repository.tenant(scope).experiences[mutation.ExperienceID.String()]
	if record == nil {
		return Experience{}, ErrNotFound
	}
	if record.aggregate.Revision != mutation.ExpectedRevision {
		return Experience{}, ErrConflict
	}
	if !mutation.Applied {
		return record.aggregate, nil
	}
	plan := mutation.Plan
	fromState := record.aggregate.State
	if plan.SupersededVersion != 0 {
		revision := record.revisions[plan.SupersededVersion]
		revision.State = plan.SupersededRevision
		record.revisions[plan.SupersededVersion] = revision
	}
	if plan.TargetRevision != "" {
		revision := record.revisions[plan.TargetVersion]
		revision.State = plan.TargetRevision
		record.revisions[plan.TargetVersion] = revision
	}
	record.aggregate.State = plan.State
	record.aggregate.ApprovedVersion = plan.ApprovedVersion
	record.aggregate.PublishedVersion = plan.PublishedVersion
	record.aggregate.Revision++
	record.aggregate.UpdatedAt = mutation.At
	record.changes = append(record.changes, Change{
		Sequence: uint64(len(record.changes) + 1), Operation: string(mutation.Operation), FromState: fromState, ToState: plan.State,
		Version: record.aggregate.LatestVersion, TargetVersion: plan.TargetVersion, Actor: mutation.Actor,
		Reason: mutation.Reason, Digest: record.revisions[plan.TargetVersion].Manifest.Digest, OccurredAt: mutation.At,
	})
	return record.aggregate, nil
}

func (repository *fakeRepository) LoadPublished(_ context.Context, scope tenant.Scope) ([]Published, error) {
	state := repository.tenant(scope)
	result := make([]Published, 0)
	for _, record := range state.experiences {
		if record.aggregate.State != StatePublished || record.aggregate.PublishedVersion == 0 {
			continue
		}
		revision := record.revisions[record.aggregate.PublishedVersion]
		result = append(result, Published{
			ExperienceID: record.aggregate.ID, Version: revision.Version, Manifest: revision.Manifest, PublishedAt: revision.CreatedAt,
		})
	}
	return result, nil
}

func (repository *fakeRepository) Revision(_ context.Context, scope tenant.Scope, identifier id.Experience, version uint32) (Revision, error) {
	record := repository.tenant(scope).experiences[identifier.String()]
	if record == nil {
		return Revision{}, ErrNotFound
	}
	revision, exists := record.revisions[version]
	if !exists {
		return Revision{}, ErrNotFound
	}
	revision.Manifest.Document.ExperienceID = identifier.String()
	revision.Manifest.Document.Version = version
	return revision, nil
}

func (repository *fakeRepository) Changes(_ context.Context, scope tenant.Scope, identifier id.Experience) ([]Change, error) {
	record := repository.tenant(scope).experiences[identifier.String()]
	if record == nil {
		return nil, ErrNotFound
	}
	return append([]Change(nil), record.changes...), nil
}

func (repository *fakeRepository) SavePin(_ context.Context, scope tenant.Scope, pin Pin) (Pin, error) {
	state := repository.tenant(scope)
	key := pin.VerificationID.String()
	if existing, exists := state.pins[key]; exists {
		return existing, nil
	}
	state.pins[key] = pin
	return pin, nil
}

func (repository *fakeRepository) FindPin(_ context.Context, scope tenant.Scope, verificationID id.Verification) (Pin, error) {
	pin, exists := repository.tenant(scope).pins[verificationID.String()]
	if !exists {
		return Pin{}, ErrNotFound
	}
	return pin, nil
}

type fakeSigner struct {
	private   []byte
	keyID     string
	failAfter int
	calls     int
}

func newFakeSigner() *fakeSigner {
	seed := make([]byte, 32)
	for index := range seed {
		seed[index] = byte(index + 1)
	}
	return &fakeSigner{private: seed, keyID: "expkey_fake_1", failAfter: -1}
}

func (signer *fakeSigner) Sign(_ context.Context, canonical []byte) (Signature, error) {
	signer.calls++
	if signer.failAfter >= 0 && signer.calls > signer.failAfter {
		return Signature{}, fmt.Errorf("signer unavailable")
	}
	private := ed25519.NewKeyFromSeed(signer.private)
	return Signature{KeyID: signer.keyID, Algorithm: contract.SignatureAlgorithm, Bytes: ed25519.Sign(private, canonical)}, nil
}

type fakeVerifier struct {
	publicKeys map[string][]byte
}

func (signer *fakeSigner) publicKey() ed25519.PublicKey {
	private := ed25519.NewKeyFromSeed(signer.private)
	return private.Public().(ed25519.PublicKey)
}

func newFakeVerifier(signer *fakeSigner) *fakeVerifier {
	private := ed25519.NewKeyFromSeed(signer.private)
	public := private.Public().(ed25519.PublicKey)
	return &fakeVerifier{publicKeys: map[string][]byte{signer.keyID: public}}
}

func (verifier *fakeVerifier) Verify(_ context.Context, keyID string, canonical []byte, signature []byte) error {
	public, exists := verifier.publicKeys[keyID]
	if !exists || !ed25519.Verify(public, canonical, signature) {
		return ErrSignature
	}
	return nil
}

type fakeAssets struct {
	failKey string
	calls   []string
}

func (assets *fakeAssets) Verify(_ context.Context, _ tenant.Scope, asset contract.Asset) error {
	assets.calls = append(assets.calls, asset.Key)
	if asset.Key == assets.failKey {
		return ErrAssetUnavailable
	}
	return nil
}

type fakeClock struct{ now time.Time }

func (clock fakeClock) Now() time.Time { return clock.now }

type fakeIDs struct {
	next int
}

func (generator *fakeIDs) NewExperience() (id.Experience, error) {
	generator.next++
	return id.ParseExperience(fmt.Sprintf("exp_%026d", generator.next))
}

func (generator *fakeIDs) NewEvent() (id.Event, error) {
	generator.next++
	return id.ParseEvent(fmt.Sprintf("evt_%026d", generator.next))
}
