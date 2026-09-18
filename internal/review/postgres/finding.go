package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func (store *Store) validateFindingWithin(ctx context.Context, scope tenant.Scope, tx platformpostgres.Transaction, value review.Case, finding review.Finding) error {
	now := store.clock.Now().UTC()
	if finding.RecordedAt.After(now) || len(finding.GrantIDs) == 0 || len(finding.GrantIDs) > 32 {
		return fmt.Errorf("%w: finding time or grant count", review.ErrForbidden)
	}
	if err := validateCaseProcessing(ctx, tx, scope, value, finding.RecordedAt, store.clock); err != nil {
		return err
	}
	encoded, err := json.Marshal([]review.PermittedFinding{{Resolution: finding.Resolution, ReasonCode: finding.ReasonCode}})
	if err != nil {
		return err
	}
	var permitted bool
	if err := tx.QueryRow(ctx, `SELECT permitted_findings @> $3::jsonb FROM idenqa.review_cases WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), value.ID.String(), encoded).Scan(&permitted); err != nil {
		return err
	}
	if !permitted {
		return fmt.Errorf("%w: finding not permitted", review.ErrForbidden)
	}
	queries := sqlgen.New(tx)
	session, err := queries.LockVerificationForUpload(ctx, sqlgen.LockVerificationForUploadParams{TenantID: scope.ID().String(), ID: value.VerificationID.String()})
	if err != nil {
		return err
	}
	if session.AuthorityID == nil {
		return fmt.Errorf("%w: missing authority", review.ErrForbidden)
	}
	response, err := queries.FindLatestSubjectResponse(ctx, sqlgen.FindLatestSubjectResponseParams{TenantID: scope.ID().String(), AuthorityID: *session.AuthorityID})
	if err != nil {
		return err
	}
	for _, grantID := range finding.GrantIDs {
		var valid bool
		err := tx.QueryRow(ctx, `SELECT g.created_at<=$9 AND g.expires_at>$9 AND g.revoked_at IS NULL AND e.state='available' AND e.integrity='verified'
 FROM idenqa.evidence_processing_grants g JOIN idenqa.evidence_assets e ON e.tenant_id=g.tenant_id AND e.id=g.evidence_id
 JOIN idenqa.review_evidence_access b ON b.tenant_id=g.tenant_id AND b.grant_id=g.id AND b.evidence_id=g.evidence_id
 WHERE g.tenant_id=$1 AND g.id=$2 AND g.verification_id=$3 AND g.authority_id=$4 AND g.response_id=$5
 AND b.reviewer_id=$6 AND b.case_id=$10 AND b.case_version=$11
 AND EXISTS(SELECT 1 FROM idenqa.evidence_grant_redemption_outcomes outcome WHERE outcome.tenant_id=b.tenant_id AND outcome.redemption_id=b.redemption_id AND outcome.grant_id=b.grant_id AND outcome.outcome='succeeded') AND g.region=$7 AND g.check_reference=$8 FOR SHARE OF g,e`, scope.ID().String(), grantID.String(), value.VerificationID.String(), *session.AuthorityID, response.ID, finding.ReviewerID, value.Region, "review."+strings.ToLower(value.ID.String()), now, value.ID.String(), value.Version-1).Scan(&valid)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("validate reviewer grant: %w", err)
		}
		if err != nil || !valid {
			return fmt.Errorf("%w: grant binding or validity", review.ErrForbidden)
		}
	}
	return nil
}
