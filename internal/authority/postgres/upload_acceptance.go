package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/authority"
	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/jackc/pgx/v5"
)

func lockAuthoritySession(ctx context.Context, queries *sqlgen.Queries, scope tenant.Scope, verificationID id.Verification, authorityID id.Authority) error {
	row, err := queries.LockVerificationForAuthority(ctx, sqlgen.LockVerificationForAuthorityParams{
		TenantID: scope.ID().String(), ID: verificationID.String(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return authority.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock authority session: %w", err)
	}
	if row.AuthorityID == nil || *row.AuthorityID != authorityID.String() {
		return authority.ErrProcessingNotPermitted
	}
	// A lock alone does not invalidate a serializable snapshot established
	// before it waited. A no-op write gives authority/response changes a parent
	// row version, forcing a stale processing transaction to retry its reads.
	affected, err := queries.FenceVerificationAuthoritySession(ctx, sqlgen.FenceVerificationAuthoritySessionParams{
		TenantID: scope.ID().String(), ID: verificationID.String(), AuthorityID: row.AuthorityID,
	})
	if err != nil {
		return fmt.Errorf("fence authority session snapshot: %w", err)
	}
	if affected != 1 {
		return authority.ErrProcessingNotPermitted
	}
	return nil
}

// ValidateUploadAcceptanceWithin rechecks the current capture and authority
// records in the acceptance transaction. The caller must already hold the
// parent session lock, and must propagate failures and roll back its effects.
// Authority transitions and subject responses acquire that same parent lock.
func ValidateUploadAcceptanceWithin(
	ctx context.Context,
	queries *sqlgen.Queries,
	scope tenant.Scope,
	upload evidence.UploadRecord,
	occurredAt time.Time,
	source clock.Clock,
) error {
	if queries == nil || source == nil || scope.ID().IsZero() || upload.TenantID != scope.ID() || occurredAt.IsZero() {
		return authority.ErrProcessingNotPermitted
	}
	session, err := queries.FindVerificationSession(ctx, sqlgen.FindVerificationSessionParams{
		TenantID: scope.ID().String(), ID: upload.VerificationID.String(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return authority.ErrProcessingNotPermitted
	}
	if err != nil {
		return fmt.Errorf("load accepting session: %w", err)
	}
	if session.State != string(verification.SessionStateCollecting) ||
		session.AuthorityID == nil || *session.AuthorityID != upload.AuthorityID.String() {
		return authority.ErrProcessingNotPermitted
	}
	token, err := queries.LockCaptureTokenForUpload(ctx, sqlgen.LockCaptureTokenForUploadParams{
		TenantID: scope.ID().String(), ID: upload.CaptureTokenID.String(), VerificationID: upload.VerificationID.String(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return authority.ErrProcessingNotPermitted
	}
	if err != nil {
		return fmt.Errorf("lock accepting capture token: %w", err)
	}
	// Observe time after acquiring the token lock: waiting on a concurrent
	// revocation must not let a backdated command outlive its token or lease.
	observedAt := source.Now().UTC()
	if observedAt.IsZero() || occurredAt.After(observedAt) || !observedAt.Before(upload.ExpiresAt) ||
		upload.LeaseExpiresAt == nil || !observedAt.Before(*upload.LeaseExpiresAt) ||
		!session.ExpiresAt.Valid || !observedAt.Before(session.ExpiresAt.Time) ||
		token.RevokedAt.Valid || !token.ExpiresAt.Valid || !observedAt.Before(token.ExpiresAt.Time) {
		return authority.ErrProcessingNotPermitted
	}
	row, err := queries.FindProcessingAuthority(ctx, sqlgen.FindProcessingAuthorityParams{
		TenantID: scope.ID().String(), ID: upload.AuthorityID.String(),
	})
	if err != nil {
		return fmt.Errorf("load accepting authority: %w", err)
	}
	declaration, err := restoreAuthority(row)
	if err != nil {
		return err
	}
	noticeRow, err := queries.FindNoticeVersion(ctx, sqlgen.FindNoticeVersionParams{
		TenantID: scope.ID().String(), ID: row.NoticeID,
	})
	if err != nil {
		return fmt.Errorf("load accepting notice: %w", err)
	}
	notice, err := restoreNotice(noticeRow)
	if err != nil {
		return err
	}
	responseRow, err := queries.FindLatestSubjectResponse(ctx, sqlgen.FindLatestSubjectResponseParams{
		TenantID: scope.ID().String(), AuthorityID: row.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return authority.ErrSubjectResponseRequired
	}
	if err != nil {
		return fmt.Errorf("load accepting subject response: %w", err)
	}
	if responseRow.CaptureTokenID != upload.CaptureTokenID.String() || responseRow.ID != upload.ResponseID.String() || !responseRow.RecordedAt.Valid ||
		responseRow.RecordedAt.Time.After(occurredAt) {
		return authority.ErrProcessingNotPermitted
	}
	response, err := restoreResponse(responseRow)
	if err != nil {
		return err
	}
	request := authority.GrantRequest{
		TenantID: scope.ID(), VerificationID: upload.VerificationID, SubjectID: upload.SubjectID,
		Purpose: string(upload.Purpose), EvidenceType: string(upload.EvidenceType),
		RecipientReference: declaration.Record().RecipientReference, Region: upload.Region,
	}
	if err := authority.Evaluate(declaration, notice, &response, request, occurredAt); err != nil {
		return err
	}
	return authority.Evaluate(declaration, notice, &response, request, observedAt)
}
