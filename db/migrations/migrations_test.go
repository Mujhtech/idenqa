package migrations

import (
	"io/fs"
	"strconv"
	"strings"
	"testing"
)

func TestLatestVersionHasPairedAssets(t *testing.T) {
	t.Parallel()

	entries, err := fs.ReadDir(Files, ".")
	if err != nil {
		t.Fatal(err)
	}
	prefix := leftPadVersion(LatestVersion) + "_"
	found := map[string]bool{"up": false, "down": false}
	for _, entry := range entries {
		for direction := range found {
			if strings.HasPrefix(entry.Name(), prefix) && strings.HasSuffix(entry.Name(), "."+direction+".sql") {
				found[direction] = true
			}
		}
	}
	for direction, exists := range found {
		if !exists {
			t.Fatalf("latest migration %d has no %s asset", LatestVersion, direction)
		}
	}
}

func leftPadVersion(version uint) string {
	value := strconv.FormatUint(uint64(version), 10)
	return strings.Repeat("0", 6-len(value)) + value
}
