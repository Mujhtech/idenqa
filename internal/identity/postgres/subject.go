package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Mujhtech/idenqa/internal/identity"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

func (s *Store) createSubject(ctx context.Context, tx pg.Transaction, scope tenant.Scope, c identity.Command) (identity.Result, error) {
	identifier, e := s.ids.New("sub")
	if e != nil {
		return identity.Result{}, e
	}
	subject := identity.Subject{ID: identifier.String(), Region: c.Region, State: "active", Version: 1, CreatedAt: c.At, UpdatedAt: c.At}
	key, wrapped, e := s.wrapNew(ctx, "identity.subject.v1", scope.ID().String(), c.Region, subject.ID)
	if e != nil {
		return identity.Result{}, e
	}
	defer clear(key)
	external, lookup, e := s.externalReference(ctx, tx, scope, subject, key, c.ExternalReference)
	if e != nil {
		return identity.Result{}, e
	}
	_, e = tx.Exec(ctx, `INSERT INTO idenqa.identity_subjects(tenant_id,id,region,state,version,external_cipher,external_token,wrapped_key,created_at,updated_at)VALUES($1,$2,$3,'active',1,$4,NULLIF($5,''),$6,$7,$7)`, scope.ID().String(), subject.ID, c.Region, external, lookup, wrapped, c.At)
	return identity.Result{Subject: &subject}, e
}

func (s *Store) externalReference(ctx context.Context, tx pg.Transaction, scope tenant.Scope, subject identity.Subject, key []byte, external *string) ([]byte, string, error) {
	if external == nil || *external == "" {
		return nil, "", nil
	}
	aad, e := contextBytes("identity.external.v1", scope.ID().String(), subject.Region, subject.ID)
	if e != nil {
		return nil, "", e
	}
	sealed, e := sealValue(key, aad, []byte(*external))
	if e != nil {
		return nil, "", e
	}
	indexKey, e := s.lookupKey(ctx, tx, scope, subject.Region, true)
	if e != nil {
		return nil, "", e
	}
	defer clear(indexKey)
	t, e := token(indexKey, "identity.external.v1", scope.ID().String(), subject.Region, *external)
	return sealed, t, e
}

func (s *Store) updateSubject(ctx context.Context, tx pg.Transaction, scope tenant.Scope, c identity.Command, subject identity.Subject, wrapped []byte) (identity.Result, error) {
	var external []byte
	var lookup string
	if c.ExternalReference != nil {
		key, e := s.unwrap(ctx, wrapped, "identity.subject.v1", scope.ID().String(), subject.Region, subject.ID)
		if e != nil {
			return identity.Result{}, e
		}
		defer clear(key)
		external, lookup, e = s.externalReference(ctx, tx, scope, subject, key, c.ExternalReference)
		if e != nil {
			return identity.Result{}, e
		}
	}
	if c.State != "" {
		subject.State = c.State
	}
	subject.Version++
	subject.UpdatedAt = c.At
	_, e := tx.Exec(ctx, `UPDATE idenqa.identity_subjects SET state=$3,version=$4,updated_at=$5,external_cipher=CASE WHEN $6 THEN $7 ELSE external_cipher END,external_token=CASE WHEN $6 THEN NULLIF($8,'') ELSE external_token END WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), subject.ID, subject.State, subject.Version, c.At, c.ExternalReference != nil, external, lookup)
	return identity.Result{Subject: &subject}, e
}
func (s *Store) link(ctx context.Context, tx pg.Transaction, scope tenant.Scope, c identity.Command, subject identity.Subject) (identity.Result, error) {
	if subject.State != "active" {
		return identity.Result{}, identity.ErrConflict
	}
	var region string
	e := tx.QueryRow(ctx, `SELECT region FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, scope.ID().String(), c.VerificationID).Scan(&region)
	if errors.Is(e, pgx.ErrNoRows) {
		return identity.Result{}, identity.ErrNotFound
	}
	if e != nil {
		return identity.Result{}, e
	}
	if region != subject.Region {
		return identity.Result{}, identity.ErrNotFound
	}
	_, e = tx.Exec(ctx, `INSERT INTO idenqa.identity_subject_verifications(tenant_id,subject_id,verification_id,actor_key_id,linked_at)VALUES($1,$2,$3,$4,$5)`, scope.ID().String(), subject.ID, c.VerificationID, c.Actor.String(), c.At)
	if e != nil {
		return identity.Result{}, e
	}
	if e = bumpSubject(ctx, tx, scope, &subject, c.At); e != nil {
		return identity.Result{}, e
	}
	return identity.Result{Subject: &subject, VerificationIDs: []string{c.VerificationID}}, nil
}

