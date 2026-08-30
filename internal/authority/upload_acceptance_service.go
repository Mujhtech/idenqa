package authority

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/objectstore"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

const (
	uploadSignatureRejection = "evidence.upload.signature_mismatch"
	uploadAuthorityRejection = "evidence.upload.authority_denied"
	uploadFailureTimeout     = 5 * time.Second
)

// UploadAcceptancePreflighter authenticates immutable request metadata and
// durably claims one fenced whole-body attempt before plaintext is consumed.
type UploadAcceptancePreflighter interface {
	Begin(
		context.Context,
		evidence.UploadPrincipal,
		id.Upload,
		evidence.UploadMetadata,
	) (evidence.Upload, error)
}

// UploadEvidenceStager protects plaintext into an exact immutable object that
// can either be atomically accepted or safely discarded.
type UploadEvidenceStager interface {
	Prepare(context.Context, tenant.Scope, evidence.ProtectionInput) (evidence.PreparedEvidence, error)
	Discard(context.Context, evidence.PreparedEvidence) error
}

// UploadAcceptanceCommitter owns the durable all-or-nothing acceptance effect.
type UploadAcceptanceCommitter interface {
	AcceptUpload(context.Context, tenant.Scope, evidence.UploadAcceptance) (evidence.Upload, error)
}

// UploadAttemptFailureRecorder owns durable retry, expiry, and terminal rejection.
type UploadAttemptFailureRecorder interface {
	evidence.UploadAttemptReleaser
	evidence.UploadAttemptRejecter
}

// UploadAcceptanceIDGenerator creates the durable evidence-ready event identity.
type UploadAcceptanceIDGenerator interface {
	NewEvent() (id.Event, error)
}

// UploadAcceptanceService joins authenticated preflight, bounded streaming
// protection, live authority evaluation, atomic persistence, and compensation.
type UploadAcceptanceService struct {
	authorities UploadAuthorityFinder
	preflight   UploadAcceptancePreflighter
	stager      UploadEvidenceStager
	reconciler  evidence.ObjectReconciliationRecorder
	failures    UploadAttemptFailureRecorder
	committer   UploadAcceptanceCommitter
	identifiers UploadAcceptanceIDGenerator
	clock       clock.Clock
}

// NewUploadAcceptanceService constructs the authority-owned acceptance workflow.
func NewUploadAcceptanceService(
	authorities UploadAuthorityFinder,
	preflight UploadAcceptancePreflighter,
	stager UploadEvidenceStager,
	reconciler evidence.ObjectReconciliationRecorder,
	failures UploadAttemptFailureRecorder,
	committer UploadAcceptanceCommitter,
	identifiers UploadAcceptanceIDGenerator,
	source clock.Clock,
) (*UploadAcceptanceService, error) {
	if authorities == nil || preflight == nil || stager == nil || reconciler == nil || failures == nil || committer == nil ||
		identifiers == nil || source == nil {
		return nil, errors.New("authority: upload acceptance dependencies are required")
	}

	return &UploadAcceptanceService{
		authorities: authorities, preflight: preflight, stager: stager, reconciler: reconciler,
		failures: failures, committer: committer, identifiers: identifiers, clock: source,
	}, nil
}

