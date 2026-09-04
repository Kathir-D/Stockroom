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
