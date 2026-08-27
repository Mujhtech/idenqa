package verification

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestServiceCreateRequiresApplicationPermission(t *testing.T) {
	t.Parallel()

	repository := &profileServiceRepositoryStub{}
	service, document := newProfileServiceFixture(t, repository, &profileIDGeneratorStub{})

	_, err := service.Create(context.Background(), access.Context{}, "attempt-1", "Standard", document)
	if !errors.Is(err, access.ErrInsufficientScope) {
		t.Fatalf("Create() error = %v, want ErrInsufficientScope", err)
	}
	if repository.replayCalls != 0 || repository.applyCalls != 0 {
		t.Fatal("unauthorised create reached persistence")
	}
}

func TestServiceCreateReplaysBeforeGeneratingIdentifier(t *testing.T) {
	t.Parallel()

	want := MutationResult{ProfileID: "prf_01K3P4NQF00000000000000000", Version: 1}
	repository := &profileServiceRepositoryStub{replay: want, replayFound: true}
	identifiers := &profileIDGeneratorStub{err: errors.New("identifier must not be generated")}
	service, document := newProfileServiceFixture(t, repository, identifiers)
	authority := newProfileServiceAuthority(t, access.Pattern("capture_profiles:write"))

	got, err := service.Create(context.Background(), authority, "attempt-1", "Standard", document)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if got != want {
		t.Fatalf("Create() = %+v, want %+v", got, want)
	}
	if identifiers.calls != 0 || repository.applyCalls != 0 || repository.replayCalls != 1 {
		t.Fatalf("calls: identifiers=%d replay=%d apply=%d", identifiers.calls, repository.replayCalls, repository.applyCalls)
	}
}

func TestServiceCreateBuildsAtomicIdempotentMutation(t *testing.T) {
	t.Parallel()

	profileID, err := id.ParseProfile("prf_01K3P4NQF00000000000000000")
	if err != nil {
		t.Fatalf("ParseProfile() error = %v", err)
	}
	repository := &profileServiceRepositoryStub{}
	identifiers := &profileIDGeneratorStub{profileID: profileID}
	service, document := newProfileServiceFixture(t, repository, identifiers)
	authority := newProfileServiceAuthority(t, access.Pattern("capture_profiles:write"))

	result, err := service.Create(context.Background(), authority, "attempt-1", "Standard", document)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if repository.applyCalls != 1 || repository.mutation.Kind != MutationCreate {
		t.Fatalf("Apply calls=%d mutation=%+v", repository.applyCalls, repository.mutation)
	}
	if repository.mutation.Idempotency.Key() != "attempt-1" ||
		repository.mutation.Idempotency.Operation() != OperationCreateProfile ||
		repository.mutation.Status != 201 {
		t.Fatalf("idempotent mutation = %+v", repository.mutation)
	}
	if result.ProfileID != profileID.String() || result.Revision != 1 || identifiers.calls != 1 {
		t.Fatalf("result=%+v identifier calls=%d", result, identifiers.calls)
	}
}

type profileServiceRepositoryStub struct {
	replay      MutationResult
	replayFound bool
	replayErr   error
	replayCalls int
	mutation    Mutation
	applyCalls  int
}

func (*profileServiceRepositoryStub) FindProfile(context.Context, tenant.Scope, id.Profile) (CaptureProfile, error) {
	return CaptureProfile{}, ErrProfileNotFound
}

func (*profileServiceRepositoryStub) FindRevision(context.Context, tenant.Scope, id.Profile, uint32) (Revision, error) {
	return Revision{}, ErrProfileNotFound
}

func (*profileServiceRepositoryStub) ListProfiles(context.Context, tenant.Scope, *ListPosition, int) (Page, error) {
	return Page{}, nil
}

func (repository *profileServiceRepositoryStub) Replay(
	context.Context,
	tenant.Scope,
	idempotency.Request,
) (MutationResult, bool, error) {
	repository.replayCalls++

	return repository.replay, repository.replayFound, repository.replayErr
}

func (*profileServiceRepositoryStub) SaveDraft(
	context.Context,
	tenant.Scope,
	id.APIKey,
	CaptureProfile,
	Revision,
	int64,
) error {
	return nil
}

