// Package postgres persists tenant-scoped append-only audit chains.
package postgres

import (
	"context"
	"crypto/ed25519"
	"crypto/subtle"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/Mujhtech/idenqa/internal/audit"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type transactionRunner interface {
	WithinTransaction(context.Context, platformpostgres.TransactionOptions, func(context.Context, platformpostgres.Transaction) error) error
}

// Store allocates tenant sequences and persists records/checkpoints atomically.
type Store struct{ pool transactionRunner }

// New constructs the PostgreSQL audit adapter.
func New(pool transactionRunner) (*Store, error) {
	if pool == nil {
		return nil, errors.New("audit postgres: pool is required")
	}
	return &Store{pool: pool}, nil
}

// Event is the reference-only consequential meaning committed to the chain.
type Event struct {
	EventID, EventType, AggregateID, ActorID, EventDigest string
	OccurredAt                                            time.Time
}

// Append allocates exactly one next tenant sequence under a row lock.
func (store *Store) Append(ctx context.Context, scope tenant.Scope, event Event) (audit.Record, error) {
	var result audit.Record
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationSerializable}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		var err error
		result, err = AppendInTransaction(ctx, tx, scope, event)
		return err
	})
	return result, err
}

// AppendInTransaction appends an audit record through a transaction already
// owned by a consequential PostgreSQL adapter. The caller must use serializable
// isolation so its state transition and this audit record commit atomically.
func AppendInTransaction(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope, event Event) (audit.Record, error) {
	if tx == nil {
		return audit.Record{}, audit.ErrInvalid
	}
	if err := setScope(ctx, tx, scope); err != nil {
		return audit.Record{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO idenqa.audit_heads (tenant_id) VALUES ($1) ON CONFLICT (tenant_id) DO NOTHING`, scope.ID().String()); err != nil {
		return audit.Record{}, fmt.Errorf("ensure audit head: %w", err)
	}
	var sequence int64
	var previousHash string
	if err := tx.QueryRow(ctx, `SELECT last_sequence, last_hash FROM idenqa.audit_heads WHERE tenant_id = $1 FOR UPDATE`, scope.ID().String()).Scan(&sequence, &previousHash); err != nil {
		return audit.Record{}, fmt.Errorf("lock audit head: %w", err)
	}
	var previous *audit.Record
	if sequence > 0 {
		if sequence == math.MaxInt64 {
			return audit.Record{}, audit.ErrInvalid
		}
		previous = &audit.Record{Sequence: uint64(sequence), Hash: previousHash}
	}
	record, err := audit.Append(previous, event.EventID, event.EventType, event.AggregateID, event.ActorID, event.EventDigest, event.OccurredAt)
	if err != nil {
		return audit.Record{}, err
	}
	sequenceValue := int64(record.Sequence) //nolint:gosec // The locked head is rejected at MaxInt64 before increment.
	_, err = tx.Exec(ctx, `INSERT INTO idenqa.audit_records
		(tenant_id, sequence, event_id, event_type, aggregate_id, actor_id, occurred_at, event_digest, previous_hash, hash)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, scope.ID().String(), sequenceValue, record.EventID,
		record.EventType, record.AggregateID, record.ActorID, record.OccurredAt, record.EventDigest, record.PreviousHash, record.Hash)
	if err != nil {
		return audit.Record{}, fmt.Errorf("insert audit record: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE idenqa.audit_heads SET last_sequence=$2,last_hash=$3 WHERE tenant_id=$1`, scope.ID().String(), sequenceValue, record.Hash); err != nil {
		return audit.Record{}, fmt.Errorf("advance audit head: %w", err)
	}
	return record, nil
}

// RegisterKey appends public verification-key history. Private keys never enter PostgreSQL.
func (store *Store) RegisterKey(ctx context.Context, keyID string, key ed25519.PublicKey, validFrom time.Time) error {
	if keyID == "" || len(key) != ed25519.PublicKeySize || validFrom.IsZero() || validFrom.Location() != time.UTC {
		return audit.ErrInvalid
	}
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.audit_keys (key_id,public_key,valid_from) VALUES ($1,$2,$3)
			ON CONFLICT (key_id) DO NOTHING`, keyID, []byte(key), validFrom)
		return err
	})
}

// RetireKey closes a public-key verification interval without deleting history.
func (store *Store) RetireKey(ctx context.Context, keyID string, retiredAt time.Time) error {
	if keyID == "" || retiredAt.IsZero() || retiredAt.Location() != time.UTC {
		return audit.ErrInvalid
	}
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		tag, err := tx.Exec(ctx, `UPDATE idenqa.audit_keys SET retired_at=$2 WHERE key_id=$1 AND retired_at IS NULL AND valid_from < $2`, keyID, retiredAt)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return audit.ErrInvalid
		}
		return nil
	})
}

// Checkpoint signs and persists the current exact tenant chain head.
func (store *Store) Checkpoint(ctx context.Context, scope tenant.Scope, keyID string, privateKey ed25519.PrivateKey, at time.Time) (audit.Checkpoint, error) {
	var result audit.Checkpoint
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationSerializable}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		var registered []byte
		var validFrom time.Time
		var retiredAt *time.Time
		if err := tx.QueryRow(ctx, `SELECT public_key,valid_from,retired_at FROM idenqa.audit_keys WHERE key_id=$1`, keyID).Scan(&registered, &validFrom, &retiredAt); err != nil {
			return audit.ErrInvalid
		}
		public, ok := privateKey.Public().(ed25519.PublicKey)
		if !ok || len(registered) != ed25519.PublicKeySize || subtle.ConstantTimeCompare(registered, public) != 1 || at.Before(validFrom) || (retiredAt != nil && !at.Before(*retiredAt)) {
			return audit.ErrInvalid
		}
		var sequence int64
		var hash string
		if err := tx.QueryRow(ctx, `SELECT last_sequence,last_hash FROM idenqa.audit_heads WHERE tenant_id=$1 FOR UPDATE`, scope.ID().String()).Scan(&sequence, &hash); err != nil {
			return fmt.Errorf("load audit head: %w", err)
		}
		if sequence <= 0 {
			return audit.ErrInvalid
		}
		checkpoint, err := audit.SignCheckpoint(scope.ID().String(), keyID, audit.Record{Sequence: uint64(sequence), Hash: hash}, at, privateKey)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO idenqa.audit_checkpoints
			(tenant_id,through_sequence,chain_hash,key_id,created_at,signature) VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (tenant_id,through_sequence) DO NOTHING`, scope.ID().String(), sequence, checkpoint.ChainHash, checkpoint.KeyID, checkpoint.CreatedAt, checkpoint.Signature)
		if err != nil {
			return fmt.Errorf("insert audit checkpoint: %w", err)
		}
		result = checkpoint
		return nil
	})
	return result, err
}

