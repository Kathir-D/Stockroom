package stockroom

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The backup export, against the live database. The point of a backup is that
// it can be read back after the machine it came from is gone, so what these
// check is that every table lands in the archive with a header, that the
// manifest describes what is actually there, that the GitHub token does not
// leave the building, and that the admin-only gate holds.

func TestBackupNowIsAdminOnly(t *testing.T) {
	db := requireTestDB(t)
	db.BackupDir = t.TempDir()
	student := actorFor(insertTestProfile(t, db, false, "student-pw"))
	if _, err := db.BackupNow(context.Background(), student); !errors.Is(err, ErrForbidden) {
		t.Errorf("BackupNow as a student = %v, want ErrForbidden", err)
	}
}

func TestBackupNowWritesAnArchive(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	base := t.TempDir()
	db.BackupDir = base
	db.PhotoBackupDir = t.TempDir()

	res, err := db.BackupNow(ctx, admin)
	if err != nil {
		t.Fatalf("BackupNow: %v", err)
	}
	if res.Skipped {
		t.Fatal("the run was skipped; another backup should not be holding the lock in a test")
	}

	// The folder is dated, so a week of nightly runs sits side by side rather
	// than overwriting each other.
	day := res.RanAt.Format("2006-01-02")
	if want := filepath.Join(base, day); res.Dir != want {
		t.Errorf("dir = %q, want %q", res.Dir, want)
	}

	// Exactly three files in the dated folder: the restorable archive and the
	// two an admin can open in Excel. The raw tables live inside the archive,
	// where the restore reads them, and a loose second copy would only be
	// another thing to keep in step.
	got := dirNames(t, res.Dir)
	want := []string{archiveAccounts, archiveName(day, false), archiveInventory}
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("the dated folder holds %v, want %v", got, want)
	}

	// The table list comes from the database rather than a list in Go, so a
	// table added by a later migration is in the backup without anyone
	// remembering to add it here. That is exactly what this compares.
	tables, err := publicTables(ctx, db.Pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tables) != len(tables) {
		t.Fatalf("exported %d tables, want %d", len(res.Tables), len(tables))
	}

	archive := openTestArchive(t, res.Archive, "")

	// Every digest in the manifest has to match, which is the check a restore
	// makes before it touches anything.
	if err := archive.verify(); err != nil {
		t.Errorf("the archive does not match its own manifest: %v", err)
	}
	if archive.manifest.SchemaVersion == "" {
		t.Error("the manifest records no schema version, so a restore cannot tell a mismatched database from a matching one")
	}
	if len(archive.manifest.Sequences) == 0 {
		t.Error("the manifest records no sequences; assets_asset_tag_seq would restart at 1 and collide")
	}

	var total int64
	for i, exported := range res.Tables {
		if exported.Table != tables[i] {
			t.Errorf("table %d = %q, want %q", i, exported.Table, tables[i])
		}
		total += exported.Rows
		content, err := archive.read(exported.File)
		if err != nil {
			t.Fatalf("the archive has no %s", exported.File)
		}
		// Parsed as CSV rather than counted as lines, because a description
		// or a damage note may hold a newline of its own inside quotes.
		records, err := csv.NewReader(bytes.NewReader(content)).ReadAll()
		if err != nil {
			t.Fatalf("read %s: %v", exported.File, err)
		}
		// A header on every file, including the empty ones: a restore reads
		// the column names from it.
		if len(records) == 0 {
			t.Fatalf("%s has no header row", exported.File)
		}
		if got := int64(len(records) - 1); got != exported.Rows {
			t.Errorf("%s holds %d data rows, but the result claims %d", exported.File, got, exported.Rows)
		}
		if want := archive.manifest.Rows[exported.Table]; want != exported.Rows {
			t.Errorf("the manifest claims %d rows for %s, the export wrote %d", want, exported.Table, exported.Rows)
		}
	}
	if res.Rows != total {
		t.Errorf("total rows = %d, want %d", res.Rows, total)
	}

	// The seeded category tree is the easiest row count to recognise, and it
	// is the one whose loss would hurt most.
	categories, err := archive.read(archiveTablesDir + "categories.csv")
	if err != nil {
		t.Fatalf("read categories.csv: %v", err)
	}
	if !strings.HasPrefix(string(categories), "id,name,parent_id,") {
		t.Errorf("categories.csv starts %q, want the column names", firstLine(string(categories)))
	}

	// inventory.csv is the file somebody opens in Excel after the machine
	// dies, so it has to be readable on its own terms rather than a second
	// copy of assets.csv.
	inventory, err := archive.read(archiveInventory)
	if err != nil {
		t.Fatalf("read inventory.csv: %v", err)
	}
	if !strings.HasPrefix(string(inventory), strings.Join(inventoryCSVHeader, ",")) {
		t.Errorf("inventory.csv starts %q, want the readable column names", firstLine(string(inventory)))
	}

	// A second run the same day replaces the day's folder rather than
	// failing on the files already there.
	if _, err := db.BackupNow(ctx, admin); err != nil {
		t.Errorf("second BackupNow the same day: %v", err)
	}

	// Both runs wrote aside and moved the folder in whole, so the dated folder
	// plus the two run-state files are all that is here. A leftover staging
	// folder would be a pile of tables that reads like a backup and isn't one.
	for _, name := range dirNames(t, base) {
		switch name {
		case day, stateFile, logFile:
		default:
			t.Errorf("the backup dir holds an unexpected %q", name)
		}
	}
}

