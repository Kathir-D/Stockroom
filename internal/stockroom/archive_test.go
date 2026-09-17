package stockroom

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// Optional archive encryption (docs/design/backup.md §C.5). Off by default,
// because a lost passphrase is an unrecoverable backup and for this deployment
// losing the backup is the likelier and worse outcome -- but what leaves the
// machine is a credential file, so a site that needs the stronger guarantee
// has to be able to have it.

func TestArchiveEncryptionRoundTrip(t *testing.T) {
	plain := []byte("tables/assets.csv and everything else")
	const passphrase = "correct horse battery staple"

	sealed, err := encryptArchive(plain, passphrase)
	if err != nil {
		t.Fatalf("encryptArchive: %v", err)
	}
	if bytes.Contains(sealed, plain) {
		t.Fatal("the encrypted archive still contains its plaintext")
	}
	if !isEncryptedArchive(sealed) {
		t.Fatal("an encrypted archive is not recognised as one, so a restore would never ask for the passphrase")
	}

	back, err := decryptArchive(sealed, passphrase)
	if err != nil {
		t.Fatalf("decryptArchive: %v", err)
	}
	if !bytes.Equal(back, plain) {
		t.Errorf("decrypted to %q, want %q", back, plain)
	}

	// Two archives under one passphrase must not share a key, so the salt is
	// fresh each time and the bytes differ.
	again, err := encryptArchive(plain, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(sealed, again) {
		t.Error("two encryptions of the same content are byte-identical; the salt is not fresh")
	}
}

func TestArchiveEncryptionRefusesAWrongPassphrase(t *testing.T) {
	sealed, err := encryptArchive([]byte("secret"), "the right one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decryptArchive(sealed, "the wrong one"); !errors.Is(err, ErrInvalid) {
		t.Errorf("decrypting with the wrong passphrase = %v, want ErrInvalid", err)
	}
	// A tampered archive fails the same way, and deliberately so: the GCM tag
	// cannot tell the two apart, and the first thing anybody does wrong is
	// mistype.
	sealed[len(sealed)-1] ^= 0xff
	if _, err := decryptArchive(sealed, "the right one"); !errors.Is(err, ErrInvalid) {
		t.Errorf("decrypting a tampered archive = %v, want ErrInvalid", err)
	}
	if _, err := decryptArchive(append([]byte{}, encryptedMagic...), "the right one"); !errors.Is(err, ErrInvalid) {
		t.Errorf("decrypting a truncated archive = %v, want ErrInvalid", err)
	}
}

// An encrypted archive with no passphrase has to say what is wrong, not fail
// as "this is not a backup".
func TestReadArchiveAsksForThePassphrase(t *testing.T) {
	sealed, err := encryptArchive([]byte("not really a zip"), "pass")
	if err != nil {
		t.Fatal(err)
	}
	_, err = readArchive(sealed, "")
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "passphrase") {
		t.Errorf("reading an encrypted archive with no passphrase = %v, want ErrInvalid asking for it", err)
	}
}
