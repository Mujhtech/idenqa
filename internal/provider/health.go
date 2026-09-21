package provider

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/observability"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// ErrHealthInvalid identifies an invalid bounded provider health request.
var ErrHealthInvalid = errors.New("provider: invalid provider health configuration")

// HealthState is a bounded provider readiness label.
type HealthState string

// Bounded provider readiness states.
const (
	HealthUnknown  HealthState = "unknown"
	HealthReady    HealthState = "ready"
	HealthDegraded HealthState = "degraded"
	HealthNotReady HealthState = "not_ready"
)

// Safe returns the canonical label, collapsing unknown states.
func (state HealthState) Safe() string {
	switch state {
	case HealthReady, HealthDegraded, HealthNotReady:
		return string(state)
	default:
		return string(HealthUnknown)
	}
}

// Stable bounded health reason codes.
const (
	HealthReasonNoEvidence       = "no_evidence"
	HealthReasonHealthy          = "healthy"
	HealthReasonStaleEvidence    = "stale_evidence"
	HealthReasonElevatedFailures = "elevated_failure_ratio"
	HealthReasonCriticalFailures = "critical_failure_ratio"
	HealthReasonAsyncBacklog     = "async_backlog"
	HealthReasonAsyncExpired     = "async_expired"
	HealthReasonCircuitOpen      = "circuit_open"
	HealthReasonCircuitHalfOpen  = "circuit_half_open"
	HealthReasonRunnerDegraded   = "runner_degraded"
	HealthReasonRunnerNotReady   = "runner_not_ready"
	HealthReasonProbeUnavailable = "runner_probe_unavailable"
)

// HealthProbe is one bounded runner probe result read through the existing
// provider HealthChecker surface. It is never invented by this package.
type HealthProbe struct {
	State     HealthState `json:"state"`
	Code      string      `json:"code,omitempty"`
	CheckedAt time.Time   `json:"checked_at"`
}

// FailureClassCount is one bounded dispatch failure class count.
type FailureClassCount struct {
	Class string `json:"class"`
	Count int64  `json:"count"`
}

// HealthEvidence is the bounded rolling-window evidence owned by Core for one
// provider configuration boundary. It contains counts only.
type HealthEvidence struct {
	AdapterID        string
	ProviderID       string
	Region           string
	Window           time.Duration
	Completed        int64
	Failed           int64
	FailureClasses   []FailureClassCount
	AsyncUnresolved  int64
	AsyncExpired     int64
	CallbacksAdopted int64
	LastActivityAt   *time.Time
	Probe            *HealthProbe
	ProbeError       bool
	Breaker          BreakerState
	BreakerSince     *time.Time
}

// HealthPolicy bounds the rolling window, thresholds, continuity, cache and
// breaker behaviour.
type HealthPolicy struct {
	Window               time.Duration
	MinimumSamples       int64
	DegradedFailureRatio float64
	NotReadyFailureRatio float64
	AsyncBacklog         int64
	StaleAfter           time.Duration
	CacheTTL             time.Duration
	ProbeTimeout         time.Duration
	Breaker              BreakerPolicy
}

// DefaultHealthPolicy returns the selected bounded baseline policy.
func DefaultHealthPolicy() HealthPolicy {
	return HealthPolicy{
		Window: 5 * time.Minute, MinimumSamples: 5, DegradedFailureRatio: 0.2,
		NotReadyFailureRatio: 0.5, AsyncBacklog: 16, StaleAfter: 15 * time.Minute,
		CacheTTL: 10 * time.Second, ProbeTimeout: 2 * time.Second, Breaker: DefaultBreakerPolicy(),
	}
}

// Validate checks the bounded policy bounds.
func (policy HealthPolicy) Validate() error {
	if policy.Window < time.Second || policy.Window > 24*time.Hour ||
		policy.MinimumSamples < 1 || policy.MinimumSamples > 1000 ||
		policy.DegradedFailureRatio <= 0 || policy.DegradedFailureRatio > 1 ||
		policy.NotReadyFailureRatio <= policy.DegradedFailureRatio || policy.NotReadyFailureRatio > 1 ||
		policy.AsyncBacklog < 1 || policy.AsyncBacklog > 1_000_000 ||
		policy.StaleAfter < policy.Window || policy.StaleAfter > 7*24*time.Hour ||
		policy.CacheTTL < time.Second || policy.CacheTTL > 10*time.Minute ||
		policy.ProbeTimeout < 100*time.Millisecond || policy.ProbeTimeout > 10*time.Second ||
		policy.Breaker.Validate() != nil {
		return ErrHealthInvalid
	}
	return nil
}

