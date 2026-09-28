package stockroom

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

func TestParseNameLine(t *testing.T) {
	cases := []struct {
		in, first, last, number, err string
	}{
		{"Jane Doe", "Jane", "Doe", "", ""},
		{"Mary Ann Lee", "Mary Ann", "Lee", "", ""},
		{"Cher", "Cher", "", "", ""},
		{"Doe, Jane", "Jane", "Doe", "", ""},
		{"Doe, Jane, 123456", "Jane", "Doe", "123456", ""},
		{"Jane Doe, 123456", "Jane", "Doe", "123456", ""},
		{"Jane\tDoe\t123456", "Jane", "Doe", "123456", ""},
		{"  Jane   Doe  ", "Jane", "Doe", "", ""},
		{"123456", "", "", "123456", "no name"},
		{"Jane, 1, 2", "Jane", "", "2", "two student numbers on one line"},
		{"a\tb\tc", "", "", "", "more than two name columns"},
	}
	for _, c := range cases {
		got := parseNameLine(c.in)
		if got.FirstName != c.first || got.LastName != c.last || got.StudentNumber != c.number || !strings.HasPrefix(got.Error, c.err) {
			t.Errorf("parseNameLine(%q) = %+v", c.in, got)
		}
	}
}

func TestNumberRange(t *testing.T) {
	taken := map[string]string{"900002": "already belongs to someone"}
	next, err := numberRange("900001", taken)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for range 3 {
		n, err := next()
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, n)
	}
	if fmt.Sprint(got) != "[900001 900003 900004]" {
		t.Errorf("range = %v, want it to step round 900002", got)
	}
	if _, err := numberRange("ABC", taken); !errors.Is(err, ErrInvalid) {
		t.Errorf("a first number with no digits = %v, want ErrInvalid", err)
	}
	// Under the digits rule, a prefix doesn't fit.
	if _, err := numberRange("G0001", taken); !errors.Is(err, ErrInvalid) {
		t.Errorf("G0001 under the digits rule = %v, want ErrInvalid", err)
	}
	// Width is kept.
	next, _ = numberRange("0098", map[string]string{})
	a, _ := next()
	b, _ := next()
	c, _ := next()
	if a != "0098" || b != "0099" || c != "0100" {
		t.Errorf("0098.. = %s %s %s", a, b, c)
	}
}

// Preview writes nothing, a line's own number is kept, the rest count from
// the range round numbers already taken, and Add creates them all.
func TestAddNames(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	existing := insertTestProfile(t, db, false, "")

	base := 700_000_000 + rand.IntN(90_000_000)
	own := fmt.Sprint(base + 5)
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `delete from profiles where student_number between $1 and $2`,
			fmt.Sprint(base), fmt.Sprint(base+10))
	})
	in := AddNamesInput{
		Names:       "Jane Doe\n\nLee, Mary Ann, " + own + "\nCher\n",
		FirstNumber: fmt.Sprint(base),
	}

	preview, err := db.PreviewAddNames(ctx, admin, in)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Failed != 0 || len(preview.Rows) != 3 {
		t.Fatalf("preview = %+v", preview)
	}
	want := []AddNamesRow{
		{Line: 1, FirstName: "Jane", LastName: "Doe", StudentNumber: fmt.Sprint(base), Assigned: true},
		{Line: 3, FirstName: "Mary Ann", LastName: "Lee", StudentNumber: own},
		{Line: 4, FirstName: "Cher", StudentNumber: fmt.Sprint(base + 1), Assigned: true},
	}
	for i, w := range want {
		if preview.Rows[i] != w {
			t.Errorf("row %d = %+v, want %+v", i, preview.Rows[i], w)
		}
	}
	var n int
	_ = db.Pool.QueryRow(ctx, `select count(*) from profiles where student_number = $1`, fmt.Sprint(base)).Scan(&n)
	if n != 0 {
		t.Fatal("the preview wrote an account")
	}

	// A number already in use is an error on its line, and Add refuses the
	// whole list while there is one.
	bad := in
	bad.Names += "Someone Else, " + *existing.StudentNumber + "\n"
	preview, err = db.PreviewAddNames(ctx, admin, bad)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Failed != 1 || !strings.Contains(preview.Rows[3].Error, "already belongs to Test User") {
		t.Fatalf("taken number: %+v", preview.Rows)
	}
	if _, err := db.AddNames(ctx, admin, bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("add with a bad line = %v, want ErrInvalid", err)
	}

	added, err := db.AddNames(ctx, admin, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range added.Rows {
		if row.ID == "" {
			t.Errorf("row %d has no id", row.Line)
		}
	}
	_ = db.Pool.QueryRow(ctx, `select count(*) from profiles where student_number in ($1, $2, $3) and password_hash is null`,
		fmt.Sprint(base), own, fmt.Sprint(base+1)).Scan(&n)
	if n != 3 {
		t.Errorf("created %d accounts, want 3 with no password", n)
	}

	// Without a range, a line with no number is an error.
	preview, _ = db.PreviewAddNames(ctx, admin, AddNamesInput{Names: "No Number"})
	if preview.Failed != 1 {
		t.Errorf("no range, no number: %+v", preview.Rows)
	}

	student := actorFor(existing)
	if _, err := db.PreviewAddNames(ctx, student, in); !errors.Is(err, ErrForbidden) {
		t.Errorf("preview as a student = %v, want ErrForbidden", err)
	}
}