// The token must not end up in the backup. app_settings lives in the public
// schema, so the export sweeps it up like any other table -- and github_token
// would be pushed to the very repository it grants write access to, where
// GitHub's secret scanning would revoke it and kill backups silently hours
// later.
func TestBackupRedactsTheGitHubToken(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	db.BackupDir = t.TempDir()

	const secret = "github_pat_TESTTOKENvalue"
	restore := withTestSettings(t, db, admin, SettingsInput{
		GitHubToken:       strPtr(secret),
		ArchivePassphrase: strPtr("test-passphrase-not-in-the-archive"),
	})
	defer restore()

	res, err := db.BackupNow(ctx, admin)
	if err != nil {
		t.Fatalf("BackupNow: %v", err)
	}
	// The archive is encrypted now, because setting a passphrase is what that
	// means -- so the decryption is part of what is being checked.
	archive := openTestArchive(t, res.Archive, "test-passphrase-not-in-the-archive")
	settingsCSV, err := archive.read(archiveTablesDir + "app_settings.csv")
	if err != nil {
		t.Fatalf("read app_settings.csv: %v", err)
	}
	if strings.Contains(string(settingsCSV), secret) {
		t.Error("app_settings.csv carries the GitHub token; it would be pushed to the repository that token unlocks")
	}
	if strings.Contains(string(settingsCSV), "test-passphrase-not-in-the-archive") {
		t.Error("app_settings.csv carries the archive passphrase; a passphrase inside the archive it encrypts protects nothing")
	}
}

// Two runs at once must not both write the same dated folder. The lock is in
// Postgres rather than in this process, which is what makes it hold across the
// server, the scheduler and cmd/restore.
func TestBackupLockMakesASecondRunASkip(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	db.BackupDir = t.TempDir()

	release, held, err := acquireBackupLock(ctx, db.Pool)
	if err != nil {
		t.Fatal(err)
	}
	if !held {
		t.Fatal("could not take the backup lock; something else is holding it")
	}
	defer release()

	res, err := db.RunBackup(ctx, BackupSourceScheduled)
	if err != nil {
		t.Fatalf("RunBackup while locked: %v", err)
	}
	if !res.Skipped {
		t.Error("a second run wrote a backup while the lock was held; two runs can now write the same folder")
	}
}

// An unset backup folder is a setting nobody filled in, not a bug and not a
// bad request: 503 with the message intact (CLAUDE.md §8.1).
func TestBackupWithNoFolderIsNotConfigured(t *testing.T) {
	db := requireTestDB(t)
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	db.BackupDir = ""
	restore := withTestSettings(t, db, admin, SettingsInput{BackupDir: strPtr("")})
	defer restore()

	if _, err := db.BackupNow(context.Background(), admin); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("BackupNow with no folder = %v, want ErrNotConfigured", err)
	}
}

/* ------------------------------------------------------------ fixtures ---- */

func strPtr(s string) *string { return &s }

// withTestSettings applies a settings change and returns the function that
// puts the previous values back. The settings row is shared by the whole
// database, so a test that changed it and did not restore it would quietly
// change the next one.
func withTestSettings(t *testing.T, db *DB, admin Actor, in SettingsInput) func() {
	t.Helper()
	ctx := context.Background()
	before, err := db.loadSettings(ctx)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if _, err := db.SaveSettings(ctx, admin, in); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	return func() {
		_, _ = db.SaveSettings(ctx, admin, SettingsInput{
			BackupDir:           &before.BackupDir,
			PhotoBackupDir:      &before.PhotoBackupDir,
			KeepDays:            &before.KeepDays,
			StaleHours:          &before.StaleHours,
			ScheduleHour:        &before.ScheduleHour,
			DriveEnabled:        &before.DriveEnabled,
			DriveRemote:         &before.DriveRemote,
			DrivePath:           &before.DrivePath,
			GitHubEnabled:       &before.GitHubEnabled,
			GitHubRepo:          &before.GitHubRepo,
			GitHubToken:         &before.GitHubToken,
			ArchivePassphrase:   &before.ArchivePassphrase,
			PhotoMinFreeGB:      &before.PhotoMinFreeGB,
			PhotoMaxGenerations: &before.PhotoMaxGenerations,
		})
	}
}

func openTestArchive(t *testing.T, path, passphrase string) *openArchive {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the archive: %v", err)
	}
	a, err := readArchive(raw, passphrase)
	if err != nil {
		t.Fatalf("open the archive: %v", err)
	}
	return a
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// rewriteArchive rebuilds an archive with one member replaced.
//
// keepManifest decides which check is under test. Left alone (true), the
// manifest still names the original digest, so the checksum catches it before
// a transaction is ever opened. Updated (false), both the digest and the row
// count are recomputed to match the new content, which is what makes the
// foreign-key check the only thing left that can catch an orphaned reference
// -- the check replica mode specifically cannot make on its own.
func rewriteArchive(t *testing.T, path, member string, content []byte, keepManifest bool) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}

	var manifest Manifest
	for _, f := range zr.File {
		if f.Name != archiveManifest {
			continue
		}
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		if err := json.Unmarshal(b, &manifest); err != nil {
			t.Fatal(err)
		}
	}
	if !keepManifest {
		sum := sha256.Sum256(content)
		manifest.Files[member] = hex.EncodeToString(sum[:])
		if table, ok := strings.CutPrefix(member, archiveTablesDir); ok {
			records, err := csv.NewReader(bytes.NewReader(content)).ReadAll()
			if err != nil {
				t.Fatal(err)
			}
			manifest.Rows[strings.TrimSuffix(table, ".csv")] = int64(len(records) - 1)
		}
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		w, err := zw.Create(f.Name)
		if err != nil {
			t.Fatal(err)
		}
		switch f.Name {
		case member:
			_, err = w.Write(content)
		case archiveManifest:
			_, err = w.Write(manifestJSON)
		default:
			rc, oerr := f.Open()
			if oerr != nil {
				t.Fatal(oerr)
			}
			_, err = io.Copy(w, rc)
			rc.Close()
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
