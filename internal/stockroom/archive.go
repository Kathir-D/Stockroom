package stockroom

import (
	"archive/zip"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/scrypt"
)

// The restorable archive: what goes in it, how its integrity is proved, and
// the optional encryption wrapped around it (docs/design/backup.md §D.1, §C.5).
//
// The export and the restore are two halves of one format, so they live beside
// each other here rather than at opposite ends of two files. A change to what
// the manifest carries has to be made in one place for both to agree.

// restoreDoc is shipped inside every archive so the instructions travel with
// the thing they describe. A backup found on a drive two years from now is no
// use if the only copy of "how do I load this" was in the repository that is
// also gone.
//
//go:embed RESTORE.md
var restoreDoc []byte

// Archive layout. These are zip paths, not filesystem paths, so they use
// forward slashes on every platform.
const (
	archiveTablesDir   = "tables/"
	archiveSequences   = "sequences.csv"
	archiveManifest    = "manifest.json"
	archiveInventory   = "inventory.csv"
	archiveAccounts    = "accounts.csv"
	archiveRestoreDoc  = "RESTORE.md"
	archiveNamePattern = "backup-%s.zip"
)

// Manifest is what makes a restore checkable rather than hopeful: it says
// which files should be in the archive, how many rows each table had, what
// schema the database was on, and what every file's bytes hash to.
type Manifest struct {
	// RanAt is when the snapshot was taken, not when the file was written.
	RanAt time.Time `json:"ran_at"`
	// SchemaVersion is the last applied migration
	// (supabase_migrations.schema_migrations). A restore into a database on a
	// different schema is refused unless the admin overrides, because the CSV
	// columns and the live columns may no longer agree.
	SchemaVersion string `json:"schema_version"`
	// Rows is table name -> row count, checked inside the restore transaction.
	Rows map[string]int64 `json:"rows"`
	// Files is zip path -> lowercase hex SHA-256, checked before the restore
	// opens a transaction. manifest.json itself is not listed: a file cannot
	// carry its own digest.
	Files map[string]string `json:"files"`
	// Sequences repeats sequences.csv in a form the restore can read without
	// parsing CSV. The CSV stays because a human opening the archive should
	// see the same thing the code does.
	Sequences []SequenceState `json:"sequences"`
	// Encrypted records that the bytes around this manifest were wrapped
	// (§C.5). It is informational: the reader detects encryption from the
	// file's magic header, because a manifest inside an encrypted archive
	// cannot be read until after it is decrypted.
	Encrypted bool `json:"encrypted"`
}

// SequenceState is one sequence exactly as it stood, both halves of it.
//
// last_value alone is not enough and neither is pg_sequences. A sequence that
// has never been read reports a null last_value in pg_sequences while the
// relation itself holds last_value = start, is_called = false -- and is_called
// is what says whether the recorded number has been handed out yet.
// setval(seq, n) defaults to is_called = true, so replaying a never-read
// sequence with the default burns its first value. Measured, not assumed:
// setval('s', 5, true) on a fresh `start 5` sequence makes nextval return 6.
type SequenceState struct {
	Name        string `json:"name"`
	LastValue   int64  `json:"last_value"`
	IsCalled    bool   `json:"is_called"`
	IncrementBy int64  `json:"increment_by"`
	MinValue    int64  `json:"min_value"`
	MaxValue    int64  `json:"max_value"`
	Cycle       bool   `json:"cycle"`
}

// sequenceCSVHeader is sequences.csv's first line, and the order the fields
// are written in.
var sequenceCSVHeader = []string{"name", "last_value", "is_called", "increment_by", "min_value", "max_value", "cycle"}

func (s SequenceState) csvRecord() []string {
	return []string{
		s.Name,
		fmt.Sprint(s.LastValue),
		fmt.Sprint(s.IsCalled),
		fmt.Sprint(s.IncrementBy),
		fmt.Sprint(s.MinValue),
		fmt.Sprint(s.MaxValue),
		fmt.Sprint(s.Cycle),
	}
}

// archiveName is the file the dated folder holds. An encrypted archive gets a
// different extension so nobody mails it to somebody expecting a zip -- the
// reader detects encryption from the bytes either way, so the name is a
// courtesy rather than a mechanism.
func archiveName(day string, encrypted bool) string {
	name := fmt.Sprintf(archiveNamePattern, day)
	if encrypted {
		name += encryptedSuffix
	}
	return name
}

