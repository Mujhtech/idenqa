package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/delivery"
	deliverytask "github.com/Mujhtech/idenqa/internal/delivery/task"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
)

// ListReadyDeliveries exposes bounded identifiers only through the privileged
// discovery function, which advances only a fair scheduling cursor. All delivery
// state access requires explicit tenant scope.
func (store *Store) ListReadyDeliveries(ctx context.Context, observedAt time.Time, batchSize int) ([]deliverytask.DeliveryTarget, error) {
	if store == nil || observedAt.IsZero() || observedAt.Location() != time.UTC || batchSize < 1 || batchSize > deliverytask.CoordinationBatch {
		return nil, delivery.ErrInvalid
	}
	var targets []deliverytask.DeliveryTarget
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		rows, err := tx.Query(ctx, `SELECT tenant_id,delivery_id,attempt_number,due_at,created_at FROM idenqa.list_ready_webhook_deliveries($1,$2)`, observedAt, batchSize)
		if err != nil {
			return fmt.Errorf("query ready deliveries: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var target deliverytask.DeliveryTarget
			var tenantValue, deliveryValue string
			if err := rows.Scan(&tenantValue, &deliveryValue, &target.AttemptNumber, &target.DueAt, &target.CreatedAt); err != nil {
				return fmt.Errorf("scan ready delivery: %w", err)
			}
			target.TenantID, err = id.ParseTenant(tenantValue)
			if err != nil {
				return delivery.ErrInvalid
			}
			target.DeliveryID, err = id.ParseDelivery(deliveryValue)
			if err != nil {
				return delivery.ErrInvalid
			}
			target.DueAt, target.CreatedAt = target.DueAt.UTC(), target.CreatedAt.UTC()
			targets = append(targets, target)
		}
		return rows.Err()
	})
	return targets, err
}

var _ deliverytask.CoordinationRepository = (*Store)(nil)
