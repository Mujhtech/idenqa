package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"time"

	auditpg "github.com/Mujhtech/idenqa/internal/audit/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	idempg "github.com/Mujhtech/idenqa/internal/platform/idempotency/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/outbox"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// AssuranceStore persists exact profiles and future-session assignment pointers.
type AssuranceStore struct {
	pool transactionRunner
	ids  interface{ NewEvent() (id.Event, error) }
}

// NewAssuranceStore constructs tenant-scoped profile persistence.
func NewAssuranceStore(pool transactionRunner, ids interface{ NewEvent() (id.Event, error) }) (*AssuranceStore, error) {
	if pool == nil || ids == nil {
		return nil, policy.ErrInvalid
	}
	return &AssuranceStore{pool, ids}, nil
}

// WriteAssurance commits an idempotent profile command with its audit and outbox effects.
func (s *AssuranceStore) WriteAssurance(ctx context.Context, scope tenant.Scope, c policy.AssuranceCommand) (policy.AssuranceResource, error) {
	var result policy.AssuranceResource
	err := s.pool.WithinTransaction(ctx, pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		q := sqlgen.New(tx)
		if _, e := q.SetTenantScope(ctx, scope.ID().String()); e != nil {
			return e
		}
		b, e := json.Marshal(c)
		if e != nil {
			return e
		}
		retry, e := idempotency.NewRequest(scope.ID(), c.Actor, "assurance."+c.Operation, c.Key, b, c.At, 24*time.Hour)
		if e != nil {
			return e
		}
		reserved, e := idempg.Reserve(ctx, q, retry)
		if e != nil {
			return e
		}
		if replay, ok := reserved.Result(); ok {
			return json.Unmarshal(replay.Body(), &result)
		}
		aggregate := ""
		changed := true
		version := int64(0)
		switch c.Operation {
		case "publish":
			if c.Profile == nil {
				return policy.ErrInvalid
			}
			canonical, digest, e := policy.ValidateAssuranceProfile(*c.Profile)
			if e != nil {
				return e
			}
			tag, e := tx.Exec(ctx, `INSERT INTO idenqa.assurance_profiles(tenant_id,name,revision,digest,canonical,actor_key_id,created_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, scope.ID().String(), c.Profile.Name, c.Profile.Revision, digest, string(canonical), c.Actor.String(), c.At)
			if e != nil {
				return e
			}
			if tag.RowsAffected() == 0 {
				changed = false
				var prior string
				if e := tx.QueryRow(ctx, `SELECT digest FROM idenqa.assurance_profiles WHERE tenant_id=$1 AND name=$2 AND revision=$3`, scope.ID().String(), c.Profile.Name, c.Profile.Revision).Scan(&prior); e != nil {
					return e
				}
				if prior != digest {
					return policy.ErrRevisionConflict
				}
			}
			p := *c.Profile
			result = policy.AssuranceResource{Profile: &p, Digest: digest}
			aggregate = "assurance." + p.Name
			version = int64(p.Revision)
		case "assign":
			// Session creation takes the same policy lock before pinning this pointer.
			var locked string
			if e := tx.QueryRow(ctx, `SELECT id FROM idenqa.policies WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, scope.ID().String(), c.PolicyID).Scan(&locked); e != nil {
				return e
			}
			var current int64
			e := tx.QueryRow(ctx, `SELECT version FROM idenqa.policy_assurance_assignments WHERE tenant_id=$1 AND policy_id=$2`, scope.ID().String(), c.PolicyID).Scan(&current)
			if e != nil && !errors.Is(e, pgx.ErrNoRows) {
				return e
			}
			if current != c.ExpectedVersion {
				return policy.ErrRevisionConflict
			}
			var name, digest *string
			var revision *uint32
			if c.Selection != nil {
				name = &c.Selection.Name
				digest = &c.Selection.Digest
				revision = &c.Selection.Revision
			}
			_, e = tx.Exec(ctx, `INSERT INTO idenqa.policy_assurance_assignments(tenant_id,policy_id,version,profile_name,profile_revision,profile_digest) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(tenant_id,policy_id) DO UPDATE SET version=EXCLUDED.version,profile_name=EXCLUDED.profile_name,profile_revision=EXCLUDED.profile_revision,profile_digest=EXCLUDED.profile_digest`, scope.ID().String(), c.PolicyID, current+1, name, revision, digest)
			if e != nil {
				return e
			}
			result = policy.AssuranceResource{Selection: c.Selection, Version: current + 1}
			aggregate = c.PolicyID
			version = current + 1
		default:
			return policy.ErrInvalid
		}
		body, e := json.Marshal(result)
		if e != nil {
			return e
		}
		if !changed {
			receipt, e := idempotency.NewResult(200, body)
			if e != nil {
				return e
			}
			return idempg.Complete(ctx, q, retry, receipt, c.At)
		}
		eventID, e := s.ids.NewEvent()
		if e != nil {
			return e
		}
		digest := assuranceDigest(body)
		if _, e = auditpg.AppendInTransaction(ctx, tx, scope, auditpg.Event{EventID: eventID.String(), EventType: "assurance." + c.Operation, AggregateID: aggregate, ActorID: c.Actor.String(), EventDigest: digest, OccurredAt: c.At}); e != nil {
			return e
		}
		intent, e := outbox.NewIntent(eventID, "assurance", aggregate, version, "assurance."+c.Operation+".v1", 1, struct {
			Digest string `json:"digest"`
		}{digest}, c.At)
		if e != nil {
			return e
		}
		if e = q.InsertOutboxEvent(ctx, sqlgen.InsertOutboxEventParams{ID: intent.ID.String(), TenantID: scope.ID().String(), AggregateType: intent.AggregateType, AggregateID: aggregate, AggregateVersion: version, EventType: intent.EventType, SchemaVersion: 1, Payload: intent.Payload, OccurredAt: timestamp(c.At), CreatedAt: timestamp(c.At)}); e != nil {
			return e
		}
		receipt, e := idempotency.NewResult(200, body)
		if e != nil {
			return e
		}
		return idempg.Complete(ctx, q, retry, receipt, c.At)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		err = policy.ErrRevisionNotFound
	}
	var db *pgconn.PgError
	if errors.As(err, &db) {
		switch db.Code {
		case "23503":
			err = policy.ErrRevisionNotFound
		case "23505", "40001", "40P01":
			err = policy.ErrRevisionConflict
		}
	}
	return result, err
}

