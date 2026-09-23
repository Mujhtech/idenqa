package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/authority"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ValidateProcessingWithin locks the parent session and verifies the current
// authority for every accepted evidence binding. The planner uses collecting;
// consequential check, policy and review commits use processing. The caller
// must keep this lock until its effects commit, and roll back if validation
// fails. Capture-token expiry is intentionally irrelevant after evidence
// acceptance.
func ValidateProcessingWithin(
	ctx context.Context,
	transaction platformpostgres.Transaction,
	scope tenant.Scope,
	verificationID id.Verification,
	occurredAt time.Time,
	source clock.Clock,
	expectedState verification.SessionState,
) error {
	return validateProcessingWithin(ctx, transaction, scope, verificationID, occurredAt, source, expectedState, false, false)
}

// ValidateExecutionWithin permits the exact execution-owned effects that may
// legitimately observe a session awaiting its authoritative external result:
// the pending dispatch and its accepted result transaction. Policy authorship,
// review routing, new planning and new capture-bound effects must continue to
// use the strict processing validation so a pending external operation can
// never be completed, rerouted or extended by a local effect.
func ValidateExecutionWithin(
	ctx context.Context,
	transaction platformpostgres.Transaction,
	scope tenant.Scope,
	verificationID id.Verification,
	occurredAt time.Time,
	source clock.Clock,
	expectedState verification.SessionState,
) error {
	return validateProcessingWithin(ctx, transaction, scope, verificationID, occurredAt, source, expectedState, false, true)
}

// ValidateReconsiderationWithin permits an explicitly authorized review of the latest
// completed decision. Live subject authority remains mandatory; the old capture
// session deadline does not erase the separate configured appeal window.
func ValidateReconsiderationWithin(ctx context.Context, transaction platformpostgres.Transaction, scope tenant.Scope, verificationID id.Verification, decisionID id.Decision, occurredAt time.Time, source clock.Clock) error {
	if transaction == nil || decisionID.IsZero() {
		return authority.ErrProcessingNotPermitted
	}
	if _, err := sqlgen.New(transaction).SetTenantScope(ctx, scope.ID().String()); err != nil {
		return err
	}
	var latest bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM idenqa.verification_decisions d WHERE d.tenant_id=$1 AND d.verification_id=$2 AND d.id=$3 AND NOT EXISTS(SELECT 1 FROM idenqa.verification_decisions next WHERE next.tenant_id=d.tenant_id AND next.verification_id=d.verification_id AND next.supersedes_id=d.id))`, scope.ID().String(), verificationID.String(), decisionID.String()).Scan(&latest); err != nil {
		return err
	}
	if !latest {
		return authority.ErrProcessingNotPermitted
	}
	return validateProcessingWithin(ctx, transaction, scope, verificationID, occurredAt, source, verification.SessionStateCompleted, true, false)
}
func validateProcessingWithin(ctx context.Context, transaction platformpostgres.Transaction, scope tenant.Scope, verificationID id.Verification, occurredAt time.Time, source clock.Clock, expectedState verification.SessionState, reconsideration, allowExternalWait bool) error {

	if transaction == nil || source == nil || scope.ID().IsZero() || verificationID.IsZero() || occurredAt.IsZero() ||
		(expectedState != verification.SessionStateCollecting && expectedState != verification.SessionStateProcessing && expectedState != verification.SessionStateManualReview && (!reconsideration || expectedState != verification.SessionStateCompleted)) {
		return authority.ErrProcessingNotPermitted
	}
	queries := sqlgen.New(transaction)
	if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
		return fmt.Errorf("set processing authority tenant scope: %w", err)
	}
	session, err := queries.LockVerificationForUpload(ctx, sqlgen.LockVerificationForUploadParams{
		TenantID: scope.ID().String(), ID: verificationID.String(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return authority.ErrProcessingNotPermitted
	}
	if err != nil {
		return fmt.Errorf("lock processing session: %w", err)
	}
	observedAt := source.Now().UTC()
	if observedAt.IsZero() || occurredAt.After(observedAt) || !executionState(session.State, expectedState, allowExternalWait) ||
		session.AuthorityID == nil || session.SubjectID == nil || session.NoticeID == nil ||
		!session.ExpiresAt.Valid || (!reconsideration && !observedAt.Before(session.ExpiresAt.Time)) ||
		!session.CaptureCompletedAt.Valid || session.CaptureCompletedAt.Time.After(occurredAt) {
		return fmt.Errorf("validate processing lifecycle and time: %w", authority.ErrProcessingNotPermitted)
	}
	row, err := queries.FindProcessingAuthority(ctx, sqlgen.FindProcessingAuthorityParams{
		TenantID: scope.ID().String(), ID: *session.AuthorityID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return authority.ErrProcessingNotPermitted
	}
	if err != nil {
		return fmt.Errorf("load processing authority: %w", err)
	}
	declaration, err := restoreAuthority(row)
	if err != nil {
		return err
	}
	if row.SubjectID != *session.SubjectID || row.NoticeID != *session.NoticeID || row.VerificationID != session.ID {
		return authority.ErrProcessingNotPermitted
	}
	noticeRow, err := queries.FindNoticeVersion(ctx, sqlgen.FindNoticeVersionParams{
		TenantID: scope.ID().String(), ID: row.NoticeID,
	})
	if err != nil {
		return fmt.Errorf("load processing notice: %w", err)
	}
	notice, err := restoreNotice(noticeRow)
	if err != nil {
		return err
	}
	responseRow, err := queries.FindLatestSubjectResponse(ctx, sqlgen.FindLatestSubjectResponseParams{
		TenantID: scope.ID().String(), AuthorityID: row.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return authority.ErrProcessingNotPermitted
	}
	if err != nil {
		return fmt.Errorf("load processing subject response: %w", err)
	}
	if !responseRow.RecordedAt.Valid || responseRow.RecordedAt.Time.After(occurredAt) {
		return authority.ErrProcessingNotPermitted
	}
	response, err := restoreResponse(responseRow)
	if err != nil {
		return err
	}
	var recoveredToken string
	err = transaction.QueryRow(ctx, `SELECT new_token_id FROM idenqa.capture_recoveries WHERE tenant_id=$1 AND verification_id=$2 ORDER BY recorded_at DESC,new_token_id DESC LIMIT 1`, scope.ID().String(), verificationID.String()).Scan(&recoveredToken)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil && recoveredToken != response.Record().CaptureTokenID.String() {
		return authority.ErrSubjectResponseRequired
	}
	return validateAcceptedProcessing(ctx, transaction, scope, verificationID, declaration, notice, response, occurredAt, observedAt)
}

// executionState reports whether a persisted lifecycle state satisfies the
// expected state. Only execution-owned callers may observe awaiting_external
// in place of processing; every other caller remains strict.
func executionState(persisted string, expected verification.SessionState, allowExternalWait bool) bool {
	if persisted == string(expected) {
		return true
	}
	return allowExternalWait && expected == verification.SessionStateProcessing && persisted == string(verification.SessionStateAwaitingExternal)
}

func validateAcceptedProcessing(
	ctx context.Context,
	transaction platformpostgres.Transaction,
	scope tenant.Scope,
	verificationID id.Verification,
	declaration authority.Authority,
	notice authority.Notice,
	response authority.Response,
	occurredAt, observedAt time.Time,
) error {
	rows, err := transaction.Query(ctx, `
