package stockroom

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The photo mirror (docs/design/backup.md §E.2).
//
// Photos are the one thing this system backs up locally and only locally, by
// decision (§B, §H): a failed drive loses uploads/ and its mirror together,
// and pointing photo_backup_dir at a second physical disk is the mitigation.
// What the mirror is for is the other, likelier loss -- somebody deletes a
// photo, or replaces the wrong one -- which is why it never deletes anything.
//
// Shape: one live generation, updated in place every run, frozen into a kept
// generation every keep_days. Not a copy per night. A nightly full copy of
// every photo would fill a disk in weeks for no gain, because photos barely
// change; an in-place mirror with a rollover keeps the same recoverability at
// the cost of one full copy per retention period.

const (
	// currentFile names the live generation. A file rather than a symlink,
	// because a symlink needs developer mode or an elevated prompt on Windows
	// and the closet PC is Windows.
	currentFile   = ".current"
	generationPfx = "gen-"
)

// PhotoMirrorResult is what one mirror run did, reported as part of the backup
// it ran inside.
type PhotoMirrorResult struct {
	Configured bool   `json:"configured"`
	Generation string `json:"generation"`
	RolledOver bool   `json:"rolled_over"`
	Copied     int    `json:"copied"`
	Unchanged  int    `json:"unchanged"`
	Bytes      int64  `json:"bytes"`
	// Error is set when the mirror failed but the database backup did not. A
	// photo that did not copy must not throw away a good database export.
	Error string `json:"error"`
}

func (r *PhotoMirrorResult) logSummary() string {
	switch {
	case r == nil:
		return "photos skipped"
	case r.Error != "":
		return "photos FAILED: " + oneLine(r.Error)
	case !r.Configured:
		return "photos not configured"
	case r.RolledOver:
		return fmt.Sprintf("photos %d copied into a new generation %s", r.Copied, r.Generation)
	default:
		return fmt.Sprintf("photos %d copied, %d unchanged", r.Copied, r.Unchanged)
	}
}

// PhotoGeneration is one frozen or live copy of uploads/.
type PhotoGeneration struct {
	Name  string    `json:"name"`
	At    time.Time `json:"at"`
	Files int       `json:"files"`
	Bytes int64     `json:"bytes"`
	Live  bool      `json:"live"`
}

// PhotoMirrorStatus is the backup screen's view of the mirror: what is there,
// how much room is left, and whether either bound has been crossed.
type PhotoMirrorStatus struct {
	Configured     bool              `json:"configured"`
	Dir            string            `json:"dir"`
	Current        string            `json:"current"`
	Generations    []PhotoGeneration `json:"generations"`
	Bytes          int64             `json:"bytes"`
	FreeBytes      int64             `json:"free_bytes"`
	MinFreeGB      int               `json:"min_free_gb"`
	MaxGenerations int               `json:"max_generations"`
	Warnings       []string          `json:"warnings"`
}

