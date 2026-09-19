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
	"github.com/Mujhtech/idenqa/internal/model"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	retrydb "github.com/Mujhtech/idenqa/internal/platform/idempotency/postgres"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type registryTransaction interface {
	WithinTransaction(context.Context, pg.TransactionOptions, func(context.Context, pg.Transaction) error) error
}

// RegistryStore commits tenant-scoped revision and deployment history atomically.
type RegistryStore struct {
	pool registryTransaction
	now  func() time.Time
}

// NewRegistryStore accepts the owned transaction boundary and deterministic clock.
func NewRegistryStore(pool registryTransaction, now func() time.Time) (*RegistryStore, error) {
	if pool == nil || now == nil {
		return nil, model.ErrRegistryInvalid
	}
	return &RegistryStore{pool, now}, nil
}

func (store *RegistryStore) readScoped(ctx context.Context, scope tenant.Scope, work func(context.Context, pg.Transaction) error) error {
	if scope.ID().IsZero() {
		return model.ErrRegistryInvalid
	}
	return store.pool.WithinTransaction(ctx, pg.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx pg.Transaction) error {
		if _, err := sqlgen.New(tx).SetTenantScope(ctx, scope.ID().String()); err != nil {
			return err
		}
		return work(ctx, tx)
	})
}

// Get reads the current pointer without changing immutable execution meaning.
func (store *RegistryStore) Get(ctx context.Context, scope tenant.Scope, name string) (model.RegistryState, error) {
	var result model.RegistryState
	err := store.readScoped(ctx, scope, func(ctx context.Context, tx pg.Transaction) error {
		var err error
		result, err = readRegistry(ctx, tx, scope, name, false)
		return err
	})
	return result, err
}
func readRegistry(ctx context.Context, tx pg.Transaction, scope tenant.Scope, name string, lock bool) (model.RegistryState, error) {
	query := `SELECT state FROM idenqa.model_registries WHERE tenant_id=$1 AND name=$2`
	if lock {
		query += ` FOR UPDATE`
	}
	var raw []byte
	err := tx.QueryRow(ctx, query, scope.ID().String(), name).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.RegistryState{}, model.ErrRegistryNotFound
	}
	if err != nil {
		return model.RegistryState{}, err
	}
	var result model.RegistryState
	err = json.Unmarshal(raw, &result)
	return result, err
}

// Revision retrieves and verifies an immutable canonical content digest.
func (store *RegistryStore) Revision(ctx context.Context, scope tenant.Scope, name, kind string, number int64) (model.RegistryRevision, error) {
	var result model.RegistryRevision
	err := store.readScoped(ctx, scope, func(ctx context.Context, tx pg.Transaction) error {
		var err error
		result, err = readRegistryRevision(ctx, tx, scope, name, kind, number)
		return err
	})
	return result, err
}
func readRegistryRevision(ctx context.Context, tx pg.Transaction, scope tenant.Scope, name, kind string, number int64) (model.RegistryRevision, error) {
	var raw []byte
	var digest string
	err := tx.QueryRow(ctx, `SELECT document,digest FROM idenqa.model_registry_revisions WHERE tenant_id=$1 AND name=$2 AND kind=$3 AND revision=$4`, scope.ID().String(), name, kind, number).Scan(&raw, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.RegistryRevision{}, model.ErrRegistryNotFound
	}
	if err != nil {
		return model.RegistryRevision{}, err
	}
	var result model.RegistryRevision
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, err
	}
	var actual string
	switch {
	case kind == "model" && result.Registration != nil && result.Thresholds == nil && result.Registration.Validate() == nil:
		actual, err = model.RevisionDigest(result.Registration)
	case kind == "threshold" && result.Thresholds != nil && result.Registration == nil && result.Thresholds.Validate() == nil:
		actual, err = model.RevisionDigest(result.Thresholds)
	default:
		return result, model.ErrRegistryInvalid
	}
	if err != nil || actual != digest || result.Digest != digest || result.Kind != kind || result.Revision != number {
		return result, model.ErrRegistryInvalid
	}
	return result, nil
}

