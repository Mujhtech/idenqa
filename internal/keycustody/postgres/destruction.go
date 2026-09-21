package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Mujhtech/idenqa/internal/keycustody"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
)

// RecordVerification persists one immutable reference-verification receipt.
func (store *Store) RecordVerification(ctx context.Context, receipt keycustody.VerificationReceipt) error {
	if store == nil || receipt.ID == "" || !receipt.Target.Valid() {
		return keycustody.ErrInvalid
	}
	encoded, err := json.Marshal(receipt.Counts)
	if err != nil {
		return keycustody.ErrInvalid
	}
	if err := store.pool.WithinTransaction(ctx, pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.key_destruction_verifications
			(id,provider,reference,version,algorithm,state,counts,total,verifier,reason,digest,verified_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			receipt.ID, receipt.Target.Provider, receipt.Target.Reference, receipt.Target.Version,
			receipt.Target.Algorithm, receipt.State, encoded, receipt.Total, receipt.Verifier,
			receipt.Reason, receipt.Digest, receipt.VerifiedAt)

		return err
	}); err != nil {
		return fmt.Errorf("record destruction verification: %w", err)
	}

	return nil
}

// Verification loads one immutable receipt.
func (store *Store) Verification(ctx context.Context, identifier string) (keycustody.VerificationReceipt, error) {
	var receipt keycustody.VerificationReceipt
	var encoded []byte
	err := store.pool.WithinTransaction(ctx, pg.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx pg.Transaction) error {
		err := tx.QueryRow(ctx, `SELECT provider,reference,version,algorithm,state,counts,total,verifier,reason,digest,verified_at
			FROM idenqa.key_destruction_verifications WHERE id=$1`, identifier).Scan(
			&receipt.Target.Provider, &receipt.Target.Reference, &receipt.Target.Version, &receipt.Target.Algorithm,
			&receipt.State, &encoded, &receipt.Total, &receipt.Verifier, &receipt.Reason, &receipt.Digest, &receipt.VerifiedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return keycustody.ErrNotFound
		}

		return err
	})
	if err != nil {
		return keycustody.VerificationReceipt{}, err
	}
	if err := json.Unmarshal(encoded, &receipt.Counts); err != nil {
		return keycustody.VerificationReceipt{}, keycustody.ErrUnavailable
	}
	receipt.ID = identifier
	receipt.VerifiedAt = receipt.VerifiedAt.UTC()

	return receipt, nil
}

// RecordSchedule persists one provider scheduling receipt.
func (store *Store) RecordSchedule(ctx context.Context, schedule keycustody.DestructionSchedule) error {
	if store == nil || schedule.ID == "" || schedule.VerificationID == "" || !schedule.Target.Valid() {
		return keycustody.ErrInvalid
	}
	var deletionAt any
	if schedule.ProviderDeletionAt != nil {
		deletionAt = *schedule.ProviderDeletionAt
	}
	if err := store.pool.WithinTransaction(ctx, pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.key_destruction_schedules
			(id,verification_id,provider,reference,version,algorithm,mode,provider_deletion_at,actor,reason,created_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			schedule.ID, schedule.VerificationID, schedule.Target.Provider, schedule.Target.Reference,
			schedule.Target.Version, schedule.Target.Algorithm, schedule.Mode, deletionAt,
			schedule.Actor, schedule.Reason, schedule.CreatedAt)

		return err
	}); err != nil {
		return fmt.Errorf("record destruction schedule: %w", err)
	}

	return nil
}

// Scan counts every durable reference to one exact wrapping identity in one
// consistent repeatable-read snapshot across all tenants.
func (store *Store) Scan(ctx context.Context, target keycustody.DestructionTarget) (map[keycustody.ReferenceClass]int64, error) {
	if store == nil || ctx == nil || !target.Valid() {
		return nil, keycustody.ErrInvalid
	}
	counts := make(map[keycustody.ReferenceClass]int64, len(keycustody.ReferenceClasses()))
	err := store.pool.WithinTransaction(ctx, pg.TransactionOptions{Isolation: pg.IsolationRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx pg.Transaction) error {
		after := ""
		for {
			tenants, err := listRewrapTenants(ctx, tx, after, 500)
			if err != nil {
				return err
			}
			if len(tenants) == 0 {
				return nil
			}
			for _, tenantID := range tenants {
				if err := setTenantScope(ctx, tx, tenantID); err != nil {
					return err
				}
				for _, class := range keycustody.ReferenceClasses() {
					count, err := countReferences(ctx, tx, tenantID, class, target)
					if err != nil {
						return err
					}
					counts[class] += count
				}
			}
			after = tenants[len(tenants)-1]
		}
	})
	if err != nil {
		return nil, err
	}

	return counts, nil
}

