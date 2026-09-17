package stockroom

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"
)

// Off-site targets (docs/design/backup.md §C.3).
//
// Two of them, both active when both are configured: Google Drive through the
// rclone binary, and GitHub through its REST API. Drive won the "which can a
// non-technical teacher set up" question outright -- handover is a Google
// sign-in screen they already recognise -- and GitHub is kept because it needs
// nothing installed at all and is the harder of the two for a school firewall
// to block.
//
// The interface differs from the design document in one place: Push takes a
// pushRequest rather than (dir, Manifest). Both targets need the base
// directory as well as the dated one -- rclone copies the whole tree, GitHub
// needs the archive's path and whether it is encrypted -- and threading four
// values through a struct beats four positional arguments nobody can read.

// BackupVersion is one past backup a target can hand back: a date to show in
// the picker and an id to fetch it by.
type BackupVersion struct {
	// ID is opaque to the UI and meaningful to the target: a path on Drive, a
	// commit sha on GitHub, a folder/file pair locally.
	ID    string    `json:"id"`
	Label string    `json:"label"`
	At    time.Time `json:"at"`
	Bytes int64     `json:"bytes"`
	Note  string    `json:"note"`
}

// pushRequest is one night's output, as a target sees it.
type pushRequest struct {
	BaseDir     string
	Dir         string
	Day         string
	ArchivePath string
	Encrypted   bool
	Manifest    Manifest
}

// BackupTarget is what a place backups are pushed to has to be able to do.
// Versions and Fetch are what make "restore from a date" a feature rather than
// a download-it-yourself instruction.
type BackupTarget interface {
	Name() string
	Push(ctx context.Context, req pushRequest) (ref string, err error)
	Versions(ctx context.Context) ([]BackupVersion, error)
	Fetch(ctx context.Context, id string) (io.ReadCloser, error)
	Test(ctx context.Context) error
}

const (
	driveTargetName  = "drive"
	githubTargetName = "github"
)

// targetEnabled is the one reading of "is this target switched on", shared by
// the push loop, the status screen and the staleness warning, so a disabled
// target cannot be stale on one surface and absent on another.
func targetEnabled(s Settings, name string) bool {
	switch name {
	case driveTargetName:
		return s.DriveEnabled && s.DriveRemote != ""
	case githubTargetName:
		return s.GitHubEnabled && s.GitHubRepo != "" && s.GitHubToken != ""
	}
	return false
}

// activeTargets returns the targets a run should push to. That is the whole of
// "push to both when both are set up".
func activeTargets(s Settings) []BackupTarget {
	out := []BackupTarget{}
	if targetEnabled(s, driveTargetName) {
		out = append(out, newDriveTarget(s))
	}
	if targetEnabled(s, githubTargetName) {
		out = append(out, newGitHubTarget(s))
	}
	return out
}

// targetByName builds one target for the browse-and-restore paths, which name
// a target rather than looping over all of them.
func targetByName(s Settings, name string) (BackupTarget, error) {
	switch name {
	case driveTargetName:
		if !targetEnabled(s, driveTargetName) {
			return nil, fmt.Errorf("%w: Google Drive backups are not set up", ErrNotConfigured)
		}
		return newDriveTarget(s), nil
	case githubTargetName:
		if !targetEnabled(s, githubTargetName) {
			return nil, fmt.Errorf("%w: GitHub backups are not set up", ErrNotConfigured)
		}
		return newGitHubTarget(s), nil
	}
	return nil, fmt.Errorf("%w: %q is not a backup target", ErrInvalid, name)
}

// pushToTargets sends one run's output to every enabled target and reports
// each outcome separately.
//
// A failure here is never the run's failure. The restorable archive is already
// on disk, and one target being unreachable says nothing about the other: the
// point of having two is that a school firewall blocking one leaves the other
// working. What a failure must do is be *visible*, which is why each result
// carries its own error into the state file and onto the backup screen.
func (db *DB) pushToTargets(ctx context.Context, settings Settings, dir string, manifest Manifest) []TargetResult {
	targets := activeTargets(settings)
	out := make([]TargetResult, 0, len(targets))
	if len(targets) == 0 {
		return out
	}
	day := filepath.Base(dir)
	req := pushRequest{
		BaseDir:     filepath.Dir(dir),
		Dir:         dir,
		Day:         day,
		ArchivePath: filepath.Join(dir, archiveName(day, settings.Encrypted())),
		Encrypted:   settings.Encrypted(),
		Manifest:    manifest,
	}
	for _, t := range targets {
		ref, err := t.Push(ctx, req)
		if err != nil {
			out = append(out, TargetResult{Target: t.Name(), Error: err.Error()})
			continue
		}
		out = append(out, TargetResult{Target: t.Name(), OK: true, Ref: ref})
	}
	return out
}

// ListBackupVersions is the restore-by-date picker's read.
func (db *DB) ListBackupVersions(ctx context.Context, actor Actor, target string) ([]BackupVersion, error) {
	if err := RequireAdmin(actor); err != nil {
		return nil, err
	}
	if target == localTarget {
		return db.ListLocalBackups(ctx, actor)
	}
	settings, err := db.loadSettings(ctx)
	if err != nil {
		return nil, err
	}
	t, err := targetByName(settings, target)
	if err != nil {
		return nil, err
	}
	// Bounded: a target that has gone quiet must not hang the admin panel
	// until the browser gives up with no message at all.
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return t.Versions(ctx)
}

// TestBackupTarget drives the settings screen's "Test connection", which
// exists so a misconfiguration is found by somebody standing in front of the
// machine rather than by nothing happening at 2 a.m.
func (db *DB) TestBackupTarget(ctx context.Context, actor Actor, target string) error {
	if err := RequireAdmin(actor); err != nil {
		return err
	}
	settings, err := db.loadSettings(ctx)
	if err != nil {
		return err
	}
	t, err := targetByName(settings, target)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return t.Test(ctx)
}