// HealthSnapshot is the bounded derived readiness of one provider
// configuration boundary. It never contains tenant identifiers beyond the
// owning scope or any provider payload.
type HealthSnapshot struct {
	AdapterID        string              `json:"adapter_id"`
	ProviderID       string              `json:"provider_id"`
	Region           string              `json:"region,omitempty"`
	State            HealthState         `json:"state"`
	ReasonCode       string              `json:"reason_code"`
	ObservedAt       time.Time           `json:"observed_at"`
	Window           time.Duration       `json:"window,omitempty"`
	Completed        int64               `json:"completed_dispatches"`
	Failed           int64               `json:"failed_dispatches"`
	FailureRatio     float64             `json:"failure_ratio"`
	FailureClasses   []FailureClassCount `json:"failure_classes,omitempty"`
	AsyncUnresolved  int64               `json:"async_unresolved_dispatches"`
	AsyncExpired     int64               `json:"async_expired_dispatches"`
	CallbacksAdopted int64               `json:"callbacks_adopted"`
	Breaker          BreakerState        `json:"breaker_state"`
	BreakerSince     *time.Time          `json:"breaker_since,omitempty"`
	Stale            bool                `json:"stale"`
	Continuity       bool                `json:"continuity"`
}

// HealthKey identifies one bounded health boundary. The dispatch envelope
// carries no registration identifier, so the adapter and resolved provider
// configuration are the stable owned key.
type HealthKey struct {
	TenantID   string
	AdapterID  string
	ProviderID string
	Region     string
}

// Valid reports whether the key can identify one bounded health boundary.
func (key HealthKey) Valid() bool {
	return key.TenantID != "" && (key.AdapterID != "" || key.ProviderID != "")
}

// HealthKeyForRegistration derives the bounded key from one persisted tenant
// registration without broadening its region.
func HealthKeyForRegistration(registration Registration) HealthKey {
	return HealthKey{TenantID: registration.TenantID, AdapterID: registration.AdapterID,
		ProviderID: registration.Configuration.ProviderID, Region: registration.Region}
}

// BreakerKey projects the bounded breaker key.
func (key HealthKey) BreakerKey() BreakerKey {
	return BreakerKey{TenantID: key.TenantID, AdapterID: key.AdapterID, ProviderID: key.ProviderID}
}

// DeriveHealth reduces bounded owned evidence and optional persisted continuity
// into one bounded snapshot. It never changes evidence meaning or assurance.
func DeriveHealth(evidence HealthEvidence, continuity *HealthSnapshot, now time.Time, policy HealthPolicy) HealthSnapshot {
	now = now.UTC()
	snapshot := HealthSnapshot{
		AdapterID: evidence.AdapterID, ProviderID: evidence.ProviderID, Region: evidence.Region,
		ObservedAt: now, Window: policy.Window, Completed: evidence.Completed, Failed: evidence.Failed,
		FailureClasses: slices.Clone(evidence.FailureClasses), AsyncUnresolved: evidence.AsyncUnresolved,
		AsyncExpired: evidence.AsyncExpired, CallbacksAdopted: evidence.CallbacksAdopted,
		Breaker: BreakerClosed,
	}
	samples := evidence.Completed + evidence.Failed
	if samples > 0 {
		snapshot.FailureRatio = float64(evidence.Failed) / float64(samples)
	}
	if evidence.Breaker != BreakerUnknown && evidence.Breaker != "" {
		snapshot.Breaker = evidence.Breaker
		snapshot.BreakerSince = evidence.BreakerSince
	} else if continuity != nil {
		snapshot.Breaker = continuity.Breaker
		snapshot.BreakerSince = continuity.BreakerSince
	}
	switch snapshot.Breaker {
	case BreakerOpen:
		return finishHealth(snapshot, HealthNotReady, HealthReasonCircuitOpen)
	case BreakerHalfOpen:
		return finishHealth(snapshot, HealthDegraded, HealthReasonCircuitHalfOpen)
	}
	if samples == 0 {
		if evidence.ProbeError && continuity == nil {
			return finishHealth(snapshot, HealthUnknown, HealthReasonProbeUnavailable)
		}
		if evidence.Probe != nil && probeFresh(evidence.Probe, now, policy.StaleAfter) {
			switch evidence.Probe.State {
			case HealthNotReady:
				return finishHealth(snapshot, HealthNotReady, HealthReasonRunnerNotReady)
			case HealthDegraded:
				return finishHealth(snapshot, HealthDegraded, HealthReasonRunnerDegraded)
			case HealthReady:
				return finishHealth(snapshot, HealthReady, HealthReasonHealthy)
			}
		}
		if evidence.AsyncUnresolved >= policy.AsyncBacklog {
			return finishHealth(snapshot, HealthDegraded, HealthReasonAsyncBacklog)
		}
		if continuity != nil {
			return deriveContinuity(snapshot, continuity, now, policy)
		}
		if evidence.LastActivityAt != nil && now.Sub(evidence.LastActivityAt.UTC()) > policy.StaleAfter {
			snapshot.Stale = true
			return finishHealth(snapshot, HealthUnknown, HealthReasonStaleEvidence)
		}
		return finishHealth(snapshot, HealthUnknown, HealthReasonNoEvidence)
	}
	if evidence.AsyncUnresolved >= policy.AsyncBacklog {
		return finishHealth(snapshot, HealthDegraded, HealthReasonAsyncBacklog)
	}
	if evidence.AsyncExpired > 0 {
		return finishHealth(snapshot, HealthDegraded, HealthReasonAsyncExpired)
	}
	if samples >= policy.MinimumSamples {
		switch {
		case snapshot.FailureRatio >= policy.NotReadyFailureRatio:
			return finishHealth(snapshot, HealthNotReady, HealthReasonCriticalFailures)
		case snapshot.FailureRatio >= policy.DegradedFailureRatio:
			return finishHealth(snapshot, HealthDegraded, HealthReasonElevatedFailures)
		}
	}
	return finishHealth(snapshot, HealthReady, HealthReasonHealthy)
}

