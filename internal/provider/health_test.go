package provider_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/observability"
	"github.com/Mujhtech/idenqa/internal/provider"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestDeriveHealthStateMachine(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	policy := provider.DefaultHealthPolicy()
	policy.MinimumSamples = 5
	lastActivity := now.Add(-time.Minute)
	probeNotReady := &provider.HealthProbe{State: provider.HealthNotReady, Code: "runner_unavailable", CheckedAt: now.Add(-time.Second)}
	probeStale := &provider.HealthProbe{State: provider.HealthReady, CheckedAt: now.Add(-policy.StaleAfter - time.Second)}
	for _, test := range []struct {
		name       string
		evidence   provider.HealthEvidence
		continuity *provider.HealthSnapshot
		wantState  provider.HealthState
		wantReason string
		wantStale  bool
	}{
		{name: "no evidence", evidence: provider.HealthEvidence{}, wantState: provider.HealthUnknown, wantReason: provider.HealthReasonNoEvidence},
		{
			name: "healthy window", evidence: provider.HealthEvidence{Completed: 10, LastActivityAt: &lastActivity},
			wantState: provider.HealthReady, wantReason: provider.HealthReasonHealthy,
		},
		{
			name: "elevated failure ratio", evidence: provider.HealthEvidence{Completed: 8, Failed: 2, LastActivityAt: &lastActivity},
			wantState: provider.HealthDegraded, wantReason: provider.HealthReasonElevatedFailures,
		},
		{
			name: "critical failure ratio", evidence: provider.HealthEvidence{Completed: 2, Failed: 6, LastActivityAt: &lastActivity},
			wantState: provider.HealthNotReady, wantReason: provider.HealthReasonCriticalFailures,
		},
		{
			name: "below minimum samples stays ready", evidence: provider.HealthEvidence{Completed: 2, Failed: 2, LastActivityAt: &lastActivity},
			wantState: provider.HealthReady, wantReason: provider.HealthReasonHealthy,
		},
		{
			name: "async backlog degrades", evidence: provider.HealthEvidence{Completed: 10, AsyncUnresolved: policy.AsyncBacklog, LastActivityAt: &lastActivity},
			wantState: provider.HealthDegraded, wantReason: provider.HealthReasonAsyncBacklog,
		},
		{
			name: "async expiry degrades", evidence: provider.HealthEvidence{Completed: 10, AsyncExpired: 1, LastActivityAt: &lastActivity},
			wantState: provider.HealthDegraded, wantReason: provider.HealthReasonAsyncExpired,
		},
		{
			name: "open breaker", evidence: provider.HealthEvidence{Completed: 10, Breaker: provider.BreakerOpen, LastActivityAt: &lastActivity},
			wantState: provider.HealthNotReady, wantReason: provider.HealthReasonCircuitOpen,
		},
		{
			name: "half-open breaker", evidence: provider.HealthEvidence{Completed: 10, Breaker: provider.BreakerHalfOpen, LastActivityAt: &lastActivity},
			wantState: provider.HealthDegraded, wantReason: provider.HealthReasonCircuitHalfOpen,
		},
		{
			name: "fresh runner probe", evidence: provider.HealthEvidence{Probe: probeNotReady},
			wantState: provider.HealthNotReady, wantReason: provider.HealthReasonRunnerNotReady,
		},
		{
			name: "stale runner probe is ignored", evidence: provider.HealthEvidence{Probe: probeStale},
			wantState: provider.HealthUnknown, wantReason: provider.HealthReasonNoEvidence,
		},
		{
			name: "probe unavailable", evidence: provider.HealthEvidence{ProbeError: true},
			wantState: provider.HealthUnknown, wantReason: provider.HealthReasonProbeUnavailable,
		},
		{
			name: "stale activity", evidence: provider.HealthEvidence{LastActivityAt: timePointer(now.Add(-policy.StaleAfter - time.Minute))},
			wantState: provider.HealthUnknown, wantReason: provider.HealthReasonStaleEvidence, wantStale: true,
		},
		{
			name:       "fresh continuity",
			evidence:   provider.HealthEvidence{},
			continuity: &provider.HealthSnapshot{State: provider.HealthDegraded, ReasonCode: provider.HealthReasonElevatedFailures, ObservedAt: now.Add(-time.Minute), Breaker: provider.BreakerClosed},
			wantState:  provider.HealthDegraded, wantReason: provider.HealthReasonElevatedFailures,
		},
		{
			name:       "stale continuity",
			evidence:   provider.HealthEvidence{},
			continuity: &provider.HealthSnapshot{State: provider.HealthReady, ReasonCode: provider.HealthReasonHealthy, ObservedAt: now.Add(-policy.StaleAfter - time.Minute), Breaker: provider.BreakerClosed},
			wantState:  provider.HealthUnknown, wantReason: provider.HealthReasonStaleEvidence, wantStale: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			snapshot := provider.DeriveHealth(test.evidence, test.continuity, now, policy)
			if snapshot.State != test.wantState || snapshot.ReasonCode != test.wantReason {
				t.Fatalf("DeriveHealth() = %s/%s, want %s/%s", snapshot.State, snapshot.ReasonCode, test.wantState, test.wantReason)
			}
			if snapshot.Stale != test.wantStale {
				t.Fatalf("Stale = %v, want %v", snapshot.Stale, test.wantStale)
			}
		})
	}
}

