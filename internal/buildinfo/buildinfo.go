// Package buildinfo exposes safe build metadata for binaries and diagnostics.
package buildinfo

import "fmt"

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// Info describes one build of Idenqa.
type Info struct {
	Version string
	Commit  string
	Date    string
}

// Current returns metadata injected at build time, or safe development values.
func Current() Info {
	return Info{
		Version: version,
		Commit:  commit,
		Date:    date,
	}
}

// String returns a stable, human-readable representation of the build.
func (info Info) String() string {
	return fmt.Sprintf("idenqa %s (commit %s, built %s)", info.Version, info.Commit, info.Date)
}
