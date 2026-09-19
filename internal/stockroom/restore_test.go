package stockroom

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

// The restore, against the live database.
//
// These tests really do truncate and reload every table, because a restore
// that has only ever been tested against a mock is a restore nobody has
// tested. They are safe to run on a development machine for one reason: each
// takes a backup *first* and restores that same archive, so the database ends
// the test holding what it held at the start. A failure mid-restore rolls the
// transaction back, which leaves it holding that too.

func TestRestoreIsAdminOnly(t *testing.T) {
	db := requireTestDB(t)
	student := actorFor(insertTestProfile(t, db, false, "student-pw"))
	_, err := db.RestoreFromZip(context.Background(), student, []byte("not an archive"),
		RestoreOptions{Confirm: restoreConfirmation})
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("RestoreFromZip as a student = %v, want ErrForbidden", err)
	}
}

// Restoring is the one destructive action in the application, so it asks for a
// word to be typed. The gate is in the package rather than in the handler, so
// the CLI is held to it too.
func TestRestoreRefusesWithoutTheConfirmation(t *testing.T) {
	db := requireTestDB(t)
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	_, err := db.RestoreFromZip(context.Background(), admin, []byte("not an archive"), RestoreOptions{})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), restoreConfirmation) {
		t.Errorf("RestoreFromZip with no confirmation = %v, want ErrInvalid naming %s", err, restoreConfirmation)
	}
}

// The round trip, which is the only real proof a backup is a backup.
func TestRestoreRoundTrip(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	db.BackupDir = t.TempDir()
	db.PhotoBackupDir = t.TempDir()

	// Read the sequence before the backup rather than hard-coding a number:
	// it was 89 when Phase 7 was specified and 130 the next day.
	seqBefore := sequenceValue(t, db, "assets_asset_tag_seq")

	res, err := db.BackupNow(ctx, admin)
	if err != nil {
		t.Fatalf("BackupNow: %v", err)
	}

	// Everything below happens *after* the snapshot, so the restore has to
	// undo all of it.
	marker := insertTestAsset(t, db, Profile{ID: admin.ID})

	archive, err := os.ReadFile(res.Archive)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := db.RestoreFromZip(ctx, admin, archive, RestoreOptions{Confirm: restoreConfirmation, Source: "test"})
	if err != nil {
		t.Fatalf("RestoreFromZip: %v", err)
	}
	if len(restored.Warnings) > 0 {
		t.Errorf("the restore could not check something it should have been able to: %v", restored.Warnings)
	}

	// The row the backup never saw is gone.
	var stillThere bool
	if err := db.Pool.QueryRow(ctx, `select exists (select 1 from assets where id = $1)`, marker).Scan(&stillThere); err != nil {
		t.Fatal(err)
	}
	if stillThere {
		t.Error("an asset created after the backup survived the restore; the tables were not replaced")
	}

	// Every table holds what the manifest said it did.
	for _, table := range restored.Tables {
		var live int64
		if err := db.Pool.QueryRow(ctx,
			`select count(*) from `+quoteTestIdent(table.Table)).Scan(&live); err != nil {
			t.Fatalf("count %s: %v", table.Table, err)
		}
		if live != table.Rows {
			t.Errorf("%s holds %d rows after the restore, the archive carried %d", table.Table, live, table.Rows)
		}
	}

	// The sequence resumes where it left off rather than at 1, which is the
	// difference between the next asset getting a fresh tag and colliding on a
	// unique index.
	if got := sequenceValue(t, db, "assets_asset_tag_seq"); got != seqBefore {
		t.Errorf("assets_asset_tag_seq is at %d after the restore, want %d", got, seqBefore)
	}

	// trg_asset_status_log fires on an assets status change and would have
	// written one junk row per asset on the way in. session_replication_role =
	// replica is what keeps activity_log restoring clean, and a count that
	// matches the archive is how that is visible.
	var activityNow int64
	if err := db.Pool.QueryRow(ctx, `select count(*) from activity_log`).Scan(&activityNow); err != nil {
		t.Fatal(err)
	}
	var wantActivity int64
	for _, table := range restored.Tables {
		if table.Table == "activity_log" {
			wantActivity = table.Rows
		}
	}
	if activityNow != wantActivity {
		t.Errorf("activity_log holds %d rows after the restore, the archive carried %d: the triggers were not suspended", activityNow, wantActivity)
	}
}

