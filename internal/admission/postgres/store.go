// Package postgres persists operational admission in the owning Core database.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	admissionv1 "github.com/Mujhtech/idenqa/contracts/admission/v1"
	"github.com/Mujhtech/idenqa/internal/admission"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/jackc/pgx/v5"
)

type runner interface {
	WithinTransaction(context.Context, pg.TransactionOptions, func(context.Context, pg.Transaction) error) error
}

// Store consumes independently pinned policy and an explicitly scoped database.
// Installation uses a separately privileged local control connection; runtime
// admission refuses RLS-bypass roles and cannot install or rewrite snapshots.
type Store struct {
	pool   runner
	policy *admission.Policy
}

// New constructs the optional local ledger without network or default quotas.
func New(pool runner, policy *admission.Policy) (*Store, error) {
	if pool == nil || policy == nil {
		return nil, admission.ErrInvalid
	}
	return &Store{pool: pool, policy: policy}, nil
}

func lock(ctx context.Context, tx pg.Transaction, tenantID string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('admission-v1/'||$1,0))`, tenantID)
	return err
}

func setScope(ctx context.Context, tx pg.Transaction, tenantID string) error {
	if _, err := id.ParseTenant(tenantID); err != nil {
		return admission.ErrInvalid
	}
	_, err := sqlgen.New(tx).SetTenantScope(ctx, tenantID)
	return err
}

func runtime(ctx context.Context, tx pg.Transaction, tenantID string) error {
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT current_setting('idenqa.tenant_id',true)=$1 AND NOT (rolsuper OR rolbypassrls) FROM pg_roles WHERE rolname=current_user`, tenantID).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return admission.ErrInvalid
	}
	return nil
}

