package stockroom

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The staging sequence in photos.go. What every one of these is really
// checking is the same promise: an upload that does not finish leaves the
// photo that was already there, because destroying it is the one failure a
// re-upload cannot repair.

// errReader fails partway, the way a connection dropping mid-upload does.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

func TestStorePhotoKeepsTheOldPhotoWhenTheCopyFails(t *testing.T) {
	uploads := t.TempDir()
	if _, err := storePhoto(uploads, "assets", "unit-1", ".jpg", strings.NewReader("first")); err != nil {
		t.Fatalf("storePhoto: %v", err)
	}

	half := io.MultiReader(strings.NewReader("half a "), errReader{})
	if _, err := storePhoto(uploads, "assets", "unit-1", ".png", half); err == nil {
		t.Fatal("storePhoto with a failing reader succeeded, want an error")
	}

	assertPhoto(t, uploads, "unit-1.jpg", "first")
	assertOnlyPhotos(t, uploads, "unit-1.jpg")
}

func TestStagedPhotoRollbackRestoresWhatItReplaced(t *testing.T) {
	uploads := t.TempDir()
	if _, err := storePhoto(uploads, "assets", "unit-1", ".jpg", strings.NewReader("first")); err != nil {
		t.Fatalf("storePhoto: %v", err)
	}

	p, err := stagePhoto(uploads, "assets", "unit-1", ".jpg", strings.NewReader("second"))
	if err != nil {
		t.Fatalf("stagePhoto: %v", err)
	}
	if err := p.publish(); err != nil {
		t.Fatalf("publish: %v", err)
	}
	// Published but not committed, which is where SetAssetPhoto stands while
	// it writes the row: the new photo is in place and the old one is still
	// recoverable.
	assertPhoto(t, uploads, "unit-1.jpg", "second")

	p.rollback()
	assertPhoto(t, uploads, "unit-1.jpg", "first")
	assertOnlyPhotos(t, uploads, "unit-1.jpg")
}

func TestStagedPhotoRollbackRemovesAPhotoThatReplacedNothing(t *testing.T) {
	uploads := t.TempDir()
	p, err := stagePhoto(uploads, "assets", "unit-1", ".jpg", strings.NewReader("first"))
	if err != nil {
		t.Fatalf("stagePhoto: %v", err)
	}
	if err := p.publish(); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// Nothing points at it, so a failed row write takes it away rather than
	// leaving a file in the uploads directory forever.
	p.rollback()
	assertOnlyPhotos(t, uploads)
}

func TestStagedPhotoDropsTheOtherExtensionOnlyOnCommit(t *testing.T) {
	uploads := t.TempDir()
	if _, err := storePhoto(uploads, "assets", "unit-1", ".jpg", strings.NewReader("first")); err != nil {
		t.Fatalf("storePhoto: %v", err)
	}

	p, err := stagePhoto(uploads, "assets", "unit-1", ".png", strings.NewReader("second"))
	if err != nil {
		t.Fatalf("stagePhoto: %v", err)
	}
	if err := p.publish(); err != nil {
		t.Fatalf("publish: %v", err)
	}
	// Until the row names the .png, the .jpg is still the photo of record and
	// has to survive a rollback.
	assertPhoto(t, uploads, "unit-1.jpg", "first")
	p.rollback()
	assertOnlyPhotos(t, uploads, "unit-1.jpg")

	// Committed, it goes: otherwise swapping a JPEG for a PNG leaves the old
	// file on disk with nothing pointing at it.
	p, err = stagePhoto(uploads, "assets", "unit-1", ".png", strings.NewReader("second"))
	if err != nil {
		t.Fatalf("stagePhoto: %v", err)
	}
	if err := p.publish(); err != nil {
		t.Fatalf("publish: %v", err)
	}
	p.commit()
	assertPhoto(t, uploads, "unit-1.png", "second")
	assertOnlyPhotos(t, uploads, "unit-1.png")
}

func assertPhoto(t *testing.T, uploads, name, want string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(uploads, "assets", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if string(got) != want {
		t.Errorf("%s = %q, want %q", name, got, want)
	}
}

// assertOnlyPhotos checks the uploads directory holds exactly these files and
// nothing else, which is how a stranded staging or backup copy shows up.
func assertOnlyPhotos(t *testing.T, uploads string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(uploads, "assets"))
	if err != nil {
		t.Fatalf("read uploads dir: %v", err)
	}
	got := make([]string, 0, len(entries))
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("uploads dir holds %v, want %v", got, want)
	}
}
