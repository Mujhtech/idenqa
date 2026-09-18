package postgres

import (
	"context"
	"time"

	"github.com/Mujhtech/idenqa/internal/identity"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	privacypg "github.com/Mujhtech/idenqa/internal/privacy/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// Cleanup erases expired encrypted values and identifier indexes in bounded,
// hold-aware transactions under the existing worker maintenance duty.
func (s *Store) Cleanup(ctx context.Context, at time.Time) error {
	var tenants []string
	e := s.pool.WithinTransaction(ctx, pg.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx pg.Transaction) error {
		rows, e := tx.Query(ctx, `SELECT tenant_id FROM idenqa.list_expired_identity_tenants($1,100)`, at)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var t string
			if e = rows.Scan(&t); e != nil {
				return e
			}
			tenants = append(tenants, t)
		}
		return rows.Err()
	})
	if e != nil {
		return e
	}
	for _, encoded := range tenants {
		identifier, e := id.ParseTenant(encoded)
		if e != nil {
			return e
		}
		scope, e := tenant.NewScope(identifier)
		if e != nil {
			return e
		}
		e = s.pool.WithinTransaction(ctx, pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
			if e = setScope(ctx, tx, scope); e != nil {
				return e
			}
			rows, e := tx.Query(ctx, `SELECT s.id,s.region FROM idenqa.identity_subjects s WHERE s.tenant_id=$1 AND EXISTS(
 SELECT 1 FROM idenqa.identity_records r JOIN idenqa.identity_record_values v ON v.tenant_id=r.tenant_id AND v.record_id=r.id WHERE r.tenant_id=s.tenant_id AND r.subject_id=s.id AND r.retain_until<=$2) AND NOT EXISTS(SELECT 1 FROM idenqa.legal_holds h WHERE h.tenant_id=s.tenant_id AND h.starts_at<=$2 AND (h.released_at IS NULL OR h.released_at>$2) AND (h.aggregate_id=s.id OR h.aggregate_id IN(SELECT verification_id FROM idenqa.identity_subject_verifications WHERE tenant_id=s.tenant_id AND subject_id=s.id))) ORDER BY s.id LIMIT 20 FOR UPDATE OF s`, encoded, at)
			if e != nil {
				return e
			}
			type target struct{ id, region string }
			var subjects []target
			for rows.Next() {
				var t target
				if e = rows.Scan(&t.id, &t.region); e != nil {
					rows.Close()
					return e
				}
				subjects = append(subjects, t)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
			for _, t := range subjects {
				held, e := privacypg.RetentionHeldWithin(ctx, tx, scope, t.id, at)
				if e != nil {
					return e
				}
				if held {
					continue
				}
				rows, e := tx.Query(ctx, `SELECT r.id FROM idenqa.identity_records r JOIN idenqa.identity_record_values v ON v.tenant_id=r.tenant_id AND v.record_id=r.id WHERE r.tenant_id=$1 AND r.subject_id=$2 AND r.retain_until<=$3 ORDER BY r.retain_until,r.id LIMIT 256`, encoded, t.id, at)
				if e != nil {
					return e
				}
				var records []string
				for rows.Next() {
					var r string
					if e = rows.Scan(&r); e != nil {
						rows.Close()
						return e
					}
					records = append(records, r)
				}
				e = rows.Err()
				rows.Close()
				if e != nil {
					return e
				}
				if len(records) == 0 {
					continue
				}
				if _, e = tx.Exec(ctx, `DELETE FROM idenqa.identity_identifier_tokens WHERE tenant_id=$1 AND record_id=ANY($2::text[])`, encoded, records); e != nil {
					return e
				}
				if _, e = tx.Exec(ctx, `DELETE FROM idenqa.identity_record_values WHERE tenant_id=$1 AND record_id=ANY($2::text[])`, encoded, records); e != nil {
					return e
				}
				var version int64
				if e = tx.QueryRow(ctx, `UPDATE idenqa.identity_subjects SET version=version+1,updated_at=$3 WHERE tenant_id=$1 AND id=$2 RETURNING version`, encoded, t.id, at).Scan(&version); e != nil {
					return e
				}
				digest, e := identity.Digest(records)
				if e != nil {
					return e
				}
				if e = s.event(ctx, tx, scope, "system:identity-retention", t.id, version, "identity.expired", digest, at); e != nil {
					return e
				}
			}
			return nil
		})
		if e != nil {
			return e
		}
	}
	return nil
}
