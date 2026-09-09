package stockroom

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

// Password and student-number bounds, enforced by every path that sets a
// password or accepts a student number.
const (
	// MinPasswordLength and MaxPasswordLength are the 8-to-72 range CLAUDE.md
	// §7 sets for every place a password is chosen: first-login setup, admin
	// resets, and the failsafe admin. The upper bound is bcrypt's, not a
	// policy: it only reads the first 72 bytes.
	MinPasswordLength = 8
	MaxPasswordLength = 72

	// MaxStudentNumberLength is a sanity bound, not the real format. The ID
	// cards in use encode six digits (CLAUDE.md §1), but the column is text so
	// leading zeros survive and a reissued longer number would still import.
	// Anything past this length is a mis-scan or a pasted line of junk.
	MaxStudentNumberLength = 32
)

// HashPassword returns a bcrypt hash for storing in profiles.password_hash.
// It enforces MinPasswordLength so every caller gets the same rule.
func HashPassword(password string) (string, error) {
	if len(password) < MinPasswordLength {
		return "", fmt.Errorf("%w: password must be at least %d characters", ErrInvalid, MinPasswordLength)
	}
	// Refuse anything past bcrypt's limit rather than silently truncating it.
	if len(password) > MaxPasswordLength {
		return "", fmt.Errorf("%w: password must be at most %d bytes", ErrInvalid, MaxPasswordLength)
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(h), nil
}

// CheckPassword compares a candidate against a stored hash. A nil hash means
// the account has never set a password (ErrPasswordNotSet); a mismatch is
// ErrBadCredentials. Until LoginByPassword lands in Phase 2 its only caller is
// failsafe_test.go, which needs it to prove EnsureFailsafeAdmin stored and
// rotated the right hash.
func CheckPassword(hash *string, password string) error {
	if hash == nil || *hash == "" {
		return ErrPasswordNotSet
	}
	err := bcrypt.CompareHashAndPassword([]byte(*hash), []byte(password))
	switch {
	case err == nil:
		return nil
	case errors.Is(err, bcrypt.ErrMismatchedHashAndPassword):
		return ErrBadCredentials
	default:
		// A malformed hash in the database is a data problem, not a wrong
		// password; surface it so it gets fixed rather than locking the user out
		// with a misleading message.
		return fmt.Errorf("check password: %w", err)
	}
}

// NormalizeStudentNumber trims whitespace and checks the value is all digits,
// which is what the ID-card barcode encodes (CLAUDE.md §1). The number is kept
// as text so leading zeros survive.
func NormalizeStudentNumber(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("%w: student number is required", ErrInvalid)
	}
	if len(s) > MaxStudentNumberLength {
		return "", fmt.Errorf("%w: student number is at most %d digits", ErrInvalid, MaxStudentNumberLength)
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return "", fmt.Errorf("%w: student number must be digits only", ErrInvalid)
		}
	}
	return s, nil
}