// Accept consumes one complete body only after preflight, protects it without
// buffering, then re-evaluates current pinned authority immediately before the
// atomic durable acceptance effect.
func (service *UploadAcceptanceService) Accept(
	ctx context.Context,
	captureContext verification.CaptureContext,
	uploadID id.Upload,
	metadata evidence.UploadMetadata,
	body io.Reader,
) (evidence.Upload, error) {
	if service == nil {
		return evidence.Upload{}, errors.New("authority: upload acceptance service is not initialised")
	}
	if body == nil {
		return evidence.Upload{}, errors.New("authority: upload body is required")
	}
	scope := captureContext.TenantScope()
	session := captureContext.Session()
	if scope.ID().IsZero() || captureContext.TokenID().IsZero() || session.ID().IsZero() ||
		session.TenantID() != scope.ID() {
		return evidence.Upload{}, ErrProcessingNotPermitted
	}

	claimed, err := service.preflight.Begin(ctx, evidence.UploadPrincipal{
		Scope: scope, CaptureTokenID: captureContext.TokenID(), VerificationID: session.ID(),
	}, uploadID, metadata)
	if err != nil {
		return evidence.Upload{}, err
	}
	if claimed.State() == evidence.UploadStateAccepted {
		return claimed, nil
	}
	reader, err := evidence.NewUploadReader(claimed, body)
	if err != nil {
		return evidence.Upload{}, service.recordFailure(ctx, scope, captureContext.TokenID(), claimed, err)
	}
	record := claimed.Record()
	prepared, err := service.stager.Prepare(ctx, scope, evidence.ProtectionInput{
		ID: record.EvidenceID, SubjectID: record.SubjectID, VerificationID: record.VerificationID,
		RequirementKey: record.RequirementKey, EvidenceType: record.EvidenceType,
		Artefact: record.Artefact, AcquisitionMethod: record.AcquisitionMethod,
		Assurances: record.Assurances, Region: record.Region, RetentionClass: record.RetentionClass,
		ContentRevision: 1, MediaType: record.MediaType, Plaintext: reader,
		CreatedAt: service.clock.Now().UTC().Truncate(time.Second),
	})
	if err != nil {
		cause := fmt.Errorf("stage protected upload: %w", err)
		if _, bodyErr := reader.Result(); bodyErr != nil && !errors.Is(bodyErr, evidence.ErrUploadBodyIncomplete) {
			cause = bodyErr
		}
		if cleanup, ok := errors.AsType[*evidence.CleanupError](err); ok {
			if recordErr := service.recordOrphan(
				ctx, scope, claimed, cleanup.Object(), service.clock.Now().UTC().Truncate(time.Second),
			); recordErr != nil {
				return evidence.Upload{}, errors.Join(cause, recordErr)
			}
		}

		return evidence.Upload{}, service.recordFailure(ctx, scope, captureContext.TokenID(), claimed, cause)
	}

	now := service.clock.Now().UTC().Truncate(time.Second)
	if err := service.reconciler.CreateObjectReconciliation(ctx, scope, claimed, prepared, now); err != nil {
		cause := fmt.Errorf("record staged upload reconciliation: %w", err)
		return evidence.Upload{}, service.discardUnrecorded(
			ctx, scope, captureContext.TokenID(), claimed, prepared, now, cause,
		)
	}
	validated, err := reader.Result()
	if err != nil {
		return evidence.Upload{}, service.discardRecordedAndFail(
			ctx, scope, captureContext.TokenID(), claimed, prepared, now, err,
		)
	}
	if !session.AcceptsCaptureAt(now) {
		return evidence.Upload{}, service.discardRecordedAndFail(
			ctx, scope, captureContext.TokenID(), claimed, prepared, now, ErrProcessingNotPermitted,
		)
	}
	snapshot, err := service.authorities.CaptureSnapshot(ctx, scope, record.VerificationID)
	if err != nil {
		return evidence.Upload{}, service.discardRecordedAndFail(
			ctx, scope, captureContext.TokenID(), claimed, prepared, now, err,
		)
	}
	if snapshot.Authority.ID() != record.AuthorityID || snapshot.Response == nil ||
		snapshot.Response.Record().ID != record.ResponseID {
		return evidence.Upload{}, service.discardRecordedAndFail(
			ctx, scope, captureContext.TokenID(), claimed, prepared, now, ErrProcessingNotPermitted,
		)
	}
	authorityRecord := snapshot.Authority.Record()
	if err := Evaluate(snapshot.Authority, snapshot.Notice, snapshot.Response, GrantRequest{
		TenantID: scope.ID(), VerificationID: record.VerificationID, SubjectID: record.SubjectID,
		Purpose: string(record.Purpose), EvidenceType: string(record.EvidenceType),
		RecipientReference: authorityRecord.RecipientReference, Region: record.Region,
	}, now); err != nil {
		return evidence.Upload{}, service.discardRecordedAndFail(
			ctx, scope, captureContext.TokenID(), claimed, prepared, now, err,
		)
	}
	eventID, err := service.identifiers.NewEvent()
	if err != nil {
		return evidence.Upload{}, service.discardRecordedAndFail(
			ctx, scope, captureContext.TokenID(), claimed, prepared, now,
			fmt.Errorf("generate evidence-ready event id: %w", err),
		)
	}
	accepted, err := service.committer.AcceptUpload(ctx, scope, evidence.UploadAcceptance{
		UploadID: claimed.ID(), CaptureTokenID: captureContext.TokenID(),
		ExpectedVersion: claimed.Version(), Attempt: claimed.Attempt(), Asset: prepared.Asset(),
		PlaintextBytes: validated.Bytes, EventID: eventID, OccurredAt: now,
	})
	if errors.Is(err, evidence.ErrUploadAcceptanceOutcomeUnknown) {
		return evidence.Upload{}, prepared.ReconciliationError(err)
	}
	if err != nil {
		return evidence.Upload{}, service.discardRecordedAndFail(
			ctx, scope, captureContext.TokenID(), claimed, prepared, now, err,
		)
	}

	return accepted, nil
}

