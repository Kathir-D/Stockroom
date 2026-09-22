package stockroom

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"unicode"
)

// What a student number is allowed to look like (TEMPLATE-TODO Phase B).
//
// This was a constant -- digits only -- and for a school whose IDs are
// `AB12345` that made Stockroom unusable rather than merely inconvenient:
// scan-to-sign-in is the headline feature (CLAUDE.md §1.1) and it is keyed on
// this. Now it is a setting, defaulting to digits so the existing install is
// bit-for-bit unchanged.
//
// **Held in a package variable, set at boot and on save**, rather than threaded
// through every caller. That is a deliberate copy of `hashCost` in
// password.go, which solves the identical problem the identical way: a value
// that is fixed for the life of the process, read by a pure validation
// function that a dozen call sites reach without a DB handle. Making
// NormalizeStudentNumber a method on DB would have touched the roster import,
// both logins, user CRUD and the failsafe admin to carry a value none of them
// has an opinion about.

// StudentNumberFormat is which rule is in force.
type StudentNumberFormat string

const (
	// FormatDigits is the default and is exactly the historical behaviour.
	FormatDigits StudentNumberFormat = "digits"
	// FormatAlphanumeric additionally allows ASCII letters. Case is preserved
	// rather than folded: the number is compared with what a scanner reads off
	// a card, and a card printed `AB12345` scans as `AB12345`.
	FormatAlphanumeric StudentNumberFormat = "alphanumeric"
	// FormatCustom uses an admin-supplied regular expression.
	FormatCustom StudentNumberFormat = "custom"
)

// studentNumberRule is the active format. Guarded by a mutex because the
// settings screen can change it while requests are in flight, and a torn read
// of a regexp pointer is a crash rather than a wrong answer.
var studentNumberRule = struct {
	sync.RWMutex
	format  StudentNumberFormat
	pattern string
	re      *regexp.Regexp
}{format: FormatDigits}

// SetStudentNumberFormat installs the rule. Called once at start-up from the
// settings row, and again whenever an admin saves the settings screen.
//
// A pattern that does not compile is refused rather than installed, because
// the failure mode of a broken rule is that **nobody can sign in** and the
// only way back is the failsafe admin -- whose number has to satisfy the same
// rule. That is close enough to locking everybody out of the building that it
// is worth an explicit error at the moment of saving.
func SetStudentNumberFormat(format StudentNumberFormat, pattern string) error {
	re, pattern, err := compileStudentNumberFormat(format, pattern)
	if err != nil {
		return err
	}

	studentNumberRule.Lock()
	defer studentNumberRule.Unlock()
	studentNumberRule.format = format
	studentNumberRule.pattern = pattern
	studentNumberRule.re = re
	return nil
}

// ValidateStudentNumberFormat checks a rule without installing it.
//
// Separate from SetStudentNumberFormat because SaveSettings has to know the
// rule is good *before* it writes the row, and must not leave the running
// process using a rule whose transaction then failed to commit. Validating and
// installing in one call made a failed write silently change how sign-in
// behaves until the next restart.
func ValidateStudentNumberFormat(format StudentNumberFormat, pattern string) error {
	_, _, err := compileStudentNumberFormat(format, pattern)
	return err
}

func compileStudentNumberFormat(format StudentNumberFormat, pattern string) (*regexp.Regexp, string, error) {
	var re *regexp.Regexp

	switch format {
	case FormatDigits, FormatAlphanumeric:
		// No pattern needed; any supplied one is ignored rather than an error,
		// so switching away from custom and back does not lose what was typed.
	case FormatCustom:
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			return nil, "", fmt.Errorf("%w: a custom student-number format needs a pattern", ErrInvalid)
		}
		// Anchored by the server, not by the admin. An unanchored `[0-9]{6}`
		// matches *inside* `garbage123456garbage`, so a mis-scan would be
		// accepted as a valid number and would create an account nobody can
		// sign into. Anchoring here means a pattern cannot be wrong in that
		// particular way however it is written.
		anchored := "^(?:" + pattern + ")$"
		compiled, err := regexp.Compile(anchored)
		if err != nil {
			return nil, "", fmt.Errorf("%w: that pattern is not a valid regular expression: %v", ErrInvalid, err)
		}
		re = compiled
	default:
		return nil, "", fmt.Errorf("%w: student number format must be digits, alphanumeric or custom (got %q)", ErrInvalid, format)
	}
	return re, pattern, nil
}

// StudentNumberRule reports the active format, for the settings screen and for
// the sign-in screen's input filter.
func StudentNumberRule() (StudentNumberFormat, string) {
	studentNumberRule.RLock()
	defer studentNumberRule.RUnlock()
	return studentNumberRule.format, studentNumberRule.pattern
}

