package evidence_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/evidence"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

func TestNewUploadBindsCanonicalRequirementAndPolicy(t *testing.T) {
	t.Parallel()

	fixture := newUploadFixture(t)
	upload, err := evidence.NewUpload(fixture.input, fixture.registry, evidence.DefaultUploadPolicy())
	if err != nil {
		t.Fatalf("NewUpload() error = %v", err)
	}
	record := upload.Record()
	if upload.State() != evidence.UploadStateIssued || upload.Version() != 1 || upload.Attempt() != 0 {
		t.Fatalf("upload state=%q version=%d attempt=%d", upload.State(), upload.Version(), upload.Attempt())
	}
	if record.ExpiresAt != fixture.input.CreatedAt.Add(evidence.DefaultUploadIntentLifetime) ||
		record.AttemptTimeout != evidence.DefaultUploadAttemptTimeout ||
		record.EncryptionPurpose != evidence.ContentEncryptionPurpose ||
		record.EvidenceID != fixture.asset.ID() || record.ExpectedDigest != fixture.asset.Record().Content.PlaintextDigest {
		t.Fatalf("upload binding = %+v", record)
	}
	if len(record.Assurances) != 2 || record.Assurances[0] != evidence.AssuranceFreshness ||
		record.Assurances[1] != evidence.AssuranceLiveCapture {
		t.Fatalf("canonical assurances = %v", record.Assurances)
	}
	fixture.input.Assurances[0] = evidence.AssuranceActiveLiveness
	fixture.input.AllowedMediaTypes[0] = "changed"
	if upload.Record().Assurances[0] != evidence.AssuranceFreshness ||
		upload.Record().AllowedMediaTypes[0] != evidence.MediaTypeJPEG {
		t.Fatal("upload retained caller-owned slices")
	}
}

func TestNewUploadRejectsPolicyExpansionAndUnsupportedEvidence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*evidence.UploadInput)
	}{
		{
			name: "body exceeds tenant maximum",
			mutate: func(input *evidence.UploadInput) {
				input.MaximumBytes = input.ExpectedBytes - 1
			},
		},
		{
			name: "tenant media does not include body",
			mutate: func(input *evidence.UploadInput) {
				input.AllowedMediaTypes = []string{evidence.MediaTypePNG}
			},
		},
		{
			name: "format outside v1 allow list",
			mutate: func(input *evidence.UploadInput) {
				input.MediaType = "image/webp"
				input.AllowedMediaTypes = []string{"image/webp"}
			},
		},
		{
			name: "tenant list expands deployment formats",
			mutate: func(input *evidence.UploadInput) {
				input.AllowedMediaTypes = []string{evidence.MediaTypeJPEG, evidence.MediaTypePNG}
			},
		},
		{
			name: "file upload claims live assurance",
			mutate: func(input *evidence.UploadInput) {
				input.AcquisitionMethod = evidence.MethodFileUpload
			},
		},
		{
			name: "intent exceeds session",
			mutate: func(input *evidence.UploadInput) {
				input.SessionExpiresAt = input.CreatedAt
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newUploadFixture(t)
			test.mutate(&fixture.input)
			policy := evidence.DefaultUploadPolicy()
			if test.name == "tenant list expands deployment formats" {
				var err error
				policy, err = evidence.NewUploadPolicy(evidence.UploadPolicyConfig{
					MaximumBytes:      evidence.DefaultUploadMaximumBytes,
					IntentLifetime:    evidence.DefaultUploadIntentLifetime,
					AttemptTimeout:    evidence.DefaultUploadAttemptTimeout,
					AllowedMediaTypes: []string{evidence.MediaTypeJPEG},
				})
				if err != nil {
					t.Fatalf("NewUploadPolicy() error = %v", err)
				}
			}
			if _, err := evidence.NewUpload(fixture.input, fixture.registry, policy); err == nil {
				t.Fatal("NewUpload() error = nil")
			}
		})
	}
}

