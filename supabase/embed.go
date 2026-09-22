// Package supabase carries the migration files into the Go binary.
//
// The directory is named for the tool that reads it during development -- the
// Supabase CLI applies these on `supabase db reset` -- but the files are the
// schema, not a Supabase artefact, and an installed Stockroom has no CLI to
// apply them with. So the same directory is also a Go package, and the server
// applies whatever is pending at boot (stockroom.Migrate).
//
// It is deliberately the *same* files rather than a copy under internal/. A
// second copy is two schemas that agree until the day somebody edits one, and
// the failure then is a production database shaped differently from every test
// that passed against it. There is one directory; the CLI globs its `*.sql`
// and ignores this file, and `go:embed` does the reverse.
package supabase

import (
	"embed"
	"io/fs"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migrations is the migration directory, rooted so that each entry is a bare
// `<version>_<name>.sql` the way stockroom.Migrate expects to read it.
var Migrations fs.FS = mustSub("migrations")

func mustSub(dir string) fs.FS {
	sub, err := fs.Sub(migrationFiles, dir)
	if err != nil {
		// Unreachable: the embed directive above fails the build before this
		// can. Panicking rather than returning an error keeps the package
		// variable usable without an init check at every call site.
		panic(err)
	}
	return sub
}