// StudentNumberFilterPattern is what the sign-in field uses to decide which
// characters it will accept as they are typed.
//
// It is served to the unauthenticated sign-in screen because that screen has
// to filter keystrokes before anybody has a session, and because the filter and
// the validator disagreeing is the exact bug this whole file exists to fix:
// §13 (2026-09-15) records that the scan-vs-typed comparison runs the keystroke
// buffer through the *field's* filter, so a filter stricter than the validator
// makes every scan look like a mismatch and demands a password from somebody
// who has not got one.
//
// A character class, not the full rule: the field filters per keystroke and
// cannot evaluate a whole-string pattern half-typed.
func StudentNumberFilterPattern() string {
	format, pattern := StudentNumberRule()
	switch format {
	case FormatAlphanumeric:
		return "[^A-Za-z0-9]"
	case FormatCustom:
		// A custom pattern could allow anything, and guessing a character
		// class out of an arbitrary regexp is not something to attempt. The
		// field filters nothing and the server has the final say -- which is
		// the safe direction: a permissive filter shows the person what they
		// typed and lets the validator explain, where a strict one silently
		// eats characters.
		_ = pattern
		return ""
	default:
		return "[^0-9]"
	}
}

// NormalizeStudentNumber trims and validates against the active format.
//
// The length bound applies whatever the format: it is a mis-scan guard, not a
// format (CLAUDE.md §13, 2026-09-08), and a scanner that reads a whole line of
// junk should be refused before it becomes an account.
func NormalizeStudentNumber(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("%w: student number is required", ErrInvalid)
	}
	if len(s) > MaxStudentNumberLength {
		return "", fmt.Errorf("%w: student number is at most %d characters", ErrInvalid, MaxStudentNumberLength)
	}

	format, pattern := StudentNumberRule()
	switch format {
	case FormatAlphanumeric:
		for _, r := range s {
			if !isASCIILetterOrDigit(r) {
				return "", fmt.Errorf("%w: student number must be letters and numbers only", ErrInvalid)
			}
		}
	case FormatCustom:
		studentNumberRule.RLock()
		re := studentNumberRule.re
		studentNumberRule.RUnlock()
		if re == nil {
			// Unreachable via SetStudentNumberFormat, which refuses to install
			// a custom format with no pattern. Falling back to digits rather
			// than accepting anything, because the two ways to be wrong here
			// are "nobody can sign in" and "anything signs in", and the first
			// is recoverable.
			return normalizeDigits(s)
		}
		if !re.MatchString(s) {
			return "", fmt.Errorf("%w: student number does not match this school's format (%s)", ErrInvalid, pattern)
		}
	default:
		return normalizeDigits(s)
	}
	return s, nil
}

func normalizeDigits(s string) (string, error) {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return "", fmt.Errorf("%w: student number must be digits only", ErrInvalid)
		}
	}
	return s, nil
}

func isASCIILetterOrDigit(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// studentNumberMatcher reports whether a stored number satisfies a rule that
// is not installed yet. The same tests NormalizeStudentNumber runs, minus the
// trimming and the length bound every stored number has already passed.
func studentNumberMatcher(format StudentNumberFormat, pattern string) (func(string) bool, error) {
	re, _, err := compileStudentNumberFormat(format, pattern)
	if err != nil {
		return nil, err
	}
	return func(s string) bool {
		switch format {
		case FormatAlphanumeric:
			for _, r := range s {
				if !isASCIILetterOrDigit(r) {
					return false
				}
			}
			return true
		case FormatCustom:
			return re.MatchString(s)
		default:
			_, err := normalizeDigits(s)
			return err == nil
		}
	}, nil
}

// accountsRefusedBy lists the accounts a new rule would lock out, first few
// only, so a format change cannot quietly strand them. Switching "letters and
// numbers" back to "digits" at a school with `AB12345` IDs would otherwise
// leave every one of those students -- and quite possibly the admin pressing
// Save -- unable to sign in, with nothing on any screen saying why.
func accountsRefusedBy(ctx context.Context, q querier, format StudentNumberFormat, pattern string) (count int, examples []string, err error) {
	matches, err := studentNumberMatcher(format, pattern)
	if err != nil {
		return 0, nil, err
	}
	rows, err := q.Query(ctx, `
		select student_number, coalesce(nullif(full_name, ''), student_number)
		  from profiles where student_number is not null order by full_name`)
	if err != nil {
		return 0, nil, mapPgError("check student numbers", err)
	}
	defer rows.Close()
	for rows.Next() {
		var number, name string
		if err := rows.Scan(&number, &name); err != nil {
			return 0, nil, mapPgError("check student numbers", err)
		}
		if !matches(number) {
			count++
			if len(examples) < 3 {
				examples = append(examples, name)
			}
		}
	}
	return count, examples, rows.Err()
}
