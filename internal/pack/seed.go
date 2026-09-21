package pack

import (
	"embed"
	"fmt"
	"io/fs"
	"slices"
	"strings"
)

// seedFiles embeds the reviewed canonical seed packs so a clean installation
// needs no external pack data.
//
//go:embed seeds/*.json
var seedFiles embed.FS

// Seeds returns the embedded reviewed packs in country order. Every seed is a
// canonical document whose digest is verified during parsing.
func Seeds() ([]Pack, error) {
	entries, err := fs.ReadDir(seedFiles, "seeds")
	if err != nil {
		return nil, fmt.Errorf("pack: read embedded seeds: %w", err)
	}
	packs := make([]Pack, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		encoded, err := seedFiles.ReadFile("seeds/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("pack: read embedded seed: %w", err)
		}
		value, err := Parse(encoded)
		if err != nil {
			return nil, fmt.Errorf("pack: parse embedded seed %s: %w", entry.Name(), err)
		}
		if entry.Name() != value.Country+".json" {
			return nil, fmt.Errorf("%w: seed file name", ErrInvalid)
		}
		packs = append(packs, value)
	}
	if len(packs) == 0 {
		return nil, fmt.Errorf("%w: empty seeds", ErrInvalid)
	}
	slices.SortFunc(packs, func(left, right Pack) int {
		return strings.Compare(left.Country, right.Country)
	})
	return packs, nil
}
