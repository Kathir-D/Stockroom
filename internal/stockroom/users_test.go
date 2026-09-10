package stockroom

import (
	"context"
	"errors"
	"testing"
	"time"
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

	p, err := db.CreateUser(ctx, admin, UserInput{
		StudentNumber: " " + sn + " ", FirstName: " Ada ", LastName: "Lovelace",
		Email: str("  "), PhotoPath: nil, IsAdmin: false,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if *p.StudentNumber != sn || *p.FirstName != "Ada" || *p.FullName != "Ada Lovelace" {
		t.Errorf("created = %+v, want trimmed fields and a derived full_name", p)
	}
	if p.Email != nil {
		t.Errorf("email = %q, want null for a blank input", *p.Email)
	}
	if p.PasswordHash != nil {
		t.Error("a user created without a password has a hash")
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

func TestCreateUserAndConflicts(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	existing := insertTestProfile(t, db, false, "")
	sn := testStudentNumber(t, db)

	// A new account starts with no password and sets one at its first scan
	// login, the same shape a roster import leaves behind.
	p, err := db.CreateUser(ctx, admin, UserInput{StudentNumber: sn, FirstName: "Pat"})
	if err != nil {
		t.Fatal(err)
	}
	if p.PasswordHash != nil {
		t.Errorf("CreateUser stored a password hash: %v", *p.PasswordHash)
	}

	cases := map[string]struct {
		in   UserInput
		want error
	}{
		"duplicate student number": {UserInput{StudentNumber: *existing.StudentNumber, FirstName: "Dup"}, ErrConflict},
		"bad student number":       {UserInput{StudentNumber: "12x", FirstName: "Bad"}, ErrInvalid},
		"no name":                  {UserInput{StudentNumber: "912345601", FirstName: " ", LastName: ""}, ErrInvalid},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := db.CreateUser(ctx, admin, c.in); !errors.Is(err, c.want) {
				t.Errorf("CreateUser = %v, want %v", err, c.want)
			}
		})
	}
}

func TestUpdateUser(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	adminP := insertTestProfile(t, db, true, "admin-pw")
	admin := actorFor(adminP)
	p := insertTestProfile(t, db, false, "keep-this-password")
	newSN := testStudentNumber(t, db)

	got, err := db.UpdateUser(ctx, admin, p.ID, UserInput{
		StudentNumber: newSN, FirstName: "Renamed", LastName: "Person", Email: str("r@school.edu"),
		PhotoPath: str("profiles/x.jpg"), IsAdmin: true,
	})
	if err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	if *got.StudentNumber != newSN || *got.FirstName != "Renamed" || *got.Email != "r@school.edu" || !got.IsAdmin || *got.PhotoPath != "profiles/x.jpg" {
		t.Errorf("updated = %+v", got)
	}
	if err := CheckPassword(got.PasswordHash, "keep-this-password"); err != nil {
		t.Errorf("UpdateUser touched the password: %v", err)
	}

	if _, err := db.UpdateUser(ctx, admin, p.ID, UserInput{StudentNumber: *adminP.StudentNumber, FirstName: "Clash"}); !errors.Is(err, ErrConflict) {
		t.Errorf("update to a taken student number = %v, want ErrConflict", err)
	}
	if _, err := db.UpdateUser(ctx, admin, "00000000-0000-0000-0000-00000000dead", UserInput{StudentNumber: "912345603", FirstName: "Ghost"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("update missing = %v, want ErrNotFound", err)
	}
	// An admin cannot strip their own flag.
	if _, err := db.UpdateUser(ctx, admin, admin.ID, UserInput{StudentNumber: *adminP.StudentNumber, FirstName: "Me", IsAdmin: false}); !errors.Is(err, ErrConflict) {
		t.Errorf("self-demotion = %v, want ErrConflict", err)
	}
}

func TestDeleteUser(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	adminP := insertTestProfile(t, db, true, "admin-pw")
	admin := actorFor(adminP)

	// Plain account: deletable.
	plain := insertTestProfile(t, db, false, "")
	if err := db.DeleteUser(ctx, admin, plain.ID); err != nil {
		t.Errorf("DeleteUser(plain) = %v", err)
	}
	if _, err := db.profileByID(ctx, plain.ID); !errors.Is(err, ErrNotFound) {
		t.Error("deleted user still exists")
	}
	if err := db.DeleteUser(ctx, admin, plain.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete = %v, want ErrNotFound", err)
	}

	// Open custody: refused with a specific message.
	holder := insertTestProfile(t, db, false, "")
	asset := insertTestAsset(t, db, adminP)
	openCustody(t, db, asset, holder, adminP, time.Now().Add(24*time.Hour))
	err := db.DeleteUser(ctx, admin, holder.ID)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("DeleteUser(open custody) = %v, want ErrConflict", err)
	}

	// Returned everything, but the history still references them: Postgres
	// refuses, and that must also read as a conflict, not a 500.
	if _, err := db.Pool.Exec(ctx, `update custody_events set checked_in_at = now(), checked_in_by = $2 where custodian_id = $1`, holder.ID, adminP.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteUser(ctx, admin, holder.ID); !errors.Is(err, ErrConflict) {
		t.Errorf("DeleteUser(closed history) = %v, want ErrConflict", err)
	}

	if err := db.DeleteUser(ctx, admin, admin.ID); !errors.Is(err, ErrConflict) {
		t.Errorf("self delete = %v, want ErrConflict", err)
	}
	if err := db.DeleteUser(ctx, admin, "junk"); !errors.Is(err, ErrInvalid) {
		t.Errorf("DeleteUser(bad id) = %v, want ErrInvalid", err)
	}
}

// An admin reset or delete drops that user's sessions, and it does so in
// this package, so any caller gets the rule and not only the HTTP handlers.
func TestPasswordResetAndDeleteDropSessions(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	auth := NewAuth(db, time.Hour)
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))

	reset := insertTestProfile(t, db, false, "old password")
	sess, err := auth.Sessions.Create(reset.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetUserPassword(ctx, admin, reset.ID, "reset password"); err != nil {
		t.Fatal(err)
	}
	if _, ok := auth.Sessions.Get(sess.Token); ok {
		t.Error("password reset left the user signed in")
	}

	deleted := insertTestProfile(t, db, false, "")
	sess, err = auth.Sessions.Create(deleted.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteUser(ctx, admin, deleted.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := auth.Sessions.Get(sess.Token); ok {
		t.Error("delete left the user signed in")
	}
}

func TestSetUserPassword(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	p := insertTestProfile(t, db, false, "old password")

	if err := db.SetUserPassword(ctx, admin, p.ID, "reset password"); err != nil {
		t.Fatal(err)
	}
	got, _ := db.profileByID(ctx, p.ID)
	if err := CheckPassword(got.PasswordHash, "reset password"); err != nil {
		t.Errorf("new password does not match: %v", err)
	}
	if err := CheckPassword(got.PasswordHash, "old password"); !errors.Is(err, ErrBadCredentials) {
		t.Error("old password still works")
	}
	if err := db.SetUserPassword(ctx, admin, p.ID, "short"); !errors.Is(err, ErrInvalid) {
		t.Errorf("short password = %v, want ErrInvalid", err)
	}
	if err := db.SetUserPassword(ctx, admin, "00000000-0000-0000-0000-00000000dead", "long enough"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing user = %v, want ErrNotFound", err)
	}
}
