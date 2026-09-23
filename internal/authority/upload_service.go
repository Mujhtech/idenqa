package authority

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

// UploadAuthorityFinder resolves the current authority state at issuance time.
type UploadAuthorityFinder interface {
	CaptureSnapshot(context.Context, tenant.Scope, id.Verification) (Snapshot, error)
}

// UploadCreator persists one fully resolved upload intent and its replay result.
type UploadCreator interface {
	CreateUpload(context.Context, tenant.Scope, evidence.UploadCreateMutation) (evidence.Upload, error)
}

// UploadIDGenerator creates the two opaque identities allocated at issuance.
type UploadIDGenerator interface {
	NewUpload() (id.Upload, error)
	NewEvidence() (id.Evidence, error)
}

// UploadRequest is the untrusted, body-free input for one upload intent.
type UploadRequest struct {
	RequirementKey    string
	Artefact          evidence.Name
	AcquisitionMethod evidence.Name
	FallbackCondition verification.FallbackCondition
	ExpectedBytes     int64
	ExpectedDigest    string
	MediaType         string
	Region            string
	Sequence          *evidence.TemporalFrame
}

// UploadService resolves a capture request against immutable requirements,
// deployment limits, and current processing authority before persistence.
type UploadService struct {
	authorities UploadAuthorityFinder
	uploads     UploadCreator
	identifiers UploadIDGenerator
	clock       clock.Clock
	catalog     evidence.Catalog
	policy      evidence.UploadPolicy
	retention   time.Duration
}

// NewUploadService constructs the authority-owned upload issuance workflow.
func NewUploadService(
	authorities UploadAuthorityFinder,
	uploads UploadCreator,
	identifiers UploadIDGenerator,
	source clock.Clock,
	catalog evidence.Catalog,
	policy evidence.UploadPolicy,
	idempotencyRetention time.Duration,
) (*UploadService, error) {
	if authorities == nil || uploads == nil || identifiers == nil || source == nil ||
		catalog.IsZero() || policy.MaximumBytes() == 0 || len(policy.AllowedMediaTypes()) == 0 ||
		idempotencyRetention <= 0 {
		return nil, errors.New("authority: upload service dependencies and retention are required")
	}

	return &UploadService{
		authorities: authorities, uploads: uploads, identifiers: identifiers, clock: source,
		catalog: catalog, policy: policy, retention: idempotencyRetention,
	}, nil
}

// Issue revalidates an authenticated capture session and current processing
// authority, resolves tenant constraints, and durably issues one upload intent.
func (service *UploadService) Issue(
	ctx context.Context,
	captureContext verification.CaptureContext,
	idempotencyKey string,
	input UploadRequest,
) (evidence.Upload, error) {
	if service == nil {
		return evidence.Upload{}, errors.New("authority: upload service is not initialised")
	}
	now := service.clock.Now().UTC().Truncate(time.Microsecond)
	scope := captureContext.TenantScope()
	session := captureContext.Session()
	if scope.ID().IsZero() || captureContext.TokenID().IsZero() ||
		session.TenantID() != scope.ID() || now.Before(session.CreatedAt()) ||
		!session.AcceptsCaptureAt(now) {
		return evidence.Upload{}, ErrProcessingNotPermitted
	}

	registry, err := service.catalog.Resolve(session.Requirements().Registry)
	if err != nil {
		return evidence.Upload{}, fmt.Errorf("resolve upload registry: %w", err)
	}
	requirement, err := resolveUploadRequirement(session.Requirements(), input)
	if err != nil {
		return evidence.Upload{}, err
	}
	if !slices.Contains(verification.EffectiveArtefacts(requirement, session.DocumentSelections()), input.Artefact) {
		return evidence.Upload{}, ErrProcessingNotPermitted
	}
	if input.Sequence != nil && (input.AcquisitionMethod != evidence.MethodLiveCamera ||
		input.Sequence.CapturedAt.Before(session.CreatedAt()) || input.Sequence.CapturedAt.After(now)) {
		return evidence.Upload{}, ErrProcessingNotPermitted
	}
	mediaTypes, maximumBytes, err := service.resolveUploadConstraints(requirement)
	if err != nil {
		return evidence.Upload{}, err
	}
	assurances, err := uploadAssurances(registry, requirement, input.AcquisitionMethod)
	if err != nil {
		return evidence.Upload{}, err
	}

	snapshot, err := service.authorities.CaptureSnapshot(ctx, scope, session.ID())
	if err != nil {
		return evidence.Upload{}, err
	}
	record := snapshot.Authority.Record()
	if err := Evaluate(snapshot.Authority, snapshot.Notice, snapshot.Response, GrantRequest{
		TenantID: scope.ID(), VerificationID: session.ID(), SubjectID: record.SubjectID,
		Purpose: string(requirement.Purpose), EvidenceType: string(requirement.EvidenceType),
		RecipientReference: record.RecipientReference, Region: input.Region,
	}, now); err != nil {
		return evidence.Upload{}, err
	}
	responseRecord := snapshot.Response.Record()
	if responseRecord.CaptureTokenID != captureContext.TokenID() {
		return evidence.Upload{}, ErrSubjectResponseRequired
	}

	canonical, err := json.Marshal(struct {
		RequirementKey    string                         `json:"requirement_key"`
		Artefact          evidence.Name                  `json:"artefact"`
		AcquisitionMethod evidence.Name                  `json:"acquisition_method"`
		FallbackCondition verification.FallbackCondition `json:"fallback_condition,omitempty"`
		ExpectedBytes     int64                          `json:"expected_bytes"`
		ExpectedDigest    string                         `json:"expected_digest"`
		MediaType         string                         `json:"media_type"`
		Region            string                         `json:"region"`
		Sequence          *evidence.TemporalFrame        `json:"sequence,omitempty"`
	}{
		input.RequirementKey, input.Artefact, input.AcquisitionMethod, input.FallbackCondition,
		input.ExpectedBytes, input.ExpectedDigest, input.MediaType, input.Region, input.Sequence,
	})
	if err != nil {
		return evidence.Upload{}, fmt.Errorf("serialise upload issue command: %w", err)
	}
	retry, err := idempotency.NewRequest(
		scope.ID(), captureContext.TokenID(), evidence.OperationCreateUpload,
		idempotencyKey, canonical, now, service.retention,
	)
	if err != nil {
		return evidence.Upload{}, err
	}
	uploadID, err := service.identifiers.NewUpload()
	if err != nil {
		return evidence.Upload{}, fmt.Errorf("generate upload id: %w", err)
	}
	evidenceID, err := service.identifiers.NewEvidence()
	if err != nil {
		return evidence.Upload{}, fmt.Errorf("generate evidence id: %w", err)
	}
	upload, err := evidence.NewUpload(evidence.UploadInput{
		ID: uploadID, TenantID: scope.ID(), CaptureTokenID: captureContext.TokenID(),
		SubjectID: record.SubjectID, VerificationID: session.ID(), EvidenceID: evidenceID,
		AuthorityID: record.ID, ResponseID: responseRecord.ID,
		ProfileID: session.ProfileID(), ProfileRevision: session.ProfileRevision(),
		ProfileDigest: session.ProfileDigest(), RequirementKey: requirement.Key,
		Purpose: requirement.Purpose, EvidenceType: requirement.EvidenceType,
		Artefact: input.Artefact, AcquisitionMethod: input.AcquisitionMethod,
		FallbackCondition: string(input.FallbackCondition),
		Assurances:        assurances, AllowedMediaTypes: mediaTypes, MaximumBytes: maximumBytes,
		ExpectedBytes: input.ExpectedBytes, ExpectedDigest: input.ExpectedDigest,
		MediaType: input.MediaType, Region: input.Region,
		RetentionClass: record.RetentionReference, CreatedAt: now,
		SessionExpiresAt: session.ExpiresAt(),
		Sequence:         input.Sequence,
	}, registry, service.policy)
	if err != nil {
		return evidence.Upload{}, err
	}

	return service.uploads.CreateUpload(ctx, scope, evidence.UploadCreateMutation{
		Upload: upload, Idempotency: retry,
	})
}

