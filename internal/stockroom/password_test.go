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
