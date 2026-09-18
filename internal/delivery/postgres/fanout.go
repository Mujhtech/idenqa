package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	webhookv1 "github.com/Mujhtech/idenqa/contracts/webhook/v1"
	"github.com/Mujhtech/idenqa/internal/delivery"
	deliverytask "github.com/Mujhtech/idenqa/internal/delivery/task"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

// EmitEventWithin stores one canonical catalogue event exactly once per dedupe
// key inside the owning domain transaction. Fanout discovery schedules it.
func EmitEventWithin(ctx context.Context, tx platformpostgres.Transaction, event webhookv1.Event, dedupeKey string) (bool, error) {
	return EmitEventWithinWrapped(ctx, tx, event, dedupeKey, nil)
}

// EmitEventWithinWrapped stores one canonical catalogue event exactly once per
// dedupe key. A non-nil wrapping means body stores KMS-wrapped ciphertext.
func EmitEventWithinWrapped(ctx context.Context, tx platformpostgres.Transaction, event webhookv1.Event, dedupeKey string, wrapping *kms.WrappedKey) (bool, error) {
	if tx == nil || len(dedupeKey) == 0 || len(dedupeKey) > 512 {
		return false, delivery.ErrInvalid
	}
	body, err := event.Canonical()
	if err != nil {
		return false, delivery.ErrInvalid
	}
	digest, err := event.Digest()
	if err != nil {
		return false, delivery.ErrInvalid
	}
	provider, reference, keyVersion, algorithm := nullableBodyWrapping(wrapping)
	tag, err := tx.Exec(ctx, `INSERT INTO idenqa.webhook_events
		(tenant_id,id,event_type,schema_version,dedupe_key,body,body_digest,state,cursor,delivered_count,created_at,body_provider,body_reference,body_key_version,body_algorithm)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'pending','',0,$8,$9,$10,$11,$12)
		ON CONFLICT (tenant_id,dedupe_key) DO NOTHING`,
		event.TenantID, event.ID, string(event.Type), event.SchemaVersion, dedupeKey, body, digest, event.CreatedAt, provider, reference, keyVersion, algorithm)
	if err != nil {
		return false, fmt.Errorf("emit webhook event: %w", err)
	}

	return tag.RowsAffected() == 1, nil
}

// EmitCatalogueEvent builds and stores one canonical catalogue event exactly
// once per stable seed inside the owning domain transaction.
func EmitCatalogueEvent(ctx context.Context, tx platformpostgres.Transaction, tenantID, region string, eventType webhookv1.Type, seed string, occurredAt time.Time, fields map[string]any) error {
	data, err := delivery.EventData(fields)
	if err != nil {
		return err
	}
	event, err := webhookv1.NewEvent(webhookv1.DeterministicEventID(seed), tenantID, region, eventType, webhookv1.SchemaVersion, occurredAt, data)
	if err != nil {
		return err
	}
	if _, err := EmitEventWithin(ctx, tx, event, seed); err != nil {
		return err
	}

	return nil
}

// FanoutEventWithin locks one pending or completed catalogue event.
func (store *Store) FanoutEventWithin(ctx context.Context, scope tenant.Scope, tx platformpostgres.Transaction, eventID id.Event) (delivery.FanoutEvent, error) {
	if tx == nil || scope.ID().IsZero() || eventID.IsZero() {
		return delivery.FanoutEvent{}, delivery.ErrInvalid
	}
	if err := setScope(ctx, tx, scope); err != nil {
		return delivery.FanoutEvent{}, err
	}
	var eventType, schemaVersion, state, cursor string
	var body []byte
	var delivered int
	var createdAt time.Time
	var provider, reference, keyVersion, algorithm *string
	err := tx.QueryRow(ctx, `SELECT event_type,schema_version,body,state,cursor,delivered_count,created_at,body_provider,body_reference,body_key_version,body_algorithm
		FROM idenqa.webhook_events WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, scope.ID().String(), eventID.String()).
		Scan(&eventType, &schemaVersion, &body, &state, &cursor, &delivered, &createdAt, &provider, &reference, &keyVersion, &algorithm)
	if errors.Is(err, pgx.ErrNoRows) {
		return delivery.FanoutEvent{}, delivery.ErrNotFound
	}
	if err != nil {
		return delivery.FanoutEvent{}, err
	}
	wrapping, err := restoreBodyWrapping(body, provider, reference, keyVersion, algorithm)
	if err != nil {
		return delivery.FanoutEvent{}, delivery.ErrInvalid
	}
	if wrapping == nil {
		event, err := delivery.ParseFanoutEvent(eventID, scope.ID(), body, state, cursor, delivered, createdAt)
		if err != nil || string(event.Type) != eventType {
			return delivery.FanoutEvent{}, delivery.ErrInvalid
		}

		return event, nil
	}
	_ = schemaVersion

	return delivery.FanoutEvent{ID: eventID, TenantID: scope.ID(), Type: webhookv1.Type(eventType), Body: append([]byte(nil), body...), BodyWrapping: wrapping, State: delivery.EventState(state), Cursor: cursor, DeliveredCount: delivered, CreatedAt: createdAt.UTC()}, nil
}

// SubscribedEndpointsWithin pages enabled endpoints subscribed to one event
// type by opaque identifier order.
func (store *Store) SubscribedEndpointsWithin(ctx context.Context, scope tenant.Scope, tx platformpostgres.Transaction, eventType webhookv1.Type, after string, limit int) ([]id.WebhookEndpoint, error) {
	if tx == nil || scope.ID().IsZero() || limit < 1 || limit > 1024 {
		return nil, delivery.ErrInvalid
	}
	if _, exists := webhookv1.Lookup(eventType); !exists {
		return nil, delivery.ErrInvalid
	}
	if err := setScope(ctx, tx, scope); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id FROM idenqa.webhook_endpoints
		WHERE tenant_id=$1 AND disabled_at IS NULL AND id>$2 AND event_types && ARRAY[$3, '*']::text[]
		ORDER BY id LIMIT $4`, scope.ID().String(), after, string(eventType), limit)
	if err != nil {
		return nil, fmt.Errorf("load subscribed endpoints: %w", err)
	}
	defer rows.Close()
	var endpoints []id.WebhookEndpoint
	for rows.Next() {
		var encoded string
		if err := rows.Scan(&encoded); err != nil {
			return nil, err
		}
		endpoint, err := id.ParseWebhookEndpoint(encoded)
		if err != nil {
			return nil, delivery.ErrInvalid
		}
		endpoints = append(endpoints, endpoint)
	}

	return endpoints, rows.Err()
}