func (service *UploadAcceptanceService) discardRecordedAndFail(
	ctx context.Context,
	scope tenant.Scope,
	principal id.CaptureToken,
	upload evidence.Upload,
	prepared evidence.PreparedEvidence,
	now time.Time,
	cause error,
) error {
	result := cause
	if err := service.stager.Discard(ctx, prepared); err != nil {
		result = errors.Join(result, fmt.Errorf("discard staged upload: %w", err))
	} else {
		resolveContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), uploadFailureTimeout)
		defer cancel()
		if err := service.reconciler.ResolveObjectReconciliation(
			resolveContext, scope, upload.ID(), upload.Attempt(), prepared.Asset().Content().Object(),
			evidence.ReconciliationDeleted, now,
		); err != nil {
			result = errors.Join(result, fmt.Errorf("resolve discarded upload reconciliation: %w", err))
		}
	}

	return service.recordFailure(ctx, scope, principal, upload, result)
}

func (service *UploadAcceptanceService) discardUnrecorded(
	ctx context.Context,
	scope tenant.Scope,
	principal id.CaptureToken,
	upload evidence.Upload,
	prepared evidence.PreparedEvidence,
	occurredAt time.Time,
	cause error,
) error {
	if err := service.stager.Discard(ctx, prepared); err != nil {
		result := errors.Join(cause, fmt.Errorf("discard staged upload: %w", err))
		if recordErr := service.recordOrphan(
			ctx, scope, upload, prepared.Asset().Content().Object(), occurredAt,
		); recordErr != nil {
			return errors.Join(result, recordErr)
		}
		return service.recordFailure(ctx, scope, principal, upload, result)
	}

	return service.recordFailure(ctx, scope, principal, upload, cause)
}

func (service *UploadAcceptanceService) recordOrphan(
	ctx context.Context,
	scope tenant.Scope,
	upload evidence.Upload,
	object objectstore.Object,
	occurredAt time.Time,
) error {
	recordContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), uploadFailureTimeout)
	defer cancel()
	if err := service.reconciler.CreateObjectReconciliationForObject(
		recordContext, scope, upload, object, occurredAt,
	); err != nil {
		return fmt.Errorf("record exact upload orphan: %w", err)
	}

	return nil
}

func (service *UploadAcceptanceService) recordFailure(
	ctx context.Context,
	scope tenant.Scope,
	principal id.CaptureToken,
	upload evidence.Upload,
	cause error,
) error {
	recordContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), uploadFailureTimeout)
	defer cancel()
	now := service.clock.Now().UTC().Truncate(time.Second)
	record := upload.Record()
	var err error
	if reason := uploadRejectionReason(cause); reason != "" && now.Before(record.ExpiresAt) &&
		record.LeaseExpiresAt != nil && now.Before(*record.LeaseExpiresAt) {
		_, err = service.failures.RejectUploadAttempt(
			recordContext, scope, principal, upload.ID(), upload.Version(), upload.Attempt(), reason, now,
		)
	} else {
		_, err = service.failures.FailUploadAttempt(
			recordContext, scope, principal, upload.ID(), upload.Version(), upload.Attempt(), now,
		)
	}
	if err != nil {
		return errors.Join(cause, fmt.Errorf("persist upload attempt failure: %w", err))
	}

	return cause
}

func uploadRejectionReason(err error) string {
	if errors.Is(err, evidence.ErrUploadSignature) {
		return uploadSignatureRejection
	}
	if errors.Is(err, ErrProcessingNotPermitted) {
		return uploadAuthorityRejection
	}
	return ""
}