// A damaged archive must cost nothing: the digests are verified before a
// transaction is ever opened, and a CSV truncated mid-quoted-field is the one
// corruption a row count cannot see.
func TestRestoreRefusesADamagedArchive(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	db.BackupDir = t.TempDir()
	db.PhotoBackupDir = t.TempDir()

	res, err := db.BackupNow(ctx, admin)
	if err != nil {
		t.Fatalf("BackupNow: %v", err)
	}
	before := countRows(t, db, "assets")

	member := archiveTablesDir + "categories.csv"
	damaged := rewriteArchive(t, res.Archive, member, []byte("id,name\nbroken"), true)

	_, err = db.RestoreFromZip(ctx, admin, damaged, RestoreOptions{Confirm: restoreConfirmation})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "categories.csv") {
		t.Errorf("restoring a damaged archive = %v, want ErrInvalid naming categories.csv", err)
	}
	if after := countRows(t, db, "assets"); after != before {
		t.Errorf("assets went from %d rows to %d; the database was touched by a restore that should have been refused", before, after)
	}
}

// The failure replica mode is specifically unable to catch on its own: an
// assets row pointing at a category the archive does not contain. Nothing on
// the way in rejects it, so the restore has to re-check every foreign key
// before it commits -- and roll back when one fails.
func TestRestoreRefusesAnInconsistentArchive(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	db.BackupDir = t.TempDir()
	db.PhotoBackupDir = t.TempDir()

	res, err := db.BackupNow(ctx, admin)
	if err != nil {
		t.Fatalf("BackupNow: %v", err)
	}
	before := countRows(t, db, "categories")

	// Drop the category the greatest number of assets sit under, and fix the
	// manifest so the checksum and row count both pass: the foreign-key check
	// is the only thing left that can catch this.
	original := readArchiveMember(t, res.Archive, archiveTablesDir+"categories.csv")
	trimmed, dropped := dropReferencedCategory(t, db, original)
	if !dropped {
		t.Skip("no category in this database has an asset filed under it, so there is nothing to orphan")
	}
	broken := rewriteArchive(t, res.Archive, archiveTablesDir+"categories.csv", trimmed, false)

	_, err = db.RestoreFromZip(ctx, admin, broken, RestoreOptions{Confirm: restoreConfirmation})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "categories") {
		t.Fatalf("restoring an archive with an orphaned reference = %v, want ErrInvalid naming categories", err)
	}
	if after := countRows(t, db, "categories"); after != before {
		t.Errorf("categories went from %d rows to %d; the transaction did not roll back", before, after)
	}
}

