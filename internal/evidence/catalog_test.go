package evidence_test

import (
	"bytes"
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

func TestCatalogSnapshotsAreNewestFirstAndDefensive(t *testing.T) {
	t.Parallel()

	catalog, err := evidence.BuiltInCatalog()
	if err != nil {
		t.Fatalf("BuiltInCatalog() error = %v", err)
	}
	snapshots, err := catalog.Snapshots()
	if err != nil {
		t.Fatalf("Snapshots() error = %v", err)
	}
	if len(snapshots) < 2 || snapshots[0].Reference.Revision <= snapshots[1].Reference.Revision {
		t.Fatalf("snapshot revisions are not newest first: %+v", snapshots)
	}
	original := append([]byte(nil), snapshots[0].Document...)
	snapshots[0].Document[0] = 'x'
	again, err := catalog.Snapshots()
	if err != nil {
		t.Fatalf("Snapshots() second call error = %v", err)
	}
	if !bytes.Equal(again[0].Document, original) {
		t.Fatal("mutating a snapshot changed the catalogue document")
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
