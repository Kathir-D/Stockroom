package stockroom

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

// The student-number format (TEMPLATE-TODO Phase B): the happy path end to end
// through the settings row, and the gate that stops a rule stranding accounts.
func TestStudentNumberFormatSetting(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	str := func(s string) *string { return &s }

	// Whatever happens, leave the package and the row on digits.
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `update app_settings set student_number_format = 'digits', student_number_pattern = null`)
		_ = SetStudentNumberFormat(FormatDigits, "")
	})

	if _, err := db.SaveSettings(ctx, admin, SettingsInput{StudentNumberFormat: str("alphanumeric")}); err != nil {
		t.Fatalf("save alphanumeric: %v", err)
	}
	number := fmt.Sprintf("ZZ%07d", rand.IntN(10_000_000))
	if got, err := NormalizeStudentNumber(number); err != nil || got != number {
		t.Fatalf("NormalizeStudentNumber(%q) under alphanumeric = %q, %v", number, got, err)
	}
	var id string
	if err := db.Pool.QueryRow(ctx, `
		insert into profiles (student_number, first_name, last_name, full_name)
		values ($1, 'Letter', 'Card', 'Letter Card') returning id`, number).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Pool.Exec(ctx, `delete from profiles where id = $1`, id) })

	// Back to digits would strand that account: refused, naming it, and the
	// running rule is untouched.
	_, err := db.SaveSettings(ctx, admin, SettingsInput{StudentNumberFormat: str("digits")})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "Letter Card") {
		t.Fatalf("switch to digits with a lettered account = %v, want ErrInvalid naming it", err)
	}
	if f, _ := StudentNumberRule(); f != FormatAlphanumeric {
		t.Errorf("a refused save changed the running rule to %q", f)
	}

	// A custom pattern that does not compile under RE2 is refused outright.
	if _, err := db.SaveSettings(ctx, admin, SettingsInput{
		StudentNumberFormat: str("custom"), StudentNumberPattern: str("(?=x)"),
	}); !errors.Is(err, ErrInvalid) {
		t.Errorf("lookahead pattern = %v, want ErrInvalid", err)
	}
}

// Only ASCII digits: Code 128 cannot carry anything else, and "１２３" must not
// be a different account from "123".
func TestNormalizeStudentNumberIsASCIIDigits(t *testing.T) {
	for _, in := range []string{"١٢٣", "１２３", "12a"} {
		if _, err := NormalizeStudentNumber(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("NormalizeStudentNumber(%q) = %v, want ErrInvalid", in, err)
		}
	}
	if got, err := NormalizeStudentNumber(" 012345 "); err != nil || got != "012345" {
		t.Errorf("NormalizeStudentNumber(012345) = %q, %v", got, err)
	}
}
