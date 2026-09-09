package stockroom

import (
	"context"
	"errors"
	"testing"
	"time"
)

func newTestAuth(t *testing.T) (*Auth, *DB) {
	t.Helper()
	db := requireTestDB(t)
	return NewAuth(db, time.Hour), db
}

func TestLoginByScanUnknownNumber(t *testing.T) {
	auth, db := newTestAuth(t)
	sn := testStudentNumber(t, db)
	if _, err := auth.LoginByScan(context.Background(), sn); !errors.Is(err, ErrNotFound) {
		t.Errorf("LoginByScan(unknown) = %v, want ErrNotFound", err)
	}
	if _, err := auth.LoginByScan(context.Background(), "abc"); !errors.Is(err, ErrInvalid) {
		t.Errorf("LoginByScan(letters) = %v, want ErrInvalid", err)
	}
}

// The roster-import case: scanning in with no password gives a limited
// session that can do nothing but set one, and setting one upgrades it.
func TestScanLoginWithoutPasswordIsLimitedUntilSet(t *testing.T) {
	auth, db := newTestAuth(t)
	ctx := context.Background()
	p := insertTestProfile(t, db, true, "") // admin flag on purpose: must not count while limited

	res, err := auth.LoginByScan(ctx, *p.StudentNumber)
	if err != nil {
		t.Fatalf("LoginByScan: %v", err)
	}
	if !res.NeedsPassword {
		t.Fatal("needs_password = false for an account with no hash")
	}
	if res.Profile.ID != p.ID {
		t.Errorf("profile = %s, want %s", res.Profile.ID, p.ID)
	}

	actor, err := auth.Resolve(ctx, res.Token)
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

	if err := auth.SetInitialPassword(ctx, actor, "short"); !errors.Is(err, ErrInvalid) {
		t.Errorf("SetInitialPassword(short) = %v, want ErrInvalid", err)
	}
	if err := auth.SetInitialPassword(ctx, actor, "my first password"); err != nil {
		t.Fatalf("SetInitialPassword: %v", err)
	}
	actor, err = auth.Resolve(ctx, res.Token)
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
	if err := auth.SetInitialPassword(ctx, actor, "another password"); !errors.Is(err, ErrConflict) {
		t.Errorf("second SetInitialPassword = %v, want ErrConflict", err)
	}

	// And the typed path now works.
	if _, err := auth.LoginByPassword(ctx, *p.StudentNumber, "my first password"); err != nil {
		t.Errorf("LoginByPassword after set = %v, want nil", err)
	}
}

func TestScanLoginWithPasswordIsFull(t *testing.T) {
	auth, db := newTestAuth(t)
	ctx := context.Background()
	p := insertTestProfile(t, db, false, "already-set-pw")

	res, err := auth.LoginByScan(ctx, " "+*p.StudentNumber+"\n") // scanner trailing newline
	if err != nil {
		t.Fatalf("LoginByScan: %v", err)
	}
	if res.NeedsPassword {
		t.Error("needs_password = true for an account with a hash")
	}
	actor, err := auth.Resolve(ctx, res.Token)
	if err != nil || actor.Limited || actor.ID != p.ID || actor.IsAdmin {
		t.Errorf("Resolve = %+v, %v; want a full non-admin actor for %s", actor, err, p.ID)
	}
}

func TestLoginByPassword(t *testing.T) {
	auth, db := newTestAuth(t)
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
			res, err := auth.LoginByPassword(ctx, c.sn, c.pw)
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			if c.want == nil && (res.Token == "" || res.NeedsPassword) {
				t.Errorf("res = %+v, want a full session", res)
			}
		})
	}
}

func TestLogoutAndResolve(t *testing.T) {
	auth, db := newTestAuth(t)
	ctx := context.Background()
	p := insertTestProfile(t, db, false, "pw-for-logout")

	res, err := auth.LoginByPassword(ctx, *p.StudentNumber, "pw-for-logout")
	if err != nil {
		t.Fatal(err)
	}
	actor, err := auth.Resolve(ctx, res.Token)
	if err != nil {
		t.Fatal(err)
	}
	auth.Logout(actor)
	if _, err := auth.Resolve(ctx, res.Token); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("Resolve after logout = %v, want ErrUnauthorized", err)
	}
	if _, err := auth.Resolve(ctx, ""); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("Resolve(\"\") = %v, want ErrUnauthorized", err)
	}
}

// Resolve reloads the profile each time, so a deleted account or a changed
// admin flag applies to the very next request.
func TestResolveReflectsProfileChanges(t *testing.T) {
	auth, db := newTestAuth(t)
	ctx := context.Background()
	p := insertTestProfile(t, db, false, "pw-changes")
	res, _ := auth.LoginByPassword(ctx, *p.StudentNumber, "pw-changes")

	if _, err := db.Pool.Exec(ctx, `update profiles set is_admin = true where id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	actor, err := auth.Resolve(ctx, res.Token)
	if err != nil || !actor.IsAdmin {
		t.Errorf("Resolve after promotion = %+v, %v; want IsAdmin", actor, err)
	}

	if _, err := db.Pool.Exec(ctx, `delete from profiles where id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Resolve(ctx, res.Token); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("Resolve after delete = %v, want ErrUnauthorized", err)
	}
}

func TestMeReportsOverdue(t *testing.T) {
	auth, db := newTestAuth(t)
	ctx := context.Background()
	p := insertTestProfile(t, db, false, "pw-overdue")
	actor := actorFor(p)

	me, err := auth.Me(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	if me.HasOverdue || me.Profile.ID != p.ID {
		t.Errorf("Me = %+v, want own profile with has_overdue=false", me)
	}

	// Something due yesterday.
	asset := insertTestAsset(t, db, p)
	openCustody(t, db, asset, p, p, time.Now().Add(-24*time.Hour))
	me, err = auth.Me(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	if !me.HasOverdue {
		t.Error("has_overdue = false with an item past due")
	}
	// Login surfaces the same flag for the sign-in warning.
	res, err := auth.LoginByPassword(ctx, *p.StudentNumber, "pw-overdue")
	if err != nil || !res.HasOverdue {
		t.Errorf("login HasOverdue = %v (%v), want true", res.HasOverdue, err)
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