// ReadAssurance reads bounded profile metadata under tenant scope.
func (s *AssuranceStore) ReadAssurance(ctx context.Context, scope tenant.Scope, v policy.AssuranceQuery) (policy.AssuranceResource, error) {
	var result policy.AssuranceResource
	e := s.pool.WithinTransaction(ctx, pg.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx pg.Transaction) error {
		if _, e := sqlgen.New(tx).SetTenantScope(ctx, scope.ID().String()); e != nil {
			return e
		}
		if v.Name != "" {
			p, d, e := readAssuranceProfile(ctx, tx, scope, v.Name, v.Revision)
			result.Profile = p
			result.Digest = d
			return e
		}
		if v.PolicyID != "" || v.VerificationID != "" {
			var name, digest *string
			var revision *uint32
			if v.PolicyID != "" {
				e := tx.QueryRow(ctx, `SELECT coalesce(a.version,0),a.profile_name,a.profile_revision,a.profile_digest FROM idenqa.policies p LEFT JOIN idenqa.policy_assurance_assignments a ON a.tenant_id=p.tenant_id AND a.policy_id=p.id WHERE p.tenant_id=$1 AND p.id=$2`, scope.ID().String(), v.PolicyID).Scan(&result.Version, &name, &revision, &digest)
				if e != nil {
					return e
				}
			} else {
				if e := tx.QueryRow(ctx, `SELECT a.profile_name,a.profile_revision,a.profile_digest FROM idenqa.verification_sessions s LEFT JOIN idenqa.verification_assurance a ON a.tenant_id=s.tenant_id AND a.verification_id=s.id WHERE s.tenant_id=$1 AND s.id=$2`, scope.ID().String(), v.VerificationID).Scan(&name, &revision, &digest); e != nil {
					return e
				}
			}
			if name != nil && revision != nil && digest != nil {
				result.Selection = &policy.AssuranceSelection{Name: *name, Revision: *revision, Digest: *digest}
			}
			return nil
		}
		afterName := ""
		afterRevision := uint64(0)
		if v.After != "" {
			separator := strings.LastIndexByte(v.After, ':')
			if separator < 1 {
				return policy.ErrInvalid
			}
			afterName = v.After[:separator]
			var err error
			afterRevision, err = strconv.ParseUint(v.After[separator+1:], 10, 32)
			if err != nil {
				return policy.ErrInvalid
			}
		}
		result.Profiles = []policy.AssuranceSelection{}
		rows, e := tx.Query(ctx, `SELECT name,revision,digest FROM idenqa.assurance_profiles WHERE tenant_id=$1 AND (name,revision)>($2,$3) ORDER BY name,revision LIMIT $4`, scope.ID().String(), afterName, int64(afterRevision), v.Limit+1)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var selection policy.AssuranceSelection
			if e = rows.Scan(&selection.Name, &selection.Revision, &selection.Digest); e != nil {
				return e
			}
			result.Profiles = append(result.Profiles, selection)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		if len(result.Profiles) > v.Limit {
			result.Profiles = result.Profiles[:v.Limit]
			last := result.Profiles[v.Limit-1]
			result.NextCursor = last.Name + ":" + fmt.Sprintf("%010d", last.Revision)
		}
		return nil
	})
	if errors.Is(e, pgx.ErrNoRows) {
		e = policy.ErrRevisionNotFound
	}
	return result, e
}
func readAssuranceProfile(ctx context.Context, tx pg.Transaction, scope tenant.Scope, name string, revision uint32) (*policy.AssuranceProfile, string, error) {
	var canonical, digest string
	if e := tx.QueryRow(ctx, `SELECT canonical,digest FROM idenqa.assurance_profiles WHERE tenant_id=$1 AND name=$2 AND revision=$3`, scope.ID().String(), name, revision).Scan(&canonical, &digest); e != nil {
		return nil, "", e
	}
	var p policy.AssuranceProfile
	if json.Unmarshal([]byte(canonical), &p) != nil {
		return nil, "", policy.ErrReproduction
	}
	b, d, e := policy.ValidateAssuranceProfile(p)
	if e != nil || d != digest || string(b) != canonical || p.Name != name || p.Revision != revision {
		return nil, "", policy.ErrReproduction
	}
	return &p, digest, nil
}