/* ------------------------------------------------------------- writing ---- */

// archiveBuilder collects files into a zip and hashes each one on the way in,
// so the manifest's digests are of exactly the bytes that were written rather
// than of a second read that could differ.
type archiveBuilder struct {
	buf   bytes.Buffer
	zw    *zip.Writer
	files map[string]string
}

func newArchiveBuilder() *archiveBuilder {
	b := &archiveBuilder{files: map[string]string{}}
	b.zw = zip.NewWriter(&b.buf)
	return b
}

// addFile copies the file at path into the archive at name.
func (b *archiveBuilder) addFile(name, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("read %s for the archive: %w", path, err)
	}
	defer f.Close()
	return b.add(name, f)
}

// addBytes writes fixed content, used for the manifest and RESTORE.md.
func (b *archiveBuilder) addBytes(name string, content []byte) error {
	return b.add(name, bytes.NewReader(content))
}

func (b *archiveBuilder) add(name string, r io.Reader) error {
	w, err := b.zw.Create(name)
	if err != nil {
		return fmt.Errorf("add %s to the archive: %w", name, err)
	}
	sum := sha256.New()
	if _, err := io.Copy(io.MultiWriter(w, sum), r); err != nil {
		return fmt.Errorf("add %s to the archive: %w", name, err)
	}
	b.files[name] = hex.EncodeToString(sum.Sum(nil))
	return nil
}

// finish closes the zip and returns its bytes.
func (b *archiveBuilder) finish() ([]byte, error) {
	if err := b.zw.Close(); err != nil {
		return nil, fmt.Errorf("finish the archive: %w", err)
	}
	return b.buf.Bytes(), nil
}

/* ------------------------------------------------------------- reading ---- */

// openArchive is one archive being read back: the zip reader, its manifest and
// the passphrase question already settled.
type openArchive struct {
	zr       *zip.Reader
	manifest Manifest
}

// readArchive decrypts if it has to, parses the manifest, and verifies every
// digest the manifest names -- all before any caller can touch the database.
//
// The digests come first on purpose. A corrupted download should cost nothing:
// no truncate, no transaction, no advisory lock. And a checksum is the only
// check that can tell "this archive is damaged" from "this archive is fine and
// your data really did change" -- a CSV truncated on a line boundary shows up
// as a row count, but one truncated in the middle of a quoted field does not.
func readArchive(data []byte, passphrase string) (*openArchive, error) {
	if isEncryptedArchive(data) {
		if passphrase == "" {
			return nil, fmt.Errorf("%w: this archive is encrypted; enter the passphrase it was written with", ErrInvalid)
		}
		plain, err := decryptArchive(data, passphrase)
		if err != nil {
			return nil, err
		}
		data = plain
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%w: this file is not a Stockroom backup archive (%v)", ErrInvalid, err)
	}
	a := &openArchive{zr: zr}

	raw, err := a.read(archiveManifest)
	if err != nil {
		return nil, fmt.Errorf("%w: the archive has no %s, so it is not a Stockroom backup", ErrInvalid, archiveManifest)
	}
	if err := json.Unmarshal(raw, &a.manifest); err != nil {
		return nil, fmt.Errorf("%w: %s is unreadable: %v", ErrInvalid, archiveManifest, err)
	}
	if err := a.verify(); err != nil {
		return nil, err
	}
	return a, nil
}

// verify checks every digest in the manifest. A file the manifest names but
// the archive lacks is as much a failure as one whose bytes changed.
func (a *openArchive) verify() error {
	names := make([]string, 0, len(a.manifest.Files))
	for name := range a.manifest.Files {
		names = append(names, name)
	}
	sort.Strings(names) // so two runs fail on the same file first

	for _, name := range names {
		content, err := a.read(name)
		if err != nil {
			return fmt.Errorf("%w: the archive is missing %s, which its manifest lists", ErrInvalid, name)
		}
		got := sha256.Sum256(content)
		if hex.EncodeToString(got[:]) != a.manifest.Files[name] {
			return fmt.Errorf("%w: %s does not match its checksum -- the archive is damaged or was edited, so nothing was restored", ErrInvalid, name)
		}
	}
	return nil
}

// read returns one file's bytes. Archives are a few hundred KB, so reading a
// member whole is simpler than streaming and costs nothing measurable.
func (a *openArchive) read(name string) ([]byte, error) {
	for _, f := range a.zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
}