func bumpSubject(ctx context.Context, tx pg.Transaction, scope tenant.Scope, subject *identity.Subject, at time.Time) error {
	subject.Version++
	subject.UpdatedAt = at
	_, e := tx.Exec(ctx, `UPDATE idenqa.identity_subjects SET version=$3,updated_at=$4 WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), subject.ID, subject.Version, at)
	return e
}

func configuration(ctx context.Context, tx pg.Transaction, scope tenant.Scope, region string, at time.Time) (identity.Configuration, int64, string, error) {
	var c identity.Configuration
	var v int64
	var digest string
	var b []byte
	e := tx.QueryRow(ctx, `SELECT version,configuration,digest FROM idenqa.identity_configurations WHERE tenant_id=$1 AND region=$2 AND recorded_at<=$3 ORDER BY version DESC LIMIT 1`, scope.ID().String(), region, at).Scan(&v, &b, &digest)
	if errors.Is(e, pgx.ErrNoRows) {
		return c, 0, "", nil
	}
	if e != nil {
		return c, 0, "", e
	}
	if e = json.Unmarshal(b, &c); e != nil {
		return c, 0, "", e
	}
	actual, e := identity.Digest(c)
	if e != nil {
		return c, 0, "", e
	}
	if actual != digest {
		return c, 0, "", identity.ErrUnavailable
	}
	return c, v, digest, c.Validate()
}

func (s *Store) configure(ctx context.Context, tx pg.Transaction, scope tenant.Scope, c identity.Command) (identity.Result, error) {
	if c.Configuration == nil || c.Configuration.Validate() != nil || c.Configuration.Region != c.Region {
		return identity.Result{}, identity.ErrInvalid
	}
	_, version, _, e := configuration(ctx, tx, scope, c.Region, c.At)
	if e != nil {
		return identity.Result{}, e
	}
	if version != c.ExpectedVersion {
		return identity.Result{}, identity.ErrConflict
	}
	digest, e := identity.Digest(c.Configuration)
	if e != nil {
		return identity.Result{}, e
	}
	b, e := json.Marshal(c.Configuration)
	if e != nil {
		return identity.Result{}, e
	}
	_, e = tx.Exec(ctx, `INSERT INTO idenqa.identity_configurations(tenant_id,version,configuration,digest,actor_key_id,recorded_at,region)VALUES($1,$2,$3,$4,$5,$6,$7)`, scope.ID().String(), version+1, b, digest, c.Actor.String(), c.At, c.Region)
	return identity.Result{Version: version + 1, Configuration: c.Configuration, Digest: digest}, e
}

func (s *Store) rebuild(ctx context.Context, tx pg.Transaction, scope tenant.Scope, c identity.Command, subject identity.Subject) (identity.Result, error) {
	if _, e := tx.Exec(ctx, `DELETE FROM idenqa.identity_current WHERE tenant_id=$1 AND subject_id=$2`, scope.ID().String(), subject.ID); e != nil {
		return identity.Result{}, e
	}
	_, e := tx.Exec(ctx, `INSERT INTO idenqa.identity_current(tenant_id,subject_id,kind,name,series_id,record_id)
 SELECT r.tenant_id,r.subject_id,r.kind,r.name,r.series_id,r.id FROM idenqa.identity_records r
 WHERE r.tenant_id=$1 AND r.subject_id=$2 AND NOT EXISTS(SELECT 1 FROM idenqa.identity_records successor WHERE successor.tenant_id=r.tenant_id AND successor.supersedes=r.id)`, scope.ID().String(), subject.ID)
	if e != nil {
		return identity.Result{}, e
	}
	if e = bumpSubject(ctx, tx, scope, &subject, c.At); e != nil {
		return identity.Result{}, e
	}
	return identity.Result{Subject: &subject}, nil
}
