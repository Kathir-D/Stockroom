package stockroom

import (
	"context"
	"errors"
	"testing"
)

func str(s string) *string { return &s }

// Every user function is admin-only. One table pins that for all of them.
func TestUserFunctionsRequireAdmin(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	student := actorFor(insertTestProfile(t, db, false, "student-pw"))
	target := insertTestProfile(t, db, false, "")
	in := UserInput{StudentNumber: "912345600", FirstName: "X"}

	calls := map[string]func() error{
		"ListUsers":       func() error { _, err := db.ListUsers(ctx, student); return err },
		"GetUser":         func() error { _, err := db.GetUser(ctx, student, target.ID); return err },
		"CreateUser":      func() error { _, err := db.CreateUser(ctx, student, in); return err },
		"UpdateUser":      func() error { _, err := db.UpdateUser(ctx, student, target.ID, in); return err },
		"DeleteUser":      func() error { return db.DeleteUser(ctx, student, target.ID) },
		"SetUserPassword": func() error { return db.SetUserPassword(ctx, student, target.ID, "new password") },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s as non-admin = %v, want ErrForbidden", name, err)
		}
	}
}

func TestCreateGetListUser(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	sn := testStudentNumber(t, db)

	in := UserInput{
		StudentNumber: " " + sn + " ", FirstName: " Ada ", LastName: "Lovelace",
		Email: str("  "), IsAdmin: false,
	}
	if _, err := db.CreateUser(ctx, admin, in); !errors.Is(err, ErrInvalid) {
		t.Fatalf("CreateUser with no password = %v, want ErrInvalid", err)
	}
	in.Password = "short"
	if _, err := db.CreateUser(ctx, admin, in); !errors.Is(err, ErrInvalid) {
		t.Fatalf("CreateUser with a short password = %v, want ErrInvalid", err)
	}
	in.Password = "ada's password"
	p, err := db.CreateUser(ctx, admin, in)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if *p.StudentNumber != sn || *p.FirstName != "Ada" || *p.FullName != "Ada Lovelace" {
		t.Errorf("created = %+v, want trimmed fields and a derived full_name", p)
	}
	if p.Email != nil {
		t.Errorf("email = %q, want null for a blank input", *p.Email)
	}
	if err := CheckPassword(p.PasswordHash, "ada's password"); err != nil {
		t.Errorf("the password given at create does not check: %v", err)
	}
	if deref(p.PasswordSetBy) != "admin" {
		t.Errorf("password_set_by = %q, want admin", deref(p.PasswordSetBy))
	}

	got, err := db.GetUser(ctx, admin, p.ID)
	if err != nil || got.ID != p.ID {
		t.Errorf("GetUser = %+v, %v", got, err)
	}
	if _, err := db.GetUser(ctx, admin, "00000000-0000-0000-0000-00000000dead"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetUser(missing) = %v, want ErrNotFound", err)
	}
	if _, err := db.GetUser(ctx, admin, "not-a-uuid"); !errors.Is(err, ErrInvalid) {
		t.Errorf("GetUser(bad id) = %v, want ErrInvalid", err)
	}

	users, err := db.ListUsers(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, u := range users {
		if u.ID == p.ID {
			found = true
		}
	}
	if !found {
		t.Error("ListUsers does not include the new user")
	}
	if len(users) > 0 && !users[0].IsAdmin {
		t.Error("ListUsers should put admins first")
	}
}
