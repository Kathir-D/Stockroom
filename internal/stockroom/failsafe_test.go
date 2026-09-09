package stockroom

import (
	"context"
	"errors"
	"testing"
)

func TestEnsureFailsafeAdminCreatesAndUpdates(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	sn := testStudentNumber(t, db)

	if err := db.EnsureFailsafeAdmin(ctx, sn, "first-password"); err != nil {
		t.Fatalf("first EnsureFailsafeAdmin: %v", err)
	}

	var isAdmin bool
	var hash *string
	var first *string
	row := db.Pool.QueryRow(ctx, `select is_admin, password_hash, first_name from profiles where student_number = $1`, sn)
	if err := row.Scan(&isAdmin, &hash, &first); err != nil {
		t.Fatalf("the failsafe profile was not created: %v", err)
	}
	if !isAdmin {
		t.Error("is_admin = false, want true")
	}
	if err := CheckPassword(hash, "first-password"); err != nil {
		t.Errorf("the stored hash does not match the password: %v", err)
	}

	// A second run with a new password (the operator edited .env) rotates the
	// hash and keeps the row. A demoted admin is promoted back.
	if _, err := db.Pool.Exec(ctx, `update profiles set is_admin = false, first_name = 'Renamed' where student_number = $1`, sn); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureFailsafeAdmin(ctx, sn, "second-password"); err != nil {
		t.Fatalf("second EnsureFailsafeAdmin: %v", err)
	}
	row = db.Pool.QueryRow(ctx, `select is_admin, password_hash, first_name from profiles where student_number = $1`, sn)
	if err := row.Scan(&isAdmin, &hash, &first); err != nil {
		t.Fatal(err)
	}
	if !isAdmin {
		t.Error("is_admin was not forced back to true")
	}
	if err := CheckPassword(hash, "second-password"); err != nil {
		t.Errorf("the hash was not rotated: %v", err)
	}
	if err := CheckPassword(hash, "first-password"); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("the old password still works: %v", err)
	}
	if first == nil || *first != "Renamed" {
		t.Errorf("first_name = %v, want the operator's edit (Renamed) to survive", first)
	}

	var n int
	if err := db.Pool.QueryRow(ctx, `select count(*) from profiles where student_number = $1`, sn).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("profiles with the failsafe number = %d, want exactly 1", n)
	}
}

func TestEnsureFailsafeAdminRejectsBadConfig(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()

	for name, c := range map[string]struct{ sn, pw string }{
		"letters":        {"admin", "a fine password"},
		"separator":      {"912-345", "a fine password"},
		"short password": {"912345", "short"},
		"long password":  {"912345", strings.Repeat("x", MaxPasswordLength+1)},
	} {
		t.Run(name, func(t *testing.T) {
			if err := db.EnsureFailsafeAdmin(ctx, c.sn, c.pw); !errors.Is(err, ErrInvalid) {
				t.Errorf("EnsureFailsafeAdmin(%q, %q) = %v, want ErrInvalid", c.sn, c.pw, err)
			}
		})
	}
}

// A blank number or password is not a bad config, it is no config: the
// operator has not set up a failsafe admin. The server logs that and starts
// anyway, so the distinction has to come back as its own error.
func TestEnsureFailsafeAdminWithoutConfig(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()

	for name, c := range map[string]struct{ sn, pw string }{
		"no number":    {"", "a fine password"},
		"blank number": {"   ", "a fine password"},
		"no password":  {"912345", ""},
		"neither":      {"", ""},
	} {
		t.Run(name, func(t *testing.T) {
			if err := db.EnsureFailsafeAdmin(ctx, c.sn, c.pw); !errors.Is(err, ErrFailsafeNotConfigured) {
				t.Errorf("EnsureFailsafeAdmin(%q, %q) = %v, want ErrFailsafeNotConfigured", c.sn, c.pw, err)
			}
		})
	}

	// Nothing was written on the way to that answer.
	var n int
	if err := db.Pool.QueryRow(ctx, `select count(*) from profiles where student_number = '912345'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("an unconfigured failsafe created %d profile(s), want 0", n)
	}
}
