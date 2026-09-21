package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/keycustody"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
)

// Create persists one new ceremony in the started state.
func (store *Store) Create(ctx context.Context, ceremony keycustody.RecoveryCeremony) (keycustody.RecoveryCeremony, error) {
	if store == nil || ceremony.ID == "" || ceremony.State != keycustody.RecoveryStarted || ceremony.Version != 1 {
		return keycustody.RecoveryCeremony{}, keycustody.ErrInvalid
	}
	var tenantID any
	if !ceremony.TenantID.IsZero() {
		tenantID = ceremony.TenantID.String()
	}
	if err := store.pool.WithinTransaction(ctx, pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.key_recovery_ceremonies
			(id,kind,class,tenant_id,target_provider,target_reference,target_version,target_algorithm,
			 state,version,started_by,started_at,approve_by,reason,updated_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
			ceremony.ID, ceremony.Kind, ceremony.Class, tenantID,
			ceremony.Target.Provider, ceremony.Target.Reference, ceremony.Target.Version, ceremony.Target.Algorithm,
			ceremony.State, ceremony.Version, ceremony.StartedBy, ceremony.StartedAt, ceremony.ApproveBy,
			ceremony.Reason, ceremony.UpdatedAt)

		return err
	}); err != nil {
		return keycustody.RecoveryCeremony{}, fmt.Errorf("create key recovery ceremony: %w", err)
	}

	return store.Load(ctx, ceremony.ID)
}

// Load returns one ceremony by identifier.
func (store *Store) Load(ctx context.Context, identifier string) (keycustody.RecoveryCeremony, error) {
	var ceremony keycustody.RecoveryCeremony
	var tenantID, approvedBy, completedBy, abortedBy, abortReason *string
	var receipt []byte
	var approvedAt, usableUntil, completedAt, abortedAt *time.Time
	err := store.pool.WithinTransaction(ctx, pg.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx pg.Transaction) error {
		err := tx.QueryRow(ctx, `SELECT kind,class,tenant_id,target_provider,target_reference,target_version,target_algorithm,
			state,version,started_by,started_at,approve_by,approved_by,approved_at,usable_until,
			completed_by,completed_at,aborted_by,aborted_at,abort_reason,receipt,reason,updated_at
			FROM idenqa.key_recovery_ceremonies WHERE id=$1`, identifier).Scan(
			&ceremony.Kind, &ceremony.Class, &tenantID,
			&ceremony.Target.Provider, &ceremony.Target.Reference, &ceremony.Target.Version, &ceremony.Target.Algorithm,
			&ceremony.State, &ceremony.Version, &ceremony.StartedBy, &ceremony.StartedAt, &ceremony.ApproveBy,
			&approvedBy, &approvedAt, &usableUntil, &completedBy, &completedAt,
			&abortedBy, &abortedAt, &abortReason, &receipt, &ceremony.Reason, &ceremony.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return keycustody.ErrNotFound
		}

		return err
	})
	if err != nil {
		return keycustody.RecoveryCeremony{}, err
	}
	ceremony.ID = identifier
	if tenantID != nil {
		parsed, err := id.ParseTenant(*tenantID)
		if err != nil {
			return keycustody.RecoveryCeremony{}, keycustody.ErrUnavailable
		}
		ceremony.TenantID = parsed
	}
	if approvedBy != nil {
		ceremony.ApprovedBy = *approvedBy
	}
	if approvedAt != nil {
		ceremony.ApprovedAt = approvedAt.UTC()
	}
	if usableUntil != nil {
		ceremony.UsableUntil = usableUntil.UTC()
	}
	if completedBy != nil {
		ceremony.CompletedBy = *completedBy
	}
	if completedAt != nil {
		ceremony.CompletedAt = completedAt.UTC()
	}
	if abortedBy != nil {
		ceremony.AbortedBy = *abortedBy
	}
	if abortedAt != nil {
		ceremony.AbortedAt = abortedAt.UTC()
	}
	if abortReason != nil {
		ceremony.AbortReason = *abortReason
	}
	if len(receipt) > 0 {
		if err := json.Unmarshal(receipt, &ceremony.Receipt); err != nil {
			return keycustody.RecoveryCeremony{}, keycustody.ErrUnavailable
		}
	}
	ceremony.StartedAt, ceremony.ApproveBy, ceremony.UpdatedAt = ceremony.StartedAt.UTC(), ceremony.ApproveBy.UTC(), ceremony.UpdatedAt.UTC()

	return ceremony, nil
}

// Advance compare-and-swaps one permitted ceremony transition.
func (store *Store) Advance(ctx context.Context, expected, next keycustody.RecoveryCeremony) (keycustody.RecoveryCeremony, error) {
	if store == nil || expected.ID == "" || next.ID != expected.ID ||
		next.Version != expected.Version+1 || next.State == expected.State {
		return keycustody.RecoveryCeremony{}, keycustody.ErrInvalid
	}
	var approvedBy, completedBy, abortedBy, abortReason any
	var approvedAt, usableUntil, completedAt, abortedAt any
	var receipt any
	if next.ApprovedBy != "" {
		approvedBy, approvedAt, usableUntil = next.ApprovedBy, next.ApprovedAt, next.UsableUntil
	}
	if next.CompletedBy != "" {
		completedBy, completedAt = next.CompletedBy, next.CompletedAt
		if next.Receipt != nil {
			encoded, err := json.Marshal(next.Receipt)
			if err != nil {
				return keycustody.RecoveryCeremony{}, keycustody.ErrInvalid
			}
			receipt = encoded
		}
	}
	if next.AbortedBy != "" {
		abortedBy, abortedAt, abortReason = next.AbortedBy, next.AbortedAt, next.AbortReason
	}
	err := store.pool.WithinTransaction(ctx, pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		tag, err := tx.Exec(ctx, `UPDATE idenqa.key_recovery_ceremonies SET
			state=$4,version=$5,approved_by=$6,approved_at=$7,usable_until=$8,
			completed_by=$9,completed_at=$10,aborted_by=$11,aborted_at=$12,abort_reason=$13,
			receipt=$14,updated_at=$15
			WHERE id=$1 AND version=$2 AND state=$3`,
			expected.ID, expected.Version, expected.State, next.State, next.Version,
			approvedBy, approvedAt, usableUntil, completedBy, completedAt,
			abortedBy, abortedAt, abortReason, receipt, next.UpdatedAt)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return keycustody.ErrConflict
		}

		return nil
	})
	if err != nil {
		return keycustody.RecoveryCeremony{}, err
	}

	return store.Load(ctx, next.ID)
}
