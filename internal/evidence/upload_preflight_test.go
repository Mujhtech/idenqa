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
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestNewUploadPreflightRequiresDependencies(t *testing.T) {
	t.Parallel()

	if _, err := evidence.NewUploadPreflight(nil, assetClock{}); err == nil {
		t.Fatal("NewUploadPreflight(nil repository) error = nil")
	}
	if _, err := evidence.NewUploadPreflight(&uploadPreflightRepository{}, nil); err == nil {
		t.Fatal("NewUploadPreflight(nil clock) error = nil")
	}
}

func TestUploadPreflightBeginClaimsExactBinding(t *testing.T) {
	t.Parallel()

	fixture := newUploadFixture(t)
	upload := fixture.upload(t)
	scope, err := tenant.NewScope(fixture.input.TenantID)
	if err != nil {
		t.Fatalf("NewScope() error = %v", err)
	}
	repository := &uploadPreflightRepository{upload: upload}
	preflight, err := evidence.NewUploadPreflight(repository, assetClock{now: fixture.now.Add(time.Minute)})
	if err != nil {
		t.Fatalf("NewUploadPreflight() error = %v", err)
	}

	claimed, err := preflight.Begin(t.Context(), evidence.UploadPrincipal{
		Scope: scope, CaptureTokenID: fixture.input.CaptureTokenID,
		VerificationID: fixture.input.VerificationID,
	}, upload.ID(), uploadMetadata(upload))
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if claimed.State() != evidence.UploadStateUploading || claimed.Version() != 2 || claimed.Attempt() != 1 {
		t.Fatalf("claimed state=%q version=%d attempt=%d", claimed.State(), claimed.Version(), claimed.Attempt())
	}
	if repository.claims != 1 || repository.captureTokenID != fixture.input.CaptureTokenID ||
		repository.expectedVersion != upload.Version() {
		t.Fatalf("claim calls=%d token=%q version=%d", repository.claims, repository.captureTokenID, repository.expectedVersion)
	}
}

func TestUploadPreflightBeginClaimsIntentCreatedEarlierInCurrentSecond(t *testing.T) {
	t.Parallel()

	fixture := newUploadFixture(t)
	fixture.input.CreatedAt = fixture.now.Add(100 * time.Microsecond)
	upload := fixture.upload(t)
	scope, err := tenant.NewScope(fixture.input.TenantID)
	if err != nil {
		t.Fatalf("NewScope() error = %v", err)
	}
	repository := &uploadPreflightRepository{upload: upload}
	preflight, err := evidence.NewUploadPreflight(
		repository,
		assetClock{now: fixture.now.Add(900 * time.Microsecond)},
	)
	if err != nil {
		t.Fatalf("NewUploadPreflight() error = %v", err)
	}

	if _, err := preflight.Begin(t.Context(), evidence.UploadPrincipal{
		Scope: scope, CaptureTokenID: fixture.input.CaptureTokenID,
		VerificationID: fixture.input.VerificationID,
	}, upload.ID(), uploadMetadata(upload)); err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
}

func TestUploadPreflightFindRequiresExactCaptureBinding(t *testing.T) {
	t.Parallel()

	fixture := newUploadFixture(t)
	upload := fixture.upload(t)
	scope, err := tenant.NewScope(fixture.input.TenantID)
	if err != nil {
		t.Fatalf("NewScope() error = %v", err)
	}
	principal := evidence.UploadPrincipal{
		Scope: scope, CaptureTokenID: fixture.input.CaptureTokenID,
		VerificationID: fixture.input.VerificationID,
	}
	repository := &uploadPreflightRepository{upload: upload}
	preflight, err := evidence.NewUploadPreflight(repository, assetClock{now: fixture.now})
	if err != nil {
		t.Fatalf("NewUploadPreflight() error = %v", err)
	}

	found, err := preflight.Find(t.Context(), principal, upload.ID())
	if err != nil || found.ID() != upload.ID() {
		t.Fatalf("Find() upload=%q error=%v", found.ID(), err)
	}

	wrong := principal
	wrong.VerificationID, _ = id.ParseVerification("ver_01ARZ3NDEKTSV4RRFFQ69G5FB9")
	if _, err := preflight.Find(t.Context(), wrong, upload.ID()); !errors.Is(err, evidence.ErrUploadNotFound) {
		t.Fatalf("Find(mismatched principal) error = %v, want ErrUploadNotFound", err)
	}
}