// CreateDeliveryIfAbsentWithin inserts an original delivery exactly once per
// endpoint and event. Repeats return false and rely on the existing intent.
func (store *Store) CreateDeliveryIfAbsentWithin(ctx context.Context, scope tenant.Scope, tx platformpostgres.Transaction, intent delivery.Intent) (bool, error) {
	if tx == nil || scope.ID().IsZero() || intent.Validate() != nil {
		return false, delivery.ErrInvalid
	}
	if err := setScope(ctx, tx, scope); err != nil {
		return false, err
	}
	provider, reference, keyVersion, algorithm := nullableBodyWrapping(intent.BodyWrapping)
	tag, err := tx.Exec(ctx, `INSERT INTO idenqa.webhook_deliveries
		(tenant_id,id,endpoint_id,event_id,event_type,body,body_digest,state,attempt_count,max_attempts,next_attempt_at,replay_of,created_at,updated_at,body_provider,body_reference,body_key_version,body_algorithm)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		ON CONFLICT (tenant_id,endpoint_id,event_id) WHERE replay_of IS NULL DO NOTHING`,
		scope.ID().String(), intent.ID.String(), intent.EndpointID.String(), intent.EventID.String(), intent.EventType, intent.Body, intent.BodyDigest, string(intent.State), intent.AttemptCount, intent.MaxAttempts, intent.NextAttemptAt, nullableDelivery(intent.ReplayOf), intent.CreatedAt, intent.UpdatedAt, provider, reference, keyVersion, algorithm)
	if err != nil {
		return false, fmt.Errorf("create fanout delivery: %w", err)
	}

	return tag.RowsAffected() == 1, nil
}

// AdvanceFanoutWithin moves the durable cursor and completes the event.
func (store *Store) AdvanceFanoutWithin(ctx context.Context, scope tenant.Scope, tx platformpostgres.Transaction, eventID id.Event, cursor string, delivered int, completed bool, at time.Time) error {
	if tx == nil || scope.ID().IsZero() || eventID.IsZero() || delivered < 0 || at.IsZero() || len(cursor) > 64 {
		return delivery.ErrInvalid
	}
	if err := setScope(ctx, tx, scope); err != nil {
		return err
	}
	state, completedAt := string(delivery.EventPending), any(nil)
	if completed {
		state, completedAt = string(delivery.EventCompleted), at
	}
	tag, err := tx.Exec(ctx, `UPDATE idenqa.webhook_events
		SET cursor=$3, delivered_count=delivered_count+$4, state=$5, completed_at=$6
		WHERE tenant_id=$1 AND id=$2 AND state='pending'`, scope.ID().String(), eventID.String(), cursor, delivered, state, completedAt)
	if err != nil {
		return fmt.Errorf("advance webhook fanout: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return delivery.ErrConflict
	}

	return nil
}

// ListReadyEvents exposes bounded identifier-only pending fanout discovery.
func (store *Store) ListReadyEvents(ctx context.Context, observedAt time.Time, batchSize int) ([]deliverytask.FanoutTarget, error) {
	if store == nil || observedAt.IsZero() || observedAt.Location() != time.UTC || batchSize < 1 || batchSize > deliverytask.CoordinationBatch {
		return nil, delivery.ErrInvalid
	}
	var targets []deliverytask.FanoutTarget
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		rows, err := tx.Query(ctx, `SELECT tenant_id,id FROM idenqa.list_ready_webhook_events($1,$2)`, observedAt, batchSize)
		if err != nil {
			return fmt.Errorf("query ready webhook events: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var target deliverytask.FanoutTarget
			var tenantValue, eventValue string
			if err := rows.Scan(&tenantValue, &eventValue); err != nil {
				return fmt.Errorf("scan ready webhook event: %w", err)
			}
			target.TenantID, err = id.ParseTenant(tenantValue)
			if err != nil {
				return delivery.ErrInvalid
			}
			target.EventID, err = id.ParseEvent(eventValue)
			if err != nil {
				return delivery.ErrInvalid
			}
			targets = append(targets, target)
		}
		return rows.Err()
	})
	return targets, err
}
