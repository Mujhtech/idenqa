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
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	retrydb "github.com/Mujhtech/idenqa/internal/platform/idempotency/postgres"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ManagementStore composes immutable catalog primitives and atomic public receipts.
type ManagementStore struct {
	*Store
	now func() time.Time
}

// NewManagementStore constructs tenant-scoped public administration persistence.
func NewManagementStore(pool transactionRunner, now func() time.Time) (*ManagementStore, error) {
	store, err := New(pool)
	if err != nil {
		return nil, err
	}
	if now == nil {
		return nil, policy.ErrInvalid
	}
	return &ManagementStore{store, now}, nil
}

type managementTransaction struct{ tx pg.Transaction }

func (bound managementTransaction) WithinTransaction(ctx context.Context, _ pg.TransactionOptions, work func(context.Context, pg.Transaction) error) error {
	return work(ctx, bound.tx)
}

// Apply commits the catalog change, replay receipt, common audit and outbox together.
func (store *ManagementStore) Apply(ctx context.Context, scope tenant.Scope, request idempotency.Request, event id.Event, command policy.Command, work func(policy.ManagedCatalog) (policy.ManagementResult, error)) (policy.ManagementResult, error) {
	if scope.ID().IsZero() || scope.ID() != request.TenantID() || event.IsZero() || work == nil {
		return policy.ManagementResult{}, policy.ErrInvalid
	}
	for range 3 {
		var result policy.ManagementResult
		err := store.pool.WithinTransaction(ctx, pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
			queries := sqlgen.New(tx)
			if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
				return err
			}
			reservation, err := retrydb.Reserve(ctx, queries, request)
			if err != nil {
				return err
			}
			if prior, ok := reservation.Result(); ok {
				if err := json.Unmarshal(prior.Body(), &result); err != nil {
					return fmt.Errorf("restore policy command receipt: %w", err)
				}
				result.Replayed = true
				return nil
			}
			bound := &ManagementStore{Store: &Store{pool: managementTransaction{tx}}, now: store.now}
			result, err = work(bound)
			if err != nil {
				return err
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				return err
			}
			receipt, err := idempotency.NewResult(200, encoded)
			if err != nil {
				return err
			}
			now := store.now().UTC().Truncate(time.Microsecond)
			if err := appendManagementAudit(ctx, tx, scope, request, event, command, result, now); err != nil {
				return err
			}
			return retrydb.Complete(ctx, queries, request, receipt, now)
		})
		if err == nil {
			return result, nil
		}
		var conflict *pgconn.PgError
		if !errors.As(err, &conflict) || (conflict.Code != "40001" && conflict.Code != "40P01") {
			return policy.ManagementResult{}, err
		}
		if ctx.Err() != nil {
			return policy.ManagementResult{}, ctx.Err()
		}
	}
	return policy.ManagementResult{}, policy.ErrActivationConflict
}
func appendManagementAudit(ctx context.Context, tx pg.Transaction, scope tenant.Scope, request idempotency.Request, event id.Event, command policy.Command, result policy.ManagementResult, now time.Time) error {
	aggregateType, version := "policy_revision", int64(result.Policy.LatestRevision)
	if result.Activation != nil {
		aggregateType, version = "policy_activation", result.Activation.Version
	}
	references := struct {
		PolicyID          string `json:"policy_id"`
		Revision          uint32 `json:"revision"`
		ActivationVersion int64  `json:"activation_version"`
		Reason            string `json:"reason,omitempty"`
	}{PolicyID: result.Policy.ID, Revision: result.Policy.LatestRevision, ActivationVersion: result.Policy.ActivationVersion, Reason: command.Reason}
	if result.Activation != nil {
		references.Revision = result.Activation.Revision
	}
	payload, err := json.Marshal(references)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(payload)
	eventType := "policy." + command.Operation + ".v1"
	if _, err := tx.Exec(ctx, `INSERT INTO idenqa.outbox_events(id,tenant_id,aggregate_type,aggregate_id,aggregate_version,event_type,schema_version,payload,occurred_at,created_at) VALUES($1,$2,$3,$4,$5,$6,1,$7,$8,$8)`, event.String(), scope.ID().String(), aggregateType, result.Policy.ID, version, eventType, string(payload), now); err != nil {
		return fmt.Errorf("append policy administration outbox: %w", err)
	}
	_, err = auditpostgres.AppendInTransaction(ctx, tx, scope, auditpostgres.Event{EventID: event.String(), EventType: eventType, AggregateID: result.Policy.ID, ActorID: request.Principal().String(), EventDigest: hex.EncodeToString(digest[:]), OccurredAt: now})
	return err
}
func (store *ManagementStore) scoped(ctx context.Context, scope tenant.Scope, readOnly bool, work func(context.Context, pg.Transaction) error) error {
	if scope.ID().IsZero() {
		return policy.ErrInvalid
	}
	return store.pool.WithinTransaction(ctx, pg.TransactionOptions{ReadOnly: readOnly}, func(ctx context.Context, tx pg.Transaction) error {
		if _, err := sqlgen.New(tx).SetTenantScope(ctx, scope.ID().String()); err != nil {
			return err
		}
		return work(ctx, tx)
	})
}