// MirrorPhotos copies uploads/ into the live generation, rolling over first if
// the generation has aged past keep_days.
//
// It is called by the backup run rather than exported to the UI as its own
// button: the photos and the database are one night's backup, and two buttons
// would let them drift apart in the one place that matters, which is how old
// each of them is.
func (db *DB) MirrorPhotos(ctx context.Context, settings Settings) (*PhotoMirrorResult, error) {
	dir := db.photoBackupDir(settings)
	if dir == "" {
		return &PhotoMirrorResult{}, nil
	}
	uploads := db.UploadsDir
	if uploads == "" {
		return &PhotoMirrorResult{Configured: true}, nil
	}
	// A machine where nobody has uploaded a photo yet has no uploads
	// directory. That is nothing to do, not a failure -- treating it as an
	// error would make every fresh install's first backup report a problem.
	if _, err := os.Stat(uploads); errors.Is(err, os.ErrNotExist) {
		return &PhotoMirrorResult{Configured: true}, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create photo backup dir: %w", err)
	}

	res := &PhotoMirrorResult{Configured: true}
	gen, rolled, err := currentGeneration(dir, settings.KeepDays)
	if err != nil {
		return nil, err
	}
	res.Generation, res.RolledOver = gen, rolled

	target := filepath.Join(dir, gen)
	err = filepath.WalkDir(uploads, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(uploads, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		// Staging and replaced-photo leftovers start with a dot (photos.go)
		// and are not photos anyone will want back.
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		dst := filepath.Join(target, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		same, err := sameFile(dst, info)
		if err != nil {
			return err
		}
		if same {
			res.Unchanged++
			res.Bytes += info.Size()
			return nil
		}
		if err := copyFile(path, dst, info); err != nil {
			return err
		}
		res.Copied++
		res.Bytes += info.Size()
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("mirror photos: %w", err)
	}
	return res, nil
}

// currentGeneration reads .current, creating a generation if there is none and
// rolling over if the live one has aged past keepDays. It returns the
// generation to write into and whether this call rolled.
func currentGeneration(dir string, keepDays int) (string, bool, error) {
	today := time.Now().Format("2006-01-02")
	fresh := generationPfx + today

	raw, err := os.ReadFile(filepath.Join(dir, currentFile))
	current := strings.TrimSpace(string(raw))
	if err != nil || current == "" || !strings.HasPrefix(current, generationPfx) {
		if err := os.MkdirAll(filepath.Join(dir, fresh), 0o755); err != nil {
			return "", false, fmt.Errorf("create photo generation: %w", err)
		}
		if err := writeCurrent(dir, fresh); err != nil {
			return "", false, err
		}
		return fresh, false, nil
	}

	started, err := time.ParseInLocation("2006-01-02", strings.TrimPrefix(current, generationPfx), time.Local)
	if err != nil || keepDays < 1 || time.Since(started) < time.Duration(keepDays)*24*time.Hour {
		// Still inside the window (or a name nothing can date, which is
		// treated as still current rather than rolled: an unreadable name is
		// not a reason to make a full copy of every photo).
		if err := os.MkdirAll(filepath.Join(dir, current), 0o755); err != nil {
			return "", false, fmt.Errorf("create photo generation: %w", err)
		}
		return current, false, nil
	}
	if current == fresh {
		return current, false, nil
	}

	// Roll over: the old generation is frozen exactly as it stands and never
	// written to again, and the new one starts as a full copy of it so the
	// live folder is always complete on its own.
	if err := copyTree(filepath.Join(dir, current), filepath.Join(dir, fresh)); err != nil {
		return "", false, fmt.Errorf("roll over photo generation: %w", err)
	}
	if err := writeCurrent(dir, fresh); err != nil {
		return "", false, err
	}
	return fresh, true, nil
}

func writeCurrent(dir, gen string) error {
	if err := os.WriteFile(filepath.Join(dir, currentFile), []byte(gen+"\n"), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", currentFile, err)
	}
	return nil
}

// sameFile reports whether dst already holds src's bytes, judged by size and
// modification time. Not a checksum: the mirror runs over every photo every
// night, and hashing a few hundred megabytes to find that nothing changed is
// the cost the comparison exists to avoid.
func sameFile(dst string, src fs.FileInfo) (bool, error) {
	info, err := os.Stat(dst)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.Size() == src.Size() && info.ModTime().Equal(src.ModTime()), nil
}

func copyFile(src, dst string, info fs.FileInfo) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".copying-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, dst); err != nil {
		_ = os.Remove(name)
		return err
	}
	// The mtime is carried across because it is half of what sameFile
	// compares: without it every file would look changed on the next run and
	// the mirror would rewrite itself nightly.
	return os.Chtimes(dst, info.ModTime(), info.ModTime())
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return copyFile(path, out, info)
	})
}

/* -------------------------------------------------------------- status ---- */

// PhotoMirrorStatus measures the mirror and reports whether either bound has
// been crossed.
//
// The bounds are alerts, not purges, and the distinction is the whole design:
// the mirror never deletes, so it only grows, and because photos are mirrored
// in the same run that writes the database backup, an exhausted disk breaks
// *the backup*. A backup system whose failure mode is silently not backing up
// is the failure this phase exists to remove. But auto-purging the only copy
// of a deleted photo defeats the mirror, so the remedy is a person deleting a
// named generation, which the backup screen offers with each one's size.
func (db *DB) PhotoMirrorStatus(ctx context.Context, settings Settings) (*PhotoMirrorStatus, error) {
	dir := db.photoBackupDir(settings)
	out := &PhotoMirrorStatus{
		Dir:            dir,
		MinFreeGB:      settings.PhotoMinFreeGB,
		MaxGenerations: settings.PhotoMaxGenerations,
		Generations:    []PhotoGeneration{},
		Warnings:       []string{},
	}
	if dir == "" {
		return out, nil
	}
	out.Configured = true

	raw, _ := os.ReadFile(filepath.Join(dir, currentFile))
	out.Current = strings.TrimSpace(string(raw))

	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), generationPfx) {
			continue
		}
		g := PhotoGeneration{Name: e.Name(), Live: e.Name() == out.Current}
		if at, err := time.ParseInLocation("2006-01-02", strings.TrimPrefix(e.Name(), generationPfx), time.Local); err == nil {
			g.At = at
		}
		g.Files, g.Bytes = treeSize(filepath.Join(dir, e.Name()))
		out.Bytes += g.Bytes
		out.Generations = append(out.Generations, g)
	}
	sort.Slice(out.Generations, func(i, j int) bool { return out.Generations[i].Name > out.Generations[j].Name })

	out.FreeBytes = freeSpace(dir)
	if settings.PhotoMinFreeGB > 0 && out.FreeBytes > 0 {
		minFree := int64(settings.PhotoMinFreeGB) << 30
		if out.FreeBytes < minFree {
			out.Warnings = append(out.Warnings, fmt.Sprintf(
				"The photo backup disk has %s free, below the %d GB you asked to be warned at. Backups stop working when the disk fills. Delete an old photo generation under Admin → Backup.",
				humanBytes(out.FreeBytes), settings.PhotoMinFreeGB))
		}
	}
	if settings.PhotoMaxGenerations > 0 && len(out.Generations) > settings.PhotoMaxGenerations {
		out.Warnings = append(out.Warnings, fmt.Sprintf(
			"There are %d photo generations, more than the %d you asked to be warned at, using %s. Nothing is deleted automatically; delete the oldest under Admin → Backup when you are sure.",
			len(out.Generations), settings.PhotoMaxGenerations, humanBytes(out.Bytes)))
	}
	return out, nil
}

