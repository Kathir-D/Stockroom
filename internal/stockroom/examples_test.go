package stockroom

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// examples/ is shipped to schools as the thing to try Stockroom with, and the
// install walkthrough points at it. These tests import every file through the
// same functions the admin panel calls, so an example that stops importing
// fails the build rather than a teacher's first afternoon (CLAUDE.md §13,
// Phase B: "the docs and the files cannot drift apart").

func openExample(t *testing.T, name string) *os.File {
	t.Helper()
	// Tests run with the package directory as the working directory.
	f, err := os.Open(filepath.Join("..", "..", "examples", name))
	if err != nil {
		t.Fatalf("open example: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestExamplesImportCleanly(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin, _ := adminAndSerial(t, db)

	// The media-department tree IS the development seed's tree, so importing
	// it over a seeded database creates nothing. A difference here means the
	// example and seed.sql have drifted.
	res, err := db.ImportCategories(ctx, admin, openExample(t, "categories.media-department.md"))
	if err != nil {
		t.Fatalf("media-department tree: %v", err)
	}
	if res.Created != 0 {
		var made [][]string
		for _, r := range res.Rows {
			if r.Created {
				made = append(made, r.Path)
			}
		}
		t.Errorf("media-department tree created %d node(s) the seed does not have: %v", res.Created, made)
	}

	theatre, err := db.ImportCategories(ctx, admin, openExample(t, "categories.theatre.md"))
	t.Cleanup(func() {
		// Leaves first: parent_id has no cascade.
		for i := len(theatre.Rows) - 1; i >= 0; i-- {
			if p := theatre.Rows[i].Path; theatre.Rows[i].Created {
				_, _ = db.Pool.Exec(ctx, `delete from categories where name = $1`, p[len(p)-1])
			}
		}
	})
	if err != nil || theatre.Created == 0 {
		t.Fatalf("theatre tree = %+v, %v", theatre, err)
	}

	t.Cleanup(func() { _, _ = db.Pool.Exec(ctx, `delete from assets where serial_number like 'EXAMPLE-%'`) })
	assets, err := db.ImportAssets(ctx, admin, openExample(t, "assets.csv"))
	if err != nil || assets.Failed != 0 || assets.Created+assets.Updated != 5 {
		t.Fatalf("assets.csv = %+v, %v; want five rows, none failed", assets, err)
	}

	t.Cleanup(func() { _, _ = db.Pool.Exec(ctx, `delete from profiles where student_number like '90000_'`) })
	prevUploads := db.UploadsDir
	db.UploadsDir = t.TempDir()
	t.Cleanup(func() { db.UploadsDir = prevUploads })
	roster, err := db.ImportRoster(ctx, admin, openExample(t, "roster.csv"), "")
	if err != nil || roster.Failed != 0 || roster.Created+roster.Updated != 5 {
		t.Fatalf("roster.csv = %+v, %v; want five rows, none failed", roster, err)
	}
}