func TestNewUploadPolicyEnforcesSelectedBounds(t *testing.T) {
	t.Parallel()

	policy, err := evidence.NewUploadPolicy(evidence.UploadPolicyConfig{
		MaximumBytes: 8 << 20, IntentLifetime: 20 * time.Minute,
		AttemptTimeout: 5 * time.Minute, AllowedMediaTypes: []string{evidence.MediaTypeJPEG},
	})
	if err != nil {
		t.Fatalf("NewUploadPolicy() error = %v", err)
	}
	fixture := newUploadFixture(t)
	fixture.input.MaximumBytes = 8 << 20
	fixture.input.AllowedMediaTypes = []string{evidence.MediaTypeJPEG}
	upload, err := evidence.NewUpload(fixture.input, fixture.registry, policy)
	if err != nil {
		t.Fatalf("NewUpload(custom policy) error = %v", err)
	}
	if upload.Record().ExpiresAt != fixture.input.CreatedAt.Add(20*time.Minute) ||
		upload.Record().AttemptTimeout != 5*time.Minute {
		t.Fatalf("custom upload policy = %+v", upload.Record())
	}
	if _, err := evidence.NewUploadPolicy(evidence.UploadPolicyConfig{
		MaximumBytes:      evidence.MaximumUploadMaximumBytes + 1,
		IntentLifetime:    evidence.DefaultUploadIntentLifetime,
		AttemptTimeout:    evidence.DefaultUploadAttemptTimeout,
		AllowedMediaTypes: []string{evidence.MediaTypeJPEG},
	}); err == nil {
		t.Fatal("NewUploadPolicy(oversized) error = nil")
	}
	if _, err := evidence.NewUploadPolicy(evidence.UploadPolicyConfig{
		MaximumBytes:      evidence.DefaultUploadMaximumBytes,
		IntentLifetime:    evidence.DefaultUploadIntentLifetime + time.Nanosecond,
		AttemptTimeout:    evidence.DefaultUploadAttemptTimeout,
		AllowedMediaTypes: []string{evidence.MediaTypeJPEG},
	}); err == nil {
		t.Fatal("NewUploadPolicy(sub-millisecond lifetime) error = nil")
	}
}

func TestUploadPolicyAccessorsReturnDeploymentLimitsDefensively(t *testing.T) {
	t.Parallel()

	policy := evidence.DefaultUploadPolicy()
	mediaTypes := policy.AllowedMediaTypes()
	mediaTypes[0] = "changed"
	if policy.MaximumBytes() != evidence.DefaultUploadMaximumBytes ||
		policy.IntentLifetime() != evidence.DefaultUploadIntentLifetime ||
		policy.AttemptTimeout() != evidence.DefaultUploadAttemptTimeout ||
		policy.AllowedMediaTypes()[0] != evidence.MediaTypeJPEG {
		t.Fatalf(
			"upload policy accessors exposed invalid state: %d %s %s %v",
			policy.MaximumBytes(),
			policy.IntentLifetime(),
			policy.AttemptTimeout(),
			policy.AllowedMediaTypes(),
		)
	}
}

func TestUploadClaimAttemptFencesExpiredLease(t *testing.T) {
	t.Parallel()

	fixture := newUploadFixture(t)
	upload := fixture.upload(t)
	first, err := upload.ClaimAttempt(upload.Version(), fixture.now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ClaimAttempt(first) error = %v", err)
	}
	if first.State() != evidence.UploadStateUploading || first.Attempt() != 1 || first.Version() != 2 {
		t.Fatalf("first attempt state=%q attempt=%d version=%d", first.State(), first.Attempt(), first.Version())
	}
	if _, err := first.ClaimAttempt(first.Version(), fixture.now.Add(2*time.Minute)); !errors.Is(err, evidence.ErrUploadConflict) {
		t.Fatalf("ClaimAttempt(active lease) error = %v", err)
	}
	second, err := first.ClaimAttempt(first.Version(), *first.Record().LeaseExpiresAt)
	if err != nil {
		t.Fatalf("ClaimAttempt(after lease) error = %v", err)
	}
	if second.Attempt() != 2 || second.Version() != 3 {
		t.Fatalf("second attempt=%d version=%d", second.Attempt(), second.Version())
	}
	if _, err := second.FailAttempt(first.Version(), first.Attempt(), fixture.now.Add(12*time.Minute)); !errors.Is(err, evidence.ErrUploadVersionConflict) {
		t.Fatalf("FailAttempt(stale fence) error = %v", err)
	}
}

