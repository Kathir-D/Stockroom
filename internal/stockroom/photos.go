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

// storePhoto writes src to <uploads>/<dir>/<base><ext> and returns the path
// relative to uploads, which is what the database stores and what /files/
// serves. ext carries its leading dot.
//
// Any copy of the same base under a different extension is removed first, so
// replacing a JPEG with a PNG doesn't leave the old file on disk with nothing
// pointing at it.
func storePhoto(uploads, dir, base, ext string, src io.Reader) (string, error) {
	if uploads == "" {
		return "", fmt.Errorf("%w: UPLOADS_DIR is not set, so there is nowhere to put the photo", ErrNotConfigured)
	}
	// base names a row (a student number or an asset id), never client text.
	// The guard is here so a future caller can't turn one into a path.
	if base == "" || strings.ContainsAny(base, `/\`) || strings.Contains(base, "..") {
		return "", fmt.Errorf("%w: %q is not a usable photo name", ErrInvalid, base)
	}

	target := filepath.Join(uploads, dir, base+ext)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", fmt.Errorf("create uploads dir: %w", err)
	}
	// Student numbers are digits and asset ids are uuids, so the pattern
	// carries no glob metacharacters.
	stale, _ := filepath.Glob(filepath.Join(uploads, dir, base+".*"))
	for _, old := range stale {
		if old != target {
			_ = os.Remove(old)
		}
	}

	out, err := os.Create(target)
	if err != nil {
		return "", fmt.Errorf("write photo: %w", err)
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		return "", fmt.Errorf("write photo: %w", err)
	}
	if err := out.Close(); err != nil {
		return "", fmt.Errorf("write photo: %w", err)
	}
	return filepath.ToSlash(filepath.Join(dir, base+ext)), nil
}
