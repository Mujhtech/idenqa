package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	webhookv1 "github.com/Mujhtech/idenqa/contracts/webhook/v1"
	deliverypostgres "github.com/Mujhtech/idenqa/internal/delivery/postgres"
	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/objectstore"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/privacy"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

type objectDeleter interface {
	Delete(context.Context, objectstore.Object) error
}

type evidenceReference struct {
	TenantID, EvidenceID, Region string
	Object                       objectstore.ObjectRecord
	Version                      int64
}

// EvidenceEraser removes one exact ciphertext version and only then marks its
// immutable metadata record deleted. It is safe to replay after either step.
type EvidenceEraser struct {
	pool    transactionRunner
	objects objectDeleter
	now     func() time.Time
}

// NewEvidenceEraser constructs the exact evidence target eraser.
func NewEvidenceEraser(pool transactionRunner, objects objectDeleter, now func() time.Time) (*EvidenceEraser, error) {
	if pool == nil || objects == nil || now == nil {
		return nil, privacy.ErrInvalid
	}
	return &EvidenceEraser{pool: pool, objects: objects, now: now}, nil
}

// EvidenceTargets plans raw and derived evidence ciphertext from authoritative
// tenant-scoped metadata rather than accepting object references from callers.
func (store *Store) EvidenceTargets(ctx context.Context, scope tenant.Scope, aggregateID, region string) ([]privacy.Target, error) {
	var result []privacy.Target
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id,retention_class,object_key,object_version,ciphertext_size,ciphertext_checksum,version
			FROM idenqa.evidence_assets WHERE tenant_id=$1 AND verification_id=$2 AND region=$3 AND state <> 'deleted'
			ORDER BY created_at,id`, scope.ID().String(), aggregateID, region)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var reference evidenceReference
			var retentionClass string
			reference.TenantID, reference.Region = scope.ID().String(), region
			if err := rows.Scan(&reference.EvidenceID, &retentionClass, &reference.Object.Key, &reference.Object.Version, &reference.Object.Size, &reference.Object.Checksum, &reference.Version); err != nil {
				return err
			}
			if _, err := objectstore.NewObject(reference.Object); err != nil {
				return privacy.ErrInvalid
			}
			encoded, err := json.Marshal(reference)
			if err != nil {
				return err
			}
			kind := string(privacy.DataClassRawEvidence)
			if retentionClass == string(privacy.DataClassDerivedEvidence) {
				kind = string(privacy.DataClassDerivedEvidence)
			}
			result = append(result, privacy.Target{Kind: kind, Reference: string(encoded), Region: region})
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(result) == 0 {
			return privacy.ErrInvalid
		}
		return nil
	})
	return result, err
}

// Delete implements privacy.TargetEraser.
func (eraser *EvidenceEraser) Delete(ctx context.Context, target privacy.Target) error {
	if eraser == nil || target.Region == "" || (target.Kind != string(privacy.DataClassRawEvidence) && target.Kind != string(privacy.DataClassDerivedEvidence)) {
		return privacy.ErrInvalid
	}
	var reference evidenceReference
	decoder := json.NewDecoder(strings.NewReader(target.Reference))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&reference); err != nil || reference.Region != target.Region || reference.TenantID == "" || reference.EvidenceID == "" || reference.Version < 1 {
		return privacy.ErrInvalid
	}
	object, err := objectstore.NewObject(reference.Object)
	if err != nil {
		return privacy.ErrInvalid
	}
	now := eraser.now().UTC()
	return eraser.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationSerializable}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		var scopeValue string
		if err := tx.QueryRow(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, reference.TenantID).Scan(&scopeValue); err != nil {
			return err
		}

		tenantID, err := id.ParseTenant(reference.TenantID)
		if err != nil {
			return privacy.ErrInvalid
		}
		scope, err := tenant.NewScope(tenantID)
		if err != nil {
			return err
		}
		var verificationID string
		if err = tx.QueryRow(ctx, `SELECT verification_id FROM idenqa.evidence_assets WHERE tenant_id=$1 AND id=$2`, reference.TenantID, reference.EvidenceID).Scan(&verificationID); err != nil {
			return err
		}
		if err = LockRetentionAggregateWithin(ctx, tx, scope, verificationID); err != nil {
			return err
		}
		aggregate := verificationID
		var subjectID string
		err = tx.QueryRow(ctx, `SELECT s.id FROM idenqa.identity_subjects s JOIN idenqa.identity_subject_verifications l ON l.tenant_id=s.tenant_id AND l.subject_id=s.id WHERE s.tenant_id=$1 AND l.verification_id=$2 AND s.state='deleting'`, reference.TenantID, verificationID).Scan(&subjectID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil {
			aggregate = subjectID
		}
		held, err := RetentionHeldWithin(ctx, tx, scope, aggregate, eraser.now().UTC())
		if err != nil {
			return err
		}
		if held {
			return privacy.ErrHeld
		}
		var state, key, version, checksum string
		var size, aggregateVersion int64
		err = tx.QueryRow(ctx, `SELECT state,object_key,object_version,ciphertext_size,ciphertext_checksum,version FROM idenqa.evidence_assets
			WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, reference.TenantID, reference.EvidenceID).Scan(&state, &key, &version, &size, &checksum, &aggregateVersion)
		if errors.Is(err, pgx.ErrNoRows) {
			return evidence.ErrNotFound
		}
		if err != nil {
			return err
		}
		if key != reference.Object.Key || version != reference.Object.Version || size != reference.Object.Size || checksum != reference.Object.Checksum {
			return privacy.ErrConflict
		}

		if state != string(evidence.StateDeleted) && aggregateVersion != reference.Version {
			return privacy.ErrConflict
		}
		if err = eraser.objects.Delete(ctx, object); err != nil {
			return fmt.Errorf("delete evidence ciphertext: %w", err)
		}
		if state == string(evidence.StateDeleted) {
			return nil
		}
		tag, err := tx.Exec(ctx, `UPDATE idenqa.evidence_assets SET state='deleted',version=version+1,updated_at=$3
			WHERE tenant_id=$1 AND id=$2 AND version=$4 AND state IN ('available','quarantined')`, reference.TenantID, reference.EvidenceID, now, reference.Version)
		if err != nil || tag.RowsAffected() != 1 {
			return errors.Join(privacy.ErrConflict, err)
		}
		_, err = tx.Exec(ctx, `INSERT INTO idenqa.evidence_asset_audit
			(tenant_id,evidence_id,aggregate_version,action,reason,occurred_at) VALUES ($1,$2,$3,'delete','retention.deleted',$4)`,
			reference.TenantID, reference.EvidenceID, reference.Version+1, now)
		if err != nil {
			return err
		}
		if err := deliverypostgres.EmitCatalogueEvent(ctx, tx, reference.TenantID, reference.Region, webhookv1.EvidenceDeleted,
			"evidence.deleted:"+reference.EvidenceID, now, map[string]any{
				"evidence_id":     reference.EvidenceID,
				"verification_id": verificationID,
				"evidence":        map[string]any{"id": reference.EvidenceID, "type": "evidence", "verification_id": verificationID, "state": "deleted"},
			}); err != nil {
			return err
		}
		return nil
	})
}

var _ privacy.TargetEraser = (*EvidenceEraser)(nil)
