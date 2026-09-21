// Package postgres persists global pack lifecycle state and immutable history.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/Mujhtech/idenqa/internal/pack"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrDigestMismatch means a stored pack identity disagrees with the embedded
// document digest and the transition must not overwrite it.
var ErrDigestMismatch = errors.New("pack postgres: stored digest does not match embedded pack")

// StorePool is the narrow PostgreSQL capability the store consumes.
type StorePool interface {
	Native() *pgxpool.Pool
	WithinTransaction(context.Context, pg.TransactionOptions, func(context.Context, pg.Transaction) error) error
}

// Store adapts pack lifecycle persistence to PostgreSQL.
type Store struct {
	pool StorePool
}

// New constructs global pack lifecycle persistence.
func New(pool StorePool) (*Store, error) {
	if pool == nil {
		return nil, fmt.Errorf("pack postgres: pool is required")
	}
	return &Store{pool: pool}, nil
}

// LoadStates reads every durable pack lifecycle projection row.
func (store *Store) LoadStates(ctx context.Context) ([]pack.StoredState, error) {
	rows, err := store.pool.Native().Query(ctx,
		`SELECT country, revision, state, digest, transition_version, updated_at
		 FROM idenqa.pack_release_states
		 ORDER BY country, revision`)
	if err != nil {
		return nil, fmt.Errorf("pack postgres: query lifecycle states: %w", err)
	}
	defer rows.Close()
	result := make([]pack.StoredState, 0)
	for rows.Next() {
		var state pack.StoredState
		if err := rows.Scan(&state.Country, &state.Revision, &state.State, &state.Digest, &state.Version, &state.UpdatedAt); err != nil {
			return nil, fmt.Errorf("pack postgres: scan lifecycle state: %w", err)
		}
		state.UpdatedAt = state.UpdatedAt.UTC()
		result = append(result, state)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pack postgres: iterate lifecycle states: %w", err)
	}
	return result, nil
}

// LoadHistory reads the append-only lifecycle history for one country.
func (store *Store) LoadHistory(ctx context.Context, country string) ([]pack.Change, error) {
	rows, err := store.pool.Native().Query(ctx,
		`SELECT country, revision, transition_version, operation, previous_state, state, pack_digest, reason, actor, recorded_at
		 FROM idenqa.pack_release_history
		 WHERE country = $1
		 ORDER BY transition_version, revision`, country)
	if err != nil {
		return nil, fmt.Errorf("pack postgres: query lifecycle history: %w", err)
	}
	defer rows.Close()
	result := make([]pack.Change, 0)
	for rows.Next() {
		var change pack.Change
		if err := rows.Scan(
			&change.Country, &change.Revision, &change.TransitionVersion, &change.Operation,
			&change.PreviousState, &change.State, &change.PackDigest, &change.Reason, &change.Actor, &change.RecordedAt,
		); err != nil {
			return nil, fmt.Errorf("pack postgres: scan lifecycle history: %w", err)
		}
		change.RecordedAt = change.RecordedAt.UTC()
		result = append(result, change)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pack postgres: iterate lifecycle history: %w", err)
	}
	return result, nil
}

// Apply commits every lifecycle change and its history atomically. A stored
// digest that disagrees with the embedded pack identity fails the transition.
func (store *Store) Apply(ctx context.Context, changes []pack.Change) error {
	if len(changes) == 0 {
		return fmt.Errorf("%w: empty lifecycle transition", pack.ErrInvalid)
	}
	return store.pool.WithinTransaction(ctx, pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, transaction pg.Transaction) error {
		for _, change := range changes {
			tag, err := transaction.Exec(ctx,
				`INSERT INTO idenqa.pack_release_states(country, revision, state, digest, transition_version, updated_at)
				 VALUES($1, $2, $3, $4, $5, $6)
				 ON CONFLICT (country, revision) DO UPDATE
				   SET state = EXCLUDED.state,
				       transition_version = EXCLUDED.transition_version,
				       updated_at = EXCLUDED.updated_at
				   WHERE idenqa.pack_release_states.digest = EXCLUDED.digest`,
				change.Country, change.Revision, string(change.State), change.PackDigest, change.TransitionVersion, change.RecordedAt)
			if err != nil {
				return fmt.Errorf("pack postgres: upsert lifecycle state: %w", err)
			}
			if tag.RowsAffected() != 1 {
				return ErrDigestMismatch
			}
			if _, err := transaction.Exec(ctx,
				`INSERT INTO idenqa.pack_release_history(country, revision, transition_version, operation, previous_state, state, pack_digest, reason, actor, recorded_at)
				 VALUES($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
				change.Country, change.Revision, change.TransitionVersion, change.Operation,
				string(change.PreviousState), string(change.State), change.PackDigest, change.Reason, change.Actor, change.RecordedAt); err != nil {
				return fmt.Errorf("pack postgres: insert lifecycle history: %w", err)
			}
		}
		return nil
	})
}