func TestUploadFailAttemptPermitsWholeBodyRetryAndExpiresAtBoundary(t *testing.T) {
	t.Parallel()

	fixture := newUploadFixture(t)
	upload := fixture.upload(t)
	claimed, err := upload.ClaimAttempt(upload.Version(), fixture.now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ClaimAttempt() error = %v", err)
	}
	retryable, err := claimed.FailAttempt(claimed.Version(), claimed.Attempt(), fixture.now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("FailAttempt(retryable) error = %v", err)
	}
	if retryable.State() != evidence.UploadStateIssued || retryable.Attempt() != 1 {
		t.Fatalf("retryable state=%q attempt=%d", retryable.State(), retryable.Attempt())
	}
	reclaimed, err := retryable.ClaimAttempt(retryable.Version(), fixture.now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("ClaimAttempt(retry) error = %v", err)
	}
	expired, err := reclaimed.FailAttempt(
		reclaimed.Version(), reclaimed.Attempt(), reclaimed.Record().ExpiresAt,
	)
	if err != nil {
		t.Fatalf("FailAttempt(expired) error = %v", err)
	}
	if expired.State() != evidence.UploadStateExpired || expired.Record().LeaseExpiresAt != nil {
		t.Fatalf("expired upload = %+v", expired.Record())
	}
}

func TestUploadAcceptsOnlyExactProtectedEvidenceFromCurrentAttempt(t *testing.T) {
	t.Parallel()

	fixture := newUploadFixture(t)
	upload := fixture.upload(t)
	claimed, err := upload.ClaimAttempt(upload.Version(), fixture.now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ClaimAttempt() error = %v", err)
	}
	if _, err := claimed.Accept(
		claimed.Version(), claimed.Attempt(), fixture.asset,
		fixture.input.ExpectedBytes+1, fixture.now.Add(2*time.Minute),
	); !errors.Is(err, evidence.ErrUploadConflict) {
		t.Fatalf("Accept(wrong bytes) error = %v", err)
	}
	accepted, err := claimed.Accept(
		claimed.Version(), claimed.Attempt(), fixture.asset,
		fixture.input.ExpectedBytes, fixture.now.Add(2*time.Minute),
	)
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if accepted.State() != evidence.UploadStateAccepted || accepted.Record().AcceptedAt == nil ||
		accepted.Record().LeaseExpiresAt != nil || accepted.Version() != claimed.Version()+1 {
		t.Fatalf("accepted upload = %+v", accepted.Record())
	}
	if _, err := accepted.Accept(
		accepted.Version(), accepted.Attempt(), fixture.asset,
		fixture.input.ExpectedBytes, fixture.now.Add(3*time.Minute),
	); !errors.Is(err, evidence.ErrUploadConflict) {
		t.Fatalf("Accept(terminal replay) error = %v", err)
	}
}

func TestUploadRejectAndExpireAreTerminal(t *testing.T) {
	t.Parallel()

	fixture := newUploadFixture(t)
	upload := fixture.upload(t)
	claimed, err := upload.ClaimAttempt(upload.Version(), fixture.now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ClaimAttempt() error = %v", err)
	}
	rejected, err := claimed.Reject(
		claimed.Version(), claimed.Attempt(), "evidence.upload.signature_mismatch",
		fixture.now.Add(2*time.Minute),
	)
	if err != nil {
		t.Fatalf("Reject() error = %v", err)
	}
	if rejected.State() != evidence.UploadStateRejected ||
		rejected.Record().RejectionReason != "evidence.upload.signature_mismatch" {
		t.Fatalf("rejected upload = %+v", rejected.Record())
	}
	if _, err := rejected.ClaimAttempt(rejected.Version(), fixture.now.Add(3*time.Minute)); !errors.Is(err, evidence.ErrUploadConflict) {
		t.Fatalf("ClaimAttempt(rejected) error = %v", err)
	}

	fresh := fixture.upload(t)
	expired, err := fresh.Expire(fresh.Version(), fresh.Record().ExpiresAt)
	if err != nil {
		t.Fatalf("Expire() error = %v", err)
	}
	if expired.State() != evidence.UploadStateExpired {
		t.Fatalf("Expire() state = %q", expired.State())
	}
}

