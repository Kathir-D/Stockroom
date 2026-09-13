package stockroom

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// Two admins replacing the same asset's photo at once. Without the lock each
// staging run scans for copies under the other extensions and drops them on
// commit, so the loser's file outlives the winner's commit and the directory
// is left holding more than one photo — the state a row pointing at the wrong
// one comes from.
func TestStorePhotoSerializesUploadsOfTheSamePhoto(t *testing.T) {
	uploads := t.TempDir()
	exts := []string{".jpg", ".png", ".webp", ".gif"}

	var wg sync.WaitGroup
	for i := range 24 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ext := exts[i%len(exts)]
			if _, err := storePhoto(uploads, "assets", "unit-1", ext, strings.NewReader(ext)); err != nil {
				t.Errorf("storePhoto: %v", err)
			}
		}(i)
	}
	wg.Wait()

	// Whichever upload landed last, it is the only thing left: no second
	// extension, and no stranded .staged- or .replaced- copy either.
	entries, err := os.ReadDir(filepath.Join(uploads, "assets"))
	if err != nil {
		t.Fatalf("read uploads dir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 1 {
		t.Errorf("uploads dir holds %v, want exactly one photo", names)
	}
}

// A rollback that cannot put the old photo back is the one case worth
// reporting: the row still names a path that is now empty, and the only copy
// is sitting under a name nobody would think to look for.
func TestStagedPhotoRollbackReportsAFailedRestore(t *testing.T) {
	uploads := t.TempDir()
	dir := filepath.Join(uploads, "assets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	p := &stagedPhoto{
		target: filepath.Join(dir, "unit-1.jpg"),
		backup: filepath.Join(dir, ".replaced-unit-1.jpg-gone"), // never written
	}

	err := p.rollback()
	if err == nil {
		t.Fatal("rollback with an unrestorable backup returned nil, want an error")
	}
	if p.backup == "" {
		t.Error("rollback cleared p.backup though the restore failed, losing where the copy went")
	}

	// The reason the upload stopped still has to survive the joining, since
	// that is what the HTTP layer maps to a status code.
	joined := p.rollbackWith(ErrNotFound)
	if !errors.Is(joined, ErrNotFound) {
		t.Errorf("rollbackWith dropped the original error: %v", joined)
	}
	if !strings.Contains(joined.Error(), "restore") {
		t.Errorf("rollbackWith dropped the restore failure: %v", joined)
	}
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
