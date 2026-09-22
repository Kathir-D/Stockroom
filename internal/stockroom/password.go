package stockroom

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// Password and student-number bounds, enforced by every path that sets a
// password or accepts a student number. What a student number may *contain*
// is a setting rather than a constant -- see student_number.go.
const (
	// MinPasswordLength and MaxPasswordLength are the 8-to-72 range CLAUDE.md
	// §7 sets for every place a password is chosen: first-login setup, admin
	// resets, and the failsafe admin. The minimum counts characters, so an
	// accented or non-Latin password is measured the way the person typing it
	// would count it. The upper bound is bcrypt's, not a policy: it only reads
	// the first 72 bytes.
	MinPasswordLength = 8
	MaxPasswordLength = 72

	// MaxStudentNumberLength is a sanity bound, not the real format. The ID
	// cards in use encode six digits (CLAUDE.md §1), but the column is text so
	// leading zeros survive and a reissued longer number would still import.
	// Anything past this length is a mis-scan or a pasted line of junk.
	MaxStudentNumberLength = 32

	// DefaultPasswordHashCost is the bcrypt work factor every released binary
	// hashes at. It is pinned by hashcost_test.go so it cannot be weakened by
	// accident.
	DefaultPasswordHashCost = bcrypt.DefaultCost
)

// hashCost is the work factor HashPassword applies. Verification never reads
// it: bcrypt stores the cost inside the hash, so rows written at any cost keep
// working.
var hashCost = DefaultPasswordHashCost

// SetPasswordHashCost sets the bcrypt work factor and returns the previous
// value. It exists for the test suites, which lower it to bcrypt.MinCost from
// TestMain: at the production cost a single hash takes tens of milliseconds,
// which is the right price for a login and the wrong one for the several
// hundred fixtures the suites create, especially under -race.
//
// Production never calls this. A cost outside bcrypt's supported range is
// ignored, so a bad value cannot silently produce a weak hash.
func SetPasswordHashCost(cost int) int {
	prev := hashCost
	if cost >= bcrypt.MinCost && cost <= bcrypt.MaxCost {
		hashCost = cost
	}
	return prev
}

// HashPassword returns a bcrypt hash for storing in profiles.password_hash.
// It enforces MinPasswordLength so every caller gets the same rule.
func HashPassword(password string) (string, error) {
	if utf8.RuneCountInString(password) < MinPasswordLength {
		return "", fmt.Errorf("%w: password must be at least %d characters", ErrInvalid, MinPasswordLength)
	}
	// Refuse anything past bcrypt's limit rather than silently truncating it.
	if len(password) > MaxPasswordLength {
		return "", fmt.Errorf("%w: password must be at most %d bytes", ErrInvalid, MaxPasswordLength)
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), hashCost)
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