func resolveUploadRequirement(
	profile verification.Profile,
	input UploadRequest,
) (verification.Requirement, error) {
	for _, requirement := range profile.Requirements {
		if requirement.Key != input.RequirementKey {
			continue
		}
		if !slices.Contains(requirement.Artefacts, input.Artefact) {
			return verification.Requirement{}, ErrProcessingNotPermitted
		}
		acquisition := requirement.Acquisition
		if input.FallbackCondition != "" {
			found := false
			for _, fallback := range requirement.Fallbacks {
				if slices.Contains(fallback.On, input.FallbackCondition) {
					acquisition = fallback.Acquisition
					found = true
					break
				}
			}
			if !found {
				return verification.Requirement{}, ErrProcessingNotPermitted
			}
		}
		if !slices.Contains(acquisition.Methods, input.AcquisitionMethod) {
			return verification.Requirement{}, ErrProcessingNotPermitted
		}

		return requirement, nil
	}

	return verification.Requirement{}, ErrProcessingNotPermitted
}

func (service *UploadService) resolveUploadConstraints(
	requirement verification.Requirement,
) ([]string, int64, error) {
	mediaTypes := service.policy.AllowedMediaTypes()
	maximumBytes := service.policy.MaximumBytes()
	for _, constraint := range requirement.Constraints {
		switch constraint.Name {
		case evidence.ConstraintAllowedMedia:
			mediaTypes = slices.Clone(constraint.Value.StringList)
		case evidence.ConstraintMaximumBytes:
			if constraint.Value.Integer < 1 || constraint.Value.Integer > maximumBytes {
				return nil, 0, errors.New("authority: capture profile upload size exceeds deployment policy")
			}
			maximumBytes = constraint.Value.Integer
		}
	}

	return mediaTypes, maximumBytes, nil
}

func uploadAssurances(
	registry evidence.Registry,
	requirement verification.Requirement,
	methodName evidence.Name,
) ([]evidence.Name, error) {
	method, exists := registry.Method(methodName)
	if !exists {
		return nil, ErrProcessingNotPermitted
	}
	for _, support := range method.Supports {
		if support.EvidenceType != requirement.EvidenceType {
			continue
		}
		assurances := make([]evidence.Name, 0, len(requirement.RequiredAssurances))
		for _, assurance := range requirement.RequiredAssurances {
			if slices.Contains(support.Assurances, assurance) {
				assurances = append(assurances, assurance)
			}
		}

		return assurances, nil
	}

	return nil, ErrProcessingNotPermitted
}
