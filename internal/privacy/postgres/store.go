// Package postgres adapts privacy lifecycle ports to tenant-scoped PostgreSQL.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	auditpostgres "github.com/Mujhtech/idenqa/internal/audit/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/privacy"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

type transactionRunner interface {
	WithinTransaction(context.Context, platformpostgres.TransactionOptions, func(context.Context, platformpostgres.Transaction) error) error
}

// Store persists deletion transitions, legal holds, tombstones, and their
// reference-only audit records atomically.
type Store struct{ pool transactionRunner }

// New constructs the privacy PostgreSQL adapter.
func New(pool transactionRunner) (*Store, error) {
	if pool == nil {
		return nil, errors.New("privacy postgres: pool is required")
	}
	return &Store{pool: pool}, nil
}

// Create persists a new deletion request and exact target set.
func (store *Store) Create(ctx context.Context, scope tenant.Scope, actor privacy.Actor, deletion privacy.Deletion) error {
	if deletion.Validate() != nil {
		return privacy.ErrInvalid
	}
	return store.write(ctx, scope, actor, deletion, "privacy.deletion.requested", func(ctx context.Context, tx platformpostgres.Transaction) error {
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.deletion_requests
			(tenant_id,id,aggregate_id,region,state,backup_expires_at,failure_class,version,requested_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,NULL,$7,$8,$9)`, scope.ID().String(), deletion.ID.String(), deletion.AggregateID,
			deletion.Region, string(deletion.State), deletion.BackupExpiresAt, deletion.Version, deletion.RequestedAt, deletion.UpdatedAt)
		if err != nil {
			return fmt.Errorf("insert deletion request: %w", err)
		}
		for _, target := range deletion.Targets {
			if _, err := tx.Exec(ctx, `INSERT INTO idenqa.deletion_targets
				(tenant_id,deletion_id,kind,reference,region,attempts,deleted_at,last_failure_class)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, scope.ID().String(), deletion.ID.String(), target.Kind,
				target.Reference, target.Region, target.Attempts, nullableTime(target.DeletedAt), nullableText(target.LastFailureClass)); err != nil {
				return fmt.Errorf("insert deletion target: %w", err)
			}
		}
		return nil
	})
}