// History returns immutable receipts newest first, bounded by version rather than offsets.
func (store *RegistryStore) History(ctx context.Context, scope tenant.Scope, name string, before int64, limit int) ([]model.RegistryReceipt, error) {
	if before < 0 || limit < 1 || limit > 100 {
		return nil, model.ErrRegistryInvalid
	}
	result := []model.RegistryReceipt{}
	err := store.readScoped(ctx, scope, func(ctx context.Context, tx pg.Transaction) error {
		rows, err := tx.Query(ctx, `SELECT receipt FROM idenqa.model_registry_history WHERE tenant_id=$1 AND name=$2 AND ($3::bigint=0 OR version<$3) ORDER BY version DESC LIMIT $4`, scope.ID().String(), name, before, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			var receipt model.RegistryReceipt
			if err := json.Unmarshal(raw, &receipt); err != nil {
				return err
			}
			result = append(result, receipt)
		}
		return rows.Err()
	})
	return result, err
}

// RollbackEligible reports whether the exact deployment was ever activated for one tenant model.
func (store *RegistryStore) RollbackEligible(ctx context.Context, scope tenant.Scope, name string, deployment model.Deployment) (bool, error) {
	var result bool
	err := store.readScoped(ctx, scope, func(ctx context.Context, tx pg.Transaction) error {
		var err error
		result, err = readRollbackEligible(ctx, tx, scope, name, deployment)
		return err
	})
	return result, err
}

