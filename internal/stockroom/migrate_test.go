package stockroom

import (
	"context"
	"testing"
	"testing/fstest"

	"stockroom/supabase"
)

// TestMigrateIsIdempotent is the one that matters on a running machine.
//
// The server calls Migrate on every start (server/main.go), so the common case
// by a very long way is a database that is already current -- and the failure
// this guards against is the expensive one: a runner that does not recognise
// what the Supabase CLI recorded would replay `create table profiles` over a
// live inventory at boot.
//
// It runs against the development database, which the CLI has already
// migrated, so "nothing to do" is both the assertion and the proof that the
// two tools agree on the version strings.
func TestMigrateIsIdempotent(t *testing.T) {
	db := requireTestDB(t)

	applied, err := Migrate(context.Background(), db.Pool, supabase.Migrations)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(applied) != 0 {
		t.Errorf("Migrate applied %v against an already-migrated database; want none", applied)
	}
}

// TestMigrateRejectsUnversionedFile is the gate.
//
// A file the runner cannot order is refused rather than skipped. Skipping is
// the tempting behaviour and the wrong one: a migration that silently does not
// run is precisely the failure this whole mechanism exists to prevent, and it
// surfaces later as a missing column in a query nobody changed.
func TestMigrateRejectsUnversionedFile(t *testing.T) {
	fsys := fstest.MapFS{
		"20260101000000_fine.sql": &fstest.MapFile{Data: []byte("select 1")},
		"oops.sql":                &fstest.MapFile{Data: []byte("select 1")},
	}
	if _, err := migrationFilenames(fsys); err == nil {
		t.Fatal("migrationFilenames accepted a file with no version prefix; want an error")
	}
}

// TestEmbeddedMigrationsAreOrdered checks the files that actually ship.
//
// Cheap, needs no database, and covers the thing a new migration gets wrong:
// a filename that does not start with a timestamp, or a duplicate version,
// either of which changes what a fresh install ends up with.
func TestEmbeddedMigrationsAreOrdered(t *testing.T) {
	files, err := migrationFilenames(supabase.Migrations)
	if err != nil {
		t.Fatalf("migrationFilenames on the embedded set: %v", err)
	}
	seen := map[string]bool{}
	for i, f := range files {
		if seen[f.version] {
			t.Errorf("two migrations share version %s", f.version)
		}
		seen[f.version] = true
		if i > 0 && files[i-1].version >= f.version {
			t.Errorf("migrations out of order: %s before %s", files[i-1].version, f.version)
		}
	}
}
