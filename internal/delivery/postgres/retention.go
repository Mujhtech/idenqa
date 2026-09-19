package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/delivery"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
)

// RetentionResult reports one bounded, installation-wide maintenance batch.
type RetentionResult struct {
	PayloadsExpired  int
	AttemptsExpired  int
	TombstonesPurged int
}

// ExpireWebhookData redacts seven-day payloads and attempts, honours active
// aggregate legal holds, and removes minimal tombstones after 365 days.
func (store *Store) ExpireWebhookData(ctx context.Context, observedAt time.Time, batchSize int) (RetentionResult, error) {
	if store == nil || observedAt.IsZero() || observedAt.Location() != time.UTC || batchSize < 1 || batchSize > 500 {
		return RetentionResult{}, delivery.ErrInvalid
	}
	var result RetentionResult
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := tx.QueryRow(ctx, `SELECT payloads_expired,attempts_expired,tombstones_purged FROM idenqa.expire_webhook_data($1,$2)`,
			observedAt, batchSize).Scan(&result.PayloadsExpired, &result.AttemptsExpired, &result.TombstonesPurged); err != nil {
			return fmt.Errorf("expire webhook data: %w", err)
		}
		return nil
	})
	return result, err
}
