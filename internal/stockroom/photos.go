package stockroom

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
		p.rollback()
		return nil, fmt.Errorf("write photo: %w", err)
	}
	if err := out.Close(); err != nil {
		p.rollback()
		return nil, fmt.Errorf("write photo: %w", err)
	}
	return p, nil
}

// publish moves the staged copy into place, setting aside whatever it
// replaced so rollback can put it back. The move is a rename, so anything
// reading the uploads directory sees the old photo or the new one and never a
// half-written file.
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
		p.rollback()
		return fmt.Errorf("write photo: %w", err)
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
// photo that was there before. Errors are dropped: the caller is already
// returning a failure, and a stranded temporary file is the smaller problem.
func (p *stagedPhoto) rollback() {
	if p.tmp != "" {
		_ = os.Remove(p.tmp)
		p.tmp = ""
	}
	if p.published {
		_ = os.Remove(p.target)
		p.published = false
	}
	if p.backup != "" {
		_ = os.Rename(p.backup, p.target)
		p.backup = ""
	}
}

// storePhoto writes src to <uploads>/<dir>/<base><ext> and returns the path
// relative to uploads. It is the whole sequence run at once, for a caller with
// no database write to keep in step with the file; SetAssetPhoto, which has
// one, drives the steps itself.
func storePhoto(uploads, dir, base, ext string, src io.Reader) (string, error) {
	p, err := stagePhoto(uploads, dir, base, ext, src)
	if err != nil {
		return "", err
	}
	if err := p.publish(); err != nil {
		p.rollback()
		return "", err
	}
	p.commit()
	return p.rel, nil
}
