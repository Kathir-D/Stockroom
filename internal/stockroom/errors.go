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
