// Package postgres persists installation-wide key rewrap progress and composes
// the per-class rewrap adapters over their owning stores.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/keyrewrap"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
)

const (
	rewrapFlagSetting = "idenqa.key_rewrap"
	retryBase         = 15 * time.Second
	retryShiftCap     = 8
)

type transactionRunner interface {
	WithinTransaction(context.Context, platformpostgres.TransactionOptions, func(context.Context, platformpostgres.Transaction) error) error
}

// Store owns installation-wide sweep state and batch audit persistence, and
// supplies the composed key provider to the class adapters.
type Store struct {
	pool      transactionRunner
	wrapper   platformcrypto.KeyWrapper
	unwrapper platformcrypto.KeyUnwrapper
	now       func() time.Time
}

// New constructs the sweep repository over the composed key provider.
func New(
	pool transactionRunner,
	wrapper platformcrypto.KeyWrapper,
	unwrapper platformcrypto.KeyUnwrapper,
	now func() time.Time,
) (*Store, error) {
	if pool == nil || wrapper == nil || unwrapper == nil || now == nil {
		return nil, keyrewrap.ErrInvalid
	}

	return &Store{pool: pool, wrapper: wrapper, unwrapper: unwrapper, now: now}, nil
}

// ListTenants pages non-deleted tenants in identifier order through the
// identifier-only SECURITY DEFINER boundary.
func (store *Store) ListTenants(ctx context.Context, after string, limit int) ([]id.Tenant, error) {
	if store == nil || limit < 1 || limit > 500 {
		return nil, keyrewrap.ErrInvalid
	}
	tenants := make([]id.Tenant, 0, limit)
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		rows, err := tx.Query(ctx, `SELECT tenant_id FROM idenqa.list_key_rewrap_tenants($1,$2)`, after, limit)
		if err != nil {
			return fmt.Errorf("list key rewrap tenants: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var encoded string
			if err := rows.Scan(&encoded); err != nil {
				return err
			}
			parsed, err := id.ParseTenant(encoded)
			if err != nil {
				return keyrewrap.ErrUnavailable
			}
			tenants = append(tenants, parsed)
		}

		return rows.Err()
	})
	if err != nil {
		return nil, err
	}

	return tenants, nil
}

// Load returns one class state when it exists.
func (store *Store) Load(ctx context.Context, class keyrewrap.Class) (keyrewrap.State, bool, error) {
	if store == nil {
		return keyrewrap.State{}, false, keyrewrap.ErrInvalid
	}
	state, err := store.load(ctx, nil, class)
	if errors.Is(err, keyrewrap.ErrConflict) {
		return keyrewrap.State{}, false, nil
	}
	if err != nil {
		return keyrewrap.State{}, false, err
	}

	return state, true, nil
}

