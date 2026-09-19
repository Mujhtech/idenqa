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
			(tenant_id,id,url,event_types,schema_version,secret_version,version,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			scope.ID().String(), endpoint.ID.String(), endpoint.URL, endpoint.EventTypes, endpoint.SchemaVersion, endpoint.Active.Version, endpoint.Version, endpoint.CreatedAt, endpoint.UpdatedAt)
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
		tag, err := tx.Exec(ctx, `UPDATE idenqa.webhook_endpoints SET url=$3,event_types=$4,schema_version=$5,secret_version=$6,
			previous_secret_version=$7,previous_secret_valid_until=$8,disabled_at=$9,disabled_reason=$10,
			version=$11,updated_at=$12 WHERE tenant_id=$1 AND id=$2 AND version=$13`, scope.ID().String(), endpoint.ID.String(), endpoint.URL,
			endpoint.EventTypes, endpoint.SchemaVersion, endpoint.Active.Version, previousVersion, previousUntil, nullableTime(endpoint.DisabledAt), nullableString(endpoint.DisabledReason), endpoint.Version, endpoint.UpdatedAt, expected)
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
		err := tx.QueryRow(ctx, `SELECT url,event_types,schema_version,secret_version,previous_secret_version,previous_secret_valid_until,disabled_at,disabled_reason,version,created_at,updated_at
			FROM idenqa.webhook_endpoints WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), endpointID.String()).Scan(&endpoint.URL, &endpoint.EventTypes, &endpoint.SchemaVersion, &activeVersion, &previousVersion, &previousUntil, &disabledAt, &disabledReason, &version, &endpoint.CreatedAt, &endpoint.UpdatedAt)
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
	return store.write(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction) error {
		return store.CreateDeliveryWithin(ctx, scope, tx, intent)
	})
}

// CreateDeliveryWithin joins delivery intent creation to its caller's transaction.
func (store *Store) CreateDeliveryWithin(ctx context.Context, scope tenant.Scope, tx platformpostgres.Transaction, intent delivery.Intent) error {
	if tx == nil || intent.Validate() != nil {
		return delivery.ErrInvalid
	}
	if err := setScope(ctx, tx, scope); err != nil {
		return err
	}
	provider, reference, keyVersion, algorithm := nullableBodyWrapping(intent.BodyWrapping)
	_, err := tx.Exec(ctx, `INSERT INTO idenqa.webhook_deliveries
	  (tenant_id,id,endpoint_id,event_id,event_type,body,body_digest,state,attempt_count,max_attempts,next_attempt_at,replay_of,created_at,updated_at,body_provider,body_reference,body_key_version,body_algorithm,payload_expires_at,retain_until)
	  VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`, scope.ID().String(), intent.ID.String(), intent.EndpointID.String(), intent.EventID.String(), intent.EventType, intent.Body, intent.BodyDigest, string(intent.State), intent.AttemptCount, intent.MaxAttempts, intent.NextAttemptAt, nullableDelivery(intent.ReplayOf), intent.CreatedAt, intent.UpdatedAt, provider, reference, keyVersion, algorithm, intent.CreatedAt.Add(7*24*time.Hour), intent.CreatedAt.Add(365*24*time.Hour))
	return err
}

func nullableBodyWrapping(wrapping *kms.WrappedKey) (any, any, any, any) {
	if wrapping == nil {
		return nil, nil, nil, nil
	}
	record := wrapping.Record()

	return record.Provider, record.Reference, record.Version, record.Algorithm
}

func restoreBodyWrapping(body []byte, provider, reference, keyVersion, algorithm *string) (*kms.WrappedKey, error) {
	present := 0
	for _, value := range []*string{provider, reference, keyVersion, algorithm} {
		if value != nil {
			present++
		}
	}
	if present == 0 {
		return nil, nil
	}
	if present != 4 {
		return nil, delivery.ErrInvalid
	}
	wrapped, err := kms.NewWrappedKey(kms.WrappedKeyRecord{Provider: *provider, Reference: *reference, Version: *keyVersion, Algorithm: *algorithm, Ciphertext: body})
	if err != nil {
		return nil, delivery.ErrInvalid
	}

	return &wrapped, nil
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
		var provider, reference, keyVersion, algorithm *string
		err := tx.QueryRow(ctx, `SELECT endpoint_id,event_id,event_type,body,body_digest,state,attempt_count,max_attempts,next_attempt_at,delivered_at,replay_of,created_at,updated_at,body_provider,body_reference,body_key_version,body_algorithm
			FROM idenqa.webhook_deliveries WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), deliveryID.String()).Scan(&endpointID, &eventID, &result.EventType, &result.Body, &result.BodyDigest, &state, &attempts, &maximum, &result.NextAttemptAt, &deliveredAt, &replayOf, &result.CreatedAt, &result.UpdatedAt, &provider, &reference, &keyVersion, &algorithm)
		if errors.Is(err, pgx.ErrNoRows) {
			return delivery.ErrNotFound
		}
		if err != nil || attempts < 0 || maximum <= 0 {
			return errors.Join(delivery.ErrInvalid, err)
		}
		if len(result.Body) == 0 {
			return delivery.ErrExpired
		}
		result.BodyWrapping, err = restoreBodyWrapping(result.Body, provider, reference, keyVersion, algorithm)
		if err != nil {
			return delivery.ErrInvalid
		}
		result.ID, result.State, result.AttemptCount, result.MaxAttempts = deliveryID, delivery.State(state), attempts, maximum
		result.CreatedAt, result.UpdatedAt, result.NextAttemptAt = result.CreatedAt.UTC(), result.UpdatedAt.UTC(), result.NextAttemptAt.UTC()
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

