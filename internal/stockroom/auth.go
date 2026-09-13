package stockroom

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Actor is the signed-in account behind a request, resolved from the session
// token by DB.Resolve and passed into every function that needs to know
// who is calling. Handlers never read the profile table for this themselves.
type Actor struct {
	ID            string
	StudentNumber string
	IsAdmin       bool
	// Limited is copied from the session: a scan login with no password set.
	// See Session.Limited.
	Limited bool
	Token   string
}

// RequireAdmin is the single gate in front of every admin-only operation
// (CLAUDE.md §7). A limited session is never an admin, whatever the flag on
// the profile says, because the account has not proven a password yet.
func RequireAdmin(a Actor) error {
	if a.Limited || !a.IsAdmin {
		return ErrForbidden
	}
	return nil
}

// RequireFullSession refuses a limited session: a scan login by an account
// that has never set a password has proven possession of a card and nothing
// else, so it may only finish setting that password (CLAUDE.md §7). The
// router enforces the same rule route by route; this is the copy that makes
// it hold for every caller of the package, not only the HTTP layer.
func RequireFullSession(a Actor) error {
	if a.Limited {
		return fmt.Errorf("%w: password not set", ErrForbidden)
	}
	return nil
}

// LoginResult is what both login paths return. NeedsPassword is true for a
// scan login by an account with no password: the token is a limited session
// whose only permitted call is SetInitialPassword.
type LoginResult struct {
	Token         string  `json:"token"`
	NeedsPassword bool    `json:"needs_password"`
	HasOverdue    bool    `json:"has_overdue"`
	Profile       Profile `json:"profile"`
}

// LoginByScan signs in from an ID-card scan: student number only, no
// password (CLAUDE.md §7). The frontend decides that a keystroke burst was a
// scan (§10); the server trusts that decision. An unknown number is
// ErrNotFound so the UI can say "not registered" rather than "wrong
// password".
func (db *DB) LoginByScan(ctx context.Context, studentNumber string) (LoginResult, error) {
	sn, err := NormalizeStudentNumber(studentNumber)
	if err != nil {
		return LoginResult{}, err
	}
	p, err := db.profileByStudentNumber(ctx, sn)
	if err != nil {
		return LoginResult{}, err
	}
	// An empty hash counts as no password, the same way CheckPassword and
	// SetInitialPassword read it, so a blank column can still be set from
	// the first scan login instead of locking the account out.
	limited := p.PasswordHash == nil || *p.PasswordHash == ""
	return db.openSession(ctx, p, limited)
}

// LoginByPassword signs in from a typed student number and password. A wrong
// password and an unknown number both come back as ErrBadCredentials so the
// response does not reveal which numbers exist. An account that has never
// set a password gets ErrPasswordNotSet, because the fix (scan the card) is
// different from "try again".
func (db *DB) LoginByPassword(ctx context.Context, studentNumber, password string) (LoginResult, error) {
	sn, err := NormalizeStudentNumber(studentNumber)
	if err != nil {
		return LoginResult{}, err
	}
	p, err := db.profileByStudentNumber(ctx, sn)
	if errors.Is(err, ErrNotFound) {
		return LoginResult{}, ErrBadCredentials
	}
	if err != nil {
		return LoginResult{}, err
	}
	if err := CheckPassword(p.PasswordHash, password); err != nil {
		return LoginResult{}, err
	}
	return db.openSession(ctx, p, false)
}

func (db *DB) openSession(ctx context.Context, p Profile, limited bool) (LoginResult, error) {
	sess, err := db.Sessions.Create(p.ID, limited)
	if err != nil {
		return LoginResult{}, err
	}
	overdue, err := db.hasOverdue(ctx, p.ID)
	if err != nil {
		db.Sessions.Delete(sess.Token)
		return LoginResult{}, err
	}
	return LoginResult{Token: sess.Token, NeedsPassword: limited, HasOverdue: overdue, Profile: p}, nil
}

