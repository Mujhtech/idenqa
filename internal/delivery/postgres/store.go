// Package postgres persists tenant webhook endpoints and delivery history.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/delivery"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

type transactionRunner interface {
	WithinTransaction(context.Context, platformpostgres.TransactionOptions, func(context.Context, platformpostgres.Transaction) error) error
}

// Store is the PostgreSQL delivery adapter.
type Store struct{ pool transactionRunner }

// New constructs a PostgreSQL delivery store.
func New(pool transactionRunner) (*Store, error) {
	if pool == nil {
		return nil, errors.New("delivery postgres: pool is required")
	}
	return &Store{pool: pool}, nil
}

// CreateEndpoint atomically stores endpoint metadata and its first wrapped secret.
func (store *Store) CreateEndpoint(ctx context.Context, scope tenant.Scope, endpoint delivery.Endpoint) error {
	if endpoint.Validate() != nil {
		return delivery.ErrInvalid
	}
	return store.write(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction) error {
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.webhook_endpoints
			(tenant_id,id,url,secret_version,version,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			scope.ID().String(), endpoint.ID.String(), endpoint.URL, endpoint.Active.Version, endpoint.Version, endpoint.CreatedAt, endpoint.UpdatedAt)
		if err != nil {
			return fmt.Errorf("insert webhook endpoint: %w", err)
		}
		return insertSecret(ctx, tx, scope, endpoint.ID, endpoint.Active)
	})
}

// UpdateEndpoint applies one optimistic rotation or disablement transition.
func (store *Store) UpdateEndpoint(ctx context.Context, scope tenant.Scope, endpoint delivery.Endpoint, expected int64) error {
	if endpoint.Validate() != nil || expected == 0 || endpoint.Version != expected+1 {
		return delivery.ErrInvalid
	}
	return store.write(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction) error {
		var activeVersion int64
		if err := tx.QueryRow(ctx, `SELECT secret_version FROM idenqa.webhook_endpoints WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, scope.ID().String(), endpoint.ID.String()).Scan(&activeVersion); errors.Is(err, pgx.ErrNoRows) {
			return delivery.ErrNotFound
		} else if err != nil {
			return err
		}
		if activeVersion != endpoint.Active.Version {
			if err := insertSecret(ctx, tx, scope, endpoint.ID, endpoint.Active); err != nil {
				return err
			}
		}
		var previousVersion any
		var previousUntil any
		if endpoint.Previous != nil {
			previousVersion, previousUntil = endpoint.Previous.Version, endpoint.PreviousValidUntil
		}
		tag, err := tx.Exec(ctx, `UPDATE idenqa.webhook_endpoints SET url=$3,secret_version=$4,
			previous_secret_version=$5,previous_secret_valid_until=$6,disabled_at=$7,disabled_reason=$8,
			version=$9,updated_at=$10 WHERE tenant_id=$1 AND id=$2 AND version=$11`, scope.ID().String(), endpoint.ID.String(), endpoint.URL,
			endpoint.Active.Version, previousVersion, previousUntil, nullableTime(endpoint.DisabledAt), nullableString(endpoint.DisabledReason), endpoint.Version, endpoint.UpdatedAt, expected)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return delivery.ErrConflict
		}
		return nil
	})
}

// FindEndpoint restores only an endpoint visible in scope.
func (store *Store) FindEndpoint(ctx context.Context, scope tenant.Scope, endpointID id.WebhookEndpoint) (delivery.Endpoint, error) {
	var endpoint delivery.Endpoint
	err := store.read(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction) error {
		var activeVersion int64
		var previousVersion *int64
		var previousUntil, disabledAt *time.Time
		var disabledReason *string
		var version int64
		err := tx.QueryRow(ctx, `SELECT url,secret_version,previous_secret_version,previous_secret_valid_until,disabled_at,disabled_reason,version,created_at,updated_at
			FROM idenqa.webhook_endpoints WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), endpointID.String()).Scan(&endpoint.URL, &activeVersion, &previousVersion, &previousUntil, &disabledAt, &disabledReason, &version, &endpoint.CreatedAt, &endpoint.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return delivery.ErrNotFound
		}
		if err != nil || activeVersion <= 0 || version <= 0 {
			return errors.Join(delivery.ErrInvalid, err)
		}
		endpoint.ID, endpoint.Version = endpointID, version
		endpoint.Active, err = findSecret(ctx, tx, scope, endpointID, activeVersion)
		if err != nil {
			return err
		}
		if previousVersion != nil {
			previous, findErr := findSecret(ctx, tx, scope, endpointID, *previousVersion)
			if findErr != nil || previousUntil == nil {
				return errors.Join(delivery.ErrInvalid, findErr)
			}
			endpoint.Previous, endpoint.PreviousValidUntil = &previous, previousUntil.UTC()
		}
		if disabledAt != nil {
			endpoint.DisabledAt = disabledAt.UTC()
		}
		if disabledReason != nil {
			endpoint.DisabledReason = *disabledReason
		}
		return endpoint.Validate()
	})
	return endpoint, err
}