// Find loads one tenant-scoped deletion and its exact targets.
func (store *Store) Find(ctx context.Context, scope tenant.Scope, identifier id.Deletion) (privacy.Deletion, error) {
	var result privacy.Deletion
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		var state string
		var failure *string
		err := tx.QueryRow(ctx, `SELECT aggregate_id,region,state,backup_expires_at,failure_class,version,requested_at,updated_at
			FROM idenqa.deletion_requests WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), identifier.String()).Scan(
			&result.AggregateID, &result.Region, &state, &result.BackupExpiresAt, &failure, &result.Version, &result.RequestedAt, &result.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return privacy.ErrInvalid
		}
		if err != nil {
			return fmt.Errorf("find deletion request: %w", err)
		}
		result.ID, result.State = identifier, privacy.DeletionState(state)
		result.BackupExpiresAt = result.BackupExpiresAt.UTC()
		result.RequestedAt = result.RequestedAt.UTC()
		result.UpdatedAt = result.UpdatedAt.UTC()
		if failure != nil {
			result.FailureClass = *failure
		}
		rows, err := tx.Query(ctx, `SELECT kind,reference,region,attempts,deleted_at,last_failure_class
			FROM idenqa.deletion_targets WHERE tenant_id=$1 AND deletion_id=$2 ORDER BY kind,reference`, scope.ID().String(), identifier.String())
		if err != nil {
			return fmt.Errorf("find deletion targets: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var target privacy.Target
			var deleted *time.Time
			var lastFailure *string
			if err := rows.Scan(&target.Kind, &target.Reference, &target.Region, &target.Attempts, &deleted, &lastFailure); err != nil {
				return fmt.Errorf("scan deletion target: %w", err)
			}
			if deleted != nil {
				target.DeletedAt = deleted.UTC()
			}
			if lastFailure != nil {
				target.LastFailureClass = *lastFailure
			}
			result.Targets = append(result.Targets, target)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate deletion targets: %w", err)
		}
		return result.Validate()
	})
	return result, err
}

// Save atomically updates one optimistic transition and its audit record.
func (store *Store) Save(ctx context.Context, scope tenant.Scope, actor privacy.Actor, deletion privacy.Deletion, expectedVersion int64) error {
	if deletion.Validate() != nil || expectedVersion < 1 || deletion.Version != expectedVersion+1 {
		return privacy.ErrInvalid
	}
	return store.write(ctx, scope, actor, deletion, "privacy.deletion."+string(deletion.State), func(ctx context.Context, tx platformpostgres.Transaction) error {
		tag, err := tx.Exec(ctx, `UPDATE idenqa.deletion_requests SET state=$3,failure_class=$4,version=$5,updated_at=$6
			WHERE tenant_id=$1 AND id=$2 AND version=$7`, scope.ID().String(), deletion.ID.String(), string(deletion.State),
			nullableText(deletion.FailureClass), deletion.Version, deletion.UpdatedAt, expectedVersion)
		if err != nil {
			return fmt.Errorf("update deletion request: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return privacy.ErrConflict
		}
		for _, target := range deletion.Targets {
			tag, err = tx.Exec(ctx, `UPDATE idenqa.deletion_targets SET attempts=$5,deleted_at=$6,last_failure_class=$7
				WHERE tenant_id=$1 AND deletion_id=$2 AND kind=$3 AND reference=$4`, scope.ID().String(), deletion.ID.String(),
				target.Kind, target.Reference, target.Attempts, nullableTime(target.DeletedAt), nullableText(target.LastFailureClass))
			if err != nil || tag.RowsAffected() != 1 {
				return errors.Join(privacy.ErrConflict, err)
			}
		}
		return nil
	})
}

// ActiveHolds returns only holds that block the aggregate at the exact instant.
func (store *Store) ActiveHolds(ctx context.Context, scope tenant.Scope, aggregateID string, at time.Time) ([]privacy.Hold, error) {
	var result []privacy.Hold
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id,authority,reason,starts_at,review_at,released_at FROM idenqa.legal_holds
			WHERE tenant_id=$1 AND aggregate_id=$2 AND starts_at <= $3 AND (released_at IS NULL OR released_at > $3)
			ORDER BY starts_at,id`, scope.ID().String(), aggregateID, at)
		if err != nil {
			return fmt.Errorf("find legal holds: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var encoded string
			var hold privacy.Hold
			var released *time.Time
			if err := rows.Scan(&encoded, &hold.Authority, &hold.Reason, &hold.StartsAt, &hold.ReviewAt, &released); err != nil {
				return err
			}
			hold.ID, err = id.ParseLegalHold(encoded)
			if err != nil {
				return privacy.ErrInvalid
			}
			hold.AggregateID = aggregateID
			hold.StartsAt = hold.StartsAt.UTC()
			hold.ReviewAt = hold.ReviewAt.UTC()
			if released != nil {
				hold.ReleasedAt = released.UTC()
			}
			result = append(result, hold)
		}
		return rows.Err()
	})
	return result, err
}

