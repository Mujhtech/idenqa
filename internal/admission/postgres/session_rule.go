package postgres

import (
	"context"

	admissionv1 "github.com/Mujhtech/idenqa/contracts/admission/v1"
	"github.com/Mujhtech/idenqa/internal/admission"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
)

// sessionRule classifies durable source lineage independently of the caller's
// requested operation. Recapture cannot borrow an ordinary creation rule, and a
// fabricated link cannot relabel an unrelated capture configuration.
func sessionRule(ctx context.Context, tx pg.Transaction, tenantID, verificationID, profile string, revision uint32, policy string) (string, error) {
	var recapture, valid bool
	err := tx.QueryRow(ctx, `SELECT
 EXISTS(SELECT 1 FROM idenqa.review_recaptures r WHERE r.tenant_id=$1 AND r.child_verification_id=$2),
 EXISTS(SELECT 1 FROM idenqa.review_recaptures r
  JOIN idenqa.verification_sessions p ON p.tenant_id=r.tenant_id AND p.id=r.parent_verification_id
  JOIN idenqa.verification_sessions c ON c.tenant_id=r.tenant_id AND c.id=r.child_verification_id
  JOIN idenqa.capture_tokens t ON t.tenant_id=r.tenant_id AND t.id=r.capture_token_id AND t.verification_id=c.id
  JOIN idenqa.policy_snapshots s ON s.tenant_id=r.tenant_id AND s.snapshot_digest=r.policy_snapshot_digest AND s.verification_id=p.id AND s.policy_id=c.policy_id
 WHERE r.tenant_id=$1 AND r.child_verification_id=$2
  AND p.source_profile_id=c.source_profile_id AND p.source_profile_revision=c.source_profile_revision
  AND p.source_profile_digest=c.source_profile_digest AND p.requirements=c.requirements
  AND p.policy_id=c.policy_id AND p.region=c.region AND r.created_at=c.created_at)`, tenantID, verificationID).Scan(&recapture, &valid)
	if err != nil {
		return "", err
	}
	if recapture {
		if !valid {
			return "", admission.ErrInvalid
		}
		return admissionv1.RecaptureOperation(profile, revision, policy)
	}
	return admissionv1.VerificationOperation(profile, revision, policy)
}