// tables lists the table names the archive carries, from tables/<name>.csv.
func (a *openArchive) tables() []string {
	out := []string{}
	for _, f := range a.zr.File {
		if strings.HasPrefix(f.Name, archiveTablesDir) && strings.HasSuffix(f.Name, ".csv") {
			out = append(out, strings.TrimSuffix(strings.TrimPrefix(f.Name, archiveTablesDir), ".csv"))
		}
	}
	sort.Strings(out)
	return out
}

/* ---------------------------------------------------------- encryption ---- */

// encryptedMagic marks an encrypted archive. It is read rather than inferred
// from the file name, because a file that has been renamed still has to
// restore -- and a passphrase prompt that depends on an extension is a prompt
// somebody will not get when they need it.
var encryptedMagic = []byte("STOCKROOM-ENC1\n")

const (
	encryptedSuffix = ".enc"
	scryptSaltLen   = 16
	// scrypt at N=2^15 takes roughly a tenth of a second on the closet PC.
	// A backup derives a key once a night and a restore once a disaster, so
	// the cost is invisible where it is paid and expensive where it is
	// attacked.
	scryptN = 1 << 15
	scryptR = 8
	scryptP = 1
	// GCM's standard nonce and tag lengths. Named so decryptArchive can size
	// the header before deriving a key -- scrypt is deliberately slow, and
	// paying it to then find there are no bytes to decrypt is wasted.
	gcmNonceLen = 12
	gcmOverhead = 16
)

func isEncryptedArchive(data []byte) bool {
	return bytes.HasPrefix(data, encryptedMagic)
}

// encryptArchive wraps the zip in AES-256-GCM under a scrypt-derived key
// (§C.5). The salt is fresh per archive, so two nights of backups under one
// passphrase do not share a key.
func encryptArchive(plain []byte, passphrase string) ([]byte, error) {
	if passphrase == "" {
		return nil, fmt.Errorf("%w: cannot encrypt an archive with a blank passphrase", ErrInvalid)
	}
	salt := make([]byte, scryptSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("encrypt archive: %w", err)
	}
	gcm, err := archiveCipher(passphrase, salt)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("encrypt archive: %w", err)
	}

	out := make([]byte, 0, len(encryptedMagic)+len(salt)+len(nonce)+len(plain)+gcm.Overhead())
	out = append(out, encryptedMagic...)
	out = append(out, salt...)
	out = append(out, nonce...)
	return gcm.Seal(out, nonce, plain, encryptedMagic), nil
}

// decryptArchive is encryptArchive's inverse. A wrong passphrase and a
// tampered archive both fail the GCM tag, and both are reported as a wrong
// passphrase: the tag cannot tell them apart, and the first thing anybody will
// have done wrong is mistype.
func decryptArchive(data []byte, passphrase string) ([]byte, error) {
	// The header is read before any key is derived, so a truncated file costs
	// nothing: scrypt is deliberately expensive and there is no sense paying
	// for it to then discover there are no bytes to decrypt.
	header := len(encryptedMagic) + scryptSaltLen + gcmNonceLen
	if len(data) < header+gcmOverhead {
		return nil, fmt.Errorf("%w: the encrypted archive is truncated", ErrInvalid)
	}
	salt := data[len(encryptedMagic) : len(encryptedMagic)+scryptSaltLen]
	nonce := data[len(encryptedMagic)+scryptSaltLen : header]

	gcm, err := archiveCipher(passphrase, salt)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, data[header:], encryptedMagic)
	if err != nil {
		return nil, fmt.Errorf("%w: the passphrase does not decrypt this archive (or the file was altered)", ErrInvalid)
	}
	return plain, nil
}

func archiveCipher(passphrase string, salt []byte) (cipher.AEAD, error) {
	key, err := scrypt.Key([]byte(passphrase), salt, scryptN, scryptR, scryptP, 32)
	if err != nil {
		return nil, fmt.Errorf("derive archive key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("derive archive key: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("derive archive key: %w", err)
	}
	return gcm, nil
}

/* --------------------------------------------------------------- files ---- */

// writeArchiveFile writes the finished archive (encrypted or not) beside the
// readable CSVs, through a temporary file so a crash mid-write cannot leave
// something that looks like an archive and is not one.
func writeArchiveFile(path string, content []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".archive-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	name := tmp.Name()
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		_ = os.Remove(name)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		_ = os.Remove(name)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chmod(name, 0o644); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