// GetPhotoMirrorStatus is PhotoMirrorStatus for a caller that has an actor and
// no settings in hand -- the photo generation picker on the backup screen.
func (db *DB) GetPhotoMirrorStatus(ctx context.Context, actor Actor) (*PhotoMirrorStatus, error) {
	if err := RequireAdmin(actor); err != nil {
		return nil, err
	}
	settings, err := db.loadSettings(ctx)
	if err != nil {
		return nil, err
	}
	return db.PhotoMirrorStatus(ctx, settings)
}

func treeSize(dir string) (int, int64) {
	var files int
	var bytes int64
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // an unreadable subtree is reported as zero, not as a failed status read
		}
		if info, err := d.Info(); err == nil {
			files++
			bytes += info.Size()
		}
		return nil
	})
	return files, bytes
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n/div >= unit && exp < 4 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTP"[exp])
}

/* ------------------------------------------------------------- restore ---- */

// RestorePhotos copies a generation back into uploads/.
//
// Anything it would overwrite is set aside first, under the photo backup
// directory rather than under uploads/: /files/ serves uploads without a
// session, so a folder of replaced photos left there would be published.
func (db *DB) RestorePhotos(ctx context.Context, actor Actor, generation string) (int, error) {
	if err := RequireAdmin(actor); err != nil {
		return 0, err
	}
	settings, err := db.loadSettings(ctx)
	if err != nil {
		return 0, err
	}
	dir := db.photoBackupDir(settings)
	if dir == "" {
		return 0, fmt.Errorf("%w: no photo backup folder is set", ErrNotConfigured)
	}
	if db.UploadsDir == "" {
		return 0, fmt.Errorf("%w: UPLOADS_DIR is not set, so there is nowhere to restore photos to", ErrNotConfigured)
	}
	if err := validGenerationName(generation); err != nil {
		return 0, err
	}
	src := filepath.Join(dir, generation)
	if info, err := os.Stat(src); err != nil || !info.IsDir() {
		return 0, fmt.Errorf("%w: there is no photo generation named %s", ErrNotFound, generation)
	}

	aside := filepath.Join(dir, "pre-restore-"+time.Now().Format("2006-01-02T150405"))
	restored := 0
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || rel == "." {
			return err
		}
		dst := filepath.Join(db.UploadsDir, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if existing, err := os.Stat(dst); err == nil && !existing.IsDir() {
			if err := copyFile(dst, filepath.Join(aside, rel), existing); err != nil {
				return fmt.Errorf("set aside the photo at %s before replacing it: %w", dst, err)
			}
		}
		if err := copyFile(path, dst, info); err != nil {
			return err
		}
		restored++
		return nil
	})
	if err != nil {
		return restored, fmt.Errorf("restore photos: %w", err)
	}
	return restored, nil
}

// DeletePhotoGeneration removes one frozen generation. The live one is
// refused: deleting the folder the next mirror run writes into would make that
// run a full copy, which is the behaviour the generational design exists to
// avoid, and it would do it silently.
func (db *DB) DeletePhotoGeneration(ctx context.Context, actor Actor, generation string) error {
	if err := RequireAdmin(actor); err != nil {
		return err
	}
	settings, err := db.loadSettings(ctx)
	if err != nil {
		return err
	}
	dir := db.photoBackupDir(settings)
	if dir == "" {
		return fmt.Errorf("%w: no photo backup folder is set", ErrNotConfigured)
	}
	if err := validGenerationName(generation); err != nil {
		return err
	}
	raw, _ := os.ReadFile(filepath.Join(dir, currentFile))
	if strings.TrimSpace(string(raw)) == generation {
		return fmt.Errorf("%w: %s is the generation photos are being copied into now. Wait until it rolls over, or change the retention period", ErrConflict, generation)
	}
	target := filepath.Join(dir, generation)
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		return fmt.Errorf("%w: there is no photo generation named %s", ErrNotFound, generation)
	}
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("delete %s: %w", generation, err)
	}
	return nil
}

// validGenerationName is the guard between an HTTP path parameter and a
// RemoveAll. A generation name comes from a list this server wrote, but it
// arrives as client text, and "gen-" plus no separators is the whole of what a
// real one looks like.
func validGenerationName(name string) error {
	if !strings.HasPrefix(name, generationPfx) ||
		strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return fmt.Errorf("%w: %q is not a photo generation name", ErrInvalid, name)
	}
	return nil
}
