package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationtask "github.com/Mujhtech/idenqa/internal/verification/task"
)

// ListDueExpirations rotates a bounded identifier-only installation discovery cursor.
func (store *StopStore) ListDueExpirations(ctx context.Context, observedAt time.Time, limit int) ([]verificationtask.ExpiryTarget, error) {
	if !utcTime(observedAt) || limit < 1 || limit > verificationtask.CoordinationBatch {
		return nil, verification.ErrSessionConflict
	}
	var targets []verificationtask.ExpiryTarget
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		rows, err := tx.Query(ctx, `SELECT tenant_id, verification_id FROM idenqa.list_due_verification_expirations($1,$2)`, observedAt, limit)
		if err != nil {
			return fmt.Errorf("discover verification expiry: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var tenantValue, verificationValue string
			if err := rows.Scan(&tenantValue, &verificationValue); err != nil {
				return err
			}
			tenantID, err := id.ParseTenant(tenantValue)
			if err != nil {
				return err
			}
			verificationID, err := id.ParseVerification(verificationValue)
			if err != nil {
				return err
			}
			targets = append(targets, verificationtask.ExpiryTarget{TenantID: tenantID, VerificationID: verificationID})
		}
		return rows.Err()
	})
	return targets, err
}