func TestUploadPreflightBeginReplaysAcceptedUploadWithoutClaim(t *testing.T) {
	t.Parallel()

	fixture := newUploadFixture(t)
	upload := fixture.upload(t)
	claimed, err := upload.ClaimAttempt(upload.Version(), fixture.now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ClaimAttempt() error = %v", err)
	}
	accepted, err := claimed.Accept(
		claimed.Version(), claimed.Attempt(), fixture.asset, fixture.input.ExpectedBytes,
		fixture.now.Add(2*time.Minute),
	)
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	scope, err := tenant.NewScope(fixture.input.TenantID)
	if err != nil {
		t.Fatalf("NewScope() error = %v", err)
	}
	principal := evidence.UploadPrincipal{
		Scope: scope, CaptureTokenID: fixture.input.CaptureTokenID,
		VerificationID: fixture.input.VerificationID,
	}

	for _, version := range []int64{upload.Version(), accepted.Version()} {
		repository := &uploadPreflightRepository{upload: accepted}
		preflight, err := evidence.NewUploadPreflight(repository, assetClock{now: fixture.now.Add(3 * time.Minute)})
		if err != nil {
			t.Fatalf("NewUploadPreflight() error = %v", err)
		}
		metadata := uploadMetadata(accepted)
		metadata.ExpectedVersion = version
		replayed, err := preflight.Begin(t.Context(), principal, accepted.ID(), metadata)
		if err != nil {
			t.Fatalf("Begin(version=%d) error = %v", version, err)
		}
		if replayed.State() != evidence.UploadStateAccepted || repository.claims != 0 {
			t.Fatalf("replayed state=%q claims=%d", replayed.State(), repository.claims)
		}
	}
}

func TestUploadPreflightBeginReportsConcurrentDuplicate(t *testing.T) {
	t.Parallel()

	fixture := newUploadFixture(t)
	upload := fixture.upload(t)
	claimed, err := upload.ClaimAttempt(upload.Version(), fixture.now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ClaimAttempt() error = %v", err)
	}
	scope, err := tenant.NewScope(fixture.input.TenantID)
	if err != nil {
		t.Fatalf("NewScope() error = %v", err)
	}
	metadata := uploadMetadata(claimed)
	metadata.ExpectedVersion = upload.Version()
	repository := &uploadPreflightRepository{upload: claimed}
	preflight, err := evidence.NewUploadPreflight(repository, assetClock{now: fixture.now.Add(2 * time.Minute)})
	if err != nil {
		t.Fatalf("NewUploadPreflight() error = %v", err)
	}

	_, err = preflight.Begin(t.Context(), evidence.UploadPrincipal{
		Scope: scope, CaptureTokenID: fixture.input.CaptureTokenID,
		VerificationID: fixture.input.VerificationID,
	}, claimed.ID(), metadata)
	if !errors.Is(err, evidence.ErrUploadConflict) || repository.claims != 0 {
		t.Fatalf("Begin() error=%v claims=%d", err, repository.claims)
	}
}

func TestUploadPreflightBeginRejectsBeforeClaim(t *testing.T) {
	t.Parallel()

	fixture := newUploadFixture(t)
	upload := fixture.upload(t)
	scope, err := tenant.NewScope(fixture.input.TenantID)
	if err != nil {
		t.Fatalf("NewScope() error = %v", err)
	}
	otherToken, otherVerification := otherUploadPrincipalIDs(t, fixture.now)

	tests := []struct {
		name      string
		principal evidence.UploadPrincipal
		metadata  evidence.UploadMetadata
		wantError error
	}{
		{
			name: "wrong capture token is hidden",
			principal: evidence.UploadPrincipal{
				Scope: scope, CaptureTokenID: otherToken, VerificationID: fixture.input.VerificationID,
			},
			metadata: uploadMetadata(upload), wantError: evidence.ErrUploadNotFound,
		},
		{
			name: "wrong verification is hidden",
			principal: evidence.UploadPrincipal{
				Scope: scope, CaptureTokenID: fixture.input.CaptureTokenID, VerificationID: otherVerification,
			},
			metadata: uploadMetadata(upload), wantError: evidence.ErrUploadNotFound,
		},
		{
			name: "stale version",
			principal: evidence.UploadPrincipal{
				Scope: scope, CaptureTokenID: fixture.input.CaptureTokenID,
				VerificationID: fixture.input.VerificationID,
			},
			metadata: func() evidence.UploadMetadata {
				value := uploadMetadata(upload)
				value.ExpectedVersion++
				return value
			}(),
			wantError: evidence.ErrUploadVersionConflict,
		},
		{
			name: "length mismatch",
			principal: evidence.UploadPrincipal{
				Scope: scope, CaptureTokenID: fixture.input.CaptureTokenID,
				VerificationID: fixture.input.VerificationID,
			},
			metadata: func() evidence.UploadMetadata {
				value := uploadMetadata(upload)
				value.ContentLength++
				return value
			}(),
			wantError: evidence.ErrUploadMetadata,
		},
		{
			name: "media mismatch",
			principal: evidence.UploadPrincipal{
				Scope: scope, CaptureTokenID: fixture.input.CaptureTokenID,
				VerificationID: fixture.input.VerificationID,
			},
			metadata: func() evidence.UploadMetadata {
				value := uploadMetadata(upload)
				value.MediaType = evidence.MediaTypePNG
				return value
			}(),
			wantError: evidence.ErrUploadMetadata,
		},
		{
			name: "digest mismatch",
			principal: evidence.UploadPrincipal{
				Scope: scope, CaptureTokenID: fixture.input.CaptureTokenID,
				VerificationID: fixture.input.VerificationID,
			},
			metadata: func() evidence.UploadMetadata {
				value := uploadMetadata(upload)
				value.Digest = ""
				return value
			}(),
			wantError: evidence.ErrUploadMetadata,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &uploadPreflightRepository{upload: upload}
			preflight, err := evidence.NewUploadPreflight(repository, assetClock{now: fixture.now.Add(time.Minute)})
			if err != nil {
				t.Fatalf("NewUploadPreflight() error = %v", err)
			}
			if _, err := preflight.Begin(t.Context(), test.principal, upload.ID(), test.metadata); !errors.Is(err, test.wantError) {
				t.Fatalf("Begin() error = %v, want %v", err, test.wantError)
			}
			if repository.claims != 0 {
				t.Fatalf("ClaimUploadAttempt() calls = %d, want 0", repository.claims)
			}
		})
	}
}

