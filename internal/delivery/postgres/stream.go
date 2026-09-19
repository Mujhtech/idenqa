package postgres

import (
	"context"
	"errors"
	"fmt"

	webhookv1 "github.com/Mujhtech/idenqa/contracts/webhook/v1"
	"github.com/Mujhtech/idenqa/internal/delivery"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

const eventStreamColumns = `id,event_type,schema_version,stream_sequence,created_at,body,body_provider,body_reference,body_key_version,body_algorithm`

// Events lists wrapped catalogue events in durable insertion order after the
// position, optionally selected by exact event types. Bodies stay wrapped.
func (store *Store) Events(ctx context.Context, scope tenant.Scope, after delivery.StreamPosition, selection []string, limit int) ([]delivery.WrappedEvent, error) {
	if after < 0 || limit < 1 || limit > delivery.MaximumStreamBatch {
		return nil, delivery.ErrInvalid
	}
	result := []delivery.WrappedEvent{}
	err := store.read(ctx, scope, func(ctx context.Context, tx pg.Transaction) error {
		var events []string
		if len(selection) > 0 {
			events = selection
		}
		rows, err := tx.Query(ctx, `SELECT `+eventStreamColumns+` FROM idenqa.webhook_events
			WHERE tenant_id=$1 AND stream_sequence > $2 AND payload_expired_at IS NULL
			  AND ($3::text[] IS NULL OR event_type = ANY($3))
			ORDER BY stream_sequence LIMIT $4`,
			scope.ID().String(), int64(after), events, limit)
		if err != nil {
			return fmt.Errorf("list webhook event stream: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			event, err := scanWrappedEvent(rows)
			if err != nil {
				return err
			}
			result = append(result, event)
		}
		return rows.Err()
	})
	return result, err
}

// LatestPosition returns the current durable tail, or zero when the tenant has
// emitted no catalogue event yet.
func (store *Store) LatestPosition(ctx context.Context, scope tenant.Scope) (delivery.StreamPosition, error) {
	var result int64
	err := store.read(ctx, scope, func(ctx context.Context, tx pg.Transaction) error {
		return tx.QueryRow(ctx, `SELECT COALESCE(MAX(stream_sequence),0) FROM idenqa.webhook_events WHERE tenant_id=$1`, scope.ID().String()).Scan(&result)
	})
	return delivery.StreamPosition(result), err
}

func scanWrappedEvent(row scanner) (delivery.WrappedEvent, error) {
	var result delivery.WrappedEvent
	var encoded, eventType, schemaVersion string
	var sequence int64
	var provider, reference, keyVersion, algorithm *string
	err := row.Scan(&encoded, &eventType, &schemaVersion, &sequence, &result.CreatedAt, &result.Body, &provider, &reference, &keyVersion, &algorithm)
	if errors.Is(err, pgx.ErrNoRows) {
		return delivery.WrappedEvent{}, delivery.ErrNotFound
	}
	if err != nil {
		return delivery.WrappedEvent{}, fmt.Errorf("read webhook event stream: %w", err)
	}
	result.CreatedAt = result.CreatedAt.UTC()
	result.Type, result.SchemaVersion, result.Sequence = webhookv1.Type(eventType), schemaVersion, delivery.StreamPosition(sequence)
	if result.ID, err = id.ParseEvent(encoded); err != nil {
		return delivery.WrappedEvent{}, delivery.ErrInvalid
	}
	if result.Wrapping, err = restoreBodyWrapping(result.Body, provider, reference, keyVersion, algorithm); err != nil || result.Wrapping == nil {
		return delivery.WrappedEvent{}, delivery.ErrInvalid
	}
	if _, exists := webhookv1.Lookup(result.Type); !exists || schemaVersion == "" {
		return delivery.WrappedEvent{}, delivery.ErrInvalid
	}

	return result, nil
}