func probeFresh(probe *HealthProbe, now time.Time, staleAfter time.Duration) bool {
	age := now.Sub(probe.CheckedAt.UTC())
	return age >= 0 && age <= staleAfter
}

func deriveContinuity(snapshot HealthSnapshot, continuity *HealthSnapshot, now time.Time, policy HealthPolicy) HealthSnapshot {
	age := now.Sub(continuity.ObservedAt.UTC())
	if age < 0 || age > policy.StaleAfter {
		snapshot.Stale = true
		return finishHealth(snapshot, HealthUnknown, HealthReasonStaleEvidence)
	}
	continued := snapshot
	continued.State = HealthState(continuity.State.Safe())
	continued.ReasonCode = continuity.ReasonCode
	continued.ObservedAt = continuity.ObservedAt.UTC()
	continued.Completed = continuity.Completed
	continued.Failed = continuity.Failed
	continued.FailureRatio = continuity.FailureRatio
	continued.FailureClasses = slices.Clone(continuity.FailureClasses)
	continued.AsyncUnresolved = continuity.AsyncUnresolved
	continued.AsyncExpired = continuity.AsyncExpired
	continued.CallbacksAdopted = continuity.CallbacksAdopted
	continued.BreakerSince = continuity.BreakerSince
	continued.Continuity = true
	return continued
}

func finishHealth(snapshot HealthSnapshot, state HealthState, reason string) HealthSnapshot {
	snapshot.State = state
	snapshot.ReasonCode = reason
	return snapshot
}

// HealthCache is a bounded in-process snapshot cache with a TTL.
type HealthCache struct {
	mu      sync.Mutex
	now     func() time.Time
	ttl     time.Duration
	limit   int
	entries map[string]healthCacheEntry
}

type healthCacheEntry struct {
	snapshot HealthSnapshot
	at       time.Time
}

// NewHealthCache constructs a bounded TTL cache.
func NewHealthCache(ttl time.Duration, limit int, now func() time.Time) (*HealthCache, error) {
	if ttl < time.Second || ttl > time.Hour || limit < 1 || limit > 1_000_000 || now == nil {
		return nil, ErrHealthInvalid
	}
	return &HealthCache{now: now, ttl: ttl, limit: limit, entries: map[string]healthCacheEntry{}}, nil
}