// Export returns a complete bounded portable tenant chain and checkpoints.
func (store *Store) Export(ctx context.Context, scope tenant.Scope) (audit.Export, map[string]ed25519.PublicKey, error) {
	result := audit.Export{SchemaVersion: audit.SchemaVersion, TenantID: scope.ID().String()}
	keys := make(map[string]ed25519.PublicKey)
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT sequence,event_id,event_type,aggregate_id,actor_id,occurred_at,event_digest,previous_hash,hash
			FROM idenqa.audit_records WHERE tenant_id=$1 ORDER BY sequence LIMIT 100001`, scope.ID().String())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var sequence int64
			var record audit.Record
			if err := rows.Scan(&sequence, &record.EventID, &record.EventType, &record.AggregateID, &record.ActorID, &record.OccurredAt, &record.EventDigest, &record.PreviousHash, &record.Hash); err != nil {
				return err
			}
			if sequence <= 0 || len(result.Records) == 100000 {
				return audit.ErrInvalid
			}
			record.Sequence = uint64(sequence)
			record.OccurredAt = record.OccurredAt.UTC()
			result.Records = append(result.Records, record)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		checkpointRows, err := tx.Query(ctx, `SELECT through_sequence,chain_hash,key_id,created_at,signature
			FROM idenqa.audit_checkpoints WHERE tenant_id=$1 ORDER BY through_sequence LIMIT 10001`, scope.ID().String())
		if err != nil {
			return err
		}
		defer checkpointRows.Close()
		for checkpointRows.Next() {
			var sequence int64
			var checkpoint audit.Checkpoint
			if err := checkpointRows.Scan(&sequence, &checkpoint.ChainHash, &checkpoint.KeyID, &checkpoint.CreatedAt, &checkpoint.Signature); err != nil {
				return err
			}
			if sequence <= 0 || len(result.Checkpoints) == 10000 {
				return audit.ErrInvalid
			}
			checkpoint.TenantID, checkpoint.ThroughSequence = scope.ID().String(), uint64(sequence)
			checkpoint.CreatedAt = checkpoint.CreatedAt.UTC()
			result.Checkpoints = append(result.Checkpoints, checkpoint)
		}
		if err := checkpointRows.Err(); err != nil {
			return err
		}
		keyRows, err := tx.Query(ctx, `SELECT DISTINCT keys.key_id,keys.public_key FROM idenqa.audit_keys keys
			JOIN idenqa.audit_checkpoints checkpoints ON checkpoints.key_id=keys.key_id WHERE checkpoints.tenant_id=$1`, scope.ID().String())
		if err != nil {
			return err
		}
		defer keyRows.Close()
		for keyRows.Next() {
			var keyID string
			var key []byte
			if err := keyRows.Scan(&keyID, &key); err != nil {
				return err
			}
			if len(key) != ed25519.PublicKeySize {
				return audit.ErrInvalid
			}
			keys[keyID] = ed25519.PublicKey(append([]byte(nil), key...))
		}
		return keyRows.Err()
	})
	return result, keys, err
}

func setScope(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope) error {
	if scope.ID().IsZero() {
		return audit.ErrInvalid
	}
	var value string
	if err := tx.QueryRow(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, scope.ID().String()).Scan(&value); err != nil {
		return fmt.Errorf("set audit tenant scope: %w", err)
	}
	return nil
}
