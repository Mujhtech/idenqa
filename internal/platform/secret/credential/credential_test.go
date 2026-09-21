package credential

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/secret"
)

type fakeSource struct {
	values map[string]string
	fail   map[string]error
	calls  map[string]int
}

func newFakeSource() *fakeSource {
	return &fakeSource{values: map[string]string{}, fail: map[string]error{}, calls: map[string]int{}}
}

func (source *fakeSource) Resolve(_ context.Context, reference secret.Reference) (secret.Value, error) {
	source.calls[reference.String()]++
	if err := source.fail[reference.String()]; err != nil {
		return secret.Value{}, err
	}
	value, ok := source.values[reference.String()]
	if !ok {
		return secret.Value{}, secret.ErrNotFound
	}
	return secret.NewValue(reference, "provider-1", []byte(value))
}

func mustReference(t *testing.T, value string) secret.Reference {
	t.Helper()
	reference, err := secret.ParseReference(value)
	if err != nil {
		t.Fatalf("ParseReference(%q) error = %v", value, err)
	}
	return reference
}

func newTestResolver(t *testing.T, source secret.Resolver, now func() time.Time) *Resolver {
	t.Helper()
	resolver, err := NewResolver(source, time.Minute, now)
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}
	return resolver
}

func TestResolverSelectsExactVersionAndInvalidates(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0).UTC()
	source := newFakeSource()
	base := mustReference(t, "secret://aws/prod/provider/tenant")
	versioned := mustReference(t, "secret://aws/prod/provider/tenant?version=v2")
	source.values[base.String()] = "current"
	source.values[versioned.String()] = "second"
	resolver := newTestResolver(t, source, func() time.Time { return now })

	value, err := resolver.ResolveVersion(context.Background(), base, "v2")
	if err != nil {
		t.Fatalf("ResolveVersion() error = %v", err)
	}
	if text, _ := value.Text(); text != "second" {
		t.Fatalf("ResolveVersion() value = %q", text)
	}
	if source.calls[versioned.String()] != 1 {
		t.Fatalf("versioned provider calls = %d", source.calls[versioned.String()])
	}
	if _, err := resolver.ResolveVersion(context.Background(), base, "v2"); err != nil {
		t.Fatalf("ResolveVersion(cached) error = %v", err)
	}
	if source.calls[versioned.String()] != 1 {
		t.Fatalf("cached provider calls = %d", source.calls[versioned.String()])
	}
	resolver.Invalidate(base)
	source.values[versioned.String()] = "rotated"
	value, err = resolver.ResolveVersion(context.Background(), base, "v2")
	if err != nil {
		t.Fatalf("ResolveVersion(after invalidation) error = %v", err)
	}
	if text, _ := value.Text(); text != "rotated" {
		t.Fatalf("ResolveVersion(after invalidation) value = %q", text)
	}
}

func TestResolverFailsClosedOnConflictingVersionPin(t *testing.T) {
	t.Parallel()

	source := newFakeSource()
	pinned := mustReference(t, "secret://aws/prod/provider/tenant?version=v1")
	resolver := newTestResolver(t, source, time.Now)
	if _, err := resolver.ResolveVersion(context.Background(), pinned, "v2"); !errors.Is(err, ErrConflict) {
		t.Fatalf("ResolveVersion(conflict) error = %v, want ErrConflict", err)
	}
	if len(source.calls) != 0 {
		t.Fatalf("provider calls = %v, want none", source.calls)
	}
}

func TestResolverPropagatesProviderRefusals(t *testing.T) {
	t.Parallel()

	source := newFakeSource()
	base := mustReference(t, "secret://aws/prod/provider/tenant")
	versioned := mustReference(t, "secret://aws/prod/provider/tenant?version=v9")
	source.fail[versioned.String()] = secret.ErrNotFound
	resolver := newTestResolver(t, source, time.Now)
	if _, err := resolver.ResolveVersion(context.Background(), base, "v9"); !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("ResolveVersion(unknown) error = %v, want ErrNotFound", err)
	}
	source.fail[versioned.String()] = secret.ErrDenied
	if _, err := resolver.ResolveVersion(context.Background(), base, "v9"); !errors.Is(err, secret.ErrDenied) {
		t.Fatalf("ResolveVersion(denied) error = %v, want ErrDenied", err)
	}
}