// Get returns one unexpired snapshot.
func (cache *HealthCache) Get(key string) (HealthSnapshot, bool) {
	if cache == nil || key == "" {
		return HealthSnapshot{}, false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	entry, exists := cache.entries[key]
	if !exists || cache.now().Sub(entry.at) >= cache.ttl {
		delete(cache.entries, key)
		return HealthSnapshot{}, false
	}
	return entry.snapshot, true
}

// Put stores one snapshot, evicting the oldest entry when full.
func (cache *HealthCache) Put(key string, snapshot HealthSnapshot) {
	if cache == nil || key == "" {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if _, exists := cache.entries[key]; !exists && len(cache.entries) >= cache.limit {
		cache.evictOldest()
	}
	cache.entries[key] = healthCacheEntry{snapshot: snapshot, at: cache.now()}
}

func (cache *HealthCache) evictOldest() {
	var oldestKey string
	var oldestAt time.Time
	first := true
	for key, entry := range cache.entries {
		if first || entry.at.Before(oldestAt) {
			oldestKey, oldestAt, first = key, entry.at, false
		}
	}
	delete(cache.entries, oldestKey)
}

// HealthEvidenceReader reads the bounded windowed evidence for one provider
// configuration boundary. It owns no provider payload.
type HealthEvidenceReader interface {
	Evidence(context.Context, tenant.Scope, HealthKey, time.Duration) (HealthEvidence, error)
}

// HealthSnapshotStore persists and loads one bounded snapshot for continuity
// and cross-process visibility.
type HealthSnapshotStore interface {
	Load(context.Context, tenant.Scope, HealthKey) (HealthSnapshot, bool, error)
	Apply(context.Context, tenant.Scope, HealthKey, Registration, HealthSnapshot) (bool, error)
}

// HealthService derives, caches, persists and announces bounded provider
// health. It never performs an invented probe: the optional checker is the
// existing provider runner health surface.
type HealthService struct {
	reader   HealthEvidenceReader
	store    HealthSnapshotStore
	breakers *BreakerRegistry
	probe    providerv1.HealthChecker
	policy   HealthPolicy
	region   string
	persist  bool
	now      func() time.Time
	cache    *HealthCache
	metrics  Metrics
}

// NewHealthService composes the bounded provider health supervisor. The probe,
// store and cache may be nil; evidence is required.
func NewHealthService(reader HealthEvidenceReader, store HealthSnapshotStore, breakers *BreakerRegistry, probe providerv1.HealthChecker, policy HealthPolicy, now func() time.Time) (*HealthService, error) {
	if reader == nil || now == nil || policy.Validate() != nil {
		return nil, ErrHealthInvalid
	}
	cache, err := NewHealthCache(policy.CacheTTL, 4096, now)
	if err != nil {
		return nil, err
	}
	return &HealthService{reader: reader, store: store, breakers: breakers, probe: probe, policy: policy, now: now, cache: cache}, nil
}

// WithMetrics attaches the bounded provider metric receiver.
func (service *HealthService) WithMetrics(metrics Metrics) *HealthService {
	if service != nil && metrics != nil {
		service.metrics = metrics
	}
	return service
}

// WithRegion pins the single deployment region this process serves, so a
// dispatch-keyed observation carries the bounded region for persistence and
// announcement. It never selects a registration.
func (service *HealthService) WithRegion(region string) *HealthService {
	if service != nil && region != "" {
		service.region = region
	}
	return service
}

// WithPersistence persists each freshly derived snapshot and announces state
// transitions. The API health read path leaves it off; the worker enables it so
// routing refreshes the bounded continuity snapshot instead of only reading it.
func (service *HealthService) WithPersistence(persist bool) *HealthService {
	if service != nil {
		service.persist = persist
	}
	return service
}

// RegistrationHealth derives the bounded snapshot for one persisted tenant
// registration. It never broadens to another region.
func (service *HealthService) RegistrationHealth(ctx context.Context, scope tenant.Scope, registration Registration) (HealthSnapshot, error) {
	if registration.AdapterID == "" || registration.Configuration.ProviderID == "" {
		return HealthSnapshot{}, ErrHealthInvalid
	}
	return service.Health(ctx, scope, HealthKeyForRegistration(registration))
}

// Health returns the cached or freshly derived bounded snapshot for one key.
// Persisted continuity is used when the owned evidence window is empty, and it
// seeds the local breaker so an open state survives a process restart.
func (service *HealthService) Health(ctx context.Context, scope tenant.Scope, key HealthKey) (HealthSnapshot, error) {
	if service == nil || scope.ID().IsZero() || !key.Valid() || key.TenantID != scope.ID().String() {
		return HealthSnapshot{}, ErrHealthInvalid
	}
	if key.Region == "" {
		key.Region = service.region
	}
	now := service.now().UTC()
	cacheKey := healthCacheKey(scope.ID().String(), key)
	if snapshot, ok := service.cache.Get(cacheKey); ok {
		service.record(snapshot)
		return snapshot, nil
	}
	var continuity *HealthSnapshot
	if service.store != nil {
		if persisted, exists, err := service.store.Load(ctx, scope, key); err != nil {
			return HealthSnapshot{}, err
		} else if exists {
			continuity = &persisted
			if service.breakers != nil && persisted.BreakerSince != nil {
				service.breakers.Seed(key.BreakerKey(), persisted.Breaker, *persisted.BreakerSince)
			}
		}
	}
	evidence, err := service.reader.Evidence(ctx, scope, key, service.policy.Window)
	if err != nil {
		if continuity == nil {
			return HealthSnapshot{}, err
		}
		evidence = HealthEvidence{AdapterID: key.AdapterID, ProviderID: key.ProviderID, Region: key.Region, Window: service.policy.Window}
	}
	if service.breakers != nil {
		if state, exists := service.breakers.State(key.BreakerKey()); exists {
			evidence.Breaker = state
		}
	}
	if evidence.Probe == nil && !evidence.ProbeError && service.probe != nil && evidence.Completed+evidence.Failed == 0 {
		probeContext, cancel := context.WithTimeout(ctx, service.policy.ProbeTimeout)
		health, probeErr := service.probe.Health(probeContext)
		cancel()
		if probeErr != nil {
			evidence.ProbeError = true
		} else {
			evidence.Probe = &HealthProbe{State: runnerHealthState(health.State), Code: health.Code, CheckedAt: health.CheckedAt}
		}
	}
	snapshot := DeriveHealth(evidence, continuity, now, service.policy)
	if service.persist && service.store != nil {
		if _, err := service.store.Apply(ctx, scope, key, Registration{}, snapshot); err != nil {
			return HealthSnapshot{}, err
		}
	}
	service.cache.Put(cacheKey, snapshot)
	service.record(snapshot)
	return snapshot, nil
}

// ObserveBreaker persists one bounded transition and announces it exactly once
// per state change. A persistence failure is reported to the caller while the
// local breaker keeps its already-applied state.
func (service *HealthService) ObserveBreaker(ctx context.Context, key BreakerKey, transition BreakerTransition) error {
	if service == nil || !transition.Changed {
		return nil
	}
	tenantID, err := id.ParseTenant(key.TenantID)
	if err != nil {
		return ErrHealthInvalid
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		return ErrHealthInvalid
	}
	healthKey := HealthKey{TenantID: key.TenantID, AdapterID: key.AdapterID, ProviderID: key.ProviderID, Region: service.region}
	evidence, err := service.reader.Evidence(ctx, scope, healthKey, service.policy.Window)
	if err != nil {
		return err
	}
	var continuity *HealthSnapshot
	if service.store != nil {
		if persisted, exists, loadErr := service.store.Load(ctx, scope, healthKey); loadErr != nil {
			return loadErr
		} else if exists {
			continuity = &persisted
		}
	}
	evidence.Breaker = transition.To
	if transition.To == BreakerOpen || transition.To == BreakerHalfOpen {
		at := transition.At.UTC()
		evidence.BreakerSince = &at
	}
	snapshot := DeriveHealth(evidence, continuity, service.now(), service.policy)
	if service.store != nil {
		if _, err = service.store.Apply(ctx, scope, healthKey, Registration{}, snapshot); err != nil {
			return err
		}
	}
	service.cache.Put(healthCacheKey(scope.ID().String(), healthKey), snapshot)
	service.record(snapshot)
	return nil
}

func healthCacheKey(tenantID string, key HealthKey) string {
	return tenantID + "|" + key.AdapterID + "|" + key.ProviderID + "|" + key.Region
}

func (service *HealthService) record(snapshot HealthSnapshot) {
	if service.metrics == nil {
		return
	}
	providerID := snapshot.AdapterID
	if providerID == "" {
		providerID = snapshot.ProviderID
	}
	service.metrics.RecordProviderHealth(observability.ProviderHealth{
		Provider: observability.Provider(providerID),
		State:    observabilityHealthState(snapshot.State),
		Region:   observability.Region(snapshot.Region),
	})
}

func observabilityHealthState(state HealthState) observability.HealthState {
	switch state {
	case HealthReady:
		return observability.HealthReady
	case HealthDegraded:
		return observability.HealthDegraded
	case HealthNotReady:
		return observability.HealthNotReady
	default:
		return observability.HealthUnknown
	}
}

func runnerHealthState(state providerv1.HealthState) HealthState {
	switch state {
	case providerv1.HealthReady:
		return HealthReady
	case providerv1.HealthDegraded:
		return HealthDegraded
	case providerv1.HealthNotReady:
		return HealthNotReady
	default:
		return HealthUnknown
	}
}
