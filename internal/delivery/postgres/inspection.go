package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/delivery"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

const endpointColumns = `id,url,event_types,schema_version,version,secret_version,previous_secret_valid_until,disabled_at,COALESCE(disabled_reason,''),created_at,updated_at`
const deliveryColumns = `id,endpoint_id,event_id,event_type,state,attempt_count,max_attempts,next_attempt_at,delivered_at,COALESCE(replay_of,''),created_at,updated_at`

type scanner interface{ Scan(...any) error }

func scanEndpoint(row scanner) (delivery.EndpointView, error) {
	var view delivery.EndpointView
	err := row.Scan(&view.ID, &view.URL, &view.EventTypes, &view.SchemaVersion, &view.Version, &view.SecretVersion, &view.PreviousValidUntil, &view.DisabledAt, &view.DisabledReason, &view.CreatedAt, &view.UpdatedAt)
	view.CreatedAt, view.UpdatedAt = view.CreatedAt.UTC(), view.UpdatedAt.UTC()
	utcPointer(view.PreviousValidUntil)
	utcPointer(view.DisabledAt)
	return view, inspectionError(err)
}
func scanDelivery(row scanner) (delivery.View, error) {
	var view delivery.View
	err := row.Scan(&view.ID, &view.EndpointID, &view.EventID, &view.EventType, &view.State, &view.AttemptCount, &view.MaxAttempts, &view.NextAttemptAt, &view.DeliveredAt, &view.ReplayOf, &view.CreatedAt, &view.UpdatedAt)
	view.NextAttemptAt, view.CreatedAt, view.UpdatedAt = view.NextAttemptAt.UTC(), view.CreatedAt.UTC(), view.UpdatedAt.UTC()
	utcPointer(view.DeliveredAt)
	return view, inspectionError(err)
}
func utcPointer(value *time.Time) {
	if value != nil {
		*value = value.UTC()
	}
}
func inspectionError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return delivery.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read webhook metadata: %w", err)
	}
	return nil
}

// Endpoints lists safe metadata using a stable ascending opaque identifier position.
func (store *Store) Endpoints(ctx context.Context, scope tenant.Scope, after string, limit int) ([]delivery.EndpointView, error) {
	if limit < 1 || limit > 101 {
		return nil, delivery.ErrInvalid
	}
	result := []delivery.EndpointView{}
	err := store.read(ctx, scope, func(ctx context.Context, tx pg.Transaction) error {
		rows, err := tx.Query(ctx, `SELECT `+endpointColumns+` FROM idenqa.webhook_endpoints WHERE tenant_id=$1 AND id>$2 ORDER BY id LIMIT $3`, scope.ID().String(), after, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			view, err := scanEndpoint(rows)
			if err != nil {
				return err
			}
			result = append(result, view)
		}
		return rows.Err()
	})
	return result, err
}

// Endpoint reads one endpoint without loading its wrapped keys.
func (store *Store) Endpoint(ctx context.Context, scope tenant.Scope, identifier id.WebhookEndpoint) (delivery.EndpointView, error) {
	var result delivery.EndpointView
	err := store.read(ctx, scope, func(ctx context.Context, tx pg.Transaction) error {
		var err error
		result, err = scanEndpoint(tx.QueryRow(ctx, `SELECT `+endpointColumns+` FROM idenqa.webhook_endpoints WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), identifier.String()))
		return err
	})
	return result, err
}

// Deliveries lists response-free event metadata for one visible endpoint.
func (store *Store) Deliveries(ctx context.Context, scope tenant.Scope, endpoint id.WebhookEndpoint, after string, limit int) ([]delivery.View, error) {
	if limit < 1 || limit > 101 {
		return nil, delivery.ErrInvalid
	}
	result := []delivery.View{}
	err := store.read(ctx, scope, func(ctx context.Context, tx pg.Transaction) error {
		var found string
		if err := tx.QueryRow(ctx, `SELECT id FROM idenqa.webhook_endpoints WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), endpoint.String()).Scan(&found); err != nil {
			return inspectionError(err)
		}
		rows, err := tx.Query(ctx, `SELECT `+deliveryColumns+` FROM idenqa.webhook_deliveries WHERE tenant_id=$1 AND endpoint_id=$2 AND id>$3 ORDER BY id LIMIT $4`, scope.ID().String(), endpoint.String(), after, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			view, err := scanDelivery(rows)
			if err != nil {
				return err
			}
			result = append(result, view)
		}
		return rows.Err()
	})
	return result, err
}

// Delivery returns safe delivery state without loading its payload.
func (store *Store) Delivery(ctx context.Context, scope tenant.Scope, identifier id.Delivery) (delivery.View, error) {
	var result delivery.View
	err := store.read(ctx, scope, func(ctx context.Context, tx pg.Transaction) error {
		var err error
		result, err = scanDelivery(tx.QueryRow(ctx, `SELECT `+deliveryColumns+` FROM idenqa.webhook_deliveries WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), identifier.String()))
		return err
	})
	return result, err
}

// Attempts returns at most the schema's twenty immutable logical attempts.
func (store *Store) Attempts(ctx context.Context, scope tenant.Scope, identifier id.Delivery) ([]delivery.AttemptView, error) {
	result := []delivery.AttemptView{}
	err := store.read(ctx, scope, func(ctx context.Context, tx pg.Transaction) error {
		var found string
		if err := tx.QueryRow(ctx, `SELECT id FROM idenqa.webhook_deliveries WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), identifier.String()).Scan(&found); err != nil {
			return inspectionError(err)
		}
		rows, err := tx.Query(ctx, `SELECT attempt_number,secret_version,status_code,error_class,retry_after_ms,response_body,response_truncated,completed_at FROM idenqa.webhook_delivery_attempts WHERE tenant_id=$1 AND delivery_id=$2 ORDER BY attempt_number LIMIT 20`, scope.ID().String(), identifier.String())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var view delivery.AttemptView
			if err := rows.Scan(&view.Number, &view.SecretVersion, &view.StatusCode, &view.ErrorClass, &view.RetryAfterMS, &view.ResponseBody, &view.ResponseTruncated, &view.CompletedAt); err != nil {
				return err
			}
			view.CompletedAt = view.CompletedAt.UTC()
			result = append(result, view)
		}
		return rows.Err()
	})
	return result, err
}