func TestHealthCacheExpiresAndStaysBounded(t *testing.T) {
	t.Parallel()
	clock := &stepClock{at: time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)}
	cache, err := provider.NewHealthCache(time.Second, 2, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := provider.HealthSnapshot{State: provider.HealthReady, ReasonCode: provider.HealthReasonHealthy}
	cache.Put("a", snapshot)
	if _, ok := cache.Get("a"); !ok {
		t.Fatal("fresh entry missing")
	}
	clock.Advance(time.Second)
	if _, ok := cache.Get("a"); ok {
		t.Fatal("expired entry returned")
	}
	clock.Advance(time.Millisecond)
	cache.Put("a", snapshot)
	clock.Advance(time.Millisecond)
	cache.Put("b", snapshot)
	clock.Advance(time.Millisecond)
	cache.Put("c", snapshot)
	if _, ok := cache.Get("a"); ok {
		t.Fatal("oldest entry was not evicted")
	}
	if _, ok := cache.Get("c"); !ok {
		t.Fatal("newest entry missing")
	}
}

func TestHealthServiceCachesEvidenceAndSeedsBreakerFromContinuity(t *testing.T) {
	t.Parallel()
	clock := &stepClock{at: time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)}
	tenantValue, err := id.ParseTenant("ten_01K4AR9V8FQ2G7ZXCPNM5T6JWH")
	if err != nil {
		t.Fatal(err)
	}
	scope, err := tenant.NewScope(tenantValue)
	if err != nil {
		t.Fatal(err)
	}
	key := provider.HealthKey{TenantID: tenantValue.String(), AdapterID: "dojah", ProviderID: "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWH"}
	policy := provider.DefaultHealthPolicy()
	reader := &healthEvidenceFake{evidence: provider.HealthEvidence{AdapterID: "dojah", ProviderID: key.ProviderID, Completed: 6}}
	breakerSince := clock.Now()
	store := &healthStoreFake{snapshot: provider.HealthSnapshot{AdapterID: "dojah", ProviderID: key.ProviderID, State: provider.HealthNotReady,
		ReasonCode: provider.HealthReasonCircuitOpen, ObservedAt: clock.Now(), Breaker: provider.BreakerOpen, BreakerSince: &breakerSince}, exists: true}
	registry, err := provider.NewBreakerRegistry(policy.Breaker, clock.Now, 8)
	if err != nil {
		t.Fatal(err)
	}
	metrics := &recordingProviderMetrics{}
	service, err := provider.NewHealthService(reader, store, registry, nil, policy, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	service.WithMetrics(metrics)
	first, err := service.Health(t.Context(), scope, key)
	if err != nil {
		t.Fatal(err)
	}
	if first.Breaker != provider.BreakerOpen {
		t.Fatalf("breaker = %s, want open", first.Breaker)
	}
	if state, exists := registry.State(key.BreakerKey()); !exists || state != provider.BreakerOpen {
		t.Fatalf("local breaker seed = %s, %v", state, exists)
	}
	second, err := service.Health(t.Context(), scope, key)
	if err != nil {
		t.Fatal(err)
	}
	if second.State != first.State || reader.calls != 1 {
		t.Fatalf("cached health = %s, evidence reads = %d", second.State, reader.calls)
	}
	clock.Advance(policy.CacheTTL)
	if _, err := service.Health(t.Context(), scope, key); err != nil {
		t.Fatal(err)
	}
	if reader.calls != 2 {
		t.Fatalf("expired cache did not refresh: %d", reader.calls)
	}
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	if len(metrics.health) == 0 || metrics.health[0].State != observability.HealthNotReady {
		t.Fatalf("recorded health = %+v", metrics.health)
	}
}

