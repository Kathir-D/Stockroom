package stockroom

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The photo mirror (docs/design/backup.md §E.2): one live generation updated
// in place, rolling over every keep_days, and never deleting anything.

func TestMirrorPhotosCopiesOnlyWhatChanged(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()

	uploads := t.TempDir()
	writeTestPhoto(t, filepath.Join(uploads, "assets", "one.jpg"), "first")
	writeTestPhoto(t, filepath.Join(uploads, "profiles", "123456.png"), "second")
	db.UploadsDir = uploads
	db.PhotoBackupDir = t.TempDir()

	settings := Settings{KeepDays: 90, PhotoMaxGenerations: 8}

	first, err := db.MirrorPhotos(ctx, settings)
	if err != nil {
		t.Fatalf("MirrorPhotos: %v", err)
	}
	if first.Copied != 2 {
		t.Errorf("first run copied %d files, want 2", first.Copied)
	}
	if _, err := os.Stat(filepath.Join(db.PhotoBackupDir, first.Generation, "assets", "one.jpg")); err != nil {
		t.Errorf("the mirror is missing a photo: %v", err)
	}

	// The second run is the one that matters: a mirror that re-copies
	// everything nightly fills the disk it is trying to protect.
	second, err := db.MirrorPhotos(ctx, settings)
	if err != nil {
		t.Fatalf("second MirrorPhotos: %v", err)
	}
	if second.Copied != 0 || second.Unchanged != 2 {
		t.Errorf("second run copied %d and skipped %d, want 0 and 2", second.Copied, second.Unchanged)
	}

	// A photo deleted from uploads/ stays in the mirror. That is the whole
	// point: the likeliest loss is somebody deleting the wrong file.
	if err := os.Remove(filepath.Join(uploads, "assets", "one.jpg")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.MirrorPhotos(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(db.PhotoBackupDir, first.Generation, "assets", "one.jpg")); err != nil {
		t.Errorf("a photo deleted from uploads/ was removed from the mirror too, so it is not recoverable: %v", err)
	}
}

// A machine where nobody has uploaded a photo yet has no uploads directory.
// That is nothing to do, not a failure -- otherwise every fresh install's
// first backup would report a problem on its first night.
func TestMirrorPhotosWithNoUploadsIsANoOp(t *testing.T) {
	db := requireTestDB(t)
	db.UploadsDir = filepath.Join(t.TempDir(), "never-created")
	db.PhotoBackupDir = t.TempDir()

	res, err := db.MirrorPhotos(context.Background(), Settings{KeepDays: 90})
	if err != nil {
		t.Fatalf("MirrorPhotos with no uploads dir = %v, want no error", err)
	}
	if res.Copied != 0 {
		t.Errorf("copied %d files from a directory that does not exist", res.Copied)
	}
}

func TestPhotoGenerationRollsOverAtTheRetentionBoundary(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()

	uploads := t.TempDir()
	writeTestPhoto(t, filepath.Join(uploads, "assets", "one.jpg"), "first")
	db.UploadsDir = uploads
	db.PhotoBackupDir = t.TempDir()

	// A generation dated far enough back that the next run has to roll.
	old := generationPfx + time.Now().AddDate(0, 0, -100).Format("2006-01-02")
	if err := os.MkdirAll(filepath.Join(db.PhotoBackupDir, old, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestPhoto(t, filepath.Join(db.PhotoBackupDir, old, "assets", "gone-from-uploads.jpg"), "kept")
	if err := writeCurrent(db.PhotoBackupDir, old); err != nil {
		t.Fatal(err)
	}

	res, err := db.MirrorPhotos(ctx, Settings{KeepDays: 90, PhotoMaxGenerations: 8})
	if err != nil {
		t.Fatalf("MirrorPhotos: %v", err)
	}
	if !res.RolledOver {
		t.Fatal("the generation did not roll over past keep_days")
	}
	if res.Generation == old {
		t.Fatal("the live generation is still the old one")
	}
	// The frozen generation keeps what it had, and the new one starts as a
	// full copy of it, so the live folder is always complete on its own.
	if _, err := os.Stat(filepath.Join(db.PhotoBackupDir, old, "assets", "gone-from-uploads.jpg")); err != nil {
		t.Errorf("the frozen generation lost a file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(db.PhotoBackupDir, res.Generation, "assets", "gone-from-uploads.jpg")); err != nil {
		t.Errorf("the new generation is not a full copy of the one it rolled from: %v", err)
	}

	// The bound is an alert, not a purge: nothing is ever deleted for the
	// mirror's own convenience.
	status, err := db.PhotoMirrorStatus(ctx, Settings{KeepDays: 90, PhotoMaxGenerations: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Warnings) == 0 {
		t.Error("two generations past a bound of one raised no warning; the disk fills silently")
	}
	if len(status.Generations) != 2 {
		t.Errorf("the status lists %d generations, want 2: nothing may be deleted automatically", len(status.Generations))
	}
}

// Deleting the live generation would make the next run a full copy, silently.
// It is refused; every other generation is a button an admin presses.
func TestDeletePhotoGenerationRefusesTheLiveOne(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	student := actorFor(insertTestProfile(t, db, false, "student-pw"))

	db.UploadsDir = t.TempDir()
	db.PhotoBackupDir = t.TempDir()
	writeTestPhoto(t, filepath.Join(db.UploadsDir, "assets", "one.jpg"), "first")

	res, err := db.MirrorPhotos(ctx, Settings{KeepDays: 90})
	if err != nil {
		t.Fatal(err)
	}

	if err := db.DeletePhotoGeneration(ctx, student, res.Generation); !errors.Is(err, ErrForbidden) {
		t.Errorf("DeletePhotoGeneration as a student = %v, want ErrForbidden", err)
	}
	if err := db.DeletePhotoGeneration(ctx, admin, res.Generation); !errors.Is(err, ErrConflict) {
		t.Errorf("deleting the live generation = %v, want ErrConflict", err)
	}
	if err := db.DeletePhotoGeneration(ctx, admin, "../../etc"); !errors.Is(err, ErrInvalid) {
		t.Errorf("deleting a path that is not a generation name = %v, want ErrInvalid", err)
	}
}

func writeTestPhoto(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
