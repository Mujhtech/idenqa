package postgres

import (
	"context"
	"time"

	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/privacy"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// LockRetentionAggregateWithin serializes erasure and new holds on a persistent
// subject or one of its linked verifications. Opaque unrelated aggregates remain valid.
func LockRetentionAggregateWithin(ctx context.Context, tx pg.Transaction, scope tenant.Scope, aggregate string) error {
	rows, e := tx.Query(ctx, `SELECT s.id FROM idenqa.identity_subjects s WHERE s.tenant_id=$1 AND
 (s.id=$2 OR EXISTS(SELECT 1 FROM idenqa.identity_subject_verifications l WHERE l.tenant_id=s.tenant_id AND l.subject_id=s.id AND l.verification_id=$2)) ORDER BY s.id FOR UPDATE OF s`, scope.ID().String(), aggregate)
	if e != nil {
		return e
	}
	for rows.Next() {
		var ignored string
		if e = rows.Scan(&ignored); e != nil {
			rows.Close()
			return e
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	rows, e = tx.Query(ctx, `SELECT id FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, scope.ID().String(), aggregate)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var ignored string
		if e = rows.Scan(&ignored); e != nil {
			return e
		}
	}
	return rows.Err()
}

// RetentionHeldWithin checks the exact aggregate and its explicitly related holds.
func RetentionHeldWithin(ctx context.Context, tx pg.Transaction, scope tenant.Scope, aggregate string, at time.Time) (bool, error) {
	var held bool
	e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM idenqa.legal_holds h WHERE h.tenant_id=$1 AND h.starts_at<=$3 AND (h.released_at IS NULL OR h.released_at>$3)
 AND (h.aggregate_id=$2 OR h.aggregate_id IN(SELECT verification_id FROM idenqa.identity_subject_verifications WHERE tenant_id=$1 AND subject_id=$2)
 OR h.aggregate_id IN(SELECT subject_id FROM idenqa.identity_subject_verifications WHERE tenant_id=$1 AND verification_id=$2)))`, scope.ID().String(), aggregate, at).Scan(&held)
	return held, e
}

// CompleteIdentityDeletionWithin finalizes the subject only after backup expiry and proof.
func CompleteIdentityDeletionWithin(ctx context.Context, tx pg.Transaction, scope tenant.Scope, deletion privacy.Deletion) error {
	// A stopped upload may register a staged object after the deletion request
	// was planned. Do not certify completion while exact orphan cleanup remains.
	var pending bool
	if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM idenqa.identity_subject_verifications l
 JOIN idenqa.evidence_upload_intents u ON u.tenant_id=l.tenant_id AND u.verification_id=l.verification_id
 JOIN idenqa.evidence_object_reconciliations r ON r.tenant_id=u.tenant_id AND r.upload_id=u.id
 WHERE l.tenant_id=$1 AND l.subject_id=$2 AND r.state<>'deleted'
 AND NOT EXISTS(SELECT 1 FROM idenqa.evidence_assets a WHERE a.tenant_id=r.tenant_id AND a.id=r.evidence_id AND a.object_key=r.object_key AND a.object_version=r.object_version AND a.state='deleted'))`, scope.ID().String(), deletion.AggregateID).Scan(&pending); e != nil {
		return e
	}
	if pending {
		return privacy.ErrConflict
	}
	_, e := tx.Exec(ctx, `UPDATE idenqa.identity_subjects SET state='deleted',version=version+1,updated_at=$4
 WHERE tenant_id=$1 AND id=$2 AND deletion_id=$3 AND state='deleting' AND erased_at IS NOT NULL`, scope.ID().String(), deletion.AggregateID, deletion.ID.String(), deletion.UpdatedAt)
	return e
}