func TestHealthServicePersistenceRefreshesOnlyOncePerTTL(t *testing.T) {
	t.Parallel()
	clock := &stepClock{at: time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)}
	tenantValue, err := id.ParseTenant("ten_01K4AR9V8FQ2G7ZXCPNM5T6JWH")
	if err != nil {
		t.Fatal(err)
	}
	scope, err := tenant.NewScope(tenantValue)
	if err != nil {
		t.Fatal(err)
	}
	key := provider.HealthKey{TenantID: tenantValue.String(), AdapterID: "dojah", ProviderID: "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWH", Region: "africa"}
	policy := provider.DefaultHealthPolicy()
	reader := &healthEvidenceFake{evidence: provider.HealthEvidence{AdapterID: "dojah", ProviderID: key.ProviderID, Failed: 6, Completed: 1}}
	store := &healthStoreFake{}
	service, err := provider.NewHealthService(reader, store, nil, nil, policy, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	service.WithPersistence(true)
	if _, err := service.Health(t.Context(), scope, key); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Health(t.Context(), scope, key); err != nil {
		t.Fatal(err)
	}
	if store.applies != 1 {
		t.Fatalf("persisted refreshes = %d, want 1", store.applies)
	}
	clock.Advance(policy.CacheTTL)
	if _, err := service.Health(t.Context(), scope, key); err != nil {
		t.Fatal(err)
	}
	if store.applies != 2 {
		t.Fatalf("persisted refreshes after TTL = %d, want 2", store.applies)
	}
}

func TestHealthServiceObservesBreakerTransitionOnce(t *testing.T) {
	t.Parallel()
	clock := &stepClock{at: time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)}
	policy := provider.DefaultHealthPolicy()
	reader := &healthEvidenceFake{evidence: provider.HealthEvidence{AdapterID: "dojah", ProviderID: "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWH", Failed: 6, Completed: 1}}
	store := &healthStoreFake{}
	service, err := provider.NewHealthService(reader, store, nil, nil, policy, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	key := provider.BreakerKey{TenantID: "ten_01K4AR9V8FQ2G7ZXCPNM5T6JWH", AdapterID: "dojah", ProviderID: "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWH"}
	transition := provider.BreakerTransition{Key: key, From: provider.BreakerClosed, To: provider.BreakerOpen, At: clock.Now(), Changed: true}
	if err := service.ObserveBreaker(t.Context(), key, transition); err != nil {
		t.Fatal(err)
	}
	if store.applies != 1 || len(store.applied) != 1 {
		t.Fatalf("applies = %d", store.applies)
	}
	applied := store.applied[0]
	if applied.State != provider.HealthNotReady || applied.ReasonCode != provider.HealthReasonCircuitOpen || applied.Breaker != provider.BreakerOpen {
		t.Fatalf("applied snapshot = %+v", applied)
	}
	// A repeated identical transition is not announced twice.
	if err := service.ObserveBreaker(t.Context(), key, transition); err != nil {
		t.Fatal(err)
	}
	// Unchanged transitions are ignored entirely.
	if err := service.ObserveBreaker(t.Context(), key, provider.BreakerTransition{Key: key, Changed: false}); err != nil {
		t.Fatal(err)
	}
	if store.applies != 2 {
		t.Fatalf("unchanged transition persisted: %d", store.applies)
	}
}