func TestUploadPreflightBeginPropagatesRepositoryFailures(t *testing.T) {
	t.Parallel()

	fixture := newUploadFixture(t)
	upload := fixture.upload(t)
	scope, err := tenant.NewScope(fixture.input.TenantID)
	if err != nil {
		t.Fatalf("NewScope() error = %v", err)
	}
	principal := evidence.UploadPrincipal{
		Scope: scope, CaptureTokenID: fixture.input.CaptureTokenID,
		VerificationID: fixture.input.VerificationID,
	}
	want := errors.New("repository failure")

	for _, test := range []struct {
		name       string
		repository *uploadPreflightRepository
	}{
		{name: "find", repository: &uploadPreflightRepository{findErr: want}},
		{name: "claim", repository: &uploadPreflightRepository{upload: upload, claimErr: want}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			preflight, err := evidence.NewUploadPreflight(test.repository, assetClock{now: fixture.now.Add(time.Minute)})
			if err != nil {
				t.Fatalf("NewUploadPreflight() error = %v", err)
			}
			if _, err := preflight.Begin(t.Context(), principal, upload.ID(), uploadMetadata(upload)); !errors.Is(err, want) {
				t.Fatalf("Begin() error = %v, want %v", err, want)
			}
		})
	}
}

type uploadPreflightRepository struct {
	upload          evidence.Upload
	findErr         error
	claimErr        error
	claims          int
	captureTokenID  id.CaptureToken
	expectedVersion int64
}

func (repository *uploadPreflightRepository) FindUpload(
	context.Context,
	tenant.Scope,
	id.Upload,
) (evidence.Upload, error) {
	return repository.upload, repository.findErr
}

func (repository *uploadPreflightRepository) ClaimUploadAttempt(
	_ context.Context,
	_ tenant.Scope,
	captureTokenID id.CaptureToken,
	_ id.Upload,
	expectedVersion int64,
	now time.Time,
) (evidence.Upload, error) {
	repository.claims++
	repository.captureTokenID = captureTokenID
	repository.expectedVersion = expectedVersion
	if repository.claimErr != nil {
		return evidence.Upload{}, repository.claimErr
	}

	return repository.upload.ClaimAttempt(expectedVersion, now)
}

func uploadMetadata(upload evidence.Upload) evidence.UploadMetadata {
	record := upload.Record()

	return evidence.UploadMetadata{
		ExpectedVersion: upload.Version(), ContentLength: record.ExpectedBytes,
		MediaType: record.MediaType, Digest: platformcrypto.Digest(record.ExpectedDigest),
	}
}

func otherUploadPrincipalIDs(t *testing.T, now time.Time) (id.CaptureToken, id.Verification) {
	t.Helper()
	generator, err := id.NewGenerator(assetClock{now: now}, bytes.NewReader(bytes.Repeat([]byte{29}, 128)))
	if err != nil {
		t.Fatalf("NewGenerator() error = %v", err)
	}
	tokenID, err := generator.NewCaptureToken()
	if err != nil {
		t.Fatalf("NewCaptureToken() error = %v", err)
	}
	verificationID, err := generator.NewVerification()
	if err != nil {
		t.Fatalf("NewVerification() error = %v", err)
	}

	return tokenID, verificationID
}