// Complete commits the final transition and immutable tombstone together.
func (store *Store) Complete(ctx context.Context, scope tenant.Scope, actor privacy.Actor, deletion privacy.Deletion, tombstone privacy.Tombstone, expectedVersion int64) error {
	return store.write(ctx, scope, actor, deletion, "privacy.deletion.completed", func(ctx context.Context, tx platformpostgres.Transaction) error {
		tag, err := tx.Exec(ctx, `UPDATE idenqa.deletion_requests SET state=$3,version=$4,updated_at=$5,completed_at=$5
			WHERE tenant_id=$1 AND id=$2 AND version=$6`, scope.ID().String(), deletion.ID.String(), string(deletion.State), deletion.Version, deletion.UpdatedAt, expectedVersion)
		if err != nil || tag.RowsAffected() != 1 {
			return errors.Join(privacy.ErrConflict, err)
		}
		tag, err = tx.Exec(ctx, `INSERT INTO idenqa.deletion_tombstones
			(tenant_id,deletion_id,aggregate_id,region,target_count,proof_digest,completed_at,retain_until)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (tenant_id,deletion_id) DO NOTHING`, scope.ID().String(),
			tombstone.DeletionID.String(), tombstone.AggregateID, tombstone.Region, tombstone.TargetCount,
			tombstone.ProofDigest, tombstone.CompletedAt, tombstone.RetainUntil)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 {
			return nil
		}
		var aggregateID, region, proofDigest string
		var targetCount int
		var completedAt, retainUntil time.Time
		if err := tx.QueryRow(ctx, `SELECT aggregate_id,region,target_count,proof_digest,completed_at,retain_until
			FROM idenqa.deletion_tombstones WHERE tenant_id=$1 AND deletion_id=$2`, scope.ID().String(), tombstone.DeletionID.String()).Scan(
			&aggregateID, &region, &targetCount, &proofDigest, &completedAt, &retainUntil); err != nil {
			return err
		}
		if aggregateID != tombstone.AggregateID || region != tombstone.Region || targetCount != tombstone.TargetCount ||
			proofDigest != tombstone.ProofDigest || !completedAt.Equal(tombstone.CompletedAt) || !retainUntil.Equal(tombstone.RetainUntil) {
			return privacy.ErrConflict
		}
		return nil
	})
}