func TestSelectHealthyPrefersReadyAndExcludesNotReady(t *testing.T) {
	t.Parallel()
	first := provider.Registration{ID: "pvr_01K4AR9V8FQ2G7ZXCPNM5T6JWH", AdapterID: "dojah", Region: "africa", Enabled: true}
	second := provider.Registration{ID: "pvr_01K4AR9V8FQ2G7ZXCPNM5T6JWK", AdapterID: "dojah", Region: "africa", Enabled: true}
	states := func(entries map[string]provider.HealthState) provider.HealthLookup {
		return func(registration provider.Registration) provider.HealthState { return entries[registration.ID] }
	}
	for _, test := range []struct {
		name    string
		entries []provider.Registration
		health  map[string]provider.HealthState
		want    string
		ok      bool
	}{
		{name: "not ready excluded", entries: []provider.Registration{first, second},
			health: map[string]provider.HealthState{first.ID: provider.HealthUnknown, second.ID: provider.HealthNotReady}, want: first.ID, ok: true},
		{name: "degraded kept alone", entries: []provider.Registration{first},
			health: map[string]provider.HealthState{first.ID: provider.HealthDegraded}, want: first.ID, ok: true},
		{name: "ready preferred over degraded", entries: []provider.Registration{first, second},
			health: map[string]provider.HealthState{first.ID: provider.HealthDegraded, second.ID: provider.HealthReady}, want: second.ID, ok: true},
		{name: "single not ready is excluded", entries: []provider.Registration{first},
			health: map[string]provider.HealthState{first.ID: provider.HealthNotReady}, ok: false},
		{name: "ambiguous ready registrations", entries: []provider.Registration{first, second},
			health: map[string]provider.HealthState{first.ID: provider.HealthReady, second.ID: provider.HealthReady}, ok: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			selected, ok := provider.SelectHealthy(test.entries, "dojah", "africa", states(test.health))
			if ok != test.ok || selected.ID != test.want {
				t.Fatalf("SelectHealthy() = %+v, %v", selected, ok)
			}
		})
	}
	// Health never broadens a route across regions.
	other := provider.Registration{ID: first.ID, AdapterID: "dojah", Region: "europe", Enabled: true}
	if _, ok := provider.SelectHealthy([]provider.Registration{other}, "dojah", "africa", func(provider.Registration) provider.HealthState { return provider.HealthReady }); ok {
		t.Fatal("cross-region registration selected")
	}
	if _, ok := provider.SelectHealthy([]provider.Registration{first, second}, "dojah", "africa", nil); ok {
		t.Fatal("ambiguous registrations selected without health")
	}
}

type healthEvidenceFake struct {
	mu       sync.Mutex
	calls    int
	evidence provider.HealthEvidence
	err      error
}

func (reader *healthEvidenceFake) Evidence(context.Context, tenant.Scope, provider.HealthKey, time.Duration) (provider.HealthEvidence, error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	reader.calls++
	return reader.evidence, reader.err
}

type healthStoreFake struct {
	mu       sync.Mutex
	snapshot provider.HealthSnapshot
	exists   bool
	loads    int
	applies  int
	applied  []provider.HealthSnapshot
}

func (store *healthStoreFake) Load(context.Context, tenant.Scope, provider.HealthKey) (provider.HealthSnapshot, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.loads++
	return store.snapshot, store.exists, nil
}

func (store *healthStoreFake) Apply(_ context.Context, _ tenant.Scope, _ provider.HealthKey, _ provider.Registration, snapshot provider.HealthSnapshot) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.applies++
	store.applied = append(store.applied, snapshot)
	store.snapshot, store.exists = snapshot, true
	return true, nil
}

func timePointer(value time.Time) *time.Time { return &value }
