package stockroom

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

// MinPasswordLength is the shortest password accepted anywhere a password is
// set: first-login setup, admin resets, and the failsafe admin.
const MinPasswordLength = 8

// HashPassword returns a bcrypt hash for storing in profiles.password_hash.
// It enforces MinPasswordLength so every caller gets the same rule.
func HashPassword(password string) (string, error) {
	if len(password) < MinPasswordLength {
		return "", fmt.Errorf("%w: password must be at least %d characters", ErrInvalid, MinPasswordLength)
	}
	// bcrypt only reads the first 72 bytes; refuse longer input rather than
	// silently truncating it.
	if len(password) > 72 {
		return "", fmt.Errorf("%w: password must be at most 72 bytes", ErrInvalid)
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(h), nil
}

// CheckPassword compares a candidate against a stored hash. A nil hash means
// the account has never set a password (ErrPasswordNotSet); a mismatch is
// ErrBadCredentials.
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
	if len(s) > 32 {
		return "", fmt.Errorf("%w: student number is too long", ErrInvalid)
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return "", fmt.Errorf("%w: student number must be digits only", ErrInvalid)
		}
	}
	return s, nil
}
