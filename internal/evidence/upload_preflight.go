package evidence

import (
	"context"
	"crypto/subtle"
	"errors"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/clock"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// UploadPrincipal is the authenticated, transport-independent capture identity
// permitted to begin one evidence-upload attempt.
type UploadPrincipal struct {
	Scope          tenant.Scope
	CaptureTokenID id.CaptureToken
	VerificationID id.Verification
}

// UploadMetadata is the body-free metadata bound to one whole-body attempt.
type UploadMetadata struct {
	ExpectedVersion int64
	ContentLength   int64
	MediaType       string
	Digest          platformcrypto.Digest
}

// UploadPreflightRepository loads an upload and atomically claims its next
// whole-body attempt. It is owned by this consuming application boundary.
type UploadPreflightRepository interface {
	FindUpload(context.Context, tenant.Scope, id.Upload) (Upload, error)
	ClaimUploadAttempt(
		context.Context,
		tenant.Scope,
		id.CaptureToken,
		id.Upload,
		int64,
		time.Time,
	) (Upload, error)
}

// UploadPreflight authenticates an intent binding and fences the attempt before
// any evidence bytes are accepted from the request body.
type UploadPreflight struct {
	repository UploadPreflightRepository
	clock      clock.Clock
}

// NewUploadPreflight constructs the body-free evidence-ingress boundary.
func NewUploadPreflight(
	repository UploadPreflightRepository,
	source clock.Clock,
) (*UploadPreflight, error) {
	if repository == nil || source == nil {
		return nil, errors.New("evidence: upload preflight dependencies are required")
	}

	return &UploadPreflight{repository: repository, clock: source}, nil
}

// Begin validates the exact authenticated binding and immutable body metadata,
// then durably claims a fenced complete-body attempt.
func (preflight *UploadPreflight) Begin(
	ctx context.Context,
	principal UploadPrincipal,
	uploadID id.Upload,
	metadata UploadMetadata,
) (Upload, error) {
	upload, err := preflight.Find(ctx, principal, uploadID)
	if err != nil {
		return Upload{}, err
	}
	record := upload.Record()
	if metadata.ContentLength != record.ExpectedBytes || metadata.MediaType != record.MediaType ||
		subtle.ConstantTimeCompare([]byte(metadata.Digest), []byte(record.ExpectedDigest)) != 1 {
		return Upload{}, ErrUploadMetadata
	}
	if record.State == UploadStateAccepted {
		// A client may retry after losing the successful response. Accept either
		// the precondition used by the accepted attempt or the returned terminal
		// version, but never read or persist the repeated body.
		if metadata.ExpectedVersion == upload.Version() ||
			(metadata.ExpectedVersion > 0 && metadata.ExpectedVersion == upload.Version()-2) {
			return upload, nil
		}

		return Upload{}, ErrUploadVersionConflict
	}
	if record.State == UploadStateUploading && metadata.ExpectedVersion == upload.Version()-1 {
		return Upload{}, ErrUploadConflict
	}
	if metadata.ExpectedVersion != upload.Version() {
		return Upload{}, ErrUploadVersionConflict
	}

	return preflight.repository.ClaimUploadAttempt(
		ctx,
		principal.Scope,
		principal.CaptureTokenID,
		uploadID,
		metadata.ExpectedVersion,
		preflight.clock.Now().UTC().Truncate(time.Second),
	)
}

// Find returns a safe upload snapshot only when it is bound to the exact
// authenticated capture principal. Binding mismatches are hidden as not found.
func (preflight *UploadPreflight) Find(
	ctx context.Context,
	principal UploadPrincipal,
	uploadID id.Upload,
) (Upload, error) {
	if preflight == nil {
		return Upload{}, errors.New("evidence: upload preflight is not initialised")
	}
	if principal.Scope.ID().IsZero() || principal.CaptureTokenID.IsZero() ||
		principal.VerificationID.IsZero() || uploadID.IsZero() {
		return Upload{}, ErrUploadNotFound
	}
	upload, err := preflight.repository.FindUpload(ctx, principal.Scope, uploadID)
	if err != nil {
		return Upload{}, err
	}
	record := upload.Record()
	if record.TenantID != principal.Scope.ID() ||
		record.CaptureTokenID != principal.CaptureTokenID ||
		record.VerificationID != principal.VerificationID {
		return Upload{}, ErrUploadNotFound
	}

	return upload, nil
}
