package evidence_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/evidence"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	tinkcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto/tink"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
	"github.com/Mujhtech/idenqa/internal/platform/kms/local"
	"github.com/Mujhtech/idenqa/internal/platform/objectstore"
	objectlocal "github.com/Mujhtech/idenqa/internal/platform/objectstore/local"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestProtectorEncryptsStoresPersistsAndReads(t *testing.T) {
	t.Parallel()

	fixture := newProtectionFixture(t)
	plaintext := bytes.Repeat([]byte("private evidence\n"), 70_000)
	input := fixture.input(bytes.NewReader(plaintext))
	asset, err := fixture.protector.Protect(context.Background(), fixture.scope, input)
	if err != nil {
		t.Fatalf("Protect() error = %v", err)
	}
	if fixture.repository.created.ID().String() != asset.ID().String() {
		t.Fatal("Protect() did not persist the returned evidence asset")
	}
	record := asset.Record()
	if record.Registry != fixture.registry.Reference() {
		t.Fatalf("registry = %+v, want %+v", record.Registry, fixture.registry.Reference())
	}
	if got := record.Content.PlaintextDigest; got != string(platformcrypto.Sum(plaintext)) {
		t.Fatalf("plaintext digest = %q, want %q", got, platformcrypto.Sum(plaintext))
	}
	reader, err := fixture.objects.Open(context.Background(), asset.Content().Object())
	if err != nil {
		t.Fatalf("objects.Open() error = %v", err)
	}
	defer func() { _ = reader.Close() }()
	var opened bytes.Buffer
	authenticatedContext, err := evidence.AuthenticatedContext(record)
	if err != nil {
		t.Fatalf("AuthenticatedContext() error = %v", err)
	}
	if err := fixture.streaming.Open(
		context.Background(),
		&opened,
		reader,
		asset.Content().Envelope(),
		authenticatedContext,
	); err != nil {
		t.Fatalf("streaming.Open() error = %v", err)
	}
	if !bytes.Equal(opened.Bytes(), plaintext) {
		t.Fatalf("opened plaintext length = %d, want %d", opened.Len(), len(plaintext))
	}
}