SELECT authority_id, response_id, subject_id, purpose, evidence_type, region, accepted_at,
 EXISTS(SELECT 1 FROM idenqa.capture_recovery_uploads recovery WHERE recovery.tenant_id=u.tenant_id AND recovery.upload_id=u.id AND recovery.new_token_id=$3 AND recovery.disposition='retained')
FROM idenqa.evidence_upload_intents u
WHERE tenant_id = $1 AND verification_id = $2 AND state = 'accepted'
ORDER BY id`, scope.ID().String(), verificationID.String(), response.Record().CaptureTokenID.String())
	if err != nil {
		return fmt.Errorf("load accepted processing bindings: %w", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var authorityID, responseID, subjectID, purpose, evidenceType, region string
		var recovered bool
		var acceptedAt pgtype.Timestamptz
		if err := rows.Scan(&authorityID, &responseID, &subjectID, &purpose, &evidenceType, &region, &acceptedAt, &recovered); err != nil {
			return fmt.Errorf("scan accepted processing binding: %w", err)
		}
		if authorityID != declaration.Record().ID.String() || (responseID != response.Record().ID.String() && !recovered) ||
			subjectID != declaration.Record().SubjectID.String() || !acceptedAt.Valid || acceptedAt.Time.After(occurredAt) {
			return fmt.Errorf("validate accepted evidence binding and time: %w", authority.ErrProcessingNotPermitted)
		}
		request := authority.GrantRequest{
			TenantID: scope.ID(), VerificationID: verificationID, SubjectID: declaration.Record().SubjectID,
			Purpose: purpose, EvidenceType: evidenceType, Region: region,
			RecipientReference: declaration.Record().RecipientReference,
		}
		if authority.Evaluate(declaration, notice, &response, request, occurredAt) != nil ||
			authority.Evaluate(declaration, notice, &response, request, observedAt) != nil {
			return authority.ErrProcessingNotPermitted
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate accepted processing bindings: %w", err)
	}
	if count == 0 {
		return authority.ErrProcessingNotPermitted
	}
	return nil
}