func (repository *profileServiceRepositoryStub) Apply(
	_ context.Context,
	_ tenant.Scope,
	mutation Mutation,
) (MutationResult, error) {
	repository.applyCalls++
	repository.mutation = mutation

	return mutation.Result, nil
}

type profileIDGeneratorStub struct {
	profileID id.Profile
	err       error
	calls     int
}

func (generator *profileIDGeneratorStub) NewProfile() (id.Profile, error) {
	generator.calls++

	return generator.profileID, generator.err
}

type profileServiceClock struct{ now time.Time }

func (source profileServiceClock) Now() time.Time { return source.now }

type profileServiceVerificationRepository struct{ record access.VerificationRecord }

func (repository profileServiceVerificationRepository) FindForVerification(
	context.Context,
	id.Tenant,
	id.APIKey,
) (access.VerificationRecord, error) {
	return repository.record, nil
}

func newProfileServiceFixture(
	t *testing.T,
	repository Repository,
	identifiers ProfileIDGenerator,
) (*Service, Profile) {
	t.Helper()

	registry, err := evidence.BuiltInRegistry()
	if err != nil {
		t.Fatalf("BuiltInRegistry() error = %v", err)
	}
	catalog, err := evidence.NewCatalog(registry)
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	document, err := NewProfile(registry, []Requirement{{
		Key:          "selfie",
		Purpose:      evidence.PurposeIdentityVerification,
		EvidenceType: evidence.EvidenceSelfieImage,
		Artefacts:    []evidence.Name{evidence.ArtefactSelfieImage},
		Acquisition: Acquisition{
			Strategy: StrategyAnyOf,
			Methods:  []evidence.Name{evidence.MethodFileUpload},
		},
	}})
	if err != nil {
		t.Fatalf("NewProfile() error = %v", err)
	}
	service, err := NewService(
		repository,
		identifiers,
		catalog,
		profileServiceClock{now: time.Date(2026, time.August, 27, 20, 0, 0, 0, time.UTC)},
		24*time.Hour,
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	return service, document
}

func newProfileServiceAuthority(t *testing.T, patterns ...access.Pattern) access.Context {
	t.Helper()

	now := time.Date(2026, time.August, 27, 20, 0, 0, 0, time.UTC)
	identifiers, err := id.NewGenerator(profileServiceClock{now: now}, bytes.NewReader(bytes.Repeat([]byte{1}, 512)))
	if err != nil {
		t.Fatalf("NewGenerator() error = %v", err)
	}
	tenantID, err := identifiers.NewTenant()
	if err != nil {
		t.Fatalf("NewTenant() error = %v", err)
	}
	keyID, err := identifiers.NewAPIKey()
	if err != nil {
		t.Fatalf("NewAPIKey() error = %v", err)
	}
	keyGenerator, err := access.NewKeyGenerator(bytes.NewReader(bytes.Repeat([]byte{0x51}, 32)))
	if err != nil {
		t.Fatalf("NewKeyGenerator() error = %v", err)
	}
	presented, err := keyGenerator.Generate(tenantID, keyID)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	peppers, err := access.NewPepperSet(1, map[access.PepperVersion][]byte{1: bytes.Repeat([]byte{0x61}, 32)})
	if err != nil {
		t.Fatalf("NewPepperSet() error = %v", err)
	}
	digest, pepperVersion, err := peppers.Digest(presented)
	if err != nil {
		t.Fatalf("Digest() error = %v", err)
	}
	grant, err := access.TenantRegistry().Resolve(patterns...)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	key, err := access.RestoreKey(access.KeyRecord{
		ID: keyID, TenantID: tenantID, Label: "Profile service test", Digest: digest,
		PepperVersion: pepperVersion, Grant: grant, Version: 1, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("RestoreKey() error = %v", err)
	}
	record, err := access.NewVerificationRecord(key, tenant.StateActive)
	if err != nil {
		t.Fatalf("NewVerificationRecord() error = %v", err)
	}
	authenticator, err := access.NewAuthenticator(
		profileServiceVerificationRepository{record: record},
		peppers,
		profileServiceClock{now: now},
	)
	if err != nil {
		t.Fatalf("NewAuthenticator() error = %v", err)
	}
	authority, err := authenticator.Authenticate(context.Background(), presented.Reveal())
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}

	return authority
}
