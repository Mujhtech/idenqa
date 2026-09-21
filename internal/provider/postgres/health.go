package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	webhookv1 "github.com/Mujhtech/idenqa/contracts/webhook/v1"
	deliverypostgres "github.com/Mujhtech/idenqa/internal/delivery/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/provider"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

// HealthStore reads bounded rolling-window dispatch evidence and persists the
// derived health snapshot for continuity and cross-process visibility. The
// optional key wrapper enables the once-per-transition provider.degraded event;
// without it snapshots are still persisted.
type HealthStore struct {
	pool    transactionRunner
	source  clock.Clock
	wrapper platformcrypto.KeyWrapper
}

// NewHealthStore constructs bounded provider health persistence.
func NewHealthStore(pool transactionRunner, source clock.Clock, wrapper platformcrypto.KeyWrapper) (*HealthStore, error) {
	if pool == nil || source == nil {
		return nil, provider.ErrHealthInvalid
	}
	return &HealthStore{pool: pool, source: source, wrapper: wrapper}, nil
}

// Evidence returns the bounded rolling-window dispatch, asynchronous,
// callback and expiry counts for one tenant provider configuration boundary.
// It never reads evidence bytes or provider payloads.
func (store *HealthStore) Evidence(ctx context.Context, scope tenant.Scope, key provider.HealthKey, window time.Duration) (provider.HealthEvidence, error) {
	evidence := provider.HealthEvidence{AdapterID: key.AdapterID, ProviderID: key.ProviderID, Region: key.Region, Window: window}
	if scope.ID().IsZero() || !key.Valid() || key.TenantID != scope.ID().String() || window < time.Second || window > 24*time.Hour {
		return provider.HealthEvidence{}, provider.ErrHealthInvalid
	}
	since := store.source.Now().UTC().Add(-window)
	err := store.readScoped(ctx, scope, func(ctx context.Context, tx pg.Transaction) error {
		var classes []byte
		var lastActivity *time.Time
		if err := tx.QueryRow(ctx, `WITH windowed AS (
				SELECT dispatch.result_body, dispatch.claimed_at
				FROM idenqa.provider_dispatches AS dispatch
				JOIN idenqa.provider_requests AS requests ON requests.tenant_id=dispatch.tenant_id AND requests.attempt_id=dispatch.attempt_id
				WHERE dispatch.tenant_id=$1
					AND requests.request_body->'configuration'->>'provider_id'=$2
					AND requests.request_body->'adapter'->>'adapter_id'=$3
					AND dispatch.claimed_at >= $4
			)
			SELECT
				count(*) FILTER (WHERE result_body->>'outcome'='completed'),
				count(*) FILTER (WHERE result_body->>'outcome'='failed'),
				(SELECT coalesce(jsonb_agg(jsonb_build_object('class', class, 'count', count) ORDER BY class), '[]'::jsonb)
					FROM (SELECT coalesce(result_body->'failure'->>'class', 'unknown') AS class, count(*) AS count
						FROM windowed WHERE result_body->>'outcome'='failed' GROUP BY 1) AS grouped),
				(SELECT max(claimed_at) FROM windowed)
			FROM windowed`,
			scope.ID().String(), key.ProviderID, key.AdapterID, since).
			Scan(&evidence.Completed, &evidence.Failed, &classes, &lastActivity); err != nil {
			return fmt.Errorf("read provider health dispatches: %w", err)
		}
		if len(classes) > 0 {
			if err := json.Unmarshal(classes, &evidence.FailureClasses); err != nil {
				return provider.ErrHealthInvalid
			}
		}
		if lastActivity != nil {
			utc := lastActivity.UTC()
			evidence.LastActivityAt = &utc
		}
		now := store.source.Now().UTC()
		if err := tx.QueryRow(ctx, `SELECT
				count(*) FILTER (WHERE (requests.request_body->>'deadline')::timestamptz >= $4 AND NOT EXISTS (
					SELECT 1 FROM idenqa.provider_dispatches AS dispatch
					WHERE dispatch.tenant_id=operations.tenant_id AND dispatch.attempt_id=operations.attempt_id AND dispatch.result_body IS NOT NULL)),
				count(*) FILTER (WHERE (requests.request_body->>'deadline')::timestamptz < $4 AND NOT EXISTS (
					SELECT 1 FROM idenqa.provider_dispatches AS dispatch
					WHERE dispatch.tenant_id=operations.tenant_id AND dispatch.attempt_id=operations.attempt_id AND dispatch.result_body IS NOT NULL))
			FROM idenqa.provider_async_operations AS operations
			JOIN idenqa.provider_requests AS requests ON requests.tenant_id=operations.tenant_id AND requests.attempt_id=operations.attempt_id
			WHERE operations.tenant_id=$1
				AND requests.request_body->'configuration'->>'provider_id'=$2
				AND requests.request_body->'adapter'->>'adapter_id'=$3`,
			scope.ID().String(), key.ProviderID, key.AdapterID, now).Scan(&evidence.AsyncUnresolved, &evidence.AsyncExpired); err != nil {
			return fmt.Errorf("read provider health async operations: %w", err)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*)
			FROM idenqa.provider_callback_receipts AS receipts
			JOIN idenqa.provider_requests AS requests ON requests.tenant_id=receipts.tenant_id AND requests.attempt_id=receipts.attempt_id
			WHERE receipts.tenant_id=$1
				AND requests.request_body->'configuration'->>'provider_id'=$2
				AND requests.request_body->'adapter'->>'adapter_id'=$3
				AND receipts.result_digest IS NOT NULL AND receipts.received_at >= $4`,
			scope.ID().String(), key.ProviderID, key.AdapterID, since).Scan(&evidence.CallbacksAdopted); err != nil {
			return fmt.Errorf("read provider health callbacks: %w", err)
		}
		return nil
	})
	if err != nil {
		return provider.HealthEvidence{}, err
	}
	return evidence, nil
}

// Load returns one persisted bounded snapshot for continuity. It never
// performs an external probe.
func (store *HealthStore) Load(ctx context.Context, scope tenant.Scope, key provider.HealthKey) (provider.HealthSnapshot, bool, error) {
	if scope.ID().IsZero() || !key.Valid() || key.TenantID != scope.ID().String() {
		return provider.HealthSnapshot{}, false, provider.ErrHealthInvalid
	}
	var snapshot provider.HealthSnapshot
	found := false
	err := store.readScoped(ctx, scope, func(ctx context.Context, tx pg.Transaction) error {
		var state, reason, breaker, region string
		var breakerSince *time.Time
		var classes []byte
		err := tx.QueryRow(ctx, `SELECT state,reason_code,breaker_state,breaker_since,region,window_seconds,completed_dispatches,failed_dispatches,failure_ratio,failure_classes,async_unresolved_dispatches,async_expired_dispatches,callbacks_adopted,observed_at
			FROM idenqa.provider_health_snapshots WHERE tenant_id=$1 AND adapter_id=$2 AND provider_id=$3 AND region=$4`,
			scope.ID().String(), key.AdapterID, key.ProviderID, key.Region).
			Scan(&state, &reason, &breaker, &breakerSince, &region, &snapshot.Window, &snapshot.Completed, &snapshot.Failed, &snapshot.FailureRatio, &classes, &snapshot.AsyncUnresolved, &snapshot.AsyncExpired, &snapshot.CallbacksAdopted, &snapshot.ObservedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("load provider health snapshot: %w", err)
		}
		found = true
		snapshot.AdapterID, snapshot.ProviderID, snapshot.Region = key.AdapterID, key.ProviderID, region
		snapshot.State, snapshot.ReasonCode, snapshot.Breaker = provider.HealthState(state), reason, provider.BreakerState(breaker)
		if breakerSince != nil {
			utc := breakerSince.UTC()
			snapshot.BreakerSince = &utc
		}
		if len(classes) > 0 {
			if err := json.Unmarshal(classes, &snapshot.FailureClasses); err != nil {
				return provider.ErrHealthInvalid
			}
		}
		snapshot.ObservedAt = snapshot.ObservedAt.UTC()
		return nil
	})
	if err != nil {
		return provider.HealthSnapshot{}, false, err
	}
	return snapshot, found, nil
}

// Apply persists one derived snapshot and emits provider.degraded exactly once
// per transition into a degraded or not-ready state. The persisted prior state,
// not the caller's local transition, decides emission so concurrent workers
// cannot announce the same transition twice.
func (store *HealthStore) Apply(ctx context.Context, scope tenant.Scope, key provider.HealthKey, registration provider.Registration, snapshot provider.HealthSnapshot) (bool, error) {
	if scope.ID().IsZero() || !key.Valid() || key.TenantID != scope.ID().String() || snapshot.State.Safe() != string(snapshot.State) ||
		!validRegistrationToken(snapshot.ReasonCode) || (snapshot.Breaker != provider.BreakerClosed && snapshot.Breaker != provider.BreakerOpen && snapshot.Breaker != provider.BreakerHalfOpen) ||
		snapshot.Window < time.Second || snapshot.ObservedAt.IsZero() {
		return false, provider.ErrHealthInvalid
	}
	if registration.ID != "" && (registration.TenantID != scope.ID().String() || registration.AdapterID != key.AdapterID ||
		registration.Configuration.ProviderID != key.ProviderID || (registration.Region != "" && registration.Region != snapshot.Region)) {
		return false, provider.ErrHealthInvalid
	}
	registrationID := any(nil)
	if registration.ID != "" {
		registrationID = registration.ID
	}
	region := snapshot.Region
	if region == "" {
		region = key.Region
	}
	if !validRegistrationToken(region) {
		return false, provider.ErrHealthInvalid
	}
	classes, err := json.Marshal(snapshot.FailureClasses)
	if err != nil || len(snapshot.FailureClasses) == 0 {
		classes = nil
	}
	// The bounded invariant requires a closed breaker to carry no open
	// timestamp and an open or half-open breaker to carry one. A local breaker
	// read without its open instant falls back to the observation time rather
	// than inventing a transition.
	breakerSince := snapshot.BreakerSince
	if snapshot.Breaker == provider.BreakerClosed {
		breakerSince = nil
	} else if breakerSince == nil {
		observed := snapshot.ObservedAt.UTC()
		breakerSince = &observed
	}
	changed := false
	err = store.pool.WithinTransaction(ctx, pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		if _, err := sqlgen.New(tx).SetTenantScope(ctx, scope.ID().String()); err != nil {
			return err
		}
		var previous string
		priorErr := tx.QueryRow(ctx, `SELECT state FROM idenqa.provider_health_snapshots WHERE tenant_id=$1 AND adapter_id=$2 AND provider_id=$3 AND region=$4 FOR UPDATE`,
			scope.ID().String(), key.AdapterID, key.ProviderID, region).Scan(&previous)
		if priorErr != nil && !errors.Is(priorErr, pgx.ErrNoRows) {
			return fmt.Errorf("lock provider health snapshot: %w", priorErr)
		}
		// An initial degraded observation is a transition from an unknown prior
		// state and must be announced once, like any later transition.
		changed = previous != string(snapshot.State)
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.provider_health_snapshots(
				tenant_id,adapter_id,provider_id,registration_id,region,state,reason_code,breaker_state,breaker_since,window_seconds,
				completed_dispatches,failed_dispatches,failure_ratio,failure_classes,async_unresolved_dispatches,async_expired_dispatches,
				callbacks_adopted,observed_at,version)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,1)
			ON CONFLICT (tenant_id,adapter_id,provider_id,region) DO UPDATE SET
				registration_id=COALESCE(EXCLUDED.registration_id, idenqa.provider_health_snapshots.registration_id),
				region=EXCLUDED.region,
				state=EXCLUDED.state, reason_code=EXCLUDED.reason_code,
				breaker_state=EXCLUDED.breaker_state, breaker_since=EXCLUDED.breaker_since,
				window_seconds=EXCLUDED.window_seconds, completed_dispatches=EXCLUDED.completed_dispatches,
				failed_dispatches=EXCLUDED.failed_dispatches, failure_ratio=EXCLUDED.failure_ratio,
				failure_classes=EXCLUDED.failure_classes, async_unresolved_dispatches=EXCLUDED.async_unresolved_dispatches,
				async_expired_dispatches=EXCLUDED.async_expired_dispatches, callbacks_adopted=EXCLUDED.callbacks_adopted,
				observed_at=EXCLUDED.observed_at, version=idenqa.provider_health_snapshots.version+1`,
			scope.ID().String(), key.AdapterID, key.ProviderID, registrationID, region, string(snapshot.State), snapshot.ReasonCode,
			string(snapshot.Breaker), breakerSince, int64(snapshot.Window/time.Second), snapshot.Completed, snapshot.Failed,
			snapshot.FailureRatio, classes, snapshot.AsyncUnresolved, snapshot.AsyncExpired, snapshot.CallbacksAdopted, snapshot.ObservedAt); err != nil {
			return fmt.Errorf("persist provider health snapshot: %w", err)
		}
		if !changed || store.wrapper == nil || (snapshot.State != provider.HealthDegraded && snapshot.State != provider.HealthNotReady) {
			return nil
		}
		reference := snapshot.ProviderID
		if registration.ID != "" {
			reference = registration.ID
		}
		fields := map[string]any{
			"provider_id": reference,
			"region":      region,
			"state":       string(snapshot.State),
			"reason_code": snapshot.ReasonCode,
			"observed_at": snapshot.ObservedAt.UTC().Format(time.RFC3339),
		}
		seed := "provider.degraded:" + reference + ":" + previous + ":" + string(snapshot.State) + ":" + snapshot.ObservedAt.UTC().Format(time.RFC3339Nano)
		return deliverypostgres.EmitCatalogueEvent(ctx, tx, store.wrapper, scope.ID().String(), region, webhookv1.ProviderDegraded, seed, snapshot.ObservedAt.UTC(), fields)
	})
	if err != nil {
		return false, err
	}
	return changed, nil
}

func (store *HealthStore) readScoped(ctx context.Context, scope tenant.Scope, work func(context.Context, pg.Transaction) error) error {
	if scope.ID().IsZero() {
		return provider.ErrHealthInvalid
	}
	return store.pool.WithinTransaction(ctx, pg.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx pg.Transaction) error {
		if _, err := sqlgen.New(tx).SetTenantScope(ctx, scope.ID().String()); err != nil {
			return err
		}
		return work(ctx, tx)
	})
}

var (
	_ provider.HealthEvidenceReader = (*HealthStore)(nil)
	_ provider.HealthSnapshotStore  = (*HealthStore)(nil)
)