func listRewrapTenants(ctx context.Context, tx pg.Transaction, after string, limit int) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT tenant_id FROM idenqa.list_key_rewrap_tenants($1,$2)`, after, limit)
	if err != nil {
		return nil, fmt.Errorf("list destruction tenants: %w", err)
	}
	defer rows.Close()
	var tenants []string
	for rows.Next() {
		var tenantID string
		if err := rows.Scan(&tenantID); err != nil {
			return nil, err
		}
		tenants = append(tenants, tenantID)
	}

	return tenants, rows.Err()
}

func countReferences(ctx context.Context, tx pg.Transaction, tenantID string, class keycustody.ReferenceClass, target keycustody.DestructionTarget) (int64, error) {
	var count int64
	var err error
	switch class {
	case keycustody.ReferenceEvidenceAsset:
		err = tx.QueryRow(ctx, `SELECT count(*) FROM idenqa.evidence_assets
			WHERE tenant_id=$1 AND key_provider=$2 AND key_reference=$3 AND key_version=$4 AND key_algorithm=$5`,
			tenantID, target.Provider, target.Reference, target.Version, target.Algorithm).Scan(&count)
	case keycustody.ReferenceWebhookEvent:
		err = tx.QueryRow(ctx, `SELECT count(*) FROM idenqa.webhook_events
			WHERE tenant_id=$1 AND body IS NOT NULL AND body_provider=$2 AND body_reference=$3 AND body_key_version=$4 AND body_algorithm=$5`,
			tenantID, target.Provider, target.Reference, target.Version, target.Algorithm).Scan(&count)
	case keycustody.ReferenceWebhookDelivery:
		err = tx.QueryRow(ctx, `SELECT count(*) FROM idenqa.webhook_deliveries
			WHERE tenant_id=$1 AND body IS NOT NULL AND body_provider=$2 AND body_reference=$3 AND body_key_version=$4 AND body_algorithm=$5`,
			tenantID, target.Provider, target.Reference, target.Version, target.Algorithm).Scan(&count)
	case keycustody.ReferenceWebhookSecret:
		err = tx.QueryRow(ctx, `SELECT count(*) FROM idenqa.webhook_secrets
			WHERE tenant_id=$1 AND provider=$2 AND reference=$3 AND key_version=$4 AND algorithm=$5`,
			tenantID, target.Provider, target.Reference, target.Version, target.Algorithm).Scan(&count)
	case keycustody.ReferenceHMACKey:
		err = tx.QueryRow(ctx, `SELECT count(*) FROM idenqa.hmac_keys
			WHERE tenant_id=$1 AND wrapped_key->>'Provider'=$2 AND wrapped_key->>'Reference'=$3
			AND wrapped_key->>'Version'=$4 AND wrapped_key->>'Algorithm'=$5`,
			tenantID, target.Provider, target.Reference, target.Version, target.Algorithm).Scan(&count)
	case keycustody.ReferenceIdentityLookupKey:
		err = tx.QueryRow(ctx, `SELECT count(*) FROM idenqa.identity_keys
			WHERE tenant_id=$1 AND wrapped_key->>'Provider'=$2 AND wrapped_key->>'Reference'=$3
			AND wrapped_key->>'Version'=$4 AND wrapped_key->>'Algorithm'=$5`,
			tenantID, target.Provider, target.Reference, target.Version, target.Algorithm).Scan(&count)
	case keycustody.ReferenceIdentitySubjectKey:
		err = tx.QueryRow(ctx, `SELECT count(*) FROM idenqa.identity_subjects
			WHERE tenant_id=$1 AND wrapped_key IS NOT NULL AND wrapped_key->>'Provider'=$2
			AND wrapped_key->>'Reference'=$3 AND wrapped_key->>'Version'=$4 AND wrapped_key->>'Algorithm'=$5`,
			tenantID, target.Provider, target.Reference, target.Version, target.Algorithm).Scan(&count)
	case keycustody.ReferenceFraudKey:
		err = tx.QueryRow(ctx, `SELECT count(*) FROM idenqa.fraud_keys
			WHERE tenant_id=$1 AND wrapped_key->>'Provider'=$2 AND wrapped_key->>'Reference'=$3
			AND wrapped_key->>'Version'=$4 AND wrapped_key->>'Algorithm'=$5`,
			tenantID, target.Provider, target.Reference, target.Version, target.Algorithm).Scan(&count)
	case keycustody.ReferenceIdentityToken:
		err = tx.QueryRow(ctx, `SELECT count(*) FROM idenqa.identity_identifier_tokens AS tokens
			JOIN idenqa.hmac_keys AS keys ON keys.tenant_id=tokens.tenant_id
			 AND keys.domain='identity.identifier.v1' AND keys.version=tokens.key_version
			WHERE tokens.tenant_id=$1 AND keys.wrapped_key->>'Provider'=$2 AND keys.wrapped_key->>'Reference'=$3
			AND keys.wrapped_key->>'Version'=$4 AND keys.wrapped_key->>'Algorithm'=$5`,
			tenantID, target.Provider, target.Reference, target.Version, target.Algorithm).Scan(&count)
	default:
		return 0, keycustody.ErrInvalid
	}
	if err != nil {
		return 0, fmt.Errorf("count %s references: %w", class, err)
	}

	return count, nil
}

func setTenantScope(ctx context.Context, tx pg.Transaction, tenantID string) error {
	var value string

	return tx.QueryRow(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, tenantID).Scan(&value)
}
