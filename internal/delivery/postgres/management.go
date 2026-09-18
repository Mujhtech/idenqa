package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	auditpostgres "github.com/Mujhtech/idenqa/internal/audit/postgres"
	"github.com/Mujhtech/idenqa/internal/delivery"
	deliverytask "github.com/Mujhtech/idenqa/internal/delivery/task"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	idempotencypostgres "github.com/Mujhtech/idenqa/internal/platform/idempotency/postgres"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5/pgconn"
)

// ManagementEnqueuer joins deliberate replay intent to the administrative commit.
type ManagementEnqueuer interface {
	EnqueueTx(context.Context, pg.Transaction, ...platformtask.Intent) error
}

// ManagementStore extends safe inspection with atomic administration/audit/task effects.
type ManagementStore struct {
	*Store
	enqueuer    ManagementEnqueuer
	identifiers deliverytask.IdentifierGenerator
	now         func() time.Time
}

// NewManagementStore composes transaction-bound Manager persistence with replay scheduling.
func NewManagementStore(pool transactionRunner, enqueuer ManagementEnqueuer, identifiers deliverytask.IdentifierGenerator, now func() time.Time) (*ManagementStore, error) {
	store, err := New(pool)
	if err != nil {
		return nil, err
	}
	if enqueuer == nil || identifiers == nil || now == nil {
		return nil, delivery.ErrInvalid
	}
	return &ManagementStore{Store: store, enqueuer: enqueuer, identifiers: identifiers, now: now}, nil
}

type boundTransaction struct{ tx pg.Transaction }

func (bound boundTransaction) WithinTransaction(ctx context.Context, _ pg.TransactionOptions, work func(context.Context, pg.Transaction) error) error {
	return work(ctx, bound.tx)
}

// ApplyManagement retains only safe metadata. Plaintext is returned by the first
// successful commit and cleared on rollback or conflict; retries never unwrap it.
func (store *ManagementStore) ApplyManagement(ctx context.Context, scope tenant.Scope, request idempotency.Request, event id.Event, command delivery.ManagementCommand, work func(delivery.Repository) (delivery.ManagementResult, []byte, error)) (delivery.ManagementResult, []byte, error) {
	if scope.ID().IsZero() || request.TenantID() != scope.ID() || event.IsZero() || work == nil {
		return delivery.ManagementResult{}, nil, delivery.ErrInvalid
	}
	for attempt := 0; attempt < 3; attempt++ {
		var result delivery.ManagementResult
		var secret []byte
		err := store.pool.WithinTransaction(ctx, pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
			if err := setScope(ctx, tx, scope); err != nil {
				return err
			}
			queries := sqlgen.New(tx)
			reservation, err := idempotencypostgres.Reserve(ctx, queries, request)
			if err != nil {
				return err
			}
			if replay, found := reservation.Result(); found {
				if err := json.Unmarshal(replay.Body(), &result); err != nil {
					return fmt.Errorf("restore webhook command receipt: %w", err)
				}
				result.Replayed = true
				return nil
			}
			bound := &Store{pool: boundTransaction{tx}}
			result, secret, err = work(bound)
			if err != nil {
				return err
			}
			now := store.now().UTC().Truncate(time.Microsecond)
			if result.Delivery != nil {
				identifier, err := id.ParseDelivery(result.Delivery.ID)
				if err != nil {
					return err
				}
				intent, err := deliverytask.NewAttemptIntent(store.identifiers, scope, identifier, 1, result.Delivery.NextAttemptAt, result.Delivery.CreatedAt.Add(deliverytask.MaximumDeliveryDuration))
				if err != nil {
					return err
				}
				if err := store.enqueuer.EnqueueTx(ctx, tx, intent); err != nil {
					return fmt.Errorf("enqueue webhook replay: %w", err)
				}
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				return err
			}
			replay, err := idempotency.NewResult(200, encoded)
			if err != nil {
				return err
			}
			if err := managementAudit(ctx, tx, scope, request, event, command, result, now); err != nil {
				return err
			}
			return idempotencypostgres.Complete(ctx, queries, request, replay, now)
		})
		if err == nil {
			return result, secret, nil
		}
		clear(secret)
		var databaseError *pgconn.PgError
		if !errors.As(err, &databaseError) || (databaseError.Code != "40001" && databaseError.Code != "40P01") {
			return delivery.ManagementResult{}, nil, err
		}
		if ctx.Err() != nil {
			return delivery.ManagementResult{}, nil, ctx.Err()
		}
	}
	return delivery.ManagementResult{}, nil, delivery.ErrConflict
}

func managementAudit(ctx context.Context, tx pg.Transaction, scope tenant.Scope, request idempotency.Request, event id.Event, command delivery.ManagementCommand, result delivery.ManagementResult, now time.Time) error {
	aggregateType, aggregateID, version := "webhook_endpoint", "", int64(1)
	if result.Endpoint != nil {
		aggregateID, version = result.Endpoint.ID, result.Endpoint.Version
	}
	if result.Delivery != nil {
		aggregateType, aggregateID = "webhook_delivery", result.Delivery.ID
	}
	if aggregateID == "" {
		return delivery.ErrInvalid
	}
	references := struct {
		EndpointID string `json:"endpoint_id,omitempty"`
		DeliveryID string `json:"delivery_id,omitempty"`
		ReplayOf   string `json:"replay_of,omitempty"`
		Reason     string `json:"reason,omitempty"`
	}{ReplayOf: command.DeliveryID, Reason: command.Reason}
	if result.Endpoint != nil {
		references.EndpointID = result.Endpoint.ID
	}
	if result.Delivery != nil {
		references.EndpointID = result.Delivery.EndpointID
		references.DeliveryID = result.Delivery.ID
	}
	payload, err := json.Marshal(references)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(payload)
	eventType := "webhook." + command.Operation + ".v1"
	if _, err := tx.Exec(ctx, `INSERT INTO idenqa.outbox_events (id,tenant_id,aggregate_type,aggregate_id,aggregate_version,event_type,schema_version,payload,occurred_at,created_at) VALUES ($1,$2,$3,$4,$5,$6,1,$7,$8,$8)`, event.String(), scope.ID().String(), aggregateType, aggregateID, version, eventType, string(payload), now); err != nil {
		return fmt.Errorf("append webhook administrative outbox: %w", err)
	}
	_, err = auditpostgres.AppendInTransaction(ctx, tx, scope, auditpostgres.Event{EventID: event.String(), EventType: eventType, AggregateID: aggregateID, ActorID: request.Principal().String(), EventDigest: hex.EncodeToString(digest[:]), OccurredAt: now})
	return err
}
