// Package migrations exposes the reviewed SQL migration assets embedded in
// the Idenqa operational CLI.
package migrations

import "embed"

// LatestVersion is the schema version required by this binary.
const LatestVersion uint = 29

// Files contains all paired up and down migration files.
//
//go:embed *.sql
var Files embed.FS
