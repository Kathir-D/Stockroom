package stockroom

import "errors"

// Sentinel errors returned by the stockroom package. server/ maps these to
// HTTP status codes; callers should compare with errors.Is.
var (
	ErrNotFound       = errors.New("not found")
	ErrForbidden      = errors.New("forbidden")
	ErrUnauthorized   = errors.New("unauthorized")
	ErrConflict       = errors.New("conflict")
	ErrInvalid        = errors.New("invalid input")
	ErrOverdueBlocked = errors.New("custodian has overdue items")
	ErrPasswordNotSet = errors.New("password not set")
	ErrBadCredentials = errors.New("bad credentials")
	// ErrPasswordRequired is a card scan by an admin account. A scan proves
	// someone holds the number, and an admin's number opens the whole admin
	// panel, so an admin always finishes with a password (CLAUDE.md §7).
	// server/ answers 401 with password_required so the sign-in screen can
	// move straight to the password field with the number kept.
	ErrPasswordRequired = errors.New("admin accounts sign in with a password")
	// ErrTooManyAttempts is a login refused before any check runs, because
	// the same number has failed too often or the sign-in routes are being
	// hit faster than a person can (429).
	ErrTooManyAttempts = errors.New("too many attempts")
	// ErrNotConfigured is a setting the operator never filled in: UPLOADS_DIR
	// for a photo, BACKUP_DIR for a backup. Nobody's request is wrong and no
	// code is broken, so it is neither a 4xx nor a 500; server/ answers 503
	// and, unlike a 500, lets the message through, because the admin reading
	// it is the person who edits .env.
	ErrNotConfigured = errors.New("not configured")
)

// ErrFailsafeNotConfigured is returned by EnsureFailsafeAdmin when
// ADMIN_STUDENT_NUMBER / ADMIN_PASSWORD are unset. It reports a choice the
// operator made, not a failure, so the server logs it and starts anyway. It
// never reaches HTTP, which is why it sits outside the block above.
var ErrFailsafeNotConfigured = errors.New("failsafe admin not configured")
