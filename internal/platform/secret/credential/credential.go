// Package credential resolves versioned secret references with a bounded cache,
// explicit invalidation and one-generation overlap for rotation.
//
// A provider configuration reference carries a secret:// reference plus an
// opaque credential version. Resolution is exact: the version selects the
// provider revision and a reference that already pins a different version fails
// closed instead of silently resolving another release. The window keeps the
// previous successfully validated generation so a rotation cannot drop
// in-flight requests whose persisted request still pins the old version.
package credential

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/secret"
)

var (
	// ErrInvalid identifies a malformed resolution request.
	ErrInvalid = errors.New("credential: invalid request")
	// ErrConflict identifies a reference whose own version pin contradicts the
	// requested credential version.
	ErrConflict = errors.New("credential: conflicting version pin")
	// ErrUnavailable identifies a missing or unvalidated credential generation.
	ErrUnavailable = errors.New("credential: unavailable")
)

const (
	maxVersionSelector = 128
	maxPinnedVersions  = 8
)

// Resolver resolves versioned references through the selected secret provider
// with a bounded TTL cache. It implements secret.Resolver for callers that only
// carry a reference, and adds explicit version selection and invalidation.
type Resolver struct {
	cache  *secret.Cache
	mutex  sync.Mutex
	pinned map[string][]secret.Reference
}

// NewResolver constructs a version-aware resolver over source. The TTL is the
// same bounded cache TTL as the underlying secret cache.
func NewResolver(source secret.Resolver, ttl time.Duration, now func() time.Time) (*Resolver, error) {
	if source == nil || now == nil {
		return nil, ErrInvalid
	}
	cache, err := secret.NewCache(source, ttl, now)
	if err != nil {
		return nil, ErrInvalid
	}
	return &Resolver{cache: cache, pinned: map[string][]secret.Reference{}}, nil
}

// Resolve implements secret.Resolver using the reference's own version
// selector. An unversioned reference resolves the provider's current revision.
func (resolver *Resolver) Resolve(ctx context.Context, reference secret.Reference) (secret.Value, error) {
	if reference.IsZero() {
		return secret.Value{}, ErrInvalid
	}
	return resolver.ResolveVersion(ctx, reference, reference.Version())
}

// ResolveVersion resolves one exact credential version. A non-empty version
// selects the provider revision; an empty version keeps the reference's own
// selector or the provider's current revision.
func (resolver *Resolver) ResolveVersion(ctx context.Context, reference secret.Reference, version string) (secret.Value, error) {
	if resolver == nil || resolver.cache == nil || ctx == nil || reference.IsZero() ||
		strings.TrimSpace(version) != version || len(version) > maxVersionSelector {
		return secret.Value{}, ErrInvalid
	}
	effective, err := versionedReference(reference, version)
	if err != nil {
		return secret.Value{}, err
	}
	value, err := resolver.cache.Resolve(ctx, effective)
	if err != nil {
		return secret.Value{}, err
	}
	if effective != reference {
		resolver.mutex.Lock()
		pinned := resolver.pinned[reference.String()]
		known := false
		for _, candidate := range pinned {
			if candidate == effective {
				known = true
				break
			}
		}
		if !known {
			if len(pinned) >= maxPinnedVersions {
				pinned = pinned[len(pinned)-maxPinnedVersions+1:]
			}
			resolver.pinned[reference.String()] = append(pinned, effective)
		}
		resolver.mutex.Unlock()
	}
	return value, nil
}

// Invalidate removes the exact, base and every versioned reference observed for
// the base so the next resolution consults the provider again.
func (resolver *Resolver) Invalidate(reference secret.Reference) {
	if resolver == nil || resolver.cache == nil || reference.IsZero() {
		return
	}
	resolver.cache.Invalidate(reference)
	resolver.mutex.Lock()
	pinned := resolver.pinned[reference.String()]
	delete(resolver.pinned, reference.String())
	resolver.mutex.Unlock()
	for _, candidate := range pinned {
		resolver.cache.Invalidate(candidate)
	}
}

func versionedReference(reference secret.Reference, version string) (secret.Reference, error) {
	if version == "" || reference.Version() == version {
		return reference, nil
	}
	if reference.Version() != "" {
		return secret.Reference{}, ErrConflict
	}
	versioned, err := secret.ParseReference(reference.String() + "?version=" + url.QueryEscape(version))
	if err != nil {
		return secret.Reference{}, ErrInvalid
	}
	return versioned, nil
}

