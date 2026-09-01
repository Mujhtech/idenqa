package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/policy"
	policytask "github.com/Mujhtech/idenqa/internal/policy/task"
)

const listReadyAuthorshipsSQL = `SELECT tenant_id, verification_id, decision_id, ready_at
FROM idenqa.list_ready_policy_authorships($1, $2)`

// ListReadyAuthorships performs bounded identifier-only system discovery.
func (store *Store) ListReadyAuthorships(ctx context.Context, observedAt time.Time, batchSize int) ([]policytask.AuthorshipTarget, error) {
	if store == nil || observedAt.IsZero() || observedAt.Location() != time.UTC || batchSize < 1 || batchSize > policytask.CoordinationBatch {
		return nil, policy.ErrInvalid
	}
	var targets []policytask.AuthorshipTarget
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		rows, err := tx.Query(ctx, listReadyAuthorshipsSQL, observedAt, batchSize)
		if err != nil {
			return fmt.Errorf("query ready policy authorships: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var tenantValue, verificationValue, decisionValue string
			var readyAt time.Time
			if err := rows.Scan(&tenantValue, &verificationValue, &decisionValue, &readyAt); err != nil {
				return fmt.Errorf("scan ready policy authorship: %w", err)
			}
			tenantID, tenantErr := id.ParseTenant(tenantValue)
			verificationID, verificationErr := id.ParseVerification(verificationValue)
			decisionID, decisionErr := id.ParseDecision(decisionValue)
			if tenantErr != nil || verificationErr != nil || decisionErr != nil || readyAt.IsZero() {
				return policy.ErrInvalid
			}
			targets = append(targets, policytask.AuthorshipTarget{TenantID: tenantID, VerificationID: verificationID, DecisionID: decisionID, ReadyAt: readyAt.UTC()})
		}
		return rows.Err()
	})
	return targets, err
}

var _ policytask.CoordinationRepository = (*Store)(nil)
