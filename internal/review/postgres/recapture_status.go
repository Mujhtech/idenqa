package postgres

import (
	"context"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// ListRecaptures exposes committed child completion via its immutable case link.
// The completed-decision pointer and decision were committed by the existing fenced effect.
func (store *RecaptureStore) ListRecaptures(ctx context.Context, scope tenant.Scope, caseID id.ReviewCase) ([]review.RecaptureStatus, error) {
	results := []review.RecaptureStatus{}
	err := store.pool.WithinTransaction(ctx, pg.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx pg.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT r.case_version,s.id,s.state,t.id,t.expires_at,s.expires_at,d.id,d.outcome,a.recorded_at,a.reviewer_id FROM idenqa.review_recaptures r JOIN idenqa.verification_sessions s ON s.tenant_id=r.tenant_id AND s.id=r.child_verification_id JOIN LATERAL (SELECT id,expires_at FROM idenqa.capture_tokens WHERE tenant_id=s.tenant_id AND verification_id=s.id ORDER BY issued_at DESC,id DESC LIMIT 1) t ON true LEFT JOIN idenqa.verification_decisions d ON d.tenant_id=s.tenant_id AND d.verification_id=s.id AND d.id=s.completed_decision_id AND s.state='completed' LEFT JOIN idenqa.review_recapture_acknowledgements a ON a.tenant_id=r.tenant_id AND a.case_id=r.case_id AND a.case_version=r.case_version AND a.decision_id=d.id WHERE r.tenant_id=$1 AND r.case_id=$2 ORDER BY r.case_version LIMIT 100`, scope.ID().String(), caseID.String())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var result review.RecaptureStatus
			var child, token string
			var decision, outcome, acknowledgedBy *string
			if err := rows.Scan(&result.CaseVersion, &child, &result.State, &token, &result.CaptureTokenExpiresAt, &result.ExpiresAt, &decision, &outcome, &result.AcknowledgedAt, &acknowledgedBy); err != nil {
				return err
			}
			result.ChildID, err = id.ParseVerification(child)
			if err != nil {
				return err
			}
			result.CaptureTokenID, err = id.ParseCaptureToken(token)
			if err != nil {
				return err
			}
			if decision != nil {
				result.DecisionID, err = id.ParseDecision(*decision)
				if err != nil {
					return err
				}
				if outcome != nil {
					result.Outcome = policy.Outcome(*outcome)
				}
			}
			if acknowledgedBy != nil {
				result.AcknowledgedBy = *acknowledgedBy
			}
			results = append(results, result)
		}
		return rows.Err()
	})
	return results, err
}
