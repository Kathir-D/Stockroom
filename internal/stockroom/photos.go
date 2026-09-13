package stockroom

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

// Photo storage. Two paths put files under UPLOADS_DIR: a roster import
// copies a profile photo named by a CSV column (roster.go), and an admin
// uploads an asset photo through the API (assets_admin.go). Both name the
// file after the row that owns it, so one function decides where a photo
// lands and what happens to the copy it replaces.
//
// A photo is written in two steps, staged then published, because the file
// and the row that points at it have to move together. Overwriting the file
// first and updating the row second means a failed UPDATE leaves the row
// naming a picture the upload already destroyed, and that is the one outcome
// re-uploading cannot repair. Staging keeps the old copy reachable until the
// row is safely pointing at the new one.

// FilesPrefix is where the server mounts UploadsDir (server/files.go), and so
// the prefix of every photo URL handed to a frontend. Profile and asset photos
// follow the same rule, so it lives beside the code that writes both.
const FilesPrefix = "/files/"

// photoURL turns a path stored relative to UploadsDir into the URL the Go
// server serves it at. A missing or blank path is nil, not an empty string,
// so the frontend tests one thing to decide whether to render an image.
func photoURL(stored *string) *string {
	if stored == nil {
		return nil
	}
	// Photos are written with forward slashes (storePhoto), but a value typed
	// into the admin panel on Windows may not be.
	rel := strings.Trim(strings.ReplaceAll(*stored, `\`, "/"), "/")
	if rel == "" {
		return nil
	}
	// Clean against a leading slash so a stored "../x" resolves inside the
	// uploads root instead of pointing above it. http.Dir refuses such a
	// request anyway; this keeps the URL itself honest.
	clean := strings.TrimPrefix(path.Clean("/"+rel), "/")
	if clean == "" || clean == "." {
		return nil
	}
	url := FilesPrefix + clean
	return &url
}

// uploadPhotoExtensions is what SetAssetPhoto accepts. The list is short on
// purpose: /files/ serves the uploads directory without a session, and
// http.FileServer picks the Content-Type from the extension, so an .html or
// .svg upload would run as a page on the web app's own origin. A roster
// import is not held to the list, because those files come off the admin's
// own disk rather than over HTTP.
var uploadPhotoExtensions = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".gif":  true,
	".webp": true,
}

// photoLocks serializes the whole stage-publish-commit sequence per photo.
// The steps are several file operations and, for an asset, a row write in the
// middle of them, so two uploads replacing the same photo interleave badly:
// one discovers the other's file as a stale copy of another extension and
// deletes it on commit, after the slower request's UPDATE has already pointed
// the row at it. The row then names a file that is gone, which is exactly the
// outcome staging exists to prevent.
//
// The key is the photo's identity, <dir>/<base> — the asset id or student
// number — not the target path, because the extension is the part that
// differs between two uploads that collide. A lock is dropped once nobody
// holds it, so the map stays the size of the uploads in flight rather than
// growing one entry per asset ever photographed.
//
// This serializes one process. It is the whole story for this deployment:
// a single Go server owns the uploads directory (CLAUDE.md §3). A second
// server pointed at the same directory would need a lock on disk instead.
var photoLocks = struct {
	mu sync.Mutex
	m  map[string]*photoLock
}{m: make(map[string]*photoLock)}

type photoLock struct {
	mu      sync.Mutex
	holders int
}

// lockPhoto blocks until this photo's identity is free, and returns the
// function that releases it. Callers hold it from before staging until commit
// or rollback has finished, so the file on disk and the row naming it settle
// together.
func lockPhoto(dir, base string) func() {
	key := filepath.ToSlash(filepath.Join(dir, base))

	photoLocks.mu.Lock()
	l, ok := photoLocks.m[key]
	if !ok {
		l = &photoLock{}
		photoLocks.m[key] = l
	}
	l.holders++
	photoLocks.mu.Unlock()

	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		photoLocks.mu.Lock()
		l.holders--
		if l.holders == 0 {
			delete(photoLocks.m, key)
		}
		photoLocks.mu.Unlock()
	}
}

// stagedPhoto is an upload that has reached the disk but is not yet the photo
// anyone will see. The sequence is stage, publish, write the row, commit; a
// failure at any point is a rollback, which puts back whatever was there
// before.
type stagedPhoto struct {
	// rel is the path relative to uploads, which is what the database
	// stores and what /files/ serves.
	rel       string
	target    string   // where the photo lands
	tmp       string   // the copy written so far, not yet in place
	stale     []string // same name under another extension, dropped on commit
	backup    string   // what target held before publish, put back on rollback
	published bool     // whether tmp has been renamed onto target
}

// stagePhoto writes src to a temporary file beside <uploads>/<dir>/<base><ext>,
// touching nothing that is already there. ext carries its leading dot.
//
// The caller holds lockPhoto(dir, base) for the whole sequence this starts:
// the stale-copy scan below reads a directory another upload of the same photo
// is about to write to.
func stagePhoto(uploads, dir, base, ext string, src io.Reader) (*stagedPhoto, error) {
	if uploads == "" {
		return nil, fmt.Errorf("%w: UPLOADS_DIR is not set, so there is nowhere to put the photo", ErrNotConfigured)
	}
	// base names a row (a student number or an asset id), never client text.
	// The guard is here so a future caller can't turn one into a path.
	if base == "" || strings.ContainsAny(base, `/\`) || strings.Contains(base, "..") {
		return nil, fmt.Errorf("%w: %q is not a usable photo name", ErrInvalid, base)
	}

	target := filepath.Join(uploads, dir, base+ext)
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, fmt.Errorf("create uploads dir: %w", err)
	}

	// Student numbers are digits and asset ids are uuids, so the pattern
	// carries no glob metacharacters. The glob runs before this run's own
	// temporary files exist, and those are named with a leading dot, so
	// neither can be mistaken for an older copy of the same photo.
	var stale []string
	matches, _ := filepath.Glob(filepath.Join(parent, base+".*"))
	for _, old := range matches {
		if old != target {
			stale = append(stale, old)
		}
	}

	out, err := os.CreateTemp(parent, ".staged-"+base+"-*")
	if err != nil {
		return nil, fmt.Errorf("write photo: %w", err)
	}
	p := &stagedPhoto{
		rel:    filepath.ToSlash(filepath.Join(dir, base+ext)),
		target: target,
		tmp:    out.Name(),
		stale:  stale,
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		return nil, p.rollbackWith(fmt.Errorf("write photo: %w", err))
	}
	if err := out.Close(); err != nil {
		return nil, p.rollbackWith(fmt.Errorf("write photo: %w", err))
	}
	return p, nil
}

// publish moves the staged copy into place, setting aside whatever it
// replaced so rollback can put it back. The move is a rename, so anything
// reading the uploads directory sees the old photo or the new one and never a
// half-written file. A publish that fails has already rolled itself back, so
// the caller returns the error rather than undoing anything further.
func (p *stagedPhoto) publish() error {
	if _, err := os.Stat(p.target); err == nil {
		aside, err := os.CreateTemp(filepath.Dir(p.target), ".replaced-"+filepath.Base(p.target)+"-*")
		if err != nil {
			return fmt.Errorf("write photo: %w", err)
		}
		name := aside.Name()
		aside.Close()
		if err := os.Rename(p.target, name); err != nil {
			_ = os.Remove(name)
			return fmt.Errorf("write photo: %w", err)
		}
		p.backup = name
	}
	if err := os.Rename(p.tmp, p.target); err != nil {
		return p.rollbackWith(fmt.Errorf("write photo: %w", err))
	}
	p.tmp, p.published = "", true
	return nil
}

// commit drops what the new photo replaced: the copy publish set aside, and
// any copy of the same name under another extension, so swapping a JPEG for a
// PNG doesn't leave the old file on disk with nothing pointing at it. It runs
// only once the database row names the new file, because until then the old
// one is still the photo of record.
func (p *stagedPhoto) commit() {
	if p.backup != "" {
		_ = os.Remove(p.backup)
		p.backup = ""
	}
	for _, old := range p.stale {
		_ = os.Remove(old)
	}
	p.stale = nil
}

// rollback undoes however much of the upload reached the disk, leaving the
// photo that was there before.
//
// A stranded temporary file is dropped silently: the caller is already
// returning a failure and the leftover is harmless. Failing to put the backup
// back is not harmless and is reported, because at that point the old photo
// is neither at its own name nor recoverable by anyone who does not know to
// look for a .replaced- file, and the row still names the path it vacated.
// p.backup is cleared only once the rename has actually succeeded, so a
// second attempt still knows where the copy went.
func (p *stagedPhoto) rollback() error {
	if p.tmp != "" {
		_ = os.Remove(p.tmp)
		p.tmp = ""
	}
	if p.published {
		_ = os.Remove(p.target)
		p.published = false
	}
	if p.backup != "" {
		if err := os.Rename(p.backup, p.target); err != nil {
			return fmt.Errorf("restore the photo %s replaced, left at %s: %w", p.target, p.backup, err)
		}
		p.backup = ""
	}
	return nil
}

// rollbackWith undoes the upload and reports both failures when putting the
// old photo back fails too. err stays in the chain either way, because it is
// the reason the upload stopped and what errors.Is upstream is matching on;
// the rollback failure rides along so a lost photo is never silent.
func (p *stagedPhoto) rollbackWith(err error) error {
	if rerr := p.rollback(); rerr != nil {
		return errors.Join(err, rerr)
	}
	return err
}

// storePhoto writes src to <uploads>/<dir>/<base><ext> and returns the path
// relative to uploads. It is the whole sequence run at once, with the lock
// held across all of it; SetAssetPhoto drives the same steps itself because
// it has more to do between them.
//
// publishRow, if it is not nil, runs once the new file is in place and before
// the copy it replaced is dropped: that is where a caller writes the database
// row naming the photo, so the file and the column settle inside one lock. An
// error from it rolls the upload back and comes out of storePhoto unchanged,
// so errors.Is upstream still matches.
func storePhoto(uploads, dir, base, ext string, src io.Reader, publishRow func(rel string) error) (string, error) {
	defer lockPhoto(dir, base)()

	p, err := stagePhoto(uploads, dir, base, ext, src)
	if err != nil {
		return "", err
	}
	if err := p.publish(); err != nil {
		return "", err
	}
	if publishRow != nil {
		if err := publishRow(p.rel); err != nil {
			return "", p.rollbackWith(err)
		}
	}
	p.commit()
	return p.rel, nil
}