// States returns every class state in fixed class order.
func (store *Store) States(ctx context.Context) ([]keyrewrap.State, error) {
	if store == nil {
		return nil, keyrewrap.ErrInvalid
	}
	states := make([]keyrewrap.State, 0, len(keyrewrap.AllClasses()))
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		for _, class := range keyrewrap.AllClasses() {
			state, err := store.load(ctx, tx, class)
			if errors.Is(err, keyrewrap.ErrConflict) {
				continue
			}
			if err != nil {
				return err
			}
			states = append(states, state)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return states, nil
}

// BeginGeneration atomically establishes or replaces the state for one
// observed epoch. Repeating the same epoch is a no-op.
func (store *Store) BeginGeneration(ctx context.Context, class keyrewrap.Class, epoch keyrewrap.Epoch, at time.Time) (keyrewrap.State, error) {
	if store == nil || at.IsZero() || epoch.IsZero() {
		return keyrewrap.State{}, keyrewrap.ErrInvalid
	}
	var state keyrewrap.State
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationSerializable}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		current, err := store.load(ctx, tx, class)
		if errors.Is(err, keyrewrap.ErrConflict) {
			encoded, err := json.Marshal(epoch.Record)
			if err != nil {
				return keyrewrap.ErrInvalid
			}
			if _, err := tx.Exec(ctx, `INSERT INTO idenqa.key_rewrap_state
				(class,epoch_key,epoch,generation,status,started_at,updated_at)
				VALUES($1,$2,$3,1,'running',$4,$4)`, class.String(), epoch.Key, encoded, at); err != nil {
				return fmt.Errorf("begin key rewrap generation: %w", err)
			}
			state, err = store.load(ctx, tx, class)

			return err
		}
		if err != nil {
			return err
		}
		if current.Epoch.Key == epoch.Key {
			state = current

			return nil
		}
		encoded, err := json.Marshal(epoch.Record)
		if err != nil {
			return keyrewrap.ErrInvalid
		}
		if _, err := tx.Exec(ctx, `UPDATE idenqa.key_rewrap_state SET
			epoch_key=$2,epoch=$3,generation=generation+1,status='running',
			cursor_tenant='',cursor_object='',processed=0,rewrapped=0,skipped=0,failed=0,
			consecutive_failures=0,last_error=NULL,next_attempt_at=NULL,
			started_at=$4,completed_at=NULL,updated_at=$4
			WHERE class=$1`, class.String(), epoch.Key, encoded, at); err != nil {
			return fmt.Errorf("reset key rewrap generation: %w", err)
		}
		state, err = store.load(ctx, tx, class)

		return err
	})

	return state, err
}

// CommitBatch compare-and-swaps the expected cursor and generation, advances
// counters, and appends one append-only audit row in the same transaction.
func (store *Store) CommitBatch(ctx context.Context, expected keyrewrap.State, batch keyrewrap.Batch) error {
	if store == nil || batch.Generation != expected.Generation || batch.OccurredAt.IsZero() {
		return keyrewrap.ErrInvalid
	}
	status := batch.Status
	if status != keyrewrap.StatusRunning && status != keyrewrap.StatusFailed && status != keyrewrap.StatusCompleted {
		return keyrewrap.ErrInvalid
	}
	consecutive := 0
	var lastError *string
	var nextAttempt any
	if status == keyrewrap.StatusFailed {
		consecutive = expected.ConsecutiveFailures + 1
		message := batch.ErrorClass
		if message == "" {
			message = "rewrap_failed"
		}
		lastError = &message
		nextAttempt = batch.OccurredAt.Add(backoff(consecutive))
	}
	var completed any
	if status == keyrewrap.StatusCompleted {
		completed = batch.OccurredAt
	}

	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		encoded, err := json.Marshal(batch.Epoch.Record)
		if err != nil {
			return keyrewrap.ErrInvalid
		}
		tag, err := tx.Exec(ctx, `UPDATE idenqa.key_rewrap_state SET
			status=$6,cursor_tenant=$7,cursor_object=$8,processed=processed+$9,
			rewrapped=rewrapped+$10,skipped=skipped+$11,failed=failed+$12,
			consecutive_failures=$13,last_error=$14,next_attempt_at=$15,
			completed_at=$16,updated_at=$17
			WHERE class=$1 AND generation=$2 AND epoch_key=$3
			AND cursor_tenant=$4 AND cursor_object=$5`,
			expected.Class.String(), expected.Generation, expected.Epoch.Key,
			expected.Cursor.Tenant.String(), expected.Cursor.Object,
			status, batch.To.Tenant.String(), batch.To.Object,
			batch.Processed, batch.Rewrapped, batch.Skipped, batch.Failed,
			consecutive, lastError, nextAttempt, completed, batch.OccurredAt)
		if err != nil {
			return fmt.Errorf("commit key rewrap batch: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return keyrewrap.ErrConflict
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.key_rewrap_audit
			(class,generation,sequence,epoch,cursor_tenant,cursor_object,next_tenant,next_object,
			 processed,rewrapped,skipped,failed,status,error_class,occurred_at)
			VALUES($1,$2,COALESCE((SELECT max(sequence) FROM idenqa.key_rewrap_audit
			 WHERE class=$1 AND generation=$2),0)+1,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
			expected.Class.String(), expected.Generation, encoded,
			expected.Cursor.Tenant.String(), expected.Cursor.Object,
			batch.To.Tenant.String(), batch.To.Object, batch.Processed, batch.Rewrapped,
			batch.Skipped, batch.Failed, status, nullableClass(batch.ErrorClass), batch.OccurredAt); err != nil {
			return fmt.Errorf("append key rewrap batch audit: %w", err)
		}

		return nil
	})
}