// CreateDelivery stores one immutable event payload and pending lifecycle.
func (store *Store) CreateDelivery(ctx context.Context, scope tenant.Scope, intent delivery.Intent) error {
	if intent.Validate() != nil {
		return delivery.ErrInvalid
	}
	return store.write(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction) error {
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.webhook_deliveries
			(tenant_id,id,endpoint_id,event_id,event_type,body,body_digest,state,attempt_count,max_attempts,next_attempt_at,replay_of,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, scope.ID().String(), intent.ID.String(), intent.EndpointID.String(), intent.EventID.String(), intent.EventType, intent.Body, intent.BodyDigest, string(intent.State), intent.AttemptCount, intent.MaxAttempts, intent.NextAttemptAt, nullableDelivery(intent.ReplayOf), intent.CreatedAt, intent.UpdatedAt)
		return err
	})
}

// FindDelivery restores only a delivery visible in scope.
func (store *Store) FindDelivery(ctx context.Context, scope tenant.Scope, deliveryID id.Delivery) (delivery.Intent, error) {
	var result delivery.Intent
	err := store.read(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction) error {
		var endpointID, eventID string
		var replayOf *string
		var state string
		var attempts, maximum int32
		var deliveredAt *time.Time
		err := tx.QueryRow(ctx, `SELECT endpoint_id,event_id,event_type,body,body_digest,state,attempt_count,max_attempts,next_attempt_at,delivered_at,replay_of,created_at,updated_at
			FROM idenqa.webhook_deliveries WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), deliveryID.String()).Scan(&endpointID, &eventID, &result.EventType, &result.Body, &result.BodyDigest, &state, &attempts, &maximum, &result.NextAttemptAt, &deliveredAt, &replayOf, &result.CreatedAt, &result.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return delivery.ErrNotFound
		}
		if err != nil || attempts < 0 || maximum <= 0 {
			return errors.Join(delivery.ErrInvalid, err)
		}
		result.ID, result.State, result.AttemptCount, result.MaxAttempts = deliveryID, delivery.State(state), attempts, maximum
		result.EndpointID, err = id.ParseWebhookEndpoint(endpointID)
		if err != nil {
			return delivery.ErrInvalid
		}
		result.EventID, err = id.ParseEvent(eventID)
		if err != nil {
			return delivery.ErrInvalid
		}
		if deliveredAt != nil {
			result.DeliveredAt = deliveredAt.UTC()
		}
		if replayOf != nil {
			result.ReplayOf, err = id.ParseDelivery(*replayOf)
			if err != nil {
				return delivery.ErrInvalid
			}
		}
		return result.Validate()
	})
	return result, err
}

// RecordAttemptWithin atomically appends safe attempt metadata and advances state.
func (store *Store) RecordAttemptWithin(ctx context.Context, scope tenant.Scope, tx platformpostgres.Transaction, deliveryID id.Delivery, attempt delivery.Attempt, succeeded, retry bool, next time.Time) error {
	if tx == nil || deliveryID.IsZero() || attempt.Number == 0 || attempt.SecretVersion == 0 || attempt.CompletedAt.IsZero() {
		return delivery.ErrInvalid
	}
	if err := setScope(ctx, tx, scope); err != nil {
		return err
	}
	var current, maximum int32
	var state string
	if err := tx.QueryRow(ctx, `SELECT attempt_count,max_attempts,state FROM idenqa.webhook_deliveries WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, scope.ID().String(), deliveryID.String()).Scan(&current, &maximum, &state); errors.Is(err, pgx.ErrNoRows) {
		return delivery.ErrNotFound
	} else if err != nil {
		return err
	}
	if state != string(delivery.StatePending) || current+1 != attempt.Number {
		return delivery.ErrConflict
	}
	diagnostic := attempt.Diagnostic
	_, err := tx.Exec(ctx, `INSERT INTO idenqa.webhook_delivery_attempts
		(tenant_id,delivery_id,attempt_number,secret_version,signature_timestamp,status_code,error_class,retry_after_ms,completed_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, scope.ID().String(), deliveryID.String(), attempt.Number, attempt.SecretVersion, attempt.SignatureTimestamp, diagnostic.StatusCode, diagnostic.ErrorClass, diagnostic.RetryAfter.Milliseconds(), attempt.CompletedAt)
	if err != nil {
		return err
	}
	nextState := delivery.StateExhausted
	var delivered any
	if succeeded {
		nextState, delivered = delivery.StateDelivered, attempt.CompletedAt
	} else if retry && attempt.Number < maximum {
		nextState = delivery.StatePending
	}
	_, err = tx.Exec(ctx, `UPDATE idenqa.webhook_deliveries SET state=$3,attempt_count=$4,next_attempt_at=$5,delivered_at=$6,updated_at=$7 WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), deliveryID.String(), string(nextState), attempt.Number, next, delivered, attempt.CompletedAt)
	return err
}

