//go:build integration

package integration_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/provider"
	providerpostgres "github.com/Mujhtech/idenqa/internal/provider/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type mutableIntegrationClock struct{ now time.Time }

func (clock *mutableIntegrationClock) Now() time.Time { return clock.now }

// TestProviderLimiterHoldsConcurrencyRateAndReleases proves the PostgreSQL
// advisory-lock limiter admits at most the configured concurrency for one
// tenant provider boundary under real parallel acquires, throttles excess
// attempts before any external effect, releases on completion and failure, and
// enforces the fixed rate window with tenant row-level security.
func TestProviderLimiterHoldsConcurrencyRateAndReleases(t *testing.T) {
	database := createIsolatedDatabase(t)
	ctx := t.Context()
	migrator, err := pg.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatal(err)
	}
	admin, err := pg.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	ids, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	tenantID, _ := seedExecutionVerification(t, admin, ids, now)
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatal(err)
	}
	cfg := poolConfig(database.url)
	cfg.Role = database.createRuntimeRole(t)
	runtime, err := pg.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	key, ok := provider.LimitKey(providerv1.Request{TenantID: tenantID.String(), ProviderID: "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWH", Adapter: providerv1.PackageProvenance{AdapterID: "dojah"}})
	if !ok {
		t.Fatal("LimitKey() = false")
	}

	store, err := providerpostgres.NewLimitStore(runtime, fixedIntegrationClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	limit := provider.Limit{MaximumConcurrent: 2, RateLimit: 1_000, RatePeriod: time.Minute, RateBurst: 1_000, LeaseTTL: time.Minute}

	// Eight parallel acquires contend for two slots. Every admitted lease is
	// held until all attempts finish so the bound is the only success path.
	start := make(chan struct{})
	var wait sync.WaitGroup
	var admitted atomic.Int64
	var leasesMu sync.Mutex
	var leases []provider.Lease
	var unexpected atomic.Int64
	for index := 0; index < 8; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			lease, err := store.Acquire(ctx, scope, key, limit)
			if err == nil {
				admitted.Add(1)
				leasesMu.Lock()
				leases = append(leases, lease)
				leasesMu.Unlock()
				return
			}
			if !errors.Is(err, provider.ErrThrottled) {
				unexpected.Add(1)
			}
		}()
	}
	close(start)
	wait.Wait()
	if admitted.Load() != 2 || unexpected.Load() != 0 {
		t.Fatalf("admitted = %d unexpected = %d, want 2 and 0", admitted.Load(), unexpected.Load())
	}
	leasesMu.Lock()
	held := append([]provider.Lease(nil), leases...)
	leasesMu.Unlock()
	for index, lease := range held {
		if err := lease.Release(ctx); err != nil {
			t.Fatalf("Release(%d) error = %v", index, err)
		}
	}
	if _, err := store.Acquire(ctx, scope, key, limit); err != nil {
		t.Fatalf("Acquire(after release) error = %v", err)
	}
	rateKey, ok := provider.LimitKey(providerv1.Request{TenantID: tenantID.String(), ProviderID: "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWK", Adapter: providerv1.PackageProvenance{AdapterID: "dojah"}})
	if !ok {
		t.Fatal("LimitKey(rate) = false")
	}

	// A fixed rate window admits RateLimit+RateBurst dispatches even when every
	// lease is released; advancing past the window admits again.
	rateLimit := provider.Limit{MaximumConcurrent: 4, RateLimit: 2, RatePeriod: time.Minute, RateBurst: 0, LeaseTTL: time.Minute}
	clock := &mutableIntegrationClock{now: now}
	rateStore, err := providerpostgres.NewLimitStore(runtime, clock)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		lease, err := rateStore.Acquire(ctx, scope, rateKey, rateLimit)
		if err != nil {
			t.Fatalf("Acquire(rate %d) error = %v", attempt, err)
		}
		if err := lease.Release(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := rateStore.Acquire(ctx, scope, rateKey, rateLimit); !errors.Is(err, provider.ErrThrottled) {
		t.Fatalf("Acquire(rate exceeded) error = %v, want ErrThrottled", err)
	}
	clock.now = clock.now.Add(2 * time.Minute)
	lease, err := rateStore.Acquire(ctx, scope, rateKey, rateLimit)
	if err != nil {
		t.Fatalf("Acquire(next window) error = %v", err)
	}
	if err := lease.Release(ctx); err != nil {
		t.Fatal(err)
	}

	// A bound lease expires so a crashed process cannot hold admission forever.
	expiring := provider.Limit{MaximumConcurrent: 1, RateLimit: 1_000, RatePeriod: time.Minute, RateBurst: 1_000, LeaseTTL: time.Second}
	if _, err := rateStore.Acquire(ctx, scope, rateKey, expiring); err != nil {
		t.Fatal(err)
	}
	if _, err := rateStore.Acquire(ctx, scope, rateKey, expiring); !errors.Is(err, provider.ErrThrottled) {
		t.Fatalf("Acquire(second slot) error = %v, want ErrThrottled", err)
	}
	clock.now = clock.now.Add(2 * time.Second)
	if _, err := rateStore.Acquire(ctx, scope, rateKey, expiring); err != nil {
		t.Fatalf("Acquire(expired lease) error = %v", err)
	}

	// Forced row-level security hides leases from an unscoped query.
	if err := runtime.WithinTransaction(ctx, pg.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx pg.Transaction) error {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM idenqa.provider_dispatch_leases`).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return errors.New("unscoped provider dispatch leases visible")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