// Lock serializes public revision numbering and activation against the policy root.
func (store *ManagementStore) Lock(ctx context.Context, scope tenant.Scope, identifier id.Policy) error {
	return store.scoped(ctx, scope, false, func(ctx context.Context, tx pg.Transaction) error {
		var found string
		err := tx.QueryRow(ctx, `SELECT id FROM idenqa.policies WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, scope.ID().String(), identifier.String()).Scan(&found)
		if errors.Is(err, pgx.ErrNoRows) {
			return policy.ErrRevisionNotFound
		}
		return err
	})
}

// WasActivated requires rollback targets to have an activation in this tenant's history.
func (store *ManagementStore) WasActivated(ctx context.Context, scope tenant.Scope, identifier id.Policy, revision uint32) (bool, error) {
	var exists bool
	err := store.scoped(ctx, scope, true, func(ctx context.Context, tx pg.Transaction) error {
		return tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM idenqa.policy_activations WHERE tenant_id=$1 AND policy_id=$2 AND revision=$3)`, scope.ID().String(), identifier.String(), int64(revision)).Scan(&exists)
	})
	return exists, err
}

const summarySelect = `SELECT p.id,COALESCE((SELECT max(r.revision) FROM idenqa.policy_revisions r WHERE r.tenant_id=p.tenant_id AND r.policy_id=p.id),0),p.active_revision,p.activation_version,p.created_at,CASE WHEN p.activation_version>0 THEN p.updated_at END FROM idenqa.policies p `

type managementScanner interface{ Scan(...any) error }

func scanSummary(row managementScanner) (policy.Summary, error) {
	var result policy.Summary
	err := row.Scan(&result.ID, &result.LatestRevision, &result.ActiveRevision, &result.ActivationVersion, &result.CreatedAt, &result.ActivatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, policy.ErrRevisionNotFound
	}
	if err != nil {
		return result, err
	}
	result.CreatedAt = result.CreatedAt.UTC()
	if result.ActivatedAt != nil {
		at := result.ActivatedAt.UTC()
		result.ActivatedAt = &at
	}
	return result, nil
}

// Inspect reads metadata without canonical policy expressions.
func (store *ManagementStore) Inspect(ctx context.Context, scope tenant.Scope, identifier id.Policy) (policy.Summary, error) {
	var result policy.Summary
	err := store.scoped(ctx, scope, true, func(ctx context.Context, tx pg.Transaction) error {
		var err error
		result, err = scanSummary(tx.QueryRow(ctx, summarySelect+`WHERE p.tenant_id=$1 AND p.id=$2`, scope.ID().String(), identifier.String()))
		return err
	})
	return result, err
}

// List provides one bounded descending identifier page.
func (store *ManagementStore) List(ctx context.Context, scope tenant.Scope, before string, limit int) ([]policy.Summary, error) {
	if limit < 1 || limit > 101 {
		return nil, policy.ErrInvalid
	}
	result := []policy.Summary{}
	err := store.scoped(ctx, scope, true, func(ctx context.Context, tx pg.Transaction) error {
		rows, err := tx.Query(ctx, summarySelect+`WHERE p.tenant_id=$1 AND ($2='' OR p.id<$2) ORDER BY p.id DESC LIMIT $3`, scope.ID().String(), before, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanSummary(rows)
			if err != nil {
				return err
			}
			result = append(result, item)
		}
		return rows.Err()
	})
	return result, err
}
