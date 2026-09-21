package experience

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"

	contract "github.com/Mujhtech/idenqa/contracts/experience/v1"
)

// DefaultMandatoryVersion is the Core-owned mandatory-copy version shipped by
// this build. New versions are additive files under mandatory/.
const DefaultMandatoryVersion = "mc-2026-09-01"

//go:embed mandatory/default_v1.json
var defaultMandatoryRaw []byte

type mandatoryCatalogueFile struct {
	Version string               `json:"version"`
	Entries []contract.CopyEntry `json:"entries"`
}

// MandatoryCatalogue is the Core-owned, non-overridable copy boundary.
type MandatoryCatalogue interface {
	Has(version string) bool
	Entries(version string) ([]contract.CopyEntry, bool)
	Versions() []string
	Digest(version string) (string, bool)
}

type embeddedMandatoryCatalogue struct {
	versions map[string][]contract.CopyEntry
	digests  map[string]string
}

// NewDefaultMandatoryCatalogue loads the reviewed embedded regulatory, consent,
// safety, and accessibility copy. Duplicate keys and duplicate versions fail
// closed at construction.
func NewDefaultMandatoryCatalogue() (MandatoryCatalogue, error) {
	var file mandatoryCatalogueFile
	if err := json.Unmarshal(defaultMandatoryRaw, &file); err != nil {
		return nil, fmt.Errorf("experience: decode mandatory copy: %w", err)
	}
	if file.Version != DefaultMandatoryVersion || len(file.Entries) == 0 {
		return nil, fmt.Errorf("%w: mandatory copy file", ErrInvalid)
	}
	entries := make([]contract.CopyEntry, len(file.Entries))
	seen := make(map[string]struct{}, len(file.Entries))
	for index, entry := range file.Entries {
		if _, duplicate := seen[entry.Key]; duplicate {
			return nil, fmt.Errorf("%w: mandatory copy key", ErrInvalid)
		}
		seen[entry.Key] = struct{}{}
		entries[index] = contract.CopyEntry{Key: entry.Key, Value: entry.Value}
	}
	digest, err := contract.DigestCopy(file.Version, entries)
	if err != nil {
		return nil, fmt.Errorf("%w: mandatory copy digest: %w", ErrInvalid, err)
	}
	return &embeddedMandatoryCatalogue{
		versions: map[string][]contract.CopyEntry{file.Version: entries},
		digests:  map[string]string{file.Version: digest},
	}, nil
}

// Has reports whether the version is a known Core-owned catalogue.
func (catalogue *embeddedMandatoryCatalogue) Has(version string) bool {
	_, exists := catalogue.versions[version]
	return exists
}

// Entries returns a defensive copy of one catalogue's ordered entries.
func (catalogue *embeddedMandatoryCatalogue) Entries(version string) ([]contract.CopyEntry, bool) {
	entries, exists := catalogue.versions[version]
	if !exists {
		return nil, false
	}
	return append([]contract.CopyEntry(nil), entries...), true
}

// Versions returns the known versions in stable order.
func (catalogue *embeddedMandatoryCatalogue) Versions() []string {
	versions := make([]string, 0, len(catalogue.versions))
	for version := range catalogue.versions {
		versions = append(versions, version)
	}
	sort.Strings(versions)
	return versions
}

// Digest returns the canonical digest of one mandatory catalogue.
func (catalogue *embeddedMandatoryCatalogue) Digest(version string) (string, bool) {
	digest, exists := catalogue.digests[version]
	return digest, exists
}
