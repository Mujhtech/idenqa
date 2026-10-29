package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	admissionv1 "github.com/Mujhtech/idenqa/contracts/admission/v1"
	"github.com/Mujhtech/idenqa/internal/admission"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
)

// Summary reads at most 64 configured counters in a single database snapshot.
// This private regional read remains available during commercial suspension.
func (s *Store) Summary(ctx context.Context, scope admissionv1.Scope) (admissionv1.Summary, error) {
	var result admissionv1.Summary
	if !scope.Valid() {
		return result, admission.ErrInvalid
	}
	err := s.pool.WithinTransaction(ctx, pg.TransactionOptions{Isolation: pg.IsolationRepeatableRead}, func(ctx context.Context, tx pg.Transaction) error {
		if err := setScope(ctx, tx, scope.TenantID); err != nil {
			return err
		}
		var now time.Time
		if err := tx.QueryRow(ctx, `SELECT transaction_timestamp()`).Scan(&now); err != nil {
			return err
		}
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT s.body FROM idenqa.admission_heads h JOIN idenqa.admission_snapshots s ON s.tenant_id=h.tenant_id AND s.revision=greatest(h.active_revision,(SELECT max(p.revision) FROM idenqa.admission_pending p WHERE p.tenant_id=$1 AND p.valid_from<=$2)) WHERE h.tenant_id=$1 AND s.tenant_id=$1`, scope.TenantID, now).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return admission.ErrUnavailable
		}
		if err != nil {
			return err
		}
		var snapshot admissionv1.Snapshot
		if json.Unmarshal(raw, &snapshot) != nil || snapshot.Scope != scope || s.policy.Verify(snapshot, now) != nil {
			return admission.ErrInvalid
		}
		result = admissionv1.Summary{Revision: snapshot.Revision, Status: snapshot.Status, ValidFrom: snapshot.ValidFrom, ValidUntil: snapshot.ValidUntil, ObservedAt: now, Limits: make([]admissionv1.CounterSummary, 0, len(snapshot.Limits))}
		if !now.Before(snapshot.ValidUntil) {
			result.Status = "expired"
		}
		for _, limit := range snapshot.Limits {
			counter := admissionv1.CounterSummary{Kind: limit.Kind, Key: limit.Key, Maximum: limit.Maximum}
			if limit.Window > 0 {
				counter.WindowStartUnixNano = now.UTC().Truncate(limit.Window).UnixNano()
			}
			err := tx.QueryRow(ctx, `SELECT used,dirty FROM idenqa.admission_counters WHERE tenant_id=$1 AND kind=$2 AND key=$3 AND window_key=$4`, scope.TenantID, limit.Kind, limit.Key, counter.WindowStartUnixNano).Scan(&counter.Used, &counter.Dirty)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			counter.Remaining = max(int64(0), counter.Maximum-counter.Used)
			if counter.Dirty {
				counter.Remaining = 0
			}
			result.Limits = append(result.Limits, counter)
		}
		return nil
	})
	return result, err
}