func digest(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// advancePolicy consults only the fixed-size pending set, never snapshot
// history. An effective higher revision also fences previously scheduled ones.
func advancePolicy(ctx context.Context, tx pg.Transaction, tenantID string, now time.Time) error {
	if _, err := tx.Exec(ctx, `UPDATE idenqa.admission_heads h SET active_revision=greatest(h.active_revision,(SELECT max(revision) FROM idenqa.admission_pending WHERE tenant_id=$1 AND valid_from<=$2)) WHERE h.tenant_id=$1`, tenantID, now); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM idenqa.admission_pending p USING idenqa.admission_heads h WHERE p.tenant_id=$1 AND h.tenant_id=$1 AND p.revision<=h.active_revision`, tenantID)
	return err
}

// Install durably acknowledges an exact signed revision. Old exact retries are
// harmless; changed meaning, rollback, reused IDs and changed window keys fail.
func (s *Store) Install(ctx context.Context, snapshot admissionv1.Snapshot) error {
	return s.pool.WithinTransaction(ctx, pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		if err := setScope(ctx, tx, snapshot.Scope.TenantID); err != nil {
			return err
		}
		if err := lock(ctx, tx, snapshot.Scope.TenantID); err != nil {
			return err
		}
		var now time.Time
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			return err
		}
		if err := s.policy.Verify(snapshot, now); err != nil {
			return err
		}
		meaning, err := digest(snapshot)
		if err != nil {
			return err
		}
		var priorDigest string
		err = tx.QueryRow(ctx, `SELECT digest FROM idenqa.admission_snapshots WHERE tenant_id=$1 AND revision=$2`, snapshot.Scope.TenantID, snapshot.Revision).Scan(&priorDigest)
		if err == nil {
			if meaning != priorDigest {
				return admission.ErrConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var latest int64
		if err := tx.QueryRow(ctx, `SELECT coalesce(max(revision),0) FROM idenqa.admission_snapshots WHERE tenant_id=$1`, snapshot.Scope.TenantID).Scan(&latest); err != nil {
			return err
		}
		if snapshot.Revision <= latest {
			return admission.ErrConflict
		}
		var reused bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM idenqa.admission_snapshots WHERE tenant_id=$1 AND id=$2)`, snapshot.Scope.TenantID, snapshot.ID).Scan(&reused); err != nil {
			return err
		}
		if reused {
			return admission.ErrConflict
		}
		// A key retains its window meaning across every revision, including a key
		// temporarily omitted then restored. Consumption cannot be reset by policy.
		for _, limit := range snapshot.Limits {
			if _, err := tx.Exec(ctx, `INSERT INTO idenqa.admission_limit_definitions(tenant_id,kind,key,window_nanoseconds) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, snapshot.Scope.TenantID, limit.Kind, limit.Key, int64(limit.Window)); err != nil {
				return err
			}
			var window int64
			if err := tx.QueryRow(ctx, `SELECT window_nanoseconds FROM idenqa.admission_limit_definitions WHERE tenant_id=$1 AND kind=$2 AND key=$3`, snapshot.Scope.TenantID, limit.Kind, limit.Key).Scan(&window); err != nil {
				return err
			}
			if window != int64(limit.Window) {
				return admission.ErrConflict
			}
		}
		raw, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO idenqa.admission_snapshots(tenant_id,revision,id,deployment_id,region,valid_from,valid_until,body,digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, snapshot.Scope.TenantID, snapshot.Revision, snapshot.ID, snapshot.Scope.DeploymentID, snapshot.Scope.Region, snapshot.ValidFrom, snapshot.ValidUntil, raw, meaning); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.admission_heads(tenant_id) VALUES($1) ON CONFLICT DO NOTHING`, snapshot.Scope.TenantID); err != nil {
			return err
		}
		if err := advancePolicy(ctx, tx, snapshot.Scope.TenantID, now); err != nil {
			return err
		}
		if snapshot.ValidFrom.After(now) {
			var pending int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM idenqa.admission_pending WHERE tenant_id=$1`, snapshot.Scope.TenantID).Scan(&pending); err != nil {
				return err
			}
			if pending >= 64 {
				return admission.ErrInvalid
			}
			if _, err := tx.Exec(ctx, `INSERT INTO idenqa.admission_pending(tenant_id,revision,valid_from) VALUES($1,$2,$3)`, snapshot.Scope.TenantID, snapshot.Revision, snapshot.ValidFrom); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(ctx, `UPDATE idenqa.admission_heads SET active_revision=$2 WHERE tenant_id=$1`, snapshot.Scope.TenantID, snapshot.Revision); err != nil {
				return err
			}
			if err := advancePolicy(ctx, tx, snapshot.Scope.TenantID, now); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `INSERT INTO idenqa.admission_events(tenant_id,kind,reference,revision) VALUES($1,'snapshot',$2,$3)`, snapshot.Scope.TenantID, snapshot.ID, snapshot.Revision)
		return err
	})
}

// AdmitWithin is called after source creation and independent policy checks in
// that same transaction. The source row precedes the tenant lock in lock order.
// Exact accepted replay bypasses a later suspension or expiry, never new work.
func (s *Store) AdmitWithin(ctx context.Context, tx pg.Transaction, op admission.Operation) error {
	if _, err := id.ParseVerification(op.ID); err != nil {
		return admission.ErrInvalid
	}
	if _, err := id.ParseTenant(op.TenantID); err != nil || op.Rule == "" {
		return admission.ErrInvalid
	}
	if err := runtime(ctx, tx, op.TenantID); err != nil {
		return err
	}
	var state, profile, sourceRegion string
	var sourceExpiry time.Time
	var policy *string
	var revision uint32
	if err := tx.QueryRow(ctx, `SELECT state,source_profile_id,source_profile_revision,policy_id,region,expires_at FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, op.TenantID, op.ID).Scan(&state, &profile, &revision, &policy, &sourceRegion, &sourceExpiry); err != nil {
		return fmt.Errorf("lock admission source: %w", err)
	}
	if policy == nil {
		return admission.ErrInvalid
	}
	want, err := sessionRule(ctx, tx, op.TenantID, op.ID, profile, revision, *policy)
	if err != nil || want != op.Rule {
		return admission.ErrInvalid
	}
	if err := lock(ctx, tx, op.TenantID); err != nil {
		return err
	}
	meaning, err := digest(op)
	if err != nil {
		return err
	}
	var storedDigest, storedState string
	err = tx.QueryRow(ctx, `SELECT digest,state FROM idenqa.admission_operations WHERE tenant_id=$1 AND id=$2`, op.TenantID, op.ID).Scan(&storedDigest, &storedState)
	if err == nil {
		if storedDigest != meaning || storedState != "active" {
			return admission.ErrConflict
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if state != "created" && state != "collecting" {
		return admission.ErrConflict
	}
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return err
	}
	if !now.Before(sourceExpiry) {
		return admission.ErrConflict
	}
	if err := advancePolicy(ctx, tx, op.TenantID, now); err != nil {
		return err
	}
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT s.body FROM idenqa.admission_heads h JOIN idenqa.admission_snapshots s ON s.tenant_id=h.tenant_id AND s.revision=h.active_revision WHERE h.tenant_id=$1 AND s.tenant_id=$1`, op.TenantID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return admission.ErrUnavailable
	}
	if err != nil {
		return err
	}
	var snapshot admissionv1.Snapshot
	if err = json.Unmarshal(raw, &snapshot); err != nil {
		return admission.ErrInvalid
	}
	if snapshot.Scope.TenantID != op.TenantID || snapshot.Scope.Region != sourceRegion {
		return admission.ErrInvalid
	}
	rule, err := s.policy.Decide(snapshot, op.Rule, now)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO idenqa.admission_operations(tenant_id,id,rule,digest,snapshot_revision,state,accepted_at) VALUES($1,$2,$3,$4,$5,'active',$6)`, op.TenantID, op.ID, op.Rule, meaning, snapshot.Revision, now); err != nil {
		return err
	}
	if err := reserveBudgets(ctx, tx, op, snapshot, rule, now); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO idenqa.admission_events(tenant_id,kind,reference,revision) VALUES($1,'accepted',$2,$3)`, op.TenantID, op.ID, snapshot.Revision)
	return err
}

// RebuildCounter repairs one indexed counter from its durable reservations under
// the shared tenant fence. A caller supplies an operation deadline; no source is
// expired or released by guesswork and no rate/usage quantity is refunded.
func (s *Store) RebuildCounter(ctx context.Context, tenantID, kind, key string, window int64, actor, reason string) error {
	if key == "" || kind == "" || !(admissionv1.Scope{TenantID: actor, DeploymentID: reason, Region: "validation"}).Valid() {
		return admission.ErrInvalid
	}
	return s.pool.WithinTransaction(ctx, pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		if err := setScope(ctx, tx, tenantID); err != nil {
			return err
		}
		if err := runtime(ctx, tx, tenantID); err != nil {
			return err
		}
		if err := lock(ctx, tx, tenantID); err != nil {
			return err
		}
		var before int64
		var wasDirty bool
		if err := tx.QueryRow(ctx, `SELECT used,dirty FROM idenqa.admission_counters WHERE tenant_id=$1 AND kind=$2 AND key=$3 AND window_key=$4 FOR UPDATE`, tenantID, kind, key, window).Scan(&before, &wasDirty); err != nil {
			return err
		}
		var used int64
		if err := tx.QueryRow(ctx, `SELECT coalesce(sum(quantity),0) FROM idenqa.admission_reservations WHERE tenant_id=$1 AND kind=$2 AND key=$3 AND window_key=$4 AND NOT released`, tenantID, kind, key, window).Scan(&used); err != nil {
			return err
		}
		result, err := tx.Exec(ctx, `UPDATE idenqa.admission_counters SET used=$5,dirty=false WHERE tenant_id=$1 AND kind=$2 AND key=$3 AND window_key=$4`, tenantID, kind, key, window, used)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return admission.ErrInvalid
		}
		var now time.Time
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			return err
		}
		reference, _ := digest([]any{kind, key, window, now})
		_, err = tx.Exec(ctx, `INSERT INTO idenqa.admission_events(tenant_id,kind,reference,revision,actor_id,reason_reference,counter_kind,counter_key,counter_window,counter_before,counter_after,counter_was_dirty) VALUES($1,'counter_rebuilt',$2,0,$3,$4,$5,$6,$7,$8,$9,$10)`, tenantID, reference, actor, reason, kind, key, window, before, used, wasDirty)
		return err
	})
}

func reserveBudgets(ctx context.Context, tx pg.Transaction, op admission.Operation, snapshot admissionv1.Snapshot, rule admissionv1.Rule, now time.Time) error {
	limits := make(map[string]admissionv1.Limit, len(snapshot.Limits))
	for _, limit := range snapshot.Limits {
		limits[limit.Kind+":"+limit.Key] = limit
	}
	for _, budget := range rule.Budgets {
		limit := limits[budget.Kind+":"+budget.Key]
		window := int64(0)
		if limit.Window > 0 {
			// Match the issuer's existing UTC fixed-duration quota windows. Store
			// their start instant, rather than choosing a different epoch origin.
			window = now.UTC().Truncate(limit.Window).UnixNano()
		}
		if budget.Quantity > limit.Maximum {
			return admission.QuotaFailure(budget.Kind, true)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.admission_counters(tenant_id,kind,key,window_key,used) VALUES($1,$2,$3,$4,0) ON CONFLICT DO NOTHING`, op.TenantID, budget.Kind, budget.Key, window); err != nil {
			return err
		}
		result, err := tx.Exec(ctx, `UPDATE idenqa.admission_counters SET used=used+$5 WHERE tenant_id=$1 AND kind=$2 AND key=$3 AND window_key=$4 AND NOT dirty AND used<=$6`, op.TenantID, budget.Kind, budget.Key, window, budget.Quantity, limit.Maximum-budget.Quantity)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			var dirty bool
			if err := tx.QueryRow(ctx, `SELECT dirty FROM idenqa.admission_counters WHERE tenant_id=$1 AND kind=$2 AND key=$3 AND window_key=$4`, op.TenantID, budget.Kind, budget.Key, window).Scan(&dirty); err != nil {
				return err
			}
			if dirty {
				return admission.ErrUnavailable
			}
			return admission.QuotaFailure(budget.Kind, false)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.admission_reservations(tenant_id,operation_id,kind,key,window_key,quantity,lifetime) VALUES($1,$2,$3,$4,$5,$6,$7)`, op.TenantID, op.ID, budget.Kind, budget.Key, window, budget.Quantity, budget.Lifetime); err != nil {
			return err
		}
	}
	return nil
}
