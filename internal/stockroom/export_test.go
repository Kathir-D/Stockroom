package stockroom

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The export is the backup's archive handed to a browser. What matters is that
// it needs none of the backup's configuration, that it is always readable
// without a passphrase, that the secrets stay out of it, and that it reads back
// through the same verifier a restore uses.
func TestExportEverything(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	student := actorFor(insertTestProfile(t, db, false, "student-pw"))

	if _, err := db.ExportEverything(ctx, student); !errors.Is(err, ErrForbidden) {
		t.Errorf("ExportEverything as a student = %v, want ErrForbidden", err)
	}

	// No backup folder anywhere: a school that never set backups up can still
	// leave. A passphrase is set, and the export ignores it.
	db.BackupDir = ""
	const secret = "github_pat_EXPORTTESTvalue"
	restore := withTestSettings(t, db, admin, SettingsInput{
		GitHubToken:       strPtr(secret),
		ArchivePassphrase: strPtr("export-is-never-encrypted"),
	})
	defer restore()

	exp, err := db.ExportEverything(ctx, admin)
	if err != nil {
		t.Fatalf("ExportEverything: %v", err)
	}
	if !strings.HasPrefix(exp.Filename, "stockroom-export-") || !strings.HasSuffix(exp.Filename, ".zip") {
		t.Errorf("filename = %q, want stockroom-export-<date>.zip", exp.Filename)
	}
	if isEncryptedArchive(exp.Archive) {
		t.Fatal("the export is encrypted; the person leaving Stockroom has to be able to open it")
	}

	// readArchive checks every digest in the manifest, so this is the same
	// test a restore would run before touching the database.
	archive, err := readArchive(exp.Archive, "")
	if err != nil {
		t.Fatalf("the export does not read back as an archive: %v", err)
	}
	for _, name := range []string{archiveInventory, archiveAccounts, archiveTablesDir + "assets.csv", archiveTablesDir + "profiles.csv"} {
		if _, err := archive.read(name); err != nil {
			t.Errorf("export is missing %s: %v", name, err)
		}
	}
	settingsCSV, err := archive.read(archiveTablesDir + "app_settings.csv")
	if err != nil {
		t.Fatalf("read app_settings.csv: %v", err)
	}
	if strings.Contains(string(settingsCSV), secret) {
		t.Error("app_settings.csv in the export carries the GitHub token")
	}
	if strings.Contains(string(settingsCSV), "export-is-never-encrypted") {
		t.Error("app_settings.csv in the export carries the archive passphrase")
	}
}
