//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	localkms "github.com/Mujhtech/idenqa/internal/platform/kms/local"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/provider"
	providerpostgres "github.com/Mujhtech/idenqa/internal/provider/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// TestProviderHealthSnapshotEmitsDegradedOnce proves the persisted continuity
// snapshot, its transition semantics and the once-per-transition
// provider.degraded catalogue event, including tenant isolation.
func TestProviderHealthSnapshotEmitsDegradedOnce(t *testing.T) {
	database := createIsolatedDatabase(t)
	ctx := t.Context()
	migrator, err := platformpostgres.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatal(err)
	}
	admin, err := platformpostgres.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	generator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	tenantID, err := generator.NewTenant()
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.tenants (id,state,version,created_at,updated_at) VALUES ($1,'active',1,$2,$2)`, tenantID.String(), now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	runtimeConfig := poolConfig(database.url)
	runtimeConfig.Role = database.createRuntimeRole(t)
	runtime, err := platformpostgres.Open(ctx, runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := localkms.Create(filepath.Join(t.TempDir(), "provider-health-keyring.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = keyring.Close() }()
	store, err := providerpostgres.NewHealthStore(runtime, fixedIntegrationClock{now: now}, keyring)
	if err != nil {
		t.Fatal(err)
	}
	providerID, err := generator.NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	key := provider.HealthKey{TenantID: tenantID.String(), AdapterID: "dojah", ProviderID: providerID.String(), Region: "africa"}
	observed := now
	snapshot := provider.HealthSnapshot{AdapterID: "dojah", ProviderID: providerID.String(), Region: "africa",
		State: provider.HealthNotReady, ReasonCode: provider.HealthReasonCriticalFailures, ObservedAt: observed,
		Window: time.Minute, Completed: 2, Failed: 6, FailureRatio: 0.75,
		FailureClasses: []provider.FailureClassCount{{Class: string(providerv1.FailureUnavailable), Count: 6}},
		Breaker:        provider.BreakerOpen, BreakerSince: &observed}
	changed, err := store.Apply(ctx, scope, key, provider.Registration{}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("initial degraded observation was not a transition")
	}
	// An identical retry of the same transition is not announced twice.
	if changed, err = store.Apply(ctx, scope, key, provider.Registration{}, snapshot); err != nil || changed {
		t.Fatalf("identical apply = %v, %v", changed, err)
	}
	if count := countProviderDegradedEvents(t, admin, tenantID.String(), providerID.String()); count != 1 {
		t.Fatalf("degraded events = %d, want 1", count)
	}
	observedDegraded := now.Add(time.Minute)
	degraded := snapshot
	degraded.State, degraded.ReasonCode = provider.HealthDegraded, provider.HealthReasonElevatedFailures
	degraded.Breaker, degraded.BreakerSince, degraded.ObservedAt = provider.BreakerClosed, nil, observedDegraded
	if changed, err = store.Apply(ctx, scope, key, provider.Registration{}, degraded); err != nil || !changed {
		t.Fatalf("degraded apply = %v, %v", changed, err)
	}
	if count := countProviderDegradedEvents(t, admin, tenantID.String(), providerID.String()); count != 2 {
		t.Fatalf("degraded events after transition = %d, want 2", count)
	}
	// A recovery to ready is persisted but is not a degradation event.
	observedReady := now.Add(2 * time.Minute)
	ready := degraded
	ready.State, ready.ReasonCode, ready.ObservedAt = provider.HealthReady, provider.HealthReasonHealthy, observedReady
	ready.Completed, ready.Failed, ready.FailureRatio = 10, 0, 0
	if changed, err = store.Apply(ctx, scope, key, provider.Registration{}, ready); err != nil || !changed {
		t.Fatalf("ready apply = %v, %v", changed, err)
	}
	if count := countProviderDegradedEvents(t, admin, tenantID.String(), providerID.String()); count != 2 {
		t.Fatalf("recovery emitted an event: %d", count)
	}
	loaded, exists, err := store.Load(ctx, scope, key)
	if err != nil || !exists {
		t.Fatalf("Load() = %v, %v", exists, err)
	}
	if loaded.State != provider.HealthReady || loaded.ReasonCode != provider.HealthReasonHealthy || loaded.Breaker != provider.BreakerClosed || loaded.Completed != 10 {
		t.Fatalf("loaded snapshot = %+v", loaded)
	}
	// A persisted event binds the bounded provider reference, never a payload.
	var aggregates []string
	if err := admin.Native().QueryRow(ctx, `SELECT aggregate_ids FROM idenqa.webhook_events WHERE tenant_id=$1 AND event_type='provider.degraded' ORDER BY created_at LIMIT 1`, tenantID.String()).Scan(&aggregates); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, aggregate := range aggregates {
		if aggregate == providerID.String() {
			found = true
		}
	}
	if !found {
		t.Fatalf("event aggregate references = %v", aggregates)
	}
	// RLS hides health snapshots even when a query omits its tenant predicate.
	if err := runtime.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM idenqa.provider_health_snapshots`).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return errors.New("unscoped health snapshots visible")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestProviderHealthBreakerRoutingAndRecovery proves, against PostgreSQL, that