// Due lists a bounded deterministic batch requiring scheduled advancement.
func (store *Store) Due(ctx context.Context, scope tenant.Scope, now time.Time, limit int) ([]id.Deletion, error) {
	if limit < 1 || limit > 1000 {
		return nil, privacy.ErrInvalid
	}
	var result []id.Deletion
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id FROM idenqa.deletion_requests WHERE tenant_id=$1 AND
			(state IN ('requested','failed','blocked_by_legal_hold') OR (state='awaiting_backup_expiry' AND backup_expires_at <= $2))
			ORDER BY updated_at,id LIMIT $3`, scope.ID().String(), now, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var encoded string
			if err := rows.Scan(&encoded); err != nil {
				return err
			}
			identifier, err := id.ParseDeletion(encoded)
			if err != nil {
				return privacy.ErrInvalid
			}
			result = append(result, identifier)
		}
		return rows.Err()
	})
	return result, err
}

// Tombstoned loads a bounded restore-replay set with exact retained targets.
func (store *Store) Tombstoned(ctx context.Context, scope tenant.Scope, limit int) ([]privacy.Deletion, error) {
	if limit < 1 || limit > 1000 {
		return nil, privacy.ErrInvalid
	}
	var identifiers []id.Deletion
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT deletion_id FROM idenqa.deletion_tombstones WHERE tenant_id=$1 ORDER BY completed_at,deletion_id LIMIT $2`, scope.ID().String(), limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var encoded string
			if err := rows.Scan(&encoded); err != nil {
				return err
			}
			identifier, err := id.ParseDeletion(encoded)
			if err != nil {
				return privacy.ErrInvalid
			}
			identifiers = append(identifiers, identifier)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	result := make([]privacy.Deletion, 0, len(identifiers))
	for _, identifier := range identifiers {
		value, err := store.Find(ctx, scope, identifier)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

// RecordTombstoneReplay appends a reference-only successful replay audit event.
func (store *Store) RecordTombstoneReplay(ctx context.Context, scope tenant.Scope, actor privacy.Actor, deletion privacy.Deletion, at time.Time) error {
	deletion.UpdatedAt = at
	return store.writeEvent(ctx, scope, actor, deletion, "privacy.deletion.tombstone_replayed", func(context.Context, platformpostgres.Transaction) error { return nil })
}

// CreateHold atomically records a hold and its audit event.
func (store *Store) CreateHold(ctx context.Context, scope tenant.Scope, actor privacy.Actor, hold privacy.Hold, occurredAt time.Time) error {
	if hold.Validate() != nil {
		return privacy.ErrInvalid
	}
	deletion := privacy.Deletion{ID: deletionReference(hold.AggregateID), AggregateID: hold.AggregateID, UpdatedAt: occurredAt}
	return store.writeEvent(ctx, scope, actor, deletion, "privacy.legal_hold.created", func(ctx context.Context, tx platformpostgres.Transaction) error {
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.legal_holds
			(tenant_id,id,aggregate_id,authority,reason,starts_at,review_at,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			scope.ID().String(), hold.ID.String(), hold.AggregateID, hold.Authority, hold.Reason, hold.StartsAt, hold.ReviewAt, occurredAt)
		return err
	})
}

// ReleaseHold ends an active hold exactly once and audits the transition.
func (store *Store) ReleaseHold(ctx context.Context, scope tenant.Scope, actor privacy.Actor, identifier id.LegalHold, at time.Time) (privacy.Hold, error) {
	var hold privacy.Hold
	deletion := privacy.Deletion{ID: deletionReference(identifier.String()), AggregateID: identifier.String(), UpdatedAt: at}
	err := store.writeEvent(ctx, scope, actor, deletion, "privacy.legal_hold.released", func(ctx context.Context, tx platformpostgres.Transaction) error {
		var encoded string
		err := tx.QueryRow(ctx, `UPDATE idenqa.legal_holds SET released_at=$3 WHERE tenant_id=$1 AND id=$2 AND released_at IS NULL
			RETURNING id,aggregate_id,authority,reason,starts_at,review_at,released_at`, scope.ID().String(), identifier.String(), at).Scan(
			&encoded, &hold.AggregateID, &hold.Authority, &hold.Reason, &hold.StartsAt, &hold.ReviewAt, &hold.ReleasedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return privacy.ErrConflict
		}
		hold.ID = identifier
		return err
	})
	return hold, err
}

func (store *Store) write(ctx context.Context, scope tenant.Scope, actor privacy.Actor, deletion privacy.Deletion, eventType string, mutate func(context.Context, platformpostgres.Transaction) error) error {
	return store.writeEvent(ctx, scope, actor, deletion, eventType, mutate)
}

func (store *Store) writeEvent(ctx context.Context, scope tenant.Scope, actor privacy.Actor, deletion privacy.Deletion, eventType string, mutate func(context.Context, platformpostgres.Transaction) error) error {
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationSerializable}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		if err := mutate(ctx, tx); err != nil {
			return err
		}
		digest := sha256.Sum256([]byte(fmt.Sprintf("%s\n%s\n%s\n%d\n", eventType, deletion.AggregateID, deletion.ID.String(), deletion.Version)))
		_, err := auditpostgres.AppendInTransaction(ctx, tx, scope, auditpostgres.Event{
			EventID: referenceToken("event", fmt.Sprintf("%s:%s:%s:%d", eventType, deletion.AggregateID, deletion.ID.String(), deletion.Version)), EventType: eventType,
			AggregateID: referenceToken("privacy", deletion.AggregateID), ActorID: referenceToken("actor", actor.ID),
			EventDigest: hex.EncodeToString(digest[:]), OccurredAt: deletion.UpdatedAt,
		})
		return err
	})
}

func setScope(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope) error {
	if scope.ID().IsZero() {
		return privacy.ErrInvalid
	}
	var value string
	return tx.QueryRow(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, scope.ID().String()).Scan(&value)
}

func referenceToken(prefix, value string) string {
	digest := sha256.Sum256([]byte(value))
	return prefix + ":" + hex.EncodeToString(digest[:12])
}

// deletionReference exists only to reuse the private audit envelope for hold
// events; the value is never persisted as a deletion identifier.
func deletionReference(value string) id.Deletion {
	identifier, _ := id.ParseDeletion("del_00000000000000000000000000")
	_ = value
	return identifier
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

var _ privacy.Repository = (*Store)(nil)
