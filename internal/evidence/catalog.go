package evidence

import (
	"errors"
	"fmt"
	"slices"
)

// ErrRegistryNotFound is returned when an exact immutable reference is not deployed.
var ErrRegistryNotFound = errors.New("evidence: registry not found")

// Catalog resolves only exact registry schema, revision, and digest references.
type Catalog struct {
	registries map[Reference]Registry
}

// Snapshot is one immutable registry reference and its canonical document.
// Document bytes are copied when the snapshot is created so callers cannot
// mutate catalogue state.
type Snapshot struct {
	Reference Reference
	Document  []byte
}

// NewCatalog validates and snapshots deployed immutable registries.
func NewCatalog(registries ...Registry) (Catalog, error) {
	if len(registries) == 0 {
		return Catalog{}, errors.New("evidence catalog requires at least one registry")
	}

	indexed := make(map[Reference]Registry, len(registries))
	for _, registry := range registries {
		reference := registry.Reference()
		if reference.SchemaVersion == 0 || reference.Revision == 0 || reference.Digest == "" {
			return Catalog{}, errors.New("evidence catalog contains an invalid registry")
		}
		if _, exists := indexed[reference]; exists {
			return Catalog{}, fmt.Errorf("evidence catalog repeats registry revision %d", reference.Revision)
		}
		indexed[reference] = registry
	}

	return Catalog{registries: indexed}, nil
}

// Resolve returns the exact deployed immutable registry or fails closed.
func (catalog Catalog) Resolve(reference Reference) (Registry, error) {
	registry, exists := catalog.registries[reference]
	if !exists {
		return Registry{}, ErrRegistryNotFound
	}

	return registry, nil
}

// Snapshots returns every deployed registry ordered from newest to oldest.
func (catalog Catalog) Snapshots() ([]Snapshot, error) {
	snapshots := make([]Snapshot, 0, len(catalog.registries))
	for reference, registry := range catalog.registries {
		document, err := registry.CanonicalJSON()
		if err != nil {
			return nil, fmt.Errorf("serialise registry revision %d: %w", reference.Revision, err)
		}
		snapshots = append(snapshots, Snapshot{Reference: reference, Document: document})
	}
	slices.SortFunc(snapshots, func(left, right Snapshot) int {
		if left.Reference.Revision > right.Reference.Revision {
			return -1
		}
		if left.Reference.Revision < right.Reference.Revision {
			return 1
		}
		return 0
	})

	return snapshots, nil
}

// IsZero reports whether the catalog contains no deployed registry.
func (catalog Catalog) IsZero() bool { return len(catalog.registries) == 0 }