func readRollbackEligible(ctx context.Context, tx pg.Transaction, scope tenant.Scope, name string, deployment model.Deployment) (bool, error) {
	raw, err := json.Marshal(deployment)
	if err != nil {
		return false, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM idenqa.model_registry_history WHERE tenant_id=$1 AND name=$2 AND receipt->>'operation' IN ('activate','rollback') AND receipt->'state'->'active'=$3::jsonb)`, scope.ID().String(), name, raw).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

// Apply reserves idempotency before locking the root; audit, outbox and history share COMMIT.
func (store *RegistryStore) Apply(ctx context.Context, scope tenant.Scope, request idempotency.Request, event id.Event, command model.RegistryCommand) (model.RegistryReceipt, error) {
	if command.Validate() != nil || scope.ID().IsZero() || scope.ID() != request.TenantID() || event.IsZero() {
		return model.RegistryReceipt{}, model.ErrRegistryInvalid
	}
	for range 3 {
		var result model.RegistryReceipt
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
					return err
				}
				result.Replayed = true
				return nil
			}
			now := store.now().UTC().Truncate(time.Microsecond)
			state, err := readRegistry(ctx, tx, scope, command.Name, true)
			if errors.Is(err, model.ErrRegistryNotFound) && command.Operation == "register" && command.ExpectedVersion == 0 {
				state = model.RegistryState{Name: command.Name, UpdatedAt: now}
				raw, encodeErr := json.Marshal(state)
				if encodeErr != nil {
					return encodeErr
				}
				if _, err = tx.Exec(ctx, `INSERT INTO idenqa.model_registries(tenant_id,name,version,state) VALUES($1,$2,0,$3) ON CONFLICT DO NOTHING`, scope.ID().String(), command.Name, raw); err != nil {
					return err
				}
				state, err = readRegistry(ctx, tx, scope, command.Name, true)
			}
			if err != nil {
				return err
			}
			if state.Version != command.ExpectedVersion || now.Before(state.UpdatedAt) {
				return model.ErrRegistryConflict
			}
			result = model.RegistryReceipt{State: state, ActorID: request.Principal().String(), Operation: command.Operation, Reason: command.Reason}
			if err := applyRegistry(ctx, tx, scope, command, &result, now); err != nil {
				return err
			}
			result.State.Version++
			result.State.UpdatedAt = now
			stateJSON, err := json.Marshal(result.State)
			if err != nil {
				return err
			}
			tag, err := tx.Exec(ctx, `UPDATE idenqa.model_registries SET version=$3,state=$4 WHERE tenant_id=$1 AND name=$2 AND version=$5`, scope.ID().String(), command.Name, result.State.Version, stateJSON, command.ExpectedVersion)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return model.ErrRegistryConflict
			}
			raw, err := json.Marshal(result)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO idenqa.model_registry_history(tenant_id,name,version,actor_key_id,receipt) VALUES($1,$2,$3,$4,$5)`, scope.ID().String(), command.Name, result.State.Version, request.Principal().String(), raw); err != nil {
				return err
			}
			// Reference-only event deliberately excludes training declarations and scores.
			payload, err := json.Marshal(struct {
				Name    string `json:"name"`
				Version int64  `json:"version"`
				Reason  string `json:"reason"`
			}{command.Name, result.State.Version, command.Reason})
			if err != nil {
				return err
			}
			digest := sha256.Sum256(payload)
			eventType := "model." + command.Operation + ".v1"
			if _, err := tx.Exec(ctx, `INSERT INTO idenqa.outbox_events(id,tenant_id,aggregate_type,aggregate_id,aggregate_version,event_type,schema_version,payload,occurred_at,created_at) VALUES($1,$2,'model_registry',$3,$4,$5,1,$6,$7,$7)`, event.String(), scope.ID().String(), command.Name, result.State.Version, eventType, payload, now); err != nil {
				return err
			}
			if _, err := auditpostgres.AppendInTransaction(ctx, tx, scope, auditpostgres.Event{EventID: event.String(), EventType: eventType, AggregateID: command.Name, ActorID: request.Principal().String(), EventDigest: hex.EncodeToString(digest[:]), OccurredAt: now}); err != nil {
				return err
			}
			receipt, err := idempotency.NewResult(200, raw)
			if err != nil {
				return err
			}
			return retrydb.Complete(ctx, queries, request, receipt, now)
		})
		if err == nil {
			return result, nil
		}
		var conflict *pgconn.PgError
		if !errors.As(err, &conflict) || (conflict.Code != "40001" && conflict.Code != "40P01") {
			return model.RegistryReceipt{}, fmt.Errorf("apply model registry: %w", err)
		}
		if ctx.Err() != nil {
			return model.RegistryReceipt{}, ctx.Err()
		}
	}
	return model.RegistryReceipt{}, model.ErrRegistryConflict
}
func applyRegistry(ctx context.Context, tx pg.Transaction, scope tenant.Scope, command model.RegistryCommand, result *model.RegistryReceipt, now time.Time) error {
	switch command.Operation {
	case "register", "threshold":
		revision := model.RegistryRevision{CreatedAt: now, Registration: command.Registration, Thresholds: command.Thresholds}
		var err error
		if command.Operation == "register" {
			result.State.ModelRevision++
			revision.Kind = "model"
			revision.Revision = result.State.ModelRevision
			revision.Digest, err = model.RevisionDigest(command.Registration)
		} else {
			result.State.ThresholdRevision++
			revision.Kind = "threshold"
			revision.Revision = result.State.ThresholdRevision
			revision.Digest, err = model.RevisionDigest(command.Thresholds)
		}
		if err != nil {
			return err
		}
		raw, err := json.Marshal(revision)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.model_registry_revisions(tenant_id,name,kind,revision,digest,document) VALUES($1,$2,$3,$4,$5,$6)`, scope.ID().String(), command.Name, revision.Kind, revision.Revision, revision.Digest, raw); err != nil {
			return err
		}
		result.Revision = &revision
	case "activate", "rollback":
		deployment := *command.Deployment
		if result.State.Active != nil && *result.State.Active == deployment {
			return model.ErrRegistryConflict
		}
		registration, err := readRegistryRevision(ctx, tx, scope, command.Name, "model", deployment.ModelRevision)
		if err != nil {
			return err
		}
		thresholds, err := readRegistryRevision(ctx, tx, scope, command.Name, "threshold", deployment.ThresholdRevision)
		if err != nil {
			return err
		}
		if err := model.ValidateDeployment(deployment, *registration.Registration, *thresholds.Thresholds); err != nil {
			return err
		}
		if command.Operation == "rollback" {
			eligible, err := readRollbackEligible(ctx, tx, scope, command.Name, deployment)
			if err != nil {
				return err
			}
			if !eligible {
				return model.ErrRegistryConflict
			}
		}
		result.State.Active = &deployment
	case "retire":
		if result.State.Active == nil {
			return model.ErrRegistryConflict
		}
		result.State.Active = nil
	default:
		return model.ErrRegistryInvalid
	}
	return nil
}