// PinAssuranceWithin snapshots the assignment, including an explicit no-profile row.
// This runs atomically with session creation; changing assignments cannot race it.
func PinAssuranceWithin(ctx context.Context, tx pg.Transaction, scope tenant.Scope, verificationID, policyID string) error {
	if policyID == "" {
		_, e := tx.Exec(ctx, `INSERT INTO idenqa.verification_assurance(tenant_id,verification_id) VALUES($1,$2)`, scope.ID().String(), verificationID)
		return e
	}
	var locked string
	if e := tx.QueryRow(ctx, `SELECT id FROM idenqa.policies WHERE tenant_id=$1 AND id=$2 FOR SHARE`, scope.ID().String(), policyID).Scan(&locked); e != nil {
		return e
	}
	_, e := tx.Exec(ctx, `INSERT INTO idenqa.verification_assurance(tenant_id,verification_id,profile_name,profile_revision,profile_digest) SELECT $1,$2,a.profile_name,a.profile_revision,a.profile_digest FROM (SELECT 1) x LEFT JOIN idenqa.policy_assurance_assignments a ON a.tenant_id=$1 AND a.policy_id=$3`, scope.ID().String(), verificationID, policyID)
	return e
}

// CopyAssuranceWithin preserves requested assurance across recapture children.
func CopyAssuranceWithin(ctx context.Context, tx pg.Transaction, scope tenant.Scope, parent, child string) error {
	_, e := tx.Exec(ctx, `INSERT INTO idenqa.verification_assurance(tenant_id,verification_id,profile_name,profile_revision,profile_digest) SELECT $1,$3,a.profile_name,a.profile_revision,a.profile_digest FROM (SELECT 1) x LEFT JOIN idenqa.verification_assurance a ON a.tenant_id=$1 AND a.verification_id=$2`, scope.ID().String(), parent, child)
	return e
}

// ValidateDecisionAssuranceWithin prevents substituted profiles and stale verified
// completion. Exact committed replay must be resolved before invoking this guard.
func ValidateDecisionAssuranceWithin(ctx context.Context, tx pg.Transaction, scope tenant.Scope, snapshot policy.Snapshot, verified bool, at time.Time) error {
	var digest *string
	e := tx.QueryRow(ctx, `SELECT profile_digest FROM idenqa.verification_assurance WHERE tenant_id=$1 AND verification_id=$2`, scope.ID().String(), snapshot.VerificationID().String()).Scan(&digest)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	expected := ""
	if digest != nil {
		expected = *digest
	}
	actual := ""
	if c := snapshot.Context(); c != nil {
		actual = c.ProfileDigest
	}
	if actual != expected {
		return policy.ErrRevisionConflict
	}
	if verified && actual != "" {
		achievement, e := policy.AchieveAssurance(snapshot.Context(), at)
		if e != nil {
			return e
		}
		if !achievement.Achieved {
			return policy.ErrStaleFact
		}
	}
	return nil
}