// an open breaker fails fast without an identity outcome, bounds its half-open
// probe, recovers after success, derives ready/not_ready from owned dispatch
// evidence and prefers the healthy registration.
func TestProviderHealthBreakerRoutingAndRecovery(t *testing.T) {
	runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
		service, _, actor, manifests := providerRegistrationFixture(t, f)
		manifest := manifests["dojah"]
		deployedID, err := f.ids.NewProvider()
		if err != nil {
			t.Fatal(err)
		}
		deploymentConfiguration := providerRegistrationWrite(t, manifest, deployedID.String(), "secret://provider/dojah/deployment").Configuration
		binding := providerRegistrationBinding(f, deploymentConfiguration)
		createRegistration := func(secret string) provider.Registration {
			providerID, err := f.ids.NewProvider()
			if err != nil {
				t.Fatal(err)
			}
			write := providerRegistrationWrite(t, manifest, providerID.String(), secret)
			created, err := service.Execute(t.Context(), actor, "register-"+providerID.String(), provider.RegistrationCommand{Operation: "create", Reason: "onboarding", Write: &write})
			if err != nil {
				t.Fatal(err)
			}
			return created.Registration
		}
		healthyRegistration := createRegistration("secret://provider/dojah/health")
		if _, err := service.Execute(t.Context(), actor, "", provider.RegistrationCommand{Operation: "enable",
			RegistrationID: healthyRegistration.ID, ExpectedVersion: healthyRegistration.Version, Reason: "enable"}); err != nil {
			t.Fatal(err)
		}
		healthyPlan, err := provider.NewRegisteredPlan(healthyRegistration, binding, manifest)
		if err != nil {
			t.Fatal(err)
		}
		unhealthyRegistration := createRegistration("secret://provider/dojah/unhealthy")
		unhealthyPlan, err := provider.NewRegisteredPlan(unhealthyRegistration, binding, manifest)
		if err != nil {
			t.Fatal(err)
		}
		recordDispatch := func(plan *provider.Plan, key string, outcome string, class string) providerv1.Request {
			request := persistSelectedProviderRequest(t, f, plan, key)
			body := map[string]any{"outcome": outcome}
			if class != "" {
				body["failure"] = map[string]any{"class": class, "code": "provider_unavailable"}
			}
			encoded, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.admin.Native().Exec(t.Context(), `INSERT INTO idenqa.provider_dispatches(tenant_id,attempt_id,request_digest,result_body,claimed_at) VALUES($1,$2,$3,$4,$5)`,
				f.scope.ID().String(), request.AttemptID, requestDigestOf(t, request), encoded, f.now); err != nil {
				t.Fatal(err)
			}
			return request
		}
		var executionRequest providerv1.Request
		for index := range 2 {
			executionRequest = recordDispatch(healthyPlan, fmt.Sprintf("healthy-failure-%d", index), "failed", "unavailable")
		}
		for index := range 4 {
			recordDispatch(healthyPlan, fmt.Sprintf("healthy-completed-%d", index), "completed", "")
		}
		for index := range 2 {
			recordDispatch(unhealthyPlan, fmt.Sprintf("unhealthy-failure-%d", index), "failed", "unavailable")
		}
		healthClock := &integrationHealthClock{at: f.now}
		policy := provider.DefaultHealthPolicy()
		policy.Window, policy.MinimumSamples, policy.DegradedFailureRatio, policy.NotReadyFailureRatio = time.Minute, 2, 0.4, 0.6
		policy.StaleAfter, policy.CacheTTL = time.Hour, time.Second
		policy.Breaker = provider.BreakerPolicy{Window: time.Minute, MinimumSamples: 2, FailureRatio: 0.5, OpenDuration: 30 * time.Second, HalfOpenProbes: 1}
		if err := policy.Validate(); err != nil {
			t.Fatal(err)
		}
		keyring, err := localkms.Create(filepath.Join(t.TempDir(), "provider-health-keyring.json"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = keyring.Close() }()
		healthStore, err := providerpostgres.NewHealthStore(f.runtime, healthClock, keyring)
		if err != nil {
			t.Fatal(err)
		}
		registry, err := provider.NewBreakerRegistry(policy.Breaker, healthClock.Now, 8)
		if err != nil {
			t.Fatal(err)
		}
		health, err := provider.NewHealthService(healthStore, healthStore, registry, nil, policy, healthClock.Now)
		if err != nil {
			t.Fatal(err)
		}
		health.WithRegion(binding.Region).WithPersistence(true)
		healthyState, err := health.RegistrationHealth(t.Context(), f.scope, healthyRegistration)
		if err != nil {
			t.Fatal(err)
		}
		unhealthyState, err := health.RegistrationHealth(t.Context(), f.scope, unhealthyRegistration)
		if err != nil {
			t.Fatal(err)
		}
		if healthyState.State != provider.HealthReady || unhealthyState.State != provider.HealthNotReady {
			t.Fatalf("derived health = %s/%s", healthyState.State, unhealthyState.State)
		}
		states := map[string]provider.HealthState{healthyRegistration.ID: healthyState.State, unhealthyRegistration.ID: unhealthyState.State}
		healthyCandidate, unhealthyCandidate := healthyRegistration, unhealthyRegistration
		healthyCandidate.Enabled, unhealthyCandidate.Enabled = true, true
		selected, ok := provider.SelectHealthy([]provider.Registration{healthyCandidate, unhealthyCandidate}, "dojah", binding.Region,
			func(registration provider.Registration) provider.HealthState { return states[registration.ID] })
		if !ok || selected.ID != healthyRegistration.ID {
			t.Fatalf("routing selection = %+v, %v", selected, ok)
		}
		executor := &scriptedExecutor{build: []func(providerv1.Request) providerv1.Result{
			func(request providerv1.Request) providerv1.Result {
				return providerFailureResult(request, providerv1.FailureUnavailable)
			},
			func(request providerv1.Request) providerv1.Result {
				return providerFailureResult(request, providerv1.FailureUnavailable)
			},
			func(request providerv1.Request) providerv1.Result {
				return providerCompletedResult(request)
			},
		}}
		breakerExecutor, err := provider.NewBreakerExecutor(executor, registry, health, healthClock.Now)
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if _, err := breakerExecutor.Execute(t.Context(), executionRequest); err != nil {
				t.Fatal(err)
			}
		}
		if executor.calls != 2 {
			t.Fatalf("inner calls = %d, want 2", executor.calls)
		}
		if count := countProviderDegradedEvents(t, f.admin, f.scope.ID().String(), healthyRegistration.Configuration.ProviderID); count != 1 {
			t.Fatalf("opening transition events = %d, want 1", count)
		}
		result, err := breakerExecutor.Execute(t.Context(), executionRequest)
		if err != nil {
			t.Fatal(err)
		}
		if executor.calls != 2 {
			t.Fatalf("open breaker dispatched: calls=%d", executor.calls)
		}
		if result.Outcome != providerv1.ResultOutcomeFailed || result.Failure == nil || result.Failure.Code != "provider_circuit_open" ||
			result.Failure.Class != providerv1.FailureUnavailable || len(result.Signals) != 0 {
			t.Fatalf("open breaker result = %+v", result)
		}
		// The open duration admits one bounded half-open probe that closes the
		// breaker on success.
		healthClock.Advance(31 * time.Second)
		recovered, err := breakerExecutor.Execute(t.Context(), executionRequest)
		if err != nil {
			t.Fatal(err)
		}
		if executor.calls != 3 || recovered.Outcome != providerv1.ResultOutcomeCompleted || len(recovered.Signals) == 0 {
			t.Fatalf("half-open recovery = %+v calls=%d", recovered, executor.calls)
		}
		recoveredState, err := health.RegistrationHealth(t.Context(), f.scope, healthyRegistration)
		if err != nil {
			t.Fatal(err)
		}
		if recoveredState.State != provider.HealthReady || recoveredState.Breaker != provider.BreakerClosed {
			t.Fatalf("recovered health = %+v", recoveredState)
		}
		// A second immediate dispatch must not open another probe window.
		if _, err := breakerExecutor.Execute(t.Context(), executionRequest); err != nil || executor.calls != 4 {
			t.Fatalf("post-recovery dispatch = %d, %v", executor.calls, err)
		}
		if count := countProviderDegradedEvents(t, f.admin, f.scope.ID().String(), healthyRegistration.Configuration.ProviderID); count != 1 {
			t.Fatalf("recovery emitted an event: %d", count)
		}
	})
}

