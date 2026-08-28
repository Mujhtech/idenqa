package evidence_test

import (
	"errors"
	"testing"

	"github.com/Mujhtech/idenqa/internal/evidence"
)

func TestCatalogResolvesOnlyExactReference(t *testing.T) {
	t.Parallel()

	registry, err := evidence.BuiltInRegistry()
	if err != nil {
		t.Fatalf("BuiltInRegistry() error = %v", err)
	}
	catalog, err := evidence.NewCatalog(registry)
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	if _, err := catalog.Resolve(registry.Reference()); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	changed := registry.Reference()
	changed.Digest = "sha256:changed"
	if _, err := catalog.Resolve(changed); !errors.Is(err, evidence.ErrRegistryNotFound) {
		t.Fatalf("Resolve(changed) error = %v, want ErrRegistryNotFound", err)
	}
}

func TestNewCatalogRejectsEmptyAndDuplicate(t *testing.T) {
	t.Parallel()

	if _, err := evidence.NewCatalog(); err == nil {
		t.Fatal("NewCatalog() error = nil")
	}
	registry, err := evidence.BuiltInRegistry()
	if err != nil {
		t.Fatalf("BuiltInRegistry() error = %v", err)
	}
	if _, err := evidence.NewCatalog(registry, registry); err == nil {
		t.Fatal("NewCatalog(duplicate) error = nil")
	}
}