// LocalCLIActor is what lets cmd/restore satisfy RequireAdmin instead of being
// exempt from it, and the security claim is the second half: no session can
// ever produce one.
func TestLocalCLIActor(t *testing.T) {
	cli := LocalCLIActor()
	if err := RequireAdmin(cli); err != nil {
		t.Errorf("RequireAdmin(LocalCLIActor()) = %v, want nil: cmd/restore could not run", err)
	}
	if !cli.trustedCLI {
		t.Error("LocalCLIActor is not marked as the local CLI, so a restore cannot record where it came from")
	}
	if actorLabel(cli) != "cli" {
		t.Errorf("actorLabel(LocalCLIActor()) = %q, want %q", actorLabel(cli), "cli")
	}

	// That an Actor literal outside this package cannot name trustedCLI is a
	// compile-time fact and cannot be asserted at runtime -- this line does
	// not compile in any other package:
	//
	//	stockroom.Actor{ID: "x", IsAdmin: true, trustedCLI: true}
	//
	// What *can* be checked is the other half of the claim: that Resolve never
	// sets it, so no request can arrive carrying one.
	db := requireTestDB(t)
	p := insertTestProfile(t, db, true, "admin-pw")
	sess, err := db.Sessions.Create(p.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := db.Resolve(context.Background(), sess.Token)
	if err != nil {
		t.Fatal(err)
	}
	if actor.trustedCLI {
		t.Error("Resolve produced a trustedCLI actor from a session; an HTTP request can now claim to be the local CLI")
	}
}

/* ------------------------------------------------------------ fixtures ---- */

func sequenceValue(t *testing.T, db *DB, name string) int64 {
	t.Helper()
	var last int64
	var called bool
	if err := db.Pool.QueryRow(context.Background(),
		`select last_value, is_called from `+quoteTestIdent(name)).Scan(&last, &called); err != nil {
		t.Fatalf("read the sequence %s: %v", name, err)
	}
	return last
}

func countRows(t *testing.T, db *DB, table string) int64 {
	t.Helper()
	var n int64
	if err := db.Pool.QueryRow(context.Background(), `select count(*) from `+quoteTestIdent(table)).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// quoteTestIdent is the test's own identifier quoting, kept separate from the
// package's so a bug in one does not hide itself in the other.
func quoteTestIdent(name string) string {
	return `"public"."` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func readArchiveMember(t *testing.T, path, member string) []byte {
	t.Helper()
	a := openTestArchive(t, path, "")
	content, err := a.read(member)
	if err != nil {
		t.Fatalf("read %s from the archive: %v", member, err)
	}
	return content
}

// dropReferencedCategory removes one category row that an asset points at, so
// the archive describes a state no foreign key would have allowed.
func dropReferencedCategory(t *testing.T, db *DB, csvContent []byte) ([]byte, bool) {
	t.Helper()
	var id string
	err := db.Pool.QueryRow(context.Background(),
		`select category_id from assets where category_id is not null limit 1`).Scan(&id)
	if err != nil {
		return csvContent, false
	}
	lines := strings.Split(string(csvContent), "\n")
	out := make([]string, 0, len(lines))
	removed := false
	for _, line := range lines {
		if strings.HasPrefix(line, id+",") {
			removed = true
			continue
		}
		out = append(out, line)
	}
	return []byte(strings.Join(out, "\n")), removed
}

// is_called is the half of a sequence's state that pg_sequences cannot supply,
// and getting it wrong is a silent off-by-one rather than a failure: setval
// defaults to is_called = true, so replaying a never-read sequence with the
// default burns its first value. This covers both states, because they are
// exactly what the flag distinguishes.
func TestSequenceStateSurvivesARoundTrip(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()

	// A sequence of its own, so the assertion does not depend on whatever the
	// development database happens to hold.
	const name = "stockroom_seq_probe"
	if _, err := db.Pool.Exec(ctx, `create sequence if not exists `+quoteTestIdent(name)+` start 5`); err != nil {
		t.Fatalf("create the probe sequence: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Pool.Exec(context.Background(), `drop sequence if exists `+quoteTestIdent(name)) })

	// Never read. pg_sequences reports a null last_value here; the relation
	// holds 5 and is_called = false, which is what the export has to capture.
	captured := findSequence(t, db, name)
	if captured.IsCalled {
		t.Fatalf("a freshly created sequence reports is_called = true; the export cannot be trusted")
	}
	if captured.LastValue != 5 {
		t.Fatalf("captured last_value = %d, want the declared start of 5", captured.LastValue)
	}

	// Move it on, then replay the captured state, exactly as a restore does.
	var burned int64
	if err := db.Pool.QueryRow(ctx, `select nextval($1)`, "public."+name).Scan(&burned); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `select setval($1, $2, $3)`, "public."+name, captured.LastValue, captured.IsCalled); err != nil {
		t.Fatal(err)
	}

	var next int64
	if err := db.Pool.QueryRow(ctx, `select nextval($1)`, "public."+name).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if next != 5 {
		t.Errorf("after replaying a never-read sequence, nextval = %d, want its start value 5: is_called was not replayed", next)
	}

	// And the used case, which is the one assets_asset_tag_seq is in.
	used := findSequence(t, db, name)
	if !used.IsCalled {
		t.Fatalf("a sequence that has been read reports is_called = false")
	}
	if _, err := db.Pool.Exec(ctx, `select setval($1, $2, $3)`, "public."+name, used.LastValue, used.IsCalled); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `select nextval($1)`, "public."+name).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if next != used.LastValue+used.IncrementBy {
		t.Errorf("after replaying a used sequence, nextval = %d, want %d", next, used.LastValue+used.IncrementBy)
	}
}

// A sequence the restore cannot reason about is refused, not skipped. Nothing
// in this schema cycles or counts downwards, so the alternative was two
// branches that would never run -- and a check that quietly passes on a
// sequence it was not designed for is the bug the whole step exists to stop.
func TestRestoreRefusesACyclingSequence(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()

	conn, err := db.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = checkSequences(ctx, tx, []SequenceState{{Name: "wrapping", LastValue: 1, IncrementBy: 1, Cycle: true}})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "cycles") {
		t.Errorf("checkSequences on a cycling sequence = %v, want ErrInvalid saying it cycles", err)
	}
	_, err = checkSequences(ctx, tx, []SequenceState{{Name: "backwards", LastValue: 10, IncrementBy: -1}})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "downwards") {
		t.Errorf("checkSequences on a descending sequence = %v, want ErrInvalid saying it counts downwards", err)
	}
}

func findSequence(t *testing.T, db *DB, name string) SequenceState {
	t.Helper()
	seqs, err := readSequences(context.Background(), db.Pool)
	if err != nil {
		t.Fatalf("read sequences: %v", err)
	}
	for _, s := range seqs {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("readSequences did not find %s", name)
	return SequenceState{}
}

// A restore that had to be forced says so on its own report.
//
// The version check refuses an archive taken on a different schema, and `force`
// is the admin overriding that refusal. Before this, the forced run answered
// with an empty `warnings` list -- identical to a restore that needed no
// override at all -- so the one record that an override happened lived only in
// the head of whoever pressed the button.
func TestForcedRestoreSaysItWasForced(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	db.BackupDir = t.TempDir()
	db.PhotoBackupDir = t.TempDir()

	res, err := db.BackupNow(ctx, admin)
	if err != nil {
		t.Fatalf("BackupNow: %v", err)
	}
	archive, err := os.ReadFile(res.Archive)
	if err != nil {
		t.Fatal(err)
	}

	// The archive's version is whatever this database is on. Move the database
	// on by one migration, and put it back afterwards: every other test reads
	// this table too.
	if res.SchemaVersion == "" {
		t.Skip("skipping: this database has no supabase_migrations table to move")
	}
	const ahead = "99999999999999"
	if _, err := db.Pool.Exec(ctx,
		`insert into supabase_migrations.schema_migrations (version) values ($1)`, ahead); err != nil {
		t.Fatalf("move the schema version on: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(),
			`delete from supabase_migrations.schema_migrations where version = $1`, ahead)
	})

	// Without force it is a refusal, and nothing is written.
	_, err = db.RestoreFromZip(ctx, admin, archive, RestoreOptions{Confirm: restoreConfirmation, Source: "test"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("restoring across a version change = %v, want ErrConflict", err)
	}
	if !strings.Contains(err.Error(), ahead) {
		t.Errorf("the refusal does not name the version this database is on: %v", err)
	}

	forced, err := db.RestoreFromZip(ctx, admin, archive,
		RestoreOptions{Confirm: restoreConfirmation, Source: "test", Force: true})
	if err != nil {
		t.Fatalf("forced restore: %v", err)
	}
	if len(forced.Warnings) == 0 {
		t.Fatal("a forced restore reported no warning, so nothing records that it was forced")
	}
	if !containsSubstring(forced.Warnings, "version") {
		t.Errorf("the warning does not mention the version change: %v", forced.Warnings)
	}
}
