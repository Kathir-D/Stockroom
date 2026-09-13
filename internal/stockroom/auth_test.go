package stockroom

import (
	"context"
	"errors"
	"testing"
)

// The roster-import case: scanning in with no password gives a limited
// session that can do nothing but set one, and setting one upgrades it.
func TestScanLoginWithoutPasswordIsLimitedUntilSet(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	p := insertTestProfile(t, db, true, "") // admin flag on purpose: must not count while limited

	res, err := db.LoginByScan(ctx, *p.StudentNumber)
	if err != nil {
		t.Fatalf("LoginByScan: %v", err)
	}
	if !res.NeedsPassword {
		t.Fatal("needs_password = false for an account with no hash")
	}
	if res.Profile.ID != p.ID {
		t.Errorf("profile = %s, want %s", res.Profile.ID, p.ID)
	}

	actor, err := db.Resolve(ctx, res.Token)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !actor.Limited {
		t.Fatal("actor is not limited")
	}
	if err := RequireAdmin(actor); !errors.Is(err, ErrForbidden) {
		t.Errorf("RequireAdmin(limited admin) = %v, want ErrForbidden", err)
	}
	if _, err := db.ListUsers(ctx, actor); !errors.Is(err, ErrForbidden) {
		t.Errorf("ListUsers with a limited session = %v, want ErrForbidden", err)
	}

	if err := db.SetInitialPassword(ctx, actor, "short"); !errors.Is(err, ErrInvalid) {
		t.Errorf("SetInitialPassword(short) = %v, want ErrInvalid", err)
	}
	if err := db.SetInitialPassword(ctx, actor, "my first password"); err != nil {
		t.Fatalf("SetInitialPassword: %v", err)
	}
	actor, err = db.Resolve(ctx, res.Token)
	if err != nil {
		t.Fatal(err)
	}
	if actor.Limited {
		t.Error("session still limited after setting a password")
	}
	if err := RequireAdmin(actor); err != nil {
		t.Errorf("RequireAdmin after upgrade = %v, want nil", err)
	}

	// Only valid once.
	if err := db.SetInitialPassword(ctx, actor, "another password"); !errors.Is(err, ErrConflict) {
		t.Errorf("second SetInitialPassword = %v, want ErrConflict", err)
	}

	// And the typed path now works.
	if _, err := db.LoginByPassword(ctx, *p.StudentNumber, "my first password"); err != nil {
		t.Errorf("LoginByPassword after set = %v, want nil", err)
	}
}

func TestLoginByPassword(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	withPw := insertTestProfile(t, db, false, "correct-password")
	noPw := insertTestProfile(t, db, false, "")
	unknown := testStudentNumber(t, db)

	cases := []struct {
		name string
		sn   string
		pw   string
		want error
	}{
		{"right password", *withPw.StudentNumber, "correct-password", nil},
		{"wrong password", *withPw.StudentNumber, "wrong-password", ErrBadCredentials},
		{"unknown number", unknown, "correct-password", ErrBadCredentials},
		{"no password set", *noPw.StudentNumber, "anything", ErrPasswordNotSet},
		{"bad number", "12ab", "x", ErrInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := db.LoginByPassword(ctx, c.sn, c.pw)
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			if c.want == nil && (res.Token == "" || res.NeedsPassword) {
				t.Errorf("res = %+v, want a full session", res)
			}
		})
	}
}

func TestRequireAdmin(t *testing.T) {
	cases := map[string]struct {
		a    Actor
		want error
	}{
		"admin":         {Actor{IsAdmin: true}, nil},
		"non-admin":     {Actor{IsAdmin: false}, ErrForbidden},
		"limited admin": {Actor{IsAdmin: true, Limited: true}, ErrForbidden},
		"zero actor":    {Actor{}, ErrForbidden},
	}
	for name, c := range cases {
		if err := RequireAdmin(c.a); !errors.Is(err, c.want) {
			t.Errorf("%s: RequireAdmin = %v, want %v", name, err, c.want)
		}
	}
}
