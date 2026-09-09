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
)

// ErrFailsafeNotConfigured is returned by EnsureFailsafeAdmin when
// ADMIN_STUDENT_NUMBER / ADMIN_PASSWORD are unset. It reports a choice the
// operator made, not a failure, so the server logs it and starts anyway. It
// never reaches HTTP, which is why it sits outside the block above.
var ErrFailsafeNotConfigured = errors.New("failsafe admin not configured")
