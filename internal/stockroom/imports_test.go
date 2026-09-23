package stockroom

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

// The bulk ways in (CLAUDE.md §13, Phase B): one happy path and one gate per
// module, the house rule (CLAUDE.md §13, 2026-09-13).

func TestImportCategoriesIsIdempotentAndOrdered(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin, _ := adminAndSerial(t, db)
	n := rand.IntN(1_000_000_000)
	typ, cat := fmt.Sprintf("ZZ Import Type %09d", n), fmt.Sprintf("ZZ Import Cat %09d", n)
	b, a := fmt.Sprintf("ZZ Model B %09d", n), fmt.Sprintf("ZZ Model A %09d", n)
	// Models deliberately listed B before A: sort_order must follow the file.
	body := typ + "\n  " + cat + "\n    " + b + "\n    " + a + "\n"
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `delete from categories where name in ($1, $2)`, a, b)
		_, _ = db.Pool.Exec(ctx, `delete from categories where name = $1`, cat)
		_, _ = db.Pool.Exec(ctx, `delete from categories where name = $1`, typ)
	})

	res, err := db.ImportCategories(ctx, admin, strings.NewReader(body))
	if err != nil {
		t.Fatalf("ImportCategories: %v", err)
	}
	if res.Format != CategoryImportText || res.Created != 4 {
		t.Fatalf("first import = %+v, want text format and 4 paths created", res)
	}
	var orderB, orderA int
	if err := db.Pool.QueryRow(ctx, `select
		(select sort_order from categories where name = $1),
		(select sort_order from categories where name = $2)`, b, a).Scan(&orderB, &orderA); err != nil {
		t.Fatal(err)
	}
	if orderB >= orderA {
		t.Errorf("sort_order B=%d A=%d; want document order, B first", orderB, orderA)
	}

	// Again, as CSV and with different case: nothing new.
	csv := "type,category,model\n" + strings.ToLower(typ) + "," + cat + "," + a + "\n"
	res, err = db.ImportCategories(ctx, admin, strings.NewReader(csv))
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if res.Format != CategoryImportCSV || res.Created != 0 || res.Existing != 1 {
		t.Errorf("re-import = %+v, want csv, 0 created, 1 existing", res)
	}
}

// A name used elsewhere in the tree refuses the whole file, and the report
// names that line and only that line -- a later, valid line must not be
// counted as failed because the first one aborted the transaction.
func TestImportCategoriesNamesTheCollision(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin, _ := adminAndSerial(t, db)
	n := rand.IntN(1_000_000_000)
	taken := fmt.Sprintf("ZZ Taken %09d", n)
	insertTestCategory(t, db, taken, nil)
	typ := fmt.Sprintf("ZZ Collide Type %09d", n)
	fine := fmt.Sprintf("ZZ Fine %09d", n)

	body := typ + "\n  " + taken + "\n  " + fine + "\n"
	res, err := db.ImportCategories(ctx, admin, strings.NewReader(body))
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), taken) {
		t.Fatalf("err = %v, want ErrInvalid naming %q", err, taken)
	}
	if res.Failed != 1 {
		t.Errorf("Failed = %d, want 1 (only the colliding line): %+v", res.Failed, res.Rows)
	}
	var count int
	_ = db.Pool.QueryRow(ctx, `select count(*) from categories where name in ($1, $2)`, typ, fine).Scan(&count)
	if count != 0 {
		t.Errorf("%d rows landed from a refused import; want none", count)
	}
}

