package stockroom

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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

	// trustedCLI marks a process that already has shell access to this
	// machine and the database URL -- cmd/restore, and the export's own
	// reads. It is unexported, so an Actor literal naming it does not compile
	// outside this package and no HTTP handler, JSON body or session lookup
	// can set it. Resolve never sets it, so no request can reach this state
	// whatever it sends.
	//
	// It is not a permission: the actor is already an admin. It exists so a
	// restore can record in the log whether it came from the command line or
	// from a named admin, which is the first question anybody asks afterwards.
	trustedCLI bool
}

// LocalCLIActor is the actor for a process that already has shell access to
// the machine and the database URL -- strictly more access than any account
// grants, so there is nothing left for an authorization check to protect.
//
// cmd/restore needs it because the case it exists for is a database with no
// accounts in it (docs/design/backup.md §C.1): there is nobody to sign in as,
// and the restore is what creates the accounts. Making the CLI *satisfy*
// RequireAdmin rather than skip it is what keeps there being one restore
// implementation -- dropping the gate would open the HTTP path, and a second
// CLI-only restore would be emergency code first run during an emergency.
//
// This function is exported and any package may call it. What no other package
// can do is produce a trustedCLI Actor any other way: the field is unexported,
// so this line does not compile outside internal/stockroom --
//
//	stockroom.Actor{ID: "x", IsAdmin: true, trustedCLI: true} // unknown field
//
// -- and that is a compile-time fact, which is why it is recorded here as a
// comment rather than as a test pretending to check it at runtime.
func LocalCLIActor() Actor {
	return Actor{ID: "cli", IsAdmin: true, trustedCLI: true}
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
	// BackupWarning rides here beside HasOverdue because it is the same shape
	// of fact -- something the person signing in should be told before they
	// start -- and because sign-in is the only moment everybody passes
	// through. See backup_status.go.
	BackupWarning *BackupWarning `json:"backup_warning"`
	// CameraWarning is set for an admin when the closet camera is on and
	// needs attention (camera_watch.go). Never for a student.
	CameraWarning *string `json:"camera_warning"`
}

// LoginByScan signs in from an ID-card scan: student number only, no
// password (CLAUDE.md §7). The frontend decides that a keystroke burst was a
// scan (§10); the server trusts that decision. An unknown number is
// ErrNotFound so the UI can say "not registered" rather than "wrong
// password".
func (db *DB) LoginByScan(ctx context.Context, studentNumber string) (LoginResult, error) {
	// Every card scan is logged, including the ones that sign nobody in
	// (ROADMAP §2.4). The code read is kept whole: the log is admin-only,
	// and a number that almost matched somebody is exactly what an admin
	// tracing a problem needs (decided 2026-09-26).
	ctx = withScan(ctx, strings.TrimSpace(studentNumber))
	sn, err := NormalizeStudentNumber(studentNumber)
	if err != nil {
		db.logFailedSignIn(ctx, "", "scan", "not a valid student number")
		return LoginResult{}, err
	}
	p, err := db.profileByStudentNumber(ctx, sn)
	if errors.Is(err, ErrNotFound) {
		db.logFailedSignIn(ctx, "", "scan", "no account has that number")
		return LoginResult{}, err
	}
	if err != nil {
		return LoginResult{}, err
	}
	// An empty hash counts as no password, the same way CheckPassword and
	// SetInitialPassword read it, so a blank column can still be set from
	// the first scan login instead of locking the account out.
	limited := p.PasswordHash == nil || *p.PasswordHash == ""
	return db.openSession(ctx, p, limited, "scan")
}

// LoginByPassword signs in from a typed student number and password. A wrong
// password and an unknown number both come back as ErrBadCredentials so the
// response does not reveal which numbers exist. An account that has never
// set a password gets ErrPasswordNotSet, because the fix (scan the card) is
// different from "try again".
func (db *DB) LoginByPassword(ctx context.Context, studentNumber, password string) (LoginResult, error) {
	sn, err := NormalizeStudentNumber(studentNumber)
	if err != nil {
		db.logFailedSignIn(ctx, strings.TrimSpace(studentNumber), "password", "not a valid student number")
		return LoginResult{}, err
	}
	p, err := db.profileByStudentNumber(ctx, sn)
	if errors.Is(err, ErrNotFound) {
		db.logFailedSignIn(ctx, sn, "password", "no account has that number")
		return LoginResult{}, ErrBadCredentials
	}
	if err != nil {
		return LoginResult{}, err
	}
	if err := CheckPassword(p.PasswordHash, password); err != nil {
		reason := "wrong password"
		if errors.Is(err, ErrPasswordNotSet) {
			reason = "the account has no password yet"
		}
		db.logFailedSignInFor(ctx, p, sn, "password", reason)
		return LoginResult{}, err
	}
	return db.openSession(ctx, p, false, "password")
}

