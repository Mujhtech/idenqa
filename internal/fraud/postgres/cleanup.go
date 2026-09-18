package postgres

import (
	"context"
	"time"

	auditpostgres "github.com/Mujhtech/idenqa/internal/audit/postgres"
	"github.com/Mujhtech/idenqa/internal/fraud"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	privacypg "github.com/Mujhtech/idenqa/internal/privacy/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// Cleanup is bounded replay-safe maintenance called under the worker's owned
// Headgate duty. Discovery supplies routing hints; every delete rechecks tenant,
// expiry and legal holds in its own transaction.
func (s *Store) Cleanup(ctx context.Context) error {
	var targets []string
	e := s.pool.WithinTransaction(ctx, pg.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx pg.Transaction) error {
		rows, e := tx.Query(ctx, `SELECT tenant_id FROM idenqa.list_expired_fraud_tenants($1,100)`, time.Now().UTC())
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var value string
			if e = rows.Scan(&value); e != nil {
				return e
			}
			targets = append(targets, value)
		}
		return rows.Err()
	})
	if e != nil {
		return e
	}
	for _, value := range targets {
		identifier, e := id.ParseTenant(value)
		if e != nil {
			return e
		}
		scope, e := tenant.NewScope(identifier)
		if e != nil {
			return e
		}
		e = s.pool.WithinTransaction(ctx, pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
			if e := setScope(ctx, tx, scope); e != nil {
				return e
			}
			rows, e := tx.Query(ctx, `SELECT DISTINCT l.verification_id FROM idenqa.fraud_links l WHERE l.tenant_id=$1 AND l.expires_at<=clock_timestamp()
 AND NOT EXISTS(SELECT 1 FROM idenqa.legal_holds h WHERE h.tenant_id=l.tenant_id AND (h.aggregate_id=l.verification_id OR h.aggregate_id IN(SELECT subject_id FROM idenqa.identity_subject_verifications WHERE tenant_id=l.tenant_id AND verification_id=l.verification_id)) AND h.starts_at<=clock_timestamp() AND h.released_at IS NULL)
 ORDER BY l.verification_id LIMIT 20`, value)
			if e != nil {
				return e
			}
			var verifications []string
			for rows.Next() {
				var verification string
				if e = rows.Scan(&verification); e != nil {
					rows.Close()
					return e
				}
				verifications = append(verifications, verification)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
			var deleted int64
			for _, verification := range verifications {
				if e = privacypg.LockRetentionAggregateWithin(ctx, tx, scope, verification); e != nil {
					return e
				}
				held, e := privacypg.RetentionHeldWithin(ctx, tx, scope, verification, time.Now().UTC())
				if e != nil {
					return e
				}
				if held {
					continue
				}
				tag, e := tx.Exec(ctx, `DELETE FROM idenqa.fraud_links WHERE (tenant_id,evidence_id,namespace,kind,source_reference) IN (SELECT tenant_id,evidence_id,namespace,kind,source_reference FROM idenqa.fraud_links WHERE tenant_id=$1 AND verification_id=$2 AND expires_at<=clock_timestamp() ORDER BY expires_at LIMIT $3 FOR UPDATE)`, value, verification, 1000-deleted)
				if e != nil {
					return e
				}
				deleted += tag.RowsAffected()
				if deleted >= 1000 {
					break
				}
			}
			if deleted == 0 {
				return nil
			}

			at := time.Now().UTC().Truncate(time.Microsecond)
			digest := fraud.Digest(struct {
				Count int64
				At    time.Time
			}{deleted, at})
			_, e = auditpostgres.AppendInTransaction(ctx, tx, scope, auditpostgres.Event{EventID: "fraud.expiry." + digest, EventType: "fraud.expired", AggregateID: "fraud.retention", ActorID: "system.fraud", EventDigest: digest, OccurredAt: at})
			return e
		})
		if e != nil {
			return e
		}
	}
	return nil
}
