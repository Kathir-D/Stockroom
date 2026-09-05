package stockroom

import (
	"errors"
	"fmt"
	"testing"
)

var sentinels = map[string]error{
	"ErrNotFound":       ErrNotFound,
	"ErrForbidden":      ErrForbidden,
	"ErrUnauthorized":   ErrUnauthorized,
	"ErrConflict":       ErrConflict,
	"ErrInvalid":        ErrInvalid,
	"ErrOverdueBlocked": ErrOverdueBlocked,
	"ErrPasswordNotSet": ErrPasswordNotSet,
	"ErrBadCredentials": ErrBadCredentials,
}

// The sentinels are compared with errors.Is, so no two of them may be equal --
// otherwise a "not found" would be served as, say, a 403.
func TestSentinelsAreDistinct(t *testing.T) {
	for aName, a := range sentinels {
		for bName, b := range sentinels {
			if aName == bName {
				continue
			}
			if errors.Is(a, b) {
				t.Errorf("errors.Is(%s, %s) = true; sentinels must be distinct", aName, bName)
			}
		}
	}
}

func TestSentinelsHaveMessages(t *testing.T) {
	for name, err := range sentinels {
		if err == nil {
			t.Errorf("%s is nil", name)
			continue
		}
		if err.Error() == "" {
			t.Errorf("%s has an empty message; it is surfaced to clients", name)
		}
	}
}

// Callers wrap sentinels with context before returning them; the wrapping must
// stay transparent to errors.Is, both for fmt.Errorf(%w) and errors.Join --
// decodeJSON in server/ relies on the errors.Join form.
func TestSentinelsSurviveWrapping(t *testing.T) {
	for name, sentinel := range sentinels {
		t.Run(name, func(t *testing.T) {
			wrapped := fmt.Errorf("checking out asset 7: %w", sentinel)
			if !errors.Is(wrapped, sentinel) {
				t.Errorf("errors.Is(fmt.Errorf(%%w), %s) = false", name)
			}
			joined := errors.Join(sentinel, errors.New("underlying detail"))
			if !errors.Is(joined, sentinel) {
				t.Errorf("errors.Is(errors.Join(...), %s) = false", name)
			}
		})
	}
}
