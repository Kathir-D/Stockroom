package stockroom

import (
	"context"
	"encoding/csv"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The CSV export, against the live database. The point of a backup is that it
// can be read back after the machine it came from is gone, so what these
// check is that every table lands in a file with a header, and that the
// admin-only gate holds.

func TestBackupNowIsAdminOnly(t *testing.T) {
	db := requireTestDB(t)
	student := actorFor(insertTestProfile(t, db, false, "student-pw"))
	if _, err := db.BackupNow(context.Background(), student, t.TempDir()); !errors.Is(err, ErrForbidden) {
		t.Errorf("BackupNow as a student = %v, want ErrForbidden", err)
	}
}

func TestExportAllTablesToCSV(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	base := t.TempDir()

	res, err := db.BackupNow(ctx, admin, base)
	if err != nil {
		t.Fatalf("BackupNow: %v", err)
	}

	// The folder is dated, so a week of nightly runs sits side by side rather
	// than overwriting each other.
	if want := filepath.Join(base, res.RanAt.Format("2006-01-02")); res.Dir != want {
		t.Errorf("dir = %q, want %q", res.Dir, want)
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
	var total int64
	for i, exported := range res.Tables {
		if exported.Table != tables[i] {
			t.Errorf("table %d = %q, want %q", i, exported.Table, tables[i])
		}
		total += exported.Rows
		// Parsed as CSV rather than counted as lines, because a description
		// or a damage note may hold a newline of its own inside quotes.
		records, err := readCSV(exported.File)
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
	}
	if res.Rows != total {
		t.Errorf("total rows = %d, want %d", res.Rows, total)
	}

	// The seeded category tree is the easiest row count to recognise, and it
	// is the one whose loss would hurt most.
	categories := filepath.Join(res.Dir, "categories.csv")
	b, err := os.ReadFile(categories)
	if err != nil {
		t.Fatalf("read categories.csv: %v", err)
	}
	if !strings.HasPrefix(string(b), "id,name,parent_id,") {
		t.Errorf("categories.csv starts %q, want the column names", firstLine(string(b)))
	}
	if strings.Count(string(b), "\n") < 2 {
		t.Errorf("categories.csv looks empty: %q", b)
	}

	// A second run the same day replaces the day's folder rather than
	// failing on the files already there.
	if _, err := db.BackupNow(ctx, admin, base); err != nil {
		t.Errorf("second BackupNow the same day: %v", err)
	}

	// Both runs wrote aside and moved the folder in whole, so the dated folder
	// is the only thing here. A leftover staging folder would be a pile of
	// tables that reads like a backup and isn't one.
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 1 || names[0] != res.RanAt.Format("2006-01-02") {
		t.Errorf("backup dir holds %v, want only the dated folder", names)
	}
}

func TestExportWithoutABackupDir(t *testing.T) {
	db := requireTestDB(t)
	_, err := db.ExportAllTablesToCSV(context.Background(), "")
	if !errors.Is(err, ErrNotConfigured) {
		t.Errorf("export with no BACKUP_DIR = %v, want ErrNotConfigured", err)
	}
	// The admin who pressed the button is the one who edits .env, so the
	// message has to name the variable rather than hide behind a 500.
	if err == nil || !strings.Contains(err.Error(), "BACKUP_DIR") {
		t.Errorf("error = %v, want it to name BACKUP_DIR", err)
	}
}

func readCSV(path string) ([][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	return r.ReadAll()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