// logFailedSignIn records a refused sign-in. The number typed or scanned is
// the code, in full (see LoginByScan). Best effort: the sign-in is refused
// either way, and a refusal must not turn into a 500 because the log could
// not be written.
func (db *DB) logFailedSignIn(ctx context.Context, code, method, reason string) {
	e := LogEntry{
		Category: LogAccount, Action: "signin_failed",
		Summary: fmt.Sprintf("Failed sign-in by %s: %s", method, reason),
		Details: map[string]any{"method": method, "reason": reason},
	}
	if code != "" {
		e.Details["code"] = code
	}
	db.logBestEffort(ctx, e)
}

// logFailedSignInFor is a refusal for a known account: a wrong password.
func (db *DB) logFailedSignInFor(ctx context.Context, p Profile, code, method, reason string) {
	db.logBestEffort(ctx, LogEntry{
		Category: LogAccount, Action: "signin_failed", ActorID: p.ID,
		Summary: fmt.Sprintf("Failed sign-in by %s for %s: %s", method, profileLabel(p), reason),
		Details: map[string]any{"method": method, "reason": reason, "code": code},
	})
}

func profileLabel(p Profile) string {
	return displayName(p.FirstName, p.LastName, p.FullName, p.StudentNumber)
}

func (db *DB) openSession(ctx context.Context, p Profile, limited bool, method string) (LoginResult, error) {
	sess, err := db.Sessions.Create(p.ID, limited)
	if err != nil {
		return LoginResult{}, err
	}
	// The sign-in is not complete until it is in the log: a sign-in that
	// left no trace is the one thing the log exists to rule out.
	summary := fmt.Sprintf("%s signed in by %s", profileLabel(p), method)
	if limited {
		summary += " (no password set yet)"
	}
	if err := db.logNow(ctx, LogEntry{
		Category: LogAccount, Action: "signin_" + method, ActorID: p.ID, Summary: summary,
		Details: map[string]any{"limited": limited},
	}); err != nil {
		db.Sessions.Delete(sess.Token)
		return LoginResult{}, err
	}
	overdue, err := db.hasOverdue(ctx, p.ID)
	if err != nil {
		db.Sessions.Delete(sess.Token)
		return LoginResult{}, err
	}
	out := LoginResult{Token: sess.Token, NeedsPassword: limited, HasOverdue: overdue, Profile: p}
	// A limited session is mid-way through setting a password and can act on
	// nothing; a warning there is noise in front of a form.
	if !limited {
		out.BackupWarning = db.backupWarningFor(ctx, p.IsAdmin)
		out.CameraWarning = db.cameraWarningFor(ctx, p.IsAdmin)
	}
	return out, nil
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
	err = db.withLoggedTx(ctx, actor.ID, "set initial password", func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`update profiles set password_hash = $2
			 where id = $1 and (password_hash is null or password_hash = '')`,
			actor.ID, hash)
		if err != nil {
			return fmt.Errorf("set initial password: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: password already set", ErrConflict)
		}
		return writeLog(ctx, tx, LogEntry{
			Category: LogAccount, Action: "password_set", ActorID: actor.ID,
			Summary: "Set a password for the first time",
		})
	})
	if err != nil {
		return err
	}
	db.Sessions.Upgrade(actor.Token)
	return nil
}

// Logout ends the actor's session.
func (db *DB) Logout(ctx context.Context, actor Actor) {
	db.Sessions.Delete(actor.Token)
	db.logBestEffort(ctx, LogEntry{
		Category: LogAccount, Action: "signout", ActorID: actor.ID, Summary: "Signed out",
	})
}

// logIdleTimeout is the SessionStore's expiry hook: a session that timed out
// is a sign-out nobody pressed, and the log says when it happened -- the
// moment the idle period ran out, not the moment the server noticed.
func (db *DB) logIdleTimeout(s Session, at time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db.logBestEffort(ctx, LogEntry{
		Category: LogAccount, Action: "idle_timeout", ActorID: s.ProfileID, At: at,
		Summary: "Signed out automatically after being idle",
	})
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
	Profile       Profile        `json:"profile"`
	HasOverdue    bool           `json:"has_overdue"`
	BackupWarning *BackupWarning `json:"backup_warning"`
	CameraWarning *string        `json:"camera_warning"`
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
	out := MeResult{Profile: p, HasOverdue: overdue}
	if !actor.Limited {
		out.BackupWarning = db.backupWarningFor(ctx, p.IsAdmin)
		out.CameraWarning = db.cameraWarningFor(ctx, p.IsAdmin)
	}
	return out, nil
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
