// Package runnersecret composes secret resolution and bounded dynamic reload
// for isolated runner processes. Operators may keep mounted files (the
// unchanged default) or bind each reloable setting to a secret:// reference.
// Initial resolution fails closed; later refresh failures keep the last good
// values so credential rotation overlaps instead of dropping requests.
package runnersecret

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/secret"
	"github.com/Mujhtech/idenqa/internal/platform/secret/credential"
	"github.com/Mujhtech/idenqa/internal/platform/secret/resolver"
)

const (
	// DefaultCacheTTL bounds staleness of provider reads between reloads.
	DefaultCacheTTL = 30 * time.Second
	// DefaultReloadInterval is the bounded periodic reconciliation interval.
	DefaultReloadInterval = 5 * time.Minute
)

// Options is the operator-selected resolver configuration.
type Options struct {
	Provider       string
	AWSRegion      string
	CacheTTL       time.Duration
	ReloadInterval time.Duration
	Now            func() time.Time
}

// Enabled reports whether a resolver is needed for the configured references.
func Enabled(references ...string) bool {
	for _, reference := range references {
		if reference != "" {
			return true
		}
	}

	return false
}

// Open constructs the selected resolver with its bounded version-aware cache.
// The returned resolver also exposes explicit invalidation for a failed reload.
func Open(ctx context.Context, options Options) (*credential.Resolver, error) {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	ttl := options.CacheTTL
	if ttl == 0 {
		ttl = DefaultCacheTTL
	}
	source, err := resolver.Open(ctx, resolver.Options{
		Provider: options.Provider, AWSRegion: options.AWSRegion, Now: now,
	})
	if err != nil {
		return nil, err
	}
	versioned, err := credential.NewResolver(source, ttl, now)
	if err != nil {
		return nil, errors.New("runner secret resolver cache is invalid")
	}
	return versioned, nil
}

// Role binds one process setting to one secret reference. Apply must publish
// the value atomically; reload applies are serialised by Reloader.
type Role struct {
	Name      string
	Reference secret.Reference
	Apply     func(secret.Value) error
}

// RoleApply wraps one role apply so a rejected payload invalidates the cached
// credential instead of being served again until the cache TTL expires.
func RoleApply(resolver *credential.Resolver, reference secret.Reference, apply func(secret.Value) error) func(secret.Value) error {
	return func(value secret.Value) error {
		if err := apply(value); err != nil {
			resolver.Invalidate(reference)
			return err
		}
		return nil
	}
}

// Reloader resolves and reapplies every configured role.
type Reloader struct{ reloader *secret.Reloader }

// NewReloader validates the roles and constructs the periodic reloader.
func NewReloader(resolver secret.Resolver, roles []Role, interval time.Duration, report func(error)) (*Reloader, error) {
	if resolver == nil || len(roles) == 0 || len(roles) > 8 {
		return nil, errors.New("runner secret reloader requires a resolver and one to eight roles")
	}
	if interval == 0 {
		interval = DefaultReloadInterval
	}
	references := make([]secret.Reference, 0, len(roles))
	byReference := make(map[secret.Reference]Role, len(roles))
	for _, role := range roles {
		if role.Name == "" || role.Reference.IsZero() || role.Apply == nil {
			return nil, errors.New("runner secret role is incomplete")
		}
		if _, duplicate := byReference[role.Reference]; duplicate {
			return nil, fmt.Errorf("runner secret reference %s is assigned to more than one role", role.Reference)
		}
		byReference[role.Reference] = role
		references = append(references, role.Reference)
	}
	reloader, err := secret.NewReloader(resolver, references, interval, func(_ context.Context, _ secret.Snapshot, next secret.Snapshot) error {
		for reference, role := range byReference {
			value, ok := next.Value(reference)
			if !ok {
				return secret.ErrNotFound
			}
			if err := role.Apply(value); err != nil {
				return fmt.Errorf("apply runner secret %s: %w", role.Name, err)
			}
		}

		return nil
	}, report)
	if err != nil {
		return nil, err
	}

	return &Reloader{reloader: reloader}, nil
}

// Prime resolves and applies the initial values. It must succeed before the
// runner starts serving so an unknown reference never degrades silently.
func (reloader *Reloader) Prime(ctx context.Context) error {
	if reloader == nil || reloader.reloader == nil {
		return errors.New("runner secret reloader is not initialised")
	}
	return reloader.reloader.Prime(ctx)
}

// Refresh resolves and reapplies one complete refresh. It is the deterministic
// seam for tests and administrative reloads.
func (reloader *Reloader) Refresh(ctx context.Context) error {
	if reloader == nil || reloader.reloader == nil {
		return errors.New("runner secret reloader is not initialised")
	}
	_, err := reloader.reloader.Reload(ctx, secret.Snapshot{})

	return err
}

// Run primes the values and then refreshes them until ctx is cancelled.
func (reloader *Reloader) Run(ctx context.Context) error {
	if reloader == nil || reloader.reloader == nil {
		return errors.New("runner secret reloader is not initialised")
	}
	return reloader.reloader.Run(ctx)
}
