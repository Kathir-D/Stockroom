package stockroom

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The roster-import case: scanning in with no password gives a limited
// session that can do nothing but set one, and setting one upgrades it.
func TestScanLoginWithoutPasswordIsLimitedUntilSet(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	p := insertTestProfile(t, db, false, "")

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
	if _, err := db.ListAssets(ctx, actor, AssetFilter{}); !errors.Is(err, ErrForbidden) {
		t.Errorf("ListAssets with a limited session = %v, want ErrForbidden", err)
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
	if _, err := db.ListAssets(ctx, actor, AssetFilter{}); err != nil {
		t.Errorf("ListAssets after upgrade = %v, want nil", err)
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

// An admin's card never opens a session (CLAUDE.md §7): with a password it
// asks for the password, and without one it is refused outright, because the
// first scan would otherwise choose the admin's password.
func TestAdminScanNeedsPassword(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	withPw := insertTestProfile(t, db, true, "admin-password")
	noPw := insertTestProfile(t, db, true, "")

	before := db.Sessions.Len()
	if _, err := db.LoginByScan(ctx, *withPw.StudentNumber); !errors.Is(err, ErrPasswordRequired) {
		t.Errorf("scan by admin = %v, want ErrPasswordRequired", err)
	}
	if _, err := db.LoginByScan(ctx, *noPw.StudentNumber); !errors.Is(err, ErrPasswordNotSet) {
		t.Errorf("scan by admin with no password = %v, want ErrPasswordNotSet", err)
	}
	if after := db.Sessions.Len(); after != before {
		t.Errorf("sessions went from %d to %d: an admin scan opened one", before, after)
	}
	if _, err := db.LoginByPassword(ctx, *withPw.StudentNumber, "admin-password"); err != nil {
		t.Errorf("typed admin login = %v, want nil", err)
	}
}

// Five wrong passwords lock the number for a while, and the lock holds even
// against the right password until it runs out.
func TestPasswordLockout(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	p := insertTestProfile(t, db, false, "right-password")
	clock := time.Now()
	db.logins().now = func() time.Time { return clock }

	for i := range loginFailLimit {
		if _, err := db.LoginByPassword(ctx, *p.StudentNumber, "wrong-password"); !errors.Is(err, ErrBadCredentials) {
			t.Fatalf("attempt %d = %v, want ErrBadCredentials", i+1, err)
		}
	}
	if _, err := db.LoginByPassword(ctx, *p.StudentNumber, "right-password"); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("right password while locked = %v, want ErrTooManyAttempts", err)
	}
	// A card scan is not a password guess and still works.
	if _, err := db.LoginByScan(ctx, *p.StudentNumber); err != nil {
		t.Errorf("scan while locked = %v, want nil", err)
	}
	clock = clock.Add(loginLockout + time.Second)
	if _, err := db.LoginByPassword(ctx, *p.StudentNumber, "right-password"); err != nil {
		t.Errorf("right password after the lockout = %v, want nil", err)
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

// Guesses sent at once are counted before bcrypt answers any of them, so a
// burst gets no more tries than one at a time.
func TestLoginGuardCountsAttemptsInFlight(t *testing.T) {
	g := newLoginGuard()
	for i := range loginFailLimit {
		if err := g.check("123456"); err != nil {
			t.Fatalf("reservation %d = %v, want nil", i+1, err)
		}
	}
	if err := g.check("123456"); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("a sixth guess in flight = %v, want ErrTooManyAttempts", err)
	}
	if err := g.check("654321"); err != nil {
		t.Errorf("another number = %v, want nil", err)
	}
	// Released attempts free their places; a success clears the number.
	g.release("123456")
	if err := g.check("123456"); err != nil {
		t.Errorf("after a release = %v, want nil", err)
	}
	g.succeed("123456")
	if g.pending["123456"] != loginFailLimit-1 {
		t.Errorf("pending after one success = %d, want %d", g.pending["123456"], loginFailLimit-1)
	}
}
