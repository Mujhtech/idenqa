package postgres

import (
	"context"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// AppendEvaluationWithin stores canonical meaning without asserting terminal authorization.
func (store *Store) AppendEvaluationWithin(ctx context.Context, scope tenant.Scope, tx platformpostgres.Transaction, snapshot policy.Snapshot, evaluation policy.Evaluation) error {
	if tx == nil || snapshot.TenantID() != scope.ID() {
		return policy.ErrInvalid
	}
	if _, err := policy.RestoreEvaluationCanonical(snapshot, evaluation.Canonical(), evaluation.Digest()); err != nil {
		return err
	}
	queries := sqlgen.New(tx)
	if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
		return err
	}
	if err := persistSnapshot(ctx, queries, snapshot); err != nil {
		return err
	}
	return persistEvaluation(ctx, queries, snapshot, evaluation)
}

// ValidateReviewDecisionWithin requires an accepted, immutable review evaluation
// tied to the original routing policy/authority before manual-review completion.
func ValidateReviewDecisionWithin(ctx context.Context, scope tenant.Scope, tx platformpostgres.Transaction, decision policy.Decision) error {
	var valid bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM idenqa.review_evaluations e
 JOIN idenqa.review_cases c ON c.tenant_id=e.tenant_id AND c.id=e.case_id AND c.version=e.case_version AND c.state='resolved'
 JOIN idenqa.policy_routing_receipts r ON r.tenant_id=c.tenant_id AND r.request_id=c.routing_request_id
 JOIN idenqa.policy_snapshots original ON original.tenant_id=r.tenant_id AND original.snapshot_digest=r.snapshot_digest
 JOIN idenqa.policy_snapshots current ON current.tenant_id=e.tenant_id AND current.snapshot_digest=e.snapshot_digest
 WHERE e.tenant_id=$1 AND e.verification_id=$2 AND c.routing_request_id=$3 AND e.snapshot_digest=$4 AND e.evaluation_digest=$5
 AND current.policy_id=original.policy_id AND current.policy_revision=original.policy_revision AND current.policy_digest=original.policy_digest
 AND (current.canonical::jsonb #>> '{context,profile_digest}') IS NOT DISTINCT FROM (original.canonical::jsonb #>> '{context,profile_digest}')
 AND current.authority_id=original.authority_id AND current.acknowledgement_id=original.acknowledgement_id AND current.region=original.region)`, scope.ID().String(), decision.Snapshot().VerificationID().String(), decision.ID().String(), decision.Snapshot().Digest(), decision.Evaluation().Digest()).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return policy.ErrInvalid
	}
	return nil
}

func reviewSessionState(ctx context.Context, scope tenant.Scope, tx platformpostgres.Transaction, verificationID id.Verification) (string, error) {
	var state string
	err := tx.QueryRow(ctx, `SELECT state FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, scope.ID().String(), verificationID.String()).Scan(&state)
	return state, err
}