// RecordAttemptWithin atomically appends bounded attempt metadata and advances state.
func (store *Store) RecordAttemptWithin(ctx context.Context, scope tenant.Scope, tx platformpostgres.Transaction, deliveryID id.Delivery, attempt delivery.Attempt, succeeded, retry bool, next time.Time) error {
	if tx == nil || deliveryID.IsZero() || attempt.Number < 1 || attempt.SecretVersion < 1 || attempt.CompletedAt.IsZero() || attempt.CompletedAt.Location() != time.UTC || next.IsZero() || next.Before(attempt.CompletedAt) || (succeeded && retry) {
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
	if err := diagnostic.Validate(); err != nil {
		return delivery.ErrInvalid
	}
	_, err := tx.Exec(ctx, `INSERT INTO idenqa.webhook_delivery_attempts
		(tenant_id,delivery_id,attempt_number,secret_version,signature_timestamp,status_code,error_class,retry_after_ms,response_body,response_truncated,completed_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, scope.ID().String(), deliveryID.String(), attempt.Number, attempt.SecretVersion, attempt.SignatureTimestamp, diagnostic.StatusCode, diagnostic.ErrorClass, diagnostic.RetryAfter.Milliseconds(), nullableString(diagnostic.Excerpt), diagnostic.Truncated, attempt.CompletedAt)
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

// RecordAttempt atomically appends one bounded attempt record and advances delivery state.
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

// FinishWithin cancels disabled deliveries or exhausts elapsed delivery windows.
// The caller must commit this mutation with the current task effect fence.
func (store *Store) FinishWithin(ctx context.Context, scope tenant.Scope, tx platformpostgres.Transaction, deliveryID id.Delivery, expected int32, state delivery.State, at time.Time) error {
	if tx == nil || deliveryID.IsZero() || expected < 0 || (state != delivery.StateCancelled && state != delivery.StateExhausted) || at.IsZero() {
		return delivery.ErrInvalid
	}
	if err := setScope(ctx, tx, scope); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE idenqa.webhook_deliveries SET state=$3,updated_at=$4
 WHERE tenant_id=$1 AND id=$2 AND state='pending' AND attempt_count=$5`, scope.ID().String(), deliveryID.String(), string(state), at, expected)
	if err != nil {
		return fmt.Errorf("finish webhook delivery: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return delivery.ErrConflict
	}
	return nil
}