// SetInitialPassword stores the first password for the actor's account and
// upgrades the session to a full one. It is only valid while the account has
// no password, null or blank, matching what LoginByScan calls a limited
// session (ErrConflict otherwise); admins reset existing passwords with
// SetUserPassword.
func (db *DB) SetInitialPassword(ctx context.Context, actor Actor, password string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	tag, err := db.Pool.Exec(ctx,
		`update profiles set password_hash = $2
		 where id = $1 and (password_hash is null or password_hash = '')`,
		actor.ID, hash)
	if err != nil {
		return fmt.Errorf("set initial password: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: password already set", ErrConflict)
	}
	db.Sessions.Upgrade(actor.Token)
	return nil
}

// Logout ends the actor's session.
func (db *DB) Logout(actor Actor) {
	db.Sessions.Delete(actor.Token)
}

// Resolve turns a session token into an Actor. The profile is reloaded from
// the database on every call, so an admin flag change or a deleted account
// takes effect on the next request, not at the next login. A missing,
// expired, or orphaned token is ErrUnauthorized.
func (db *DB) Resolve(ctx context.Context, token string) (Actor, error) {
	if token == "" {
		return Actor{}, ErrUnauthorized
	}
	sess, ok := db.Sessions.Get(token)
	if !ok {
		return Actor{}, ErrUnauthorized
	}
	p, err := db.profileByID(ctx, sess.ProfileID)
	if errors.Is(err, ErrNotFound) {
		db.Sessions.Delete(token)
		return Actor{}, ErrUnauthorized
	}
	if err != nil {
		return Actor{}, err
	}
	sn := ""
	if p.StudentNumber != nil {
		sn = *p.StudentNumber
	}
	return Actor{ID: p.ID, StudentNumber: sn, IsAdmin: p.IsAdmin, Limited: sess.Limited, Token: token}, nil
}

// MeResult is the signed-in account plus the overdue flag that drives the
// warning shown at sign-in (CLAUDE.md §7).
type MeResult struct {
	Profile    Profile `json:"profile"`
	HasOverdue bool    `json:"has_overdue"`
}

// Me returns the actor's own profile. Any session, including a limited one,
// may call it, because the set-password screen shows who is signing in.
func (db *DB) Me(ctx context.Context, actor Actor) (MeResult, error) {
	p, err := db.profileByID(ctx, actor.ID)
	if err != nil {
		return MeResult{}, err
	}
	overdue, err := db.hasOverdue(ctx, actor.ID)
	if err != nil {
		return MeResult{}, err
	}
	return MeResult{Profile: p, HasOverdue: overdue}, nil
}

// hasOverdue reports whether profileID holds anything past its due date.
func (db *DB) hasOverdue(ctx context.Context, profileID string) (bool, error) {
	var overdue bool
	err := db.Pool.QueryRow(ctx,
		`select exists (select 1 from overdue_custody where custodian_id = $1)`, profileID).Scan(&overdue)
	if err != nil {
		return false, fmt.Errorf("check overdue: %w", err)
	}
	return overdue, nil
}

// profileColumns is the select list every profile query uses, in the order
// scanProfile expects.
const profileColumns = `id, email, password_hash, full_name, role, student_number,
	first_name, last_name, photo_path, is_admin, created_at`

func scanProfile(row pgx.Row) (Profile, error) {
	var p Profile
	err := row.Scan(&p.ID, &p.Email, &p.PasswordHash, &p.FullName, &p.Role, &p.StudentNumber,
		&p.FirstName, &p.LastName, &p.PhotoPath, &p.IsAdmin, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	if err != nil {
		return Profile{}, fmt.Errorf("scan profile: %w", err)
	}
	p.PhotoURL = photoURL(p.PhotoPath)
	return p, nil
}

func (db *DB) profileByID(ctx context.Context, id string) (Profile, error) {
	return scanProfile(db.Pool.QueryRow(ctx,
		`select `+profileColumns+` from profiles where id = $1`, id))
}

func (db *DB) profileByStudentNumber(ctx context.Context, sn string) (Profile, error) {
	return scanProfile(db.Pool.QueryRow(ctx,
		`select `+profileColumns+` from profiles where student_number = $1`, sn))
}