func TestImportAssetsUpsertsAndSaysWhatItReplaced(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin, serial := adminAndSerial(t, db)
	t.Cleanup(func() { _, _ = db.Pool.Exec(ctx, `delete from assets where serial_number = $1`, serial) })

	res, err := db.ImportAssets(ctx, admin, strings.NewReader("\ufeffserial_number,name\n"+serial+",First name\n\n"))
	if err != nil || res.Created != 1 {
		t.Fatalf("first import = %+v, %v; want 1 created", res, err)
	}
	// The second row repeats the first's serial: refused, naming line 2,
	// rather than updating the item the same file created a line earlier.
	// The blank line is skipped by the reader, so the repeat sits on file
	// line 4: the report has to say 4, not the record count 3.
	res, err = db.ImportAssets(ctx, admin, strings.NewReader("serial_number,name\n"+serial+",Second name\n\n"+serial+",Third name\n"))
	if err != nil || res.Updated != 1 || res.Failed != 1 {
		t.Fatalf("second import = %+v, %v; want 1 updated, 1 failed", res, err)
	}
	if e := res.Rows[1].Error; !strings.Contains(e, "line 2") || res.Rows[1].Line != 4 {
		t.Errorf("repeat row = line %d, %q; want line 4, naming line 2", res.Rows[1].Line, e)
	}
	if note := res.Rows[0].Note; !strings.Contains(note, "First name") {
		t.Errorf("update note = %q, want it to name the item it replaced", note)
	}

	// A malformed line stops the run but keeps the report: the rows before
	// it have committed, and the admin needs to be told so.
	res, err = db.ImportAssets(ctx, admin, strings.NewReader("serial_number,name\n"+serial+",Fourth name\n"+serial+"X,Dell 27\" monitor\n"))
	if err != nil || res.Updated != 1 || res.Failed != 1 || res.Rows[1].Line != 3 {
		t.Fatalf("import with a bare quote = %+v, %v; want 1 updated, 1 failed on line 3", res, err)
	}

	// A student cannot import.
	student := actorFor(insertTestProfile(t, db, false, "student-pw"))
	if _, err := db.ImportAssets(ctx, student, strings.NewReader("serial_number,name\nX,Y\n")); !errors.Is(err, ErrForbidden) {
		t.Errorf("student import err = %v, want ErrForbidden", err)
	}
}

func TestBulkAddContinuesTheSeries(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin, _ := adminAndSerial(t, db)
	prefix := fmt.Sprintf("ZZB%06d", rand.IntN(1_000_000))
	t.Cleanup(func() { _, _ = db.Pool.Exec(ctx, `delete from assets where serial_number like $1`, prefix+"-%") })

	in := BulkAddInput{Name: "  Test battery ", Prefix: prefix, Count: 3}
	added, err := db.BulkAddAssets(ctx, admin, in)
	if err != nil {
		t.Fatalf("first BulkAddAssets: %v", err)
	}
	if added[0].Name != "Test battery" {
		t.Errorf("stored name = %q, want it trimmed as the preview showed it", added[0].Name)
	}
	prev, err := db.BulkPreviewSerials(ctx, admin, BulkAddInput{Name: "Test battery", Prefix: prefix, Count: 2})
	if err != nil {
		t.Fatalf("BulkPreviewSerials: %v", err)
	}
	want := []string{prefix + "-004", prefix + "-005"}
	if strings.Join(prev.Serials, ",") != strings.Join(want, ",") || len(prev.Existing) != 0 {
		t.Errorf("preview = %+v, want %v with nothing existing", prev, want)
	}

	if _, err := db.BulkPreviewSerials(ctx, admin, BulkAddInput{Name: "x", Prefix: "has space", Count: 1}); !errors.Is(err, ErrInvalid) {
		t.Errorf("prefix with a space: err = %v, want ErrInvalid", err)
	}
}

// An Excel CSV starts with a BOM, and it must still be sniffed as a CSV.
func TestParseCategoryFileStripsBOM(t *testing.T) {
	format, paths, err := parseCategoryFile("\ufefftype,category,model\nLenses,Zooms,Canon 24-70\n")
	if err != nil || format != CategoryImportCSV || len(paths) != 1 || len(paths[0]) != 3 {
		t.Fatalf("parseCategoryFile = %v, %v, %v; want one csv path of 3", format, paths, err)
	}
}

// The text parser, without a database: tabs are one level each, and a
// Markdown file's prose is not a category.
func TestParseCategoryText(t *testing.T) {
	cases := []struct {
		name, body string
		want       []string
	}{
		{"tabs", "Lenses\n\tZooms\n\t\tCanon 70-200\n", []string{"Lenses", "Lenses/Zooms", "Lenses/Zooms/Canon 70-200"}},
		{"spaces", "Lenses\n  Zooms\n", []string{"Lenses", "Lenses/Zooms"}},
		{"markdown with prose", "# Title\n\nSome words about the file.\n\n## Lights\n\n### Studio\n- Aputure 120d\n", []string{"Lights", "Lights/Studio", "Lights/Studio/Aputure 120d"}},
	}
	for _, c := range cases {
		paths, err := parseCategoryText(c.body)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var got []string
		for _, p := range paths {
			got = append(got, strings.Join(p, "/"))
		}
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// An upload over the cap is refused whole, not cut off at the limit and
// imported as if the file ended there.
func TestReadCappedRefusesOversize(t *testing.T) {
	if _, err := readCapped(strings.NewReader("12345"), 5); err != nil {
		t.Errorf("exactly the cap = %v, want nil", err)
	}
	if _, err := readCapped(strings.NewReader("123456"), 5); !errors.Is(err, ErrInvalid) {
		t.Errorf("one byte over = %v, want ErrInvalid", err)
	}
}
