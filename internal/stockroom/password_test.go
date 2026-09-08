package stockroom

import (
	"errors"
	"strings"
	"testing"
)

func TestHashAndCheckPassword(t *testing.T) {
	hash, err := HashPassword("correct horse")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$2a$") {
		t.Errorf("hash = %q, want a bcrypt hash", hash)
	}
	if hash == "correct horse" {
		t.Fatal("the hash is the plaintext")
	}

	if err := CheckPassword(&hash, "correct horse"); err != nil {
		t.Errorf("CheckPassword(right password) = %v, want nil", err)
	}
	if err := CheckPassword(&hash, "wrong horse"); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("CheckPassword(wrong password) = %v, want ErrBadCredentials", err)
	}
}

// Two hashes of the same password differ (bcrypt salts), so equality of the
// stored column can never stand in for a password check.
func TestHashPasswordIsSalted(t *testing.T) {
	a, _ := HashPassword("same password")
	b, _ := HashPassword("same password")
	if a == b {
		t.Error("two hashes of the same password are identical")
	}
}

func TestHashPasswordRejectsBadInput(t *testing.T) {
	for name, pw := range map[string]string{
		"empty":     "",
		"too short": strings.Repeat("x", MinPasswordLength-1),
		"too long":  strings.Repeat("x", 73),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := HashPassword(pw); !errors.Is(err, ErrInvalid) {
				t.Errorf("HashPassword(%q) = %v, want ErrInvalid", pw, err)
			}
		})
	}
	if _, err := HashPassword(strings.Repeat("x", MinPasswordLength)); err != nil {
		t.Errorf("a password of exactly the minimum length is rejected: %v", err)
	}
}

// A roster-imported user has no hash. That is a distinct state from a wrong
// password, because the UI prompts them to set one instead of saying "wrong".
func TestCheckPasswordWithoutHash(t *testing.T) {
	empty := ""
	for name, h := range map[string]*string{"nil": nil, "empty": &empty} {
		t.Run(name, func(t *testing.T) {
			if err := CheckPassword(h, "anything"); !errors.Is(err, ErrPasswordNotSet) {
				t.Errorf("CheckPassword(%s hash) = %v, want ErrPasswordNotSet", name, err)
			}
		})
	}
}

// A corrupt hash must not read as "wrong password".
func TestCheckPasswordMalformedHash(t *testing.T) {
	bad := "not-a-bcrypt-hash"
	err := CheckPassword(&bad, "anything")
	if err == nil || errors.Is(err, ErrBadCredentials) || errors.Is(err, ErrPasswordNotSet) {
		t.Errorf("CheckPassword(malformed hash) = %v, want a distinct error", err)
	}
}

func TestNormalizeStudentNumber(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"123456", "123456", true},
		{"  123456\n", "123456", true},
		{"000123", "000123", true}, // leading zeros survive
		{"", "", false},
		{"   ", "", false},
		{"12a456", "", false},
		{"123-456", "", false},
		{strings.Repeat("1", 33), "", false},
	}
	for _, c := range cases {
		got, err := NormalizeStudentNumber(c.in)
		if c.ok && (err != nil || got != c.want) {
			t.Errorf("NormalizeStudentNumber(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
		if !c.ok && !errors.Is(err, ErrInvalid) {
			t.Errorf("NormalizeStudentNumber(%q) = %q, %v; want ErrInvalid", c.in, got, err)
		}
	}
}
