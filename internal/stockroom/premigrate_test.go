package stockroom

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"stockroom/supabase"
)

// dummyVersion sorts after every real migration and is removed after each
// test, so the shared development database never keeps it.
const dummyVersion = "29990101000000"

// migrationsPlusDummy is the embedded set with one harmless migration added,
// which makes the development database look one migration behind.
func migrationsPlusDummy(t *testing.T, db *DB) fs.FS {
	t.Helper()
	fsys := fstest.MapFS{}
	entries, err := fs.ReadDir(supabase.Migrations, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := fs.ReadFile(supabase.Migrations, e.Name())
		if err != nil {
			t.Fatal(err)
		}
		fsys[e.Name()] = &fstest.MapFile{Data: data}
	}
	fsys[dummyVersion+"_premigrate_test.sql"] = &fstest.MapFile{Data: []byte("select 1")}
	t.Cleanup(func() { forgetDummy(t, db) })
	return fsys
}

func forgetDummy(t *testing.T, db *DB) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(),
		`delete from supabase_migrations.schema_migrations where version = $1`, dummyVersion); err != nil {
		t.Errorf("remove the dummy migration: %v", err)
	}
}

func dummyApplied(t *testing.T, db *DB) bool {
	t.Helper()
	var n int
	if err := db.Pool.QueryRow(context.Background(),
		`select count(*) from supabase_migrations.schema_migrations where version = $1`, dummyVersion).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

// fakePGDump puts a pg_dump script first on PATH. It writes its arguments and
// whether PGPASSWORD arrived, or fails when fail is true.
func fakePGDump(t *testing.T, fail bool) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake pg_dump is a shell script")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n"
	if fail {
		script += "echo 'pg_dump: error: connection refused' >&2\nexit 1\n"
	} else {
		script += `echo "-- args: $*"` + "\n" + `echo "-- password set: ${PGPASSWORD:+yes}"` + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "pg_dump"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	old := pgDumpFallbacks
	pgDumpFallbacks = func() []string { return nil }
	t.Cleanup(func() { pgDumpFallbacks = old })
}

func TestPendingMigrationsSeesTheDummy(t *testing.T) {
	db := requireTestDB(t)
	pending, err := PendingMigrations(context.Background(), db.Pool, migrationsPlusDummy(t, db))
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0] != dummyVersion {
		t.Errorf("pending = %v, want [%s]", pending, dummyVersion)
	}
}

func TestPrepareSchemaDumpsBeforeMigrating(t *testing.T) {
	db := requireTestDB(t)
	fakePGDump(t, false)
	dir := filepath.Join(t.TempDir(), "pre-migrate")

	applied, dump, err := PrepareSchema(context.Background(), db.Pool, migrationsPlusDummy(t, db), SchemaOptions{
		Dump:        PreMigrateDumpRequired,
		Dir:         dir,
		DatabaseURL: "postgresql://someone:s3cret@127.0.0.1:54322/postgres",
	})
	if err != nil {
		t.Fatalf("PrepareSchema: %v", err)
	}
	if len(applied) != 1 || !dummyApplied(t, db) {
		t.Errorf("applied = %v, want the dummy", applied)
	}
	if !strings.HasPrefix(filepath.Base(dump), "pre-migrate-") || !strings.Contains(dump, "-to-"+dummyVersion+"-") {
		t.Errorf("dump path = %q", dump)
	}
	st, err := os.Stat(dump)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("dump mode = %v, want 600", st.Mode().Perm())
	}
	body, _ := os.ReadFile(dump)
	if strings.Contains(string(body), "s3cret") {
		t.Error("the password reached pg_dump's arguments")
	}
	if !strings.Contains(string(body), "password set: yes") || !strings.Contains(string(body), "--username someone") {
		t.Errorf("pg_dump saw %q", body)
	}
}

func TestPrepareSchemaStopsWhenTheDumpFails(t *testing.T) {
	db := requireTestDB(t)
	fakePGDump(t, true)

	_, _, err := PrepareSchema(context.Background(), db.Pool, migrationsPlusDummy(t, db), SchemaOptions{
		Dump:        PreMigrateDumpRequired,
		Dir:         t.TempDir(),
		DatabaseURL: testDatabaseURL(),
	})
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("err = %v, want the dump's failure", err)
	}
	if dummyApplied(t, db) {
		t.Error("the migration ran after the dump failed")
	}
}

func TestPrepareSchemaStopsWithoutPGDump(t *testing.T) {
	db := requireTestDB(t)
	fakePGDump(t, false)
	t.Setenv("PATH", t.TempDir())

	_, _, err := PrepareSchema(context.Background(), db.Pool, migrationsPlusDummy(t, db), SchemaOptions{
		Dump:        PreMigrateDumpRequired,
		Dir:         t.TempDir(),
		DatabaseURL: testDatabaseURL(),
	})
	if !errors.Is(err, ErrPGDumpNotFound) {
		t.Fatalf("err = %v, want ErrPGDumpNotFound", err)
	}
	if dummyApplied(t, db) {
		t.Error("the migration ran with no pg_dump")
	}
}

func TestPrepareSchemaOffSkipsTheDump(t *testing.T) {
	db := requireTestDB(t)
	fakePGDump(t, true)

	applied, dump, err := PrepareSchema(context.Background(), db.Pool, migrationsPlusDummy(t, db), SchemaOptions{
		Dump: PreMigrateDumpOff,
	})
	if err != nil || dump != "" || len(applied) != 1 {
		t.Errorf("applied %v, dump %q, err %v; want the dummy applied and no dump", applied, dump, err)
	}
}

// TestMigrateRefusesANewerDatabase: installing an older binary over a newer
// one must stop at boot, not fail on the first query that meets a new column.
func TestMigrateRefusesANewerDatabase(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	if _, err := db.Pool.Exec(ctx, `
		insert into supabase_migrations.schema_migrations (version, name) values ($1, 'from_the_future')`,
		dummyVersion); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { forgetDummy(t, db) })

	if _, err := Migrate(ctx, db.Pool, supabase.Migrations); !errors.Is(err, ErrDatabaseNewer) {
		t.Errorf("Migrate: err = %v, want ErrDatabaseNewer", err)
	}
	if _, err := PendingMigrations(ctx, db.Pool, supabase.Migrations); !errors.Is(err, ErrDatabaseNewer) {
		t.Errorf("PendingMigrations: err = %v, want ErrDatabaseNewer", err)
	}
}

func TestPruneDumpsKeepsTheNewest(t *testing.T) {
	dir := t.TempDir()
	for i := range 13 {
		name := filepath.Join(dir, "pre-migrate-a-to-b-"+string(rune('a'+i))+".sql")
		if err := os.WriteFile(name, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pruneDumps(dir, 10)
	left, _ := filepath.Glob(filepath.Join(dir, "*.sql"))
	if len(left) != 10 {
		t.Fatalf("%d dumps left, want 10", len(left))
	}
	for _, gone := range []string{"a", "b", "c"} {
		if _, err := os.Stat(filepath.Join(dir, "pre-migrate-a-to-b-"+gone+".sql")); err == nil {
			t.Errorf("the oldest dump %s survived", gone)
		}
	}
}