type integrationHealthClock struct {
	mu sync.Mutex
	at time.Time
}

func (clock *integrationHealthClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.at
}

func (clock *integrationHealthClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.at = clock.at.Add(duration)
}

type scriptedExecutor struct {
	calls int
	build []func(providerv1.Request) providerv1.Result
}

func (executor *scriptedExecutor) Execute(_ context.Context, request providerv1.Request) (providerv1.Result, error) {
	index := executor.calls
	if index >= len(executor.build) {
		index = len(executor.build) - 1
	}
	executor.calls++
	return executor.build[index](request), nil
}

func providerFailureResult(request providerv1.Request, class providerv1.FailureClass) providerv1.Result {
	return providerv1.Result{Contract: request.Contract, AttemptID: request.AttemptID, Outcome: providerv1.ResultOutcomeFailed,
		Failure: &providerv1.Failure{Class: class, Code: "provider_unavailable", Retry: providerv1.RetryBackoff}, CompletedAt: request.Deadline.Add(-time.Second)}
}

func providerCompletedResult(request providerv1.Request) providerv1.Result {
	return providerv1.Result{Contract: request.Contract, AttemptID: request.AttemptID, Outcome: providerv1.ResultOutcomeCompleted,
		Signals: []providerv1.Signal{{Name: "idenqa.signal.document_quality", Outcome: providerv1.SignalOutcomeSatisfied}}, CompletedAt: request.Deadline.Add(-time.Second)}
}

func countProviderDegradedEvents(t *testing.T, pool *platformpostgres.Pool, tenantID, providerReference string) int {
	t.Helper()
	var count int
	if err := pool.Native().QueryRow(t.Context(), `SELECT count(*) FROM idenqa.webhook_events
		WHERE tenant_id=$1 AND event_type='provider.degraded' AND aggregate_ids && ARRAY[$2]`, tenantID, providerReference).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