// RecordAttempt atomically appends one response-free attempt and advances delivery state.
func (store *Store) RecordAttempt(ctx context.Context, scope tenant.Scope, deliveryID id.Delivery, attempt delivery.Attempt, succeeded, retry bool, next time.Time) error {
	return store.write(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction) error {
		return store.RecordAttemptWithin(ctx, scope, tx, deliveryID, attempt, succeeded, retry, next)
	})
}

func insertSecret(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope, endpointID id.WebhookEndpoint, secret delivery.Secret) error {
	record := secret.Wrapped.Record()
	_, err := tx.Exec(ctx, `INSERT INTO idenqa.webhook_secrets
		(tenant_id,endpoint_id,version,provider,reference,key_version,algorithm,ciphertext,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, scope.ID().String(), endpointID.String(), secret.Version, record.Provider, record.Reference, record.Version, record.Algorithm, record.Ciphertext, secret.CreatedAt)
	return err
}

func findSecret(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope, endpointID id.WebhookEndpoint, version int64) (delivery.Secret, error) {
	var record kms.WrappedKeyRecord
	var created time.Time
	err := tx.QueryRow(ctx, `SELECT provider,reference,key_version,algorithm,ciphertext,created_at FROM idenqa.webhook_secrets WHERE tenant_id=$1 AND endpoint_id=$2 AND version=$3`, scope.ID().String(), endpointID.String(), version).Scan(&record.Provider, &record.Reference, &record.Version, &record.Algorithm, &record.Ciphertext, &created)
	if err != nil {
		return delivery.Secret{}, err
	}
	wrapped, err := kms.NewWrappedKey(record)
	return delivery.Secret{Version: version, Wrapped: wrapped, CreatedAt: created.UTC()}, err
}

func (store *Store) write(ctx context.Context, scope tenant.Scope, work func(context.Context, platformpostgres.Transaction) error) error {
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		return work(ctx, tx)
	})
}

func (store *Store) read(ctx context.Context, scope tenant.Scope, work func(context.Context, platformpostgres.Transaction) error) error {
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		return work(ctx, tx)
	})
}

func setScope(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope) error {
	if scope.ID().IsZero() {
		return delivery.ErrInvalid
	}
	var value string
	return tx.QueryRow(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, scope.ID().String()).Scan(&value)
}

func nullableDelivery(value id.Delivery) any {
	if value.IsZero() {
		return nil
	}
	return value.String()
}
func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

var _ delivery.Repository = (*Store)(nil)
