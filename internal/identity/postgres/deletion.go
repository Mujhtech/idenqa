package postgres

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/identity"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/privacy"
	privacypg "github.com/Mujhtech/idenqa/internal/privacy/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type deletionReference struct{ TenantID, SubjectID, Region, DeletionID, ActorID string }

func linkedVerifications(ctx context.Context, tx pg.Transaction, scope tenant.Scope, subject string) ([]id.Verification, error) {
	rows, e := tx.Query(ctx, `SELECT verification_id FROM idenqa.identity_subject_verifications WHERE tenant_id=$1 AND subject_id=$2 ORDER BY verification_id LIMIT 257`, scope.ID().String(), subject)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var ids []id.Verification
	for rows.Next() {
		var v string
		if e = rows.Scan(&v); e != nil {
			return nil, e
		}
		identifier, e := id.ParseVerification(v)
		if e != nil {
			return nil, e
		}
		ids = append(ids, identifier)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	if len(ids) > 256 {
		return nil, identity.ErrUnavailable
	}
	return ids, nil
}
func (s *Store) requestDeletion(ctx context.Context, tx pg.Transaction, scope tenant.Scope, c identity.Command, subject identity.Subject) (identity.Result, error) {
	if s.stopper == nil {
		return identity.Result{}, identity.ErrUnavailable
	}
	verifications, e := linkedVerifications(ctx, tx, scope, subject.ID)
	if e != nil {
		return identity.Result{}, e
	}
	targets := []privacy.Target{}
	for _, verification := range verifications {
		if e = s.stopper.CancelForDeletionWithin(ctx, scope, tx, verification, c.Actor, c.At); e != nil {
			return identity.Result{}, e
		}
		if len(targets) >= 255 {
			return identity.Result{}, identity.ErrUnavailable
		}
		planned, e := privacypg.EvidenceTargetsWithin(ctx, tx, scope, verification.String(), subject.Region, 255-len(targets))
		if e != nil {
			return identity.Result{}, e
		}
		targets = append(targets, planned...)
	}
	deletionID, e := s.ids.NewDeletion()
	if e != nil {
		return identity.Result{}, e
	}
	reference, e := json.Marshal(deletionReference{scope.ID().String(), subject.ID, subject.Region, deletionID.String(), c.Actor.String()})
	if e != nil {
		return identity.Result{}, e
	}
	targets = append(targets, privacy.Target{Kind: "identity_subject", Reference: string(reference), Region: subject.Region})
	deletion, e := privacy.NewDeletion(deletionID, subject.ID, subject.Region, targets, c.At, c.At.Add(privacy.SelectedDefaults()[privacy.DataClassBackup]))
	if e != nil {
		return identity.Result{}, e
	}
	deletion.BackupRetention = privacy.SelectedDefaults()[privacy.DataClassBackup]
	store, e := privacypg.New(s.pool, s.wrapper)
	if e != nil {
		return identity.Result{}, e
	}
	if e = store.CreateWithin(ctx, scope, tx, privacy.Actor{ID: c.Actor.String(), Permissions: []privacy.Permission{privacy.PermissionRequestDeletion}}, deletion); e != nil {
		return identity.Result{}, e
	}
	subject.State = "deleting"
	subject.DeletionID = deletionID.String()
	subject.Version++
	subject.UpdatedAt = c.At
	_, e = tx.Exec(ctx, `UPDATE idenqa.identity_subjects SET state='deleting',deletion_id=$3,version=$4,updated_at=$5 WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), subject.ID, subject.DeletionID, subject.Version, c.At)
	return identity.Result{Subject: &subject, DeletionID: subject.DeletionID}, e
}

// Eraser dispatches exact identity targets alongside the existing evidence eraser.
type Eraser struct {
	store    *Store
	fallback privacy.TargetEraser
	now      func() time.Time
}

// NewEraser preserves existing evidence deletion and adds purpose-owned crypto-shredding.
func NewEraser(store *Store, fallback privacy.TargetEraser, now func() time.Time) (*Eraser, error) {
	if store == nil || fallback == nil || now == nil {
		return nil, identity.ErrInvalid
	}
	return &Eraser{store, fallback, now}, nil
}

// Delete removes identifying values and indexes, preserving immutable reference-only history.
func (e *Eraser) Delete(ctx context.Context, target privacy.Target) error {
	if target.Kind != "identity_subject" {
		return e.fallback.Delete(ctx, target)
	}
	var ref deletionReference
	decoder := json.NewDecoder(strings.NewReader(target.Reference))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&ref); err != nil || ref.Region != target.Region {
		return privacy.ErrInvalid
	}
	tenantID, err := id.ParseTenant(ref.TenantID)
	if err != nil {
		return privacy.ErrInvalid
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		return err
	}
	if _, err = id.ParseSubject(ref.SubjectID); err != nil {
		return privacy.ErrInvalid
	}
	if _, err = id.ParseDeletion(ref.DeletionID); err != nil {
		return privacy.ErrInvalid
	}
	actor, err := id.ParseAPIKey(ref.ActorID)
	if err != nil {
		return privacy.ErrInvalid
	}
	return e.store.pool.WithinTransaction(ctx, pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		subject, _, _, err := loadSubject(ctx, tx, scope, ref.SubjectID, ref.Region, true)
		if err != nil {
			return err
		}
		var authorized bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM idenqa.deletion_requests d JOIN idenqa.deletion_targets t ON t.tenant_id=d.tenant_id AND t.deletion_id=d.id
 WHERE d.tenant_id=$1 AND d.id=$2 AND d.aggregate_id=$3 AND d.region=$4 AND t.kind='identity_subject' AND t.reference=$5)`, ref.TenantID, ref.DeletionID, ref.SubjectID, ref.Region, target.Reference).Scan(&authorized)
		if err != nil {
			return err
		}
		if !authorized {
			return privacy.ErrInvalid
		}
		now := e.now().UTC().Truncate(time.Microsecond)
		held, err := privacypg.RetentionHeldWithin(ctx, tx, scope, subject.ID, now)
		if err != nil {
			return err
		}
		if held {
			return privacy.ErrHeld
		}
		// This also supports pre-admission tombstone replay after restoring an older
		// snapshot whose subject lifecycle still says active.
		if subject.State == "active" || subject.State == "suspended" {
			if e.store.stopper == nil {
				return identity.ErrUnavailable
			}
			verifications, err := linkedVerifications(ctx, tx, scope, subject.ID)
			if err != nil {
				return err
			}
			for _, verification := range verifications {
				if err = e.store.stopper.CancelForDeletionWithin(ctx, scope, tx, verification, actor, now); err != nil {
					return err
				}
			}
		}
		for _, statement := range []string{
			`DELETE FROM idenqa.identity_identifier_tokens WHERE tenant_id=$1 AND subject_id=$2`,
			`DELETE FROM idenqa.identity_current WHERE tenant_id=$1 AND subject_id=$2`,
			`DELETE FROM idenqa.identity_record_values WHERE tenant_id=$1 AND subject_id=$2`,
			`DELETE FROM idenqa.fraud_links WHERE tenant_id=$1 AND verification_id IN(SELECT verification_id FROM idenqa.identity_subject_verifications WHERE tenant_id=$1 AND subject_id=$2)`,
		} {
			if _, err = tx.Exec(ctx, statement, ref.TenantID, ref.SubjectID); err != nil {
				return err
			}
		}
		if subject.ErasedAt != nil && subject.State != "active" && subject.State != "suspended" {
			return nil
		}
		_, err = tx.Exec(ctx, `UPDATE idenqa.identity_subjects SET state='deleting',deletion_id=$3,external_cipher=NULL,external_token=NULL,wrapped_key=NULL,erased_at=$4,version=version+1,updated_at=$4 WHERE tenant_id=$1 AND id=$2`, ref.TenantID, ref.SubjectID, ref.DeletionID, now)
		if err != nil {
			return err
		}
		digest, err := identity.Digest([]string{ref.SubjectID, ref.DeletionID})
		if err != nil {
			return err
		}
		return e.store.event(ctx, tx, scope, "system:privacy-worker", subject.ID, subject.Version+1, "identity.erased", digest, now)
	})
}

var _ privacy.TargetEraser = (*Eraser)(nil)
