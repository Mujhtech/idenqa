package runnersecret

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/secret"
	"github.com/Mujhtech/idenqa/internal/platform/secret/resolver"
)

type fakeResolver struct {
	values map[string]string
	fail   map[string]error
	calls  map[string]int
}

func newFakeResolver() *fakeResolver {
	return &fakeResolver{values: map[string]string{}, fail: map[string]error{}, calls: map[string]int{}}
}

func (resolver *fakeResolver) Resolve(_ context.Context, reference secret.Reference) (secret.Value, error) {
	resolver.calls[reference.String()]++
	if err := resolver.fail[reference.String()]; err != nil {
		return secret.Value{}, err
	}
	value, ok := resolver.values[reference.String()]
	if !ok {
		return secret.Value{}, secret.ErrNotFound
	}
	return secret.NewValue(reference, "v1", []byte(value))
}

func mustRole(t *testing.T, name, reference string, apply func(secret.Value) error) Role {
	t.Helper()
	parsed, err := secret.ParseReference(reference)
	if err != nil {
		t.Fatalf("ParseReference(%q) error = %v", reference, err)
	}
	return Role{Name: name, Reference: parsed, Apply: apply}
}

func TestReloaderAppliesWithOverlapAndKeepsLastGoodValues(t *testing.T) {
	t.Parallel()

	source := newFakeResolver()
	credentialReference := "secret://aws/prod/runner/credential" //nolint:gosec // reference text, not secret material.
	gatewayReference := "secret://aws/prod/runner/gateway"
	source.values[credentialReference] = "idq_wrk_v1_first"
	source.values[gatewayReference] = "idq_gw_v1_first"

	var accepted []string
	var gateway string
	reloader, err := NewReloader(source, []Role{
		mustRole(t, "credential", credentialReference, func(value secret.Value) error {
			text, err := value.Text()
			if err != nil {
				return err
			}
			// Overlap: the previous credential remains accepted while the new
			// one is added.
			accepted = append(accepted, text)
			return nil
		}),
		mustRole(t, "gateway", gatewayReference, func(value secret.Value) error {
			text, err := value.Text()
			if err != nil {
				return err
			}
			gateway = text
			return nil
		}),
	}, time.Minute, nil)
	if err != nil {
		t.Fatalf("NewReloader() error = %v", err)
	}

	if err := reloader.Prime(context.Background()); err != nil {
		t.Fatalf("Prime() error = %v", err)
	}
	if len(accepted) != 1 || accepted[0] != "idq_wrk_v1_first" || gateway != "idq_gw_v1_first" {
		t.Fatalf("Prime() accepted=%v gateway=%q", accepted, gateway)
	}

	source.values[credentialReference] = "idq_wrk_v1_second"
	source.values[gatewayReference] = "idq_gw_v1_second"
	if err := reloader.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if len(accepted) != 2 || accepted[1] != "idq_wrk_v1_second" || gateway != "idq_gw_v1_second" {
		t.Fatalf("Refresh() accepted=%v gateway=%q", accepted, gateway)
	}

	// A failed refresh must not unpublish the last good values.
	source.fail[credentialReference] = secret.ErrNotFound
	if err := reloader.Refresh(context.Background()); !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("Refresh(failing) error = %v, want ErrNotFound", err)
	}
	if len(accepted) != 2 || gateway != "idq_gw_v1_second" {
		t.Fatalf("failed Refresh() accepted=%v gateway=%q", accepted, gateway)
	}
}

func TestReloaderFailsClosedOnUnknownReference(t *testing.T) {
	t.Parallel()

	source := newFakeResolver()
	source.values["secret://aws/prod/known"] = "value"
	reloader, err := NewReloader(source, []Role{
		mustRole(t, "known", "secret://aws/prod/known", func(secret.Value) error { return nil }),
		mustRole(t, "unknown", "secret://aws/prod/unknown", func(secret.Value) error { return nil }),
	}, time.Minute, nil)
	if err != nil {
		t.Fatalf("NewReloader() error = %v", err)
	}
	if err := reloader.Prime(context.Background()); !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("Prime() error = %v, want ErrNotFound", err)
	}
}

func TestOpenAndEnabled(t *testing.T) {
	t.Parallel()

	if Enabled("", "") {
		t.Fatal("Enabled() = true for only file-bound settings")
	}
	if !Enabled("", "secret://file/run/secrets/credential") {
		t.Fatal("Enabled() = false with one configured reference")
	}
	if _, err := Open(context.Background(), Options{Provider: "unknown", Now: time.Now}); err == nil {
		t.Fatal("Open(unknown provider) must fail closed")
	}
	opened, err := Open(context.Background(), Options{Provider: "file", Now: time.Now})
	if err != nil || opened == nil {
		t.Fatalf("Open(file) = %v, %v", opened, err)
	}
	if _, err := Open(context.Background(), Options{Provider: "aws", Now: time.Now}); err == nil {
		t.Fatal("Open(aws without region) must fail closed")
	}
	if _, err := resolver.Open(context.Background(), resolver.Options{Provider: "aws", Now: time.Now}); err == nil {
		t.Fatal("resolver.Open(aws without region) must fail closed")
	}
}