func TestProtectorPreparesWithoutPersistenceAndDiscardsExactCiphertext(t *testing.T) {
	t.Parallel()

	fixture := newProtectionFixture(t)
	prepared, err := fixture.protector.Prepare(
		context.Background(),
		fixture.scope,
		fixture.input(bytes.NewReader([]byte("private evidence"))),
	)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if !fixture.repository.created.ID().IsZero() {
		t.Fatal("Prepare() persisted evidence before the acceptance transaction")
	}
	object := prepared.Asset().Content().Object()
	reader, err := fixture.objects.Open(context.Background(), object)
	if err != nil {
		t.Fatalf("objects.Open(prepared) error = %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("prepared reader.Close() error = %v", err)
	}
	acceptanceCause := evidence.ErrUploadAcceptanceOutcomeUnknown
	reconciliation := prepared.ReconciliationError(acceptanceCause)
	reconciliationError, ok := errors.AsType[*evidence.AcceptanceReconciliationError](reconciliation)
	if !ok || !errors.Is(reconciliation, evidence.ErrAcceptanceReconciliationRequired) ||
		!errors.Is(reconciliation, acceptanceCause) || reconciliationError.Object() != object {
		t.Fatalf("ReconciliationError() = %v, object = %+v", reconciliation, reconciliationError)
	}
	if err := fixture.protector.Discard(context.Background(), prepared); err != nil {
		t.Fatalf("Discard() error = %v", err)
	}
	if _, err := fixture.objects.Open(context.Background(), object); !errors.Is(err, objectlocal.ErrUnavailable) {
		t.Fatalf("objects.Open(discarded) error = %v, want ErrUnavailable", err)
	}
}

func TestProtectorDeletesExactCiphertextAfterPersistenceFailure(t *testing.T) {
	t.Parallel()

	fixture := newProtectionFixture(t)
	persistenceError := errors.New("database unavailable")
	fixture.repository.err = persistenceError
	_, err := fixture.protector.Protect(
		context.Background(),
		fixture.scope,
		fixture.input(bytes.NewReader([]byte("private evidence"))),
	)
	if !errors.Is(err, persistenceError) {
		t.Fatalf("Protect() error = %v, want persistence error", err)
	}
	if _, err := fixture.objects.Open(context.Background(), fixture.repository.created.Content().Object()); !errors.Is(err, objectlocal.ErrUnavailable) {
		t.Fatalf("objects.Open(compensated) error = %v, want ErrUnavailable", err)
	}
}

func TestProtectorReportsExactObjectWhenCompensationFails(t *testing.T) {
	t.Parallel()

	registry, err := evidence.BuiltInRegistry()
	if err != nil {
		t.Fatalf("BuiltInRegistry() error = %v", err)
	}
	input, scope := protectionInput(t, bytes.NewReader([]byte("private evidence")))
	contextValue, err := evidence.AuthenticatedContext(evidence.Record{
		ID: input.ID, TenantID: scope.ID(), VerificationID: input.VerificationID,
		RequirementKey: input.RequirementKey, EvidenceType: input.EvidenceType,
		Artefact: input.Artefact, AcquisitionMethod: input.AcquisitionMethod,
		Registry: registry.Reference(), ContentRevision: input.ContentRevision,
	})
	if err != nil {
		t.Fatalf("AuthenticatedContext() error = %v", err)
	}
	wrapped, err := kms.NewWrappedKey(kms.WrappedKeyRecord{
		Provider: "local.file", Reference: "local-keyring", Version: "v1",
		Algorithm: "AES256_GCM", Ciphertext: []byte("wrapped"),
	})
	if err != nil {
		t.Fatalf("NewWrappedKey() error = %v", err)
	}
	envelope, err := platformcrypto.NewEnvelope(platformcrypto.EnvelopeRecord{
		FormatVersion: 1, ContentAlgorithm: "AES256_GCM_HKDF_1MB",
		Purpose: "idenqa.evidence.content", WrappedKey: wrapped.Record(),
		ContextSchemaVersion: contextValue.SchemaVersion(), ContextDigest: string(contextValue.Digest()),
	})
	if err != nil {
		t.Fatalf("NewEnvelope() error = %v", err)
	}
	object, err := objectstore.NewObject(objectstore.ObjectRecord{
		Key: "tenants/ten_1/evidence/evd_1/content/1", Version: "version-1", Size: 10,
		Checksum: string(platformcrypto.Sum([]byte("ciphertext"))),
	})
	if err != nil {
		t.Fatalf("NewObject() error = %v", err)
	}
	objects := &protectionObjectStoreStub{
		object: object, envelope: envelope, deleteErr: errors.New("storage unavailable"),
	}
	repository := &protectionRepositoryStub{err: errors.New("database unavailable")}
	protector, err := evidence.NewProtector(objects, objects, repository, registry, time.Second)
	if err != nil {
		t.Fatalf("NewProtector() error = %v", err)
	}
	_, err = protector.Protect(context.Background(), scope, input)
	if !errors.Is(err, evidence.ErrCleanupRequired) {
		t.Fatalf("Protect() error = %v, want ErrCleanupRequired", err)
	}
	cleanup, ok := errors.AsType[*evidence.CleanupError](err)
	if !ok || cleanup.Object().Record() != object.Record() {
		t.Fatalf("CleanupError object = %+v, want %+v", cleanup, object.Record())
	}

	objects.putErr = errors.New("upload outcome ambiguous")
	_, err = protector.Protect(context.Background(), scope, input)
	if !errors.Is(err, evidence.ErrCleanupRequired) {
		t.Fatalf("Protect(ambiguous upload) error = %v, want ErrCleanupRequired", err)
	}
	cleanup, ok = errors.AsType[*evidence.CleanupError](err)
	if !ok || cleanup.Object().Record() != object.Record() {
		t.Fatalf("CleanupError(ambiguous upload) object = %+v, want %+v", cleanup, object.Record())
	}
}

type protectionFixture struct {
	registry   evidence.Registry
	scope      tenant.Scope
	keyring    *local.Keyring
	streaming  *tinkcrypto.Streaming
	objects    *objectlocal.Store
	repository *protectionRepositoryStub
	protector  *evidence.Protector
	baseInput  evidence.ProtectionInput
}

func newProtectionFixture(t *testing.T) protectionFixture {
	t.Helper()
	registry, err := evidence.BuiltInRegistry()
	if err != nil {
		t.Fatalf("BuiltInRegistry() error = %v", err)
	}
	input, scope := protectionInput(t, nil)
	keyring, err := local.Create(filepath.Join(t.TempDir(), "keyring.json"))
	if err != nil {
		t.Fatalf("local.Create() error = %v", err)
	}
	t.Cleanup(func() { _ = keyring.Close() })
	purpose, err := kms.NewPurpose("idenqa.evidence.content")
	if err != nil {
		t.Fatalf("NewPurpose() error = %v", err)
	}
	streaming, err := tinkcrypto.NewStreaming(keyring, keyring, purpose)
	if err != nil {
		t.Fatalf("NewStreaming() error = %v", err)
	}
	objects, err := objectlocal.Open(objectlocal.Config{
		Directory: t.TempDir(), MaxObjectBytes: 4 * 1024 * 1024,
	})
	if err != nil {
		t.Fatalf("objectlocal.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = objects.Close() })
	repository := &protectionRepositoryStub{}
	protector, err := evidence.NewProtector(streaming, objects, repository, registry, time.Second)
	if err != nil {
		t.Fatalf("NewProtector() error = %v", err)
	}

	return protectionFixture{
		registry: registry, scope: scope, keyring: keyring, streaming: streaming, objects: objects,
		repository: repository, protector: protector, baseInput: input,
	}
}

func (fixture protectionFixture) input(plaintext io.Reader) evidence.ProtectionInput {
	input := fixture.baseInput
	input.Plaintext = plaintext

	return input
}

type protectionRepositoryStub struct {
	created evidence.Asset
	err     error
}

func (repository *protectionRepositoryStub) Create(
	_ context.Context,
	_ tenant.Scope,
	asset evidence.Asset,
) error {
	repository.created = asset

	return repository.err
}

type protectionObjectStoreStub struct {
	object    objectstore.Object
	envelope  platformcrypto.Envelope
	putErr    error
	deleteErr error
}

func (store *protectionObjectStoreStub) Seal(
	_ context.Context,
	destination io.Writer,
	plaintext io.Reader,
	_ platformcrypto.Context,
) (platformcrypto.Envelope, error) {
	_, err := io.Copy(destination, plaintext)

	return store.envelope, err
}

func (store *protectionObjectStoreStub) Put(
	_ context.Context,
	_ objectstore.Key,
	write func(io.Writer) error,
) (objectstore.Object, error) {
	if err := write(io.Discard); err != nil {
		return objectstore.Object{}, err
	}

	return store.object, store.putErr
}

func (store *protectionObjectStoreStub) Delete(context.Context, objectstore.Object) error {
	return store.deleteErr
}

func protectionInput(t *testing.T, plaintext io.Reader) (evidence.ProtectionInput, tenant.Scope) {
	t.Helper()
	now := time.Date(2026, time.August, 28, 14, 0, 0, 0, time.UTC)
	generator, err := id.NewGenerator(assetClock{now: now}, bytes.NewReader(bytes.Repeat([]byte{0x27}, 256)))
	if err != nil {
		t.Fatalf("NewGenerator() error = %v", err)
	}
	tenantID, err := generator.NewTenant()
	if err != nil {
		t.Fatalf("NewTenant() error = %v", err)
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatalf("tenant.NewScope() error = %v", err)
	}
	subjectID, err := generator.NewSubject()
	if err != nil {
		t.Fatalf("NewSubject() error = %v", err)
	}
	verificationID, err := generator.NewVerification()
	if err != nil {
		t.Fatalf("NewVerification() error = %v", err)
	}
	evidenceID, err := generator.NewEvidence()
	if err != nil {
		t.Fatalf("NewEvidence() error = %v", err)
	}

	return evidence.ProtectionInput{
		ID: evidenceID, SubjectID: subjectID, VerificationID: verificationID,
		RequirementKey: "selfie", EvidenceType: evidence.EvidenceSelfieImage,
		Artefact: evidence.ArtefactSelfieImage, AcquisitionMethod: evidence.MethodLiveCamera,
		Assurances: []evidence.Name{evidence.AssuranceFreshness, evidence.AssuranceLiveCapture},
		Region:     "idenqa.region.local", RetentionClass: "idenqa.retention.standard",
		ContentRevision: 1, MediaType: "image/jpeg", Plaintext: plaintext, CreatedAt: now,
	}, scope
}
