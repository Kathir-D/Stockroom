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