func TestUploadRejectsNonMonotonicTransitionTimes(t *testing.T) {
	t.Parallel()

	fixture := newUploadFixture(t)
	upload := fixture.upload(t)
	if _, err := upload.ClaimAttempt(upload.Version(), fixture.now.Add(-time.Second)); !errors.Is(err, evidence.ErrUploadConflict) {
		t.Fatalf("ClaimAttempt(before creation) error = %v", err)
	}
	claimed, err := upload.ClaimAttempt(upload.Version(), fixture.now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ClaimAttempt() error = %v", err)
	}
	if _, err := claimed.Accept(
		claimed.Version(), claimed.Attempt(), fixture.asset,
		fixture.input.ExpectedBytes, fixture.now,
	); !errors.Is(err, evidence.ErrUploadConflict) {
		t.Fatalf("Accept(before claim) error = %v", err)
	}
	if _, err := claimed.Reject(
		claimed.Version(), claimed.Attempt(), "evidence.upload.invalid_signature", fixture.now,
	); !errors.Is(err, evidence.ErrUploadConflict) {
		t.Fatalf("Reject(before claim) error = %v", err)
	}
}

type uploadFixture struct {
	now      time.Time
	registry evidence.Registry
	asset    evidence.Asset
	input    evidence.UploadInput
}

func newUploadFixture(t *testing.T) uploadFixture {
	t.Helper()

	record, registry := validAssetRecord(t)
	asset, err := evidence.NewAvailable(record, registry)
	if err != nil {
		t.Fatalf("NewAvailable() error = %v", err)
	}
	now := record.CreatedAt
	generator, err := id.NewGenerator(
		assetClock{now: now}, bytes.NewReader(bytes.Repeat([]byte{11}, 256)),
	)
	if err != nil {
		t.Fatalf("NewGenerator() error = %v", err)
	}
	uploadID, _ := generator.NewUpload()
	captureTokenID, _ := generator.NewCaptureToken()
	authorityID, _ := generator.NewAuthority()
	responseID, _ := generator.NewAcknowledgement()
	profileID, _ := generator.NewProfile()
	assetRecord := asset.Record()

	return uploadFixture{
		now: now, registry: registry, asset: asset,
		input: evidence.UploadInput{
			ID: uploadID, TenantID: assetRecord.TenantID, CaptureTokenID: captureTokenID,
			SubjectID: assetRecord.SubjectID, VerificationID: assetRecord.VerificationID,
			EvidenceID: assetRecord.ID, AuthorityID: authorityID, ResponseID: responseID,
			ProfileID: profileID, ProfileRevision: 1,
			ProfileDigest:  string(platformcrypto.Sum([]byte("profile snapshot"))),
			RequirementKey: assetRecord.RequirementKey, Purpose: evidence.PurposeIdentityVerification,
			EvidenceType: assetRecord.EvidenceType, Artefact: assetRecord.Artefact,
			AcquisitionMethod: assetRecord.AcquisitionMethod,
			Assurances:        []evidence.Name{evidence.AssuranceLiveCapture, evidence.AssuranceFreshness},
			AllowedMediaTypes: []string{evidence.MediaTypePNG, evidence.MediaTypeJPEG},
			MaximumBytes:      evidence.DefaultUploadMaximumBytes,
			ExpectedBytes:     int64(len("plaintext")), ExpectedDigest: assetRecord.Content.PlaintextDigest,
			MediaType: assetRecord.Content.MediaType, Region: assetRecord.Region,
			RetentionClass: assetRecord.RetentionClass, CreatedAt: now,
			SessionExpiresAt: now.Add(time.Hour),
		},
	}
}

func (fixture uploadFixture) upload(t *testing.T) evidence.Upload {
	t.Helper()
	upload, err := evidence.NewUpload(fixture.input, fixture.registry, evidence.DefaultUploadPolicy())
	if err != nil {
		t.Fatalf("NewUpload() error = %v", err)
	}

	return upload
}