type generation struct {
	reference secret.Reference
	version   string
	value     secret.Value
}

// Window holds the active and immediately previous validated credential
// generation for one reference. Resolving a persisted request that still pins
// the previous version keeps working while the new generation is adopted, and a
// new reference that cannot resolve or validate leaves both generations
// untouched.
type Window struct {
	resolver *Resolver
	validate func(secret.Value) error
	mutex    sync.Mutex
	current  *generation
	previous *generation
}

// NewWindow constructs a rotation window. validate must reject any unusable
// credential payload and must not retain the value.
func NewWindow(resolver *Resolver, validate func(secret.Value) error) (*Window, error) {
	if resolver == nil || validate == nil {
		return nil, ErrInvalid
	}
	return &Window{resolver: resolver, validate: validate}, nil
}

// Resolve returns the active, overlapping or newly resolved generation for the
// exact reference and version. A provider refusal fails closed without
// disturbing the active generation.
func (window *Window) Resolve(ctx context.Context, reference secret.Reference, version string) (secret.Value, error) {
	if window == nil || window.resolver == nil || ctx == nil || reference.IsZero() {
		return secret.Value{}, ErrInvalid
	}
	window.mutex.Lock()
	if value, ok := window.generationLocked(window.current, reference, version); ok {
		window.mutex.Unlock()
		return value, nil
	}
	if value, ok := window.generationLocked(window.previous, reference, version); ok {
		window.mutex.Unlock()
		return value, nil
	}
	window.mutex.Unlock()

	value, err := window.resolver.ResolveVersion(ctx, reference, version)
	if err != nil {
		return secret.Value{}, err
	}
	if err := window.validate(value); err != nil {
		window.resolver.Invalidate(reference)
		return secret.Value{}, err
	}
	window.mutex.Lock()
	retired := window.previous
	window.previous = window.current
	window.current = &generation{reference: reference, version: version, value: value}
	window.mutex.Unlock()
	// The displaced generation leaves the overlap window, so its cached payload
	// is invalidated rather than retained until the cache TTL expires.
	if retired != nil && retired != window.previous {
		window.resolver.Invalidate(retired.reference)
	}
	return value, nil
}

func (window *Window) generationLocked(candidate *generation, reference secret.Reference, version string) (secret.Value, bool) {
	if candidate == nil || candidate.reference.String() != reference.String() || candidate.version != version {
		return secret.Value{}, false
	}
	return candidate.value, true
}

// Current returns the active validated generation.
func (window *Window) Current() (secret.Value, bool) {
	if window == nil {
		return secret.Value{}, false
	}
	window.mutex.Lock()
	defer window.mutex.Unlock()
	if window.current == nil {
		return secret.Value{}, false
	}
	return window.current.value, true
}

// Invalidate drops every cached payload this window observed so the next
// resolution consults the provider again. The active generation stays in force
// until a replacement resolves and validates.
func (window *Window) Invalidate() {
	if window == nil || window.resolver == nil {
		return
	}
	window.mutex.Lock()
	current, previous := window.current, window.previous
	window.mutex.Unlock()
	if current != nil {
		window.resolver.Invalidate(current.reference)
	}
	if previous != nil {
		window.resolver.Invalidate(previous.reference)
	}
}

// Refresh re-resolves and revalidates the active generation. A failure keeps the
// last good generation in force; the returned error reports the failed refresh.
func (window *Window) Refresh(ctx context.Context) error {
	if window == nil || window.resolver == nil || ctx == nil {
		return ErrInvalid
	}
	window.mutex.Lock()
	current := window.current
	window.mutex.Unlock()
	if current == nil {
		return ErrUnavailable
	}
	window.resolver.Invalidate(current.reference)
	value, err := window.resolver.ResolveVersion(ctx, current.reference, current.version)
	if err != nil {
		return err
	}
	if err := window.validate(value); err != nil {
		window.resolver.Invalidate(current.reference)
		return err
	}
	window.mutex.Lock()
	if window.current != nil && window.current.reference.String() == current.reference.String() &&
		window.current.version == current.version {
		window.current.value = value
	}
	window.mutex.Unlock()
	return nil
}