func TestResolverHonoursBoundedTTL(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0).UTC()
	source := newFakeSource()
	base := mustReference(t, "secret://aws/prod/provider/tenant")
	source.values[base.String()] = "first"
	resolver := newTestResolver(t, source, func() time.Time { return now })
	if _, err := resolver.ResolveVersion(context.Background(), base, ""); err != nil {
		t.Fatalf("ResolveVersion() error = %v", err)
	}
	source.values[base.String()] = "second"
	if value, err := resolver.ResolveVersion(context.Background(), base, ""); err != nil {
		t.Fatalf("ResolveVersion(cached) error = %v", err)
	} else if text, _ := value.Text(); text != "first" {
		t.Fatalf("cached value = %q, want first", text)
	}
	now = now.Add(2 * time.Minute)
	if value, err := resolver.ResolveVersion(context.Background(), base, ""); err != nil {
		t.Fatalf("ResolveVersion(expired) error = %v", err)
	} else if text, _ := value.Text(); text != "second" {
		t.Fatalf("expired value = %q, want second", text)
	}
}

func TestWindowOverlapsOneGeneration(t *testing.T) {
	t.Parallel()

	source := newFakeSource()
	base := mustReference(t, "secret://aws/prod/provider/tenant")
	first := mustReference(t, "secret://aws/prod/provider/tenant?version=v1")
	second := mustReference(t, "secret://aws/prod/provider/tenant?version=v2")
	third := mustReference(t, "secret://aws/prod/provider/tenant?version=v3")
	source.values[first.String()] = "one"
	source.values[second.String()] = "two"
	source.values[third.String()] = "three"
	window, err := NewWindow(newTestResolver(t, source, time.Now), func(value secret.Value) error {
		text, err := value.Text()
		if err != nil || strings.TrimSpace(text) == "" {
			return ErrUnavailable
		}
		return nil
	})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	if _, err := window.Resolve(context.Background(), base, "v1"); err != nil {
		t.Fatalf("Resolve(v1) error = %v", err)
	}
	if value, err := window.Resolve(context.Background(), base, "v2"); err != nil {
		t.Fatalf("Resolve(v2) error = %v", err)
	} else if text, _ := value.Text(); text != "two" {
		t.Fatalf("Resolve(v2) value = %q", text)
	}
	// The previous generation still overlaps so an in-flight request pinned to
	// v1 is not dropped by the rotation.
	if value, err := window.Resolve(context.Background(), base, "v1"); err != nil {
		t.Fatalf("Resolve(v1 overlap) error = %v", err)
	} else if text, _ := value.Text(); text != "one" {
		t.Fatalf("Resolve(v1 overlap) value = %q", text)
	}
	if _, err := window.Resolve(context.Background(), base, "v3"); err != nil {
		t.Fatalf("Resolve(v3) error = %v", err)
	}
	delete(source.values, first.String())
	if _, err := window.Resolve(context.Background(), base, "v1"); !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("Resolve(v1 retired) error = %v, want ErrNotFound", err)
	}
	if current, ok := window.Current(); !ok {
		t.Fatal("Current() = false after v3")
	} else if text, _ := current.Text(); text != "three" {
		t.Fatalf("Current() value = %q", text)
	}
}

