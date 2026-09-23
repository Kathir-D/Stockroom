// Package examples carries the example data into the binary, so the setup
// wizard's "Load some examples" works on an install that has no checkout --
// which is every install (CLAUDE.md §13, Phase B).
package examples

import "embed"

// FS holds every example file at its own name.
//
//go:embed *.md *.csv
var FS embed.FS
