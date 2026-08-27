package buildinfo_test

import (
	"testing"

	"github.com/Mujhtech/idenqa/internal/buildinfo"
)

func TestInfoString(t *testing.T) {
	t.Parallel()

	info := buildinfo.Info{
		Version: "v0.1.0",
		Commit:  "abc1234",
		Date:    "2026-08-27T12:00:00Z",
	}

	want := "idenqa v0.1.0 (commit abc1234, built 2026-08-27T12:00:00Z)"
	if got := info.String(); got != want {
		t.Fatalf("Info.String() = %q, want %q", got, want)
	}
}

func TestCurrentHasSafeDefaults(t *testing.T) {
	t.Parallel()

	info := buildinfo.Current()
	if info.Version == "" || info.Commit == "" || info.Date == "" {
		t.Fatalf("Current() returned empty metadata: %+v", info)
	}
}
