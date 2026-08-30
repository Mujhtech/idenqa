package evidence

import (
	"context"
	"errors"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

const maxCaptureCompletions = 256

// CaptureCompletion is the minimum accepted binding needed to recover one
// completed capture step. Evidence content and integrity metadata are absent.
type CaptureCompletion struct {
	UploadID          id.Upload
	EvidenceID        id.Evidence
	RequirementKey    string
	EvidenceType      Name
	Artefact          Name
	AcquisitionMethod Name
	FallbackCondition string
}

// CaptureProgress is an authoritative capture-token-scoped completion snapshot.
type CaptureProgress struct {
	VerificationID id.Verification
	Completions    []CaptureCompletion
}

// AcceptedUploadLister is the durable projection consumed by capture recovery.
type AcceptedUploadLister interface {
	ListAcceptedUploads(
		context.Context,
		tenant.Scope,
		id.CaptureToken,
		id.Verification,
		int32,
	) ([]Upload, error)
}

// ProgressReader returns only accepted uploads for an exact authenticated
// capture principal.
type ProgressReader struct{ uploads AcceptedUploadLister }

// NewProgressReader constructs the capture progress read boundary.
func NewProgressReader(uploads AcceptedUploadLister) (*ProgressReader, error) {
	if uploads == nil {
		return nil, errors.New("evidence: capture progress repository is required")
	}

	return &ProgressReader{uploads: uploads}, nil
}

// Find returns an ordered, bounded completion snapshot for one capture token.
func (reader *ProgressReader) Find(
	ctx context.Context,
	principal UploadPrincipal,
) (CaptureProgress, error) {
	if reader == nil || principal.Scope.ID().IsZero() || principal.CaptureTokenID.IsZero() ||
		principal.VerificationID.IsZero() {
		return CaptureProgress{}, ErrUploadNotFound
	}
	uploads, err := reader.uploads.ListAcceptedUploads(
		ctx,
		principal.Scope,
		principal.CaptureTokenID,
		principal.VerificationID,
		maxCaptureCompletions+1,
	)
	if err != nil {
		return CaptureProgress{}, err
	}
	if len(uploads) > maxCaptureCompletions {
		return CaptureProgress{}, errors.New("evidence: capture progress exceeds completion limit")
	}
	completions := make([]CaptureCompletion, len(uploads))
	seenUploads := make(map[id.Upload]struct{}, len(uploads))
	seenEvidence := make(map[id.Evidence]struct{}, len(uploads))
	for index, upload := range uploads {
		record := upload.Record()
		if record.State != UploadStateAccepted || record.TenantID != principal.Scope.ID() ||
			record.CaptureTokenID != principal.CaptureTokenID ||
			record.VerificationID != principal.VerificationID {
			return CaptureProgress{}, errors.New("evidence: capture progress binding is invalid")
		}
		if _, exists := seenUploads[record.ID]; exists {
			return CaptureProgress{}, errors.New("evidence: capture progress repeats an upload")
		}
		if _, exists := seenEvidence[record.EvidenceID]; exists {
			return CaptureProgress{}, errors.New("evidence: capture progress repeats evidence")
		}
		seenUploads[record.ID] = struct{}{}
		seenEvidence[record.EvidenceID] = struct{}{}
		completions[index] = CaptureCompletion{
			UploadID: record.ID, EvidenceID: record.EvidenceID,
			RequirementKey: record.RequirementKey, EvidenceType: record.EvidenceType,
			Artefact: record.Artefact, AcquisitionMethod: record.AcquisitionMethod,
			FallbackCondition: record.FallbackCondition,
		}
	}

	return CaptureProgress{VerificationID: principal.VerificationID, Completions: completions}, nil
}
