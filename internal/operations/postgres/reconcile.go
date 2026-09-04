// Package postgres implements open-source operational PostgreSQL diagnostics.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/operations"
	"github.com/jackc/pgx/v5"
)

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Reconcile returns bounded counts only; it never reads payload, evidence, or secrets.
func Reconcile(ctx context.Context, database rowQuerier, now time.Time) (operations.ReconciliationReport, error) {
	if database == nil || now.IsZero() || now.Location() != time.UTC {
		return operations.ReconciliationReport{}, operations.ErrInvalid
	}
	var privileged bool
	if err := database.QueryRow(ctx, `SELECT rolsuper OR rolbypassrls FROM pg_catalog.pg_roles WHERE rolname=current_user`).Scan(&privileged); err != nil {
		return operations.ReconciliationReport{}, fmt.Errorf("check reconciliation privilege: %w", err)
	}
	if !privileged {
		return operations.ReconciliationReport{}, errors.New("operations postgres: BYPASSRLS role is required")
	}
	report := operations.ReconciliationReport{CheckedAt: now}
	queries := []struct {
		name string
		out  *int64
		sql  string
	}{
		{"pending_outbox", &report.PendingOutbox, `SELECT count(*) FROM idenqa.outbox_events WHERE published_at IS NULL`},
		{"evidence_obligations", &report.EvidenceObligations, `SELECT count(*) FROM idenqa.evidence_object_reconciliations WHERE state IN ('pending','claimed')`},
		{"verification_obligations", &report.VerificationObligations, `SELECT count(*) FROM idenqa.verification_reconciliations WHERE status IN ('pending','claimed')`},
		{"deletion_workflows", &report.DeletionWorkflows, `SELECT count(*) FROM idenqa.deletion_requests WHERE state <> 'completed'`},
		{"expired_leases", &report.ExpiredLeases, `SELECT
			(SELECT count(*) FROM idenqa.evidence_object_reconciliations WHERE state='claimed' AND lease_expires_at <= $1) +
			(SELECT count(*) FROM idenqa.verification_reconciliations WHERE status='claimed' AND lease_expires_at <= $1)`},
		{"tombstone_gaps", &report.TombstoneGaps, `SELECT count(*) FROM idenqa.deletion_requests requests
			LEFT JOIN idenqa.deletion_tombstones tombstones ON tombstones.tenant_id=requests.tenant_id AND tombstones.deletion_id=requests.id
			WHERE requests.state='completed' AND tombstones.deletion_id IS NULL`},
		{"audit_head_gaps", &report.AuditHeadGaps, `SELECT count(*) FROM idenqa.audit_heads heads WHERE heads.last_sequence <>
			COALESCE((SELECT max(records.sequence) FROM idenqa.audit_records records WHERE records.tenant_id=heads.tenant_id),0)`},
	}
	for _, query := range queries {
		arguments := []any(nil)
		if query.name == "expired_leases" {
			arguments = []any{now}
		}
		if err := database.QueryRow(ctx, query.sql, arguments...).Scan(query.out); err != nil {
			return operations.ReconciliationReport{}, fmt.Errorf("query %s: %w", query.name, err)
		}
	}
	return report, nil
}
