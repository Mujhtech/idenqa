package secret

import (
	"context"
	"errors"
	"sync"
	"time"
)

const (
	minCacheTTL = 100 * time.Millisecond
	maxCacheTTL = time.Hour
)

type cacheEntry struct {
	value     Value
	expiresAt time.Time
}

// Cache is a short-TTL resolution cache with explicit invalidation. A failed
// refresh never serves a stale payload: expiry removes the entry before the
// provider is consulted again.
type Cache struct {
	mutex    sync.Mutex
	resolver Resolver
	ttl      time.Duration
	now      func() time.Time
	entries  map[string]cacheEntry
}

// NewCache constructs a bounded TTL cache around resolver.
func NewCache(resolver Resolver, ttl time.Duration, now func() time.Time) (*Cache, error) {
	if resolver == nil || now == nil || ttl < minCacheTTL || ttl > maxCacheTTL {
		return nil, ErrInvalid
	}
	return &Cache{resolver: resolver, ttl: ttl, now: now, entries: map[string]cacheEntry{}}, nil
}

// Resolve returns the cached payload for reference or resolves and caches it.
func (cache *Cache) Resolve(ctx context.Context, reference Reference) (Value, error) {
	if cache == nil || cache.resolver == nil || ctx == nil || reference.IsZero() {
		return Value{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	now := cache.now().UTC()
	cache.mutex.Lock()
	entry, exists := cache.entries[reference.String()]
	if exists && now.Before(entry.expiresAt) {
		cache.mutex.Unlock()
		return entry.value, nil
	}
	if exists {
		delete(cache.entries, reference.String())
	}
	cache.mutex.Unlock()

	value, err := cache.resolver.Resolve(ctx, reference)
	if err != nil {
		return Value{}, err
	}
	if value.Reference() != reference {
		return Value{}, ErrInvalid
	}
	cache.mutex.Lock()
	cache.entries[reference.String()] = cacheEntry{value: value, expiresAt: now.Add(cache.ttl)}
	cache.mutex.Unlock()

	return value, nil
}

// Invalidate removes one reference so its next resolution consults the provider.
func (cache *Cache) Invalidate(reference Reference) {
	if cache == nil || reference.IsZero() {
		return
	}
	cache.mutex.Lock()
	delete(cache.entries, reference.String())
	cache.mutex.Unlock()
}

// InvalidateAll removes every cached payload.
func (cache *Cache) InvalidateAll() {
	if cache == nil {
		return
	}
	cache.mutex.Lock()
	cache.entries = map[string]cacheEntry{}
	cache.mutex.Unlock()
}

// Snapshot is an immutable set of resolved payloads keyed by exact reference.
type Snapshot struct {
	values map[Reference]Value
}

// NewSnapshot validates and snapshots resolved values.
func NewSnapshot(values map[Reference]Value) (Snapshot, error) {
	if len(values) == 0 {
		return Snapshot{}, ErrInvalid
	}
	copied := make(map[Reference]Value, len(values))
	for reference, value := range values {
		if reference.IsZero() || value.Reference() != reference {
			return Snapshot{}, ErrInvalid
		}
		copied[reference] = value
	}
	return Snapshot{values: copied}, nil
}

// Value returns the exact payload for reference.
func (snapshot Snapshot) Value(reference Reference) (Value, bool) {
	value, ok := snapshot.values[reference]
	return value, ok
}

// Reloader re-resolves a bounded reference set and publishes atomic snapshots.
// The previous snapshot remains in force until a complete refresh succeeds, so
// rotated credentials overlap an active acceptance window instead of failing
// closed mid-flight. Initial priming is strict: an unknown reference stops
// startup rather than silently degrading to file fallback.
type Reloader struct {
	resolver   Resolver
	references []Reference
	interval   time.Duration
	apply      func(context.Context, Snapshot, Snapshot) error
	report     func(error)
}

// NewReloader validates a bounded reload plan. apply receives the previous and
// next snapshots and must be safe for repeated invocation.
func NewReloader(
	resolver Resolver,
	references []Reference,
	interval time.Duration,
	apply func(context.Context, Snapshot, Snapshot) error,
	report func(error),
) (*Reloader, error) {
	if resolver == nil || len(references) == 0 || len(references) > 16 ||
		interval < time.Second || interval > time.Hour || apply == nil {
		return nil, ErrInvalid
	}
	unique := make([]Reference, 0, len(references))
	seen := map[string]struct{}{}
	for _, reference := range references {
		if reference.IsZero() {
			return nil, ErrInvalid
		}
		if _, duplicate := seen[reference.String()]; duplicate {
			continue
		}
		seen[reference.String()] = struct{}{}
		unique = append(unique, reference)
	}
	return &Reloader{
		resolver: resolver, references: unique, interval: interval, apply: apply, report: report,
	}, nil
}

// Prime resolves and applies the initial snapshot. The zero snapshot is passed
// as the previous state.
func (reloader *Reloader) Prime(ctx context.Context) error {
	next, err := reloader.resolve(ctx)
	if err != nil {
		return err
	}
	return reloader.apply(ctx, Snapshot{}, next)
}

// Run refreshes the snapshot until ctx is cancelled. Callers must Prime first
// so a fresh process fails closed before serving. A failed refresh is reported
// without replacing the last good snapshot.
func (reloader *Reloader) Run(ctx context.Context) error {
	previous := Snapshot{}
	ticker := time.NewTicker(reloader.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			next, err := reloader.refresh(ctx, previous)
			if err != nil {
				if reloader.report != nil {
					reloader.report(err)
				}
				continue
			}
			previous = next
		}
	}
}

// Reload resolves one complete refresh and applies it. It is the deterministic
// test seam for Run's periodic behaviour.
func (reloader *Reloader) Reload(ctx context.Context, previous Snapshot) (Snapshot, error) {
	if ctx == nil {
		return Snapshot{}, ErrInvalid
	}
	return reloader.refresh(ctx, previous)
}

func (reloader *Reloader) refresh(ctx context.Context, previous Snapshot) (Snapshot, error) {
	next, err := reloader.resolve(ctx)
	if err != nil {
		return previous, err
	}
	if err := reloader.apply(ctx, previous, next); err != nil {
		return previous, err
	}
	return next, nil
}

func (reloader *Reloader) resolve(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	values := make(map[Reference]Value, len(reloader.references))
	for _, reference := range reloader.references {
		value, err := reloader.resolver.Resolve(ctx, reference)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return Snapshot{}, ErrNotFound
			}
			return Snapshot{}, err
		}
		values[reference] = value
	}
	return NewSnapshot(values)
}
