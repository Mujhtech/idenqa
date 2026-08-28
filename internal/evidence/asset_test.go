package evidence_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/evidence"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
	"github.com/Mujhtech/idenqa/internal/platform/objectstore"
)

type assetClock struct{ now time.Time }

func (clock assetClock) Now() time.Time { return clock.now }

func TestNewAvailableValidatesProtectedEvidence(t *testing.T) {
	t.Parallel()

	record, registry := validAssetRecord(t)
	asset, err := evidence.NewAvailable(record, registry)
	if err != nil {
		t.Fatalf("NewAvailable() error = %v", err)
	}
	if asset.State() != evidence.StateAvailable || !asset.CanRead() || asset.Version() != 1 {
		t.Fatalf("NewAvailable() state = %q, readable = %t, version = %d", asset.State(), asset.CanRead(), asset.Version())
	}
	if got := asset.Record().Assurances; len(got) != 2 || got[0] != evidence.AssuranceFreshness ||
		got[1] != evidence.AssuranceLiveCapture {
		t.Fatalf("assurances = %v", got)
	}
}

func TestNewAvailableRejectsUnsupportedAssurance(t *testing.T) {
	t.Parallel()

	record, registry := validAssetRecord(t)
	record.AcquisitionMethod = evidence.MethodFileUpload
	record.Assurances = []evidence.Name{evidence.AssuranceFreshness}
	context, err := evidence.AuthenticatedContext(record)
	if err != nil {
		t.Fatalf("AuthenticatedContext() error = %v", err)
	}
	record.Content.Envelope.ContextDigest = string(context.Digest())
	if _, err := evidence.NewAvailable(record, registry); err == nil {
		t.Fatal("NewAvailable(unsupported assurance) error = nil")
	}
}

func TestNewAvailableRejectsWrongAuthenticatedContext(t *testing.T) {
	t.Parallel()

	record, registry := validAssetRecord(t)
	record.RequirementKey = "different_requirement"
	if _, err := evidence.NewAvailable(record, registry); err == nil {
		t.Fatal("NewAvailable(wrong context) error = nil")
	}
}

func TestQuarantineDeniesReadsAndChecksVersion(t *testing.T) {
	t.Parallel()

	record, registry := validAssetRecord(t)
	asset, err := evidence.NewAvailable(record, registry)
	if err != nil {
		t.Fatalf("NewAvailable() error = %v", err)
	}
	if _, err := asset.Quarantine(2, "idenqa.quarantine.policy", record.CreatedAt.Add(time.Minute)); !errors.Is(err, evidence.ErrVersionConflict) {
		t.Fatalf("Quarantine(stale) error = %v, want ErrVersionConflict", err)
	}
	quarantined, err := asset.FailIntegrity(1, "idenqa.quarantine.integrity_mismatch", record.CreatedAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("FailIntegrity() error = %v", err)
	}
	if quarantined.CanRead() || quarantined.State() != evidence.StateQuarantined ||
		quarantined.Record().Integrity != evidence.IntegrityFailed {
		t.Fatalf("quarantined state = %q, integrity = %q, readable = %t", quarantined.State(),
			quarantined.Record().Integrity, quarantined.CanRead())
	}
}

func TestAuthenticatedContextChangesAcrossTenant(t *testing.T) {
	t.Parallel()

	record, _ := validAssetRecord(t)
	first, err := evidence.AuthenticatedContext(record)
	if err != nil {
		t.Fatalf("AuthenticatedContext() error = %v", err)
	}
	otherTenant, err := id.ParseTenant("ten_01K3P4NQF00000000000000000")
	if err != nil {
		t.Fatalf("ParseTenant() error = %v", err)
	}
	record.TenantID = otherTenant
	second, err := evidence.AuthenticatedContext(record)
	if err != nil {
		t.Fatalf("AuthenticatedContext(other tenant) error = %v", err)
	}
	if first.Digest() == second.Digest() {
		t.Fatal("authenticated context digest did not bind tenant")
	}
}

func validAssetRecord(t *testing.T) (evidence.Record, evidence.Registry) {
	t.Helper()

	now := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	generator, err := id.NewGenerator(assetClock{now: now}, bytes.NewReader(bytes.Repeat([]byte{7}, 256)))
	if err != nil {
		t.Fatalf("NewGenerator() error = %v", err)
	}
	tenantID, err := generator.NewTenant()
	if err != nil {
		t.Fatalf("NewTenant() error = %v", err)
	}
	verificationID, err := generator.NewVerification()
	if err != nil {
		t.Fatalf("NewVerification() error = %v", err)
	}
	evidenceID, err := generator.NewEvidence()
	if err != nil {
		t.Fatalf("NewEvidence() error = %v", err)
	}
	subjectID, err := generator.NewSubject()
	if err != nil {
		t.Fatalf("NewSubject() error = %v", err)
	}
	record := evidence.Record{
		ID: evidenceID, TenantID: tenantID, SubjectID: subjectID, VerificationID: verificationID,
		RequirementKey: "selfie", EvidenceType: evidence.EvidenceSelfieImage,
		Artefact: evidence.ArtefactSelfieImage, AcquisitionMethod: evidence.MethodLiveCamera,
		Assurances: []evidence.Name{evidence.AssuranceLiveCapture, evidence.AssuranceFreshness, evidence.AssuranceFreshness},
		Region:     "idenqa.region.local", RetentionClass: "idenqa.retention.standard",
		ContentRevision: 1, CreatedAt: now,
	}
	registry, err := evidence.BuiltInRegistry()
	if err != nil {
		t.Fatalf("BuiltInRegistry() error = %v", err)
	}
	record.Registry = registry.Reference()
	context, err := evidence.AuthenticatedContext(record)
	if err != nil {
		t.Fatalf("AuthenticatedContext() error = %v", err)
	}
	record.Content = evidence.ContentRecord{
		Object: objectstore.ObjectRecord{
			Key: "opaque/tenant/evidence/content", Version: "version-1", Size: 10,
			Checksum: string(platformcrypto.Sum([]byte("ciphertext"))),
		},
		Envelope: platformcrypto.EnvelopeRecord{
			FormatVersion: 1, ContentAlgorithm: "AES256_GCM_HKDF_1MB", Purpose: "idenqa.evidence.content",
			WrappedKey: kms.WrappedKeyRecord{
				Provider: "local.file", Reference: "keyring://evidence", Version: "v1",
				Algorithm: "AES256_GCM", Ciphertext: []byte("wrapped-keyset"),
			},
			ContextSchemaVersion: context.SchemaVersion(), ContextDigest: string(context.Digest()),
		},
		PlaintextDigest: string(platformcrypto.Sum([]byte("plaintext"))),
		MediaType:       "image/jpeg",
	}
	return record, registry
}