func TestWindowKeepsGenerationWhenRotationFails(t *testing.T) {
	t.Parallel()

	source := newFakeSource()
	base := mustReference(t, "secret://aws/prod/provider/tenant")
	first := mustReference(t, "secret://aws/prod/provider/tenant?version=v1")
	second := mustReference(t, "secret://aws/prod/provider/tenant?version=v2")
	source.values[first.String()] = "one"
	source.fail[second.String()] = secret.ErrDenied
	calls := 0
	window, err := NewWindow(newTestResolver(t, source, time.Now), func(secret.Value) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	if _, err := window.Resolve(context.Background(), base, "v1"); err != nil {
		t.Fatalf("Resolve(v1) error = %v", err)
	}
	if _, err := window.Resolve(context.Background(), base, "v2"); !errors.Is(err, secret.ErrDenied) {
		t.Fatalf("Resolve(v2 denied) error = %v, want ErrDenied", err)
	}
	if current, ok := window.Current(); !ok {
		t.Fatal("Current() = false after failed rotation")
	} else if text, _ := current.Text(); text != "one" {
		t.Fatalf("Current() value = %q, want one", text)
	}
	if calls != 1 {
		t.Fatalf("validate calls = %d, want 1", calls)
	}
}

func TestWindowRejectsInvalidRotationWithoutReplacingCurrent(t *testing.T) {
	t.Parallel()

	source := newFakeSource()
	base := mustReference(t, "secret://aws/prod/provider/tenant")
	first := mustReference(t, "secret://aws/prod/provider/tenant?version=v1")
	second := mustReference(t, "secret://aws/prod/provider/tenant?version=v2")
	source.values[first.String()] = "good"
	source.values[second.String()] = "bad"
	window, err := NewWindow(newTestResolver(t, source, time.Now), func(value secret.Value) error {
		text, _ := value.Text()
		if text != "good" {
			return ErrUnavailable
		}
		return nil
	})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	if _, err := window.Resolve(context.Background(), base, "v1"); err != nil {
		t.Fatalf("Resolve(v1) error = %v", err)
	}
	if _, err := window.Resolve(context.Background(), base, "v2"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Resolve(v2 invalid) error = %v, want ErrUnavailable", err)
	}
	if current, ok := window.Current(); !ok {
		t.Fatal("Current() = false after invalid rotation")
	} else if text, _ := current.Text(); text != "good" {
		t.Fatalf("Current() value = %q, want good", text)
	}
	// The failed generation was invalidated, so a corrected value resolves.
	source.values[second.String()] = "good"
	if value, err := window.Resolve(context.Background(), base, "v2"); err != nil {
		t.Fatalf("Resolve(v2 corrected) error = %v", err)
	} else if text, _ := value.Text(); text != "good" {
		t.Fatalf("Resolve(v2 corrected) value = %q", text)
	}
}

func TestWindowRefreshKeepsLastGoodOnFailure(t *testing.T) {
	t.Parallel()

	source := newFakeSource()
	base := mustReference(t, "secret://aws/prod/provider/tenant")
	source.values[base.String()] = "first"
	window, err := NewWindow(newTestResolver(t, source, time.Now), func(secret.Value) error { return nil })
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	if _, err := window.Resolve(context.Background(), base, ""); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	source.values[base.String()] = "second"
	if err := window.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if current, _ := window.Current(); true {
		if text, _ := current.Text(); text != "second" {
			t.Fatalf("Current() after refresh = %q", text)
		}
	}
	source.fail[base.String()] = secret.ErrUnavailable
	if err := window.Refresh(context.Background()); !errors.Is(err, secret.ErrUnavailable) {
		t.Fatalf("Refresh(failing) error = %v, want ErrUnavailable", err)
	}
	if current, _ := window.Current(); true {
		if text, _ := current.Text(); text != "second" {
			t.Fatalf("Current() after failed refresh = %q, want second", text)
		}
	}
}

func TestNewResolverAndWindowValidation(t *testing.T) {
	t.Parallel()

	if _, err := NewResolver(nil, time.Minute, time.Now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("NewResolver(nil) error = %v", err)
	}
	if _, err := NewResolver(newFakeSource(), 0, time.Now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("NewResolver(zero TTL) error = %v", err)
	}
	if _, err := NewWindow(nil, func(secret.Value) error { return nil }); !errors.Is(err, ErrInvalid) {
		t.Fatalf("NewWindow(nil resolver) error = %v", err)
	}
	if _, err := NewWindow(newTestResolver(t, newFakeSource(), time.Now), nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("NewWindow(nil validate) error = %v", err)
	}
}