// setRewrapFlag enables the narrow wrapping-only trigger transitions inside
// one transaction. The flag is transaction-local and cleared at commit.
func setRewrapFlag(ctx context.Context, tx platformpostgres.Transaction) error {
	var value string
	if err := tx.QueryRow(ctx, `SELECT set_config($1,'on',true)`, rewrapFlagSetting).Scan(&value); err != nil {
		return fmt.Errorf("enable key rewrap transition: %w", err)
	}

	return nil
}

func setScope(ctx context.Context, tx platformpostgres.Transaction, tenantID string) error {
	var value string

	return tx.QueryRow(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, tenantID).Scan(&value)
}

func (store *Store) load(ctx context.Context, tx platformpostgres.Transaction, class keyrewrap.Class) (keyrewrap.State, error) {
	var state keyrewrap.State
	var epochKey string
	var raw []byte
	var cursorTenant, cursorObject string
	var status string
	var lastError *string
	var nextAttemptAt, completedAt *time.Time
	query := `SELECT epoch_key,epoch,generation,status,cursor_tenant,cursor_object,
		processed,rewrapped,skipped,failed,consecutive_failures,last_error,
		next_attempt_at,started_at,completed_at,updated_at
		FROM idenqa.key_rewrap_state WHERE class=$1`
	if tx != nil {
		err := tx.QueryRow(ctx, query, class.String()).Scan(&epochKey, &raw, &state.Generation, &status,
			&cursorTenant, &cursorObject, &state.Processed, &state.Rewrapped, &state.Skipped, &state.Failed,
			&state.ConsecutiveFailures, &lastError, &nextAttemptAt, &state.StartedAt, &completedAt, &state.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return keyrewrap.State{}, keyrewrap.ErrConflict
		}
		if err != nil {
			return keyrewrap.State{}, err
		}
	} else {
		err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, inner platformpostgres.Transaction) error {
			var err error
			state, err = store.load(ctx, inner, class)

			return err
		})
		if err != nil {
			return keyrewrap.State{}, err
		}

		return state, nil
	}
	var record kms.WrappedKeyRecord
	if err := json.Unmarshal(raw, &record); err != nil || epochKey == "" {
		return keyrewrap.State{}, keyrewrap.ErrUnavailable
	}
	epoch, err := keyrewrap.EpochFromRecord(record)
	if err != nil || epoch.Key != epochKey {
		return keyrewrap.State{}, keyrewrap.ErrUnavailable
	}
	if cursorTenant != "" {
		tenantID, err := id.ParseTenant(cursorTenant)
		if err != nil {
			return keyrewrap.State{}, keyrewrap.ErrUnavailable
		}
		state.Cursor.Tenant = tenantID
	}
	state.Class, state.Epoch, state.Status, state.Cursor.Object = class, epoch, status, cursorObject
	if lastError != nil {
		state.LastError = *lastError
	}
	if nextAttemptAt != nil {
		state.NextAttemptAt = nextAttemptAt.UTC()
	}
	if completedAt != nil {
		state.CompletedAt = completedAt.UTC()
	}
	state.StartedAt, state.UpdatedAt = state.StartedAt.UTC(), state.UpdatedAt.UTC()

	return state, nil
}

func backoff(consecutive int) time.Duration {
	delay := retryBase
	for index := 0; index < consecutive && index < retryShiftCap; index++ {
		delay *= 2
	}
	if delay > keyrewrap.DefaultRetryCap {
		return keyrewrap.DefaultRetryCap
	}

	return delay
}

func nullableClass(value string) any {
	if value == "" {
		return nil
	}

	return value
}
