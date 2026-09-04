package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/privacy"
	privacytask "github.com/Mujhtech/idenqa/internal/privacy/task"
)

const listDueDeletionsSQL = `SELECT tenant_id, deletion_id, workflow_version, due_at
FROM idenqa.list_due_privacy_deletions($1, $2)`

// ListDueDeletions performs bounded identifier-only system discovery through
// the explicitly granted SECURITY DEFINER function.
func (store *Store) ListDueDeletions(ctx context.Context, observedAt time.Time, batchSize int) ([]privacytask.DeletionTarget, error) {
	if store == nil || observedAt.IsZero() || observedAt.Location() != time.UTC || batchSize < 1 || batchSize > privacytask.CoordinationBatch {
		return nil, privacy.ErrInvalid
	}
	var targets []privacytask.DeletionTarget
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		rows, err := tx.Query(ctx, listDueDeletionsSQL, observedAt, batchSize)
		if err != nil {
			return fmt.Errorf("query due privacy deletions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var tenantValue, deletionValue string
			var dueAt time.Time
			var version int64
			if err := rows.Scan(&tenantValue, &deletionValue, &version, &dueAt); err != nil {
				return fmt.Errorf("scan due privacy deletion: %w", err)
			}
			tenantID, tenantErr := id.ParseTenant(tenantValue)
			deletionID, deletionErr := id.ParseDeletion(deletionValue)
			if tenantErr != nil || deletionErr != nil || version < 1 || dueAt.IsZero() {
				return privacy.ErrInvalid
			}
			targets = append(targets, privacytask.DeletionTarget{TenantID: tenantID, DeletionID: deletionID, Version: version, DueAt: dueAt.UTC()})
		}
		return rows.Err()
	})
	return targets, err
}

var _ privacytask.CoordinationRepository = (*Store)(nil)
