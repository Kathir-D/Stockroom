package stockroom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"
	"time"
)

// Google Drive, through the rclone binary (docs/design/backup.md §E.3).
//
// rclone owns the OAuth token and its refresh; this package never talks to the
// Drive API and holds no Google credential. That is the whole reason it is
// here rather than a hand-written OAuth flow: token refresh, resumable
// uploads and Drive's quirks are somebody else's solved problem, and the
// secret lives in rclone's own config file rather than in a column this
// application backs up.

const (
	rcloneBinary = "rclone"
	// Drive is a network round trip on a school connection at 2 a.m. Ten
	// minutes is far longer than a few hundred KB needs and short enough that
	// a wedged process does not hold the backup lock all night.
	driveTimeout = 10 * time.Minute
)

// driveRetryDelays is the backoff between attempts. A push that fails because
// the uplink is briefly down should not wait until tomorrow night to try
// again, and a push that fails because the token was revoked should stop
// quickly and say so rather than grinding for an hour.
var driveRetryDelays = []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute}

type driveTarget struct {
	remote string
	path   string
	// retries is separated out so tests can drive the loop without sleeping
	// for eight minutes.
	retries []time.Duration
}

func newDriveTarget(s Settings) *driveTarget {
	p := s.DrivePath
	if p == "" {
		p = "stockroom"
	}
	return &driveTarget{remote: s.DriveRemote, path: p, retries: driveRetryDelays}
}

func (d *driveTarget) Name() string { return driveTargetName }

// remotePath is the "remote:path" rclone argument, with an optional suffix.
func (d *driveTarget) remotePath(suffix ...string) string {
	p := d.path
	if len(suffix) > 0 {
		p = path.Join(append([]string{p}, suffix...)...)
	}
	return d.remote + ":" + p
}

// Push mirrors the whole backup directory up, not just tonight's folder.
//
// Copying the tree means a night the uplink was down is caught up the next
// time one succeeds, rather than being lost because the only push that would
// ever have carried it already failed. rclone copies by name and size, so
// re-offering a folder that is already there costs a listing and no transfer.
func (d *driveTarget) Push(ctx context.Context, req pushRequest) (string, error) {
	args := []string{"copy", req.BaseDir, d.remotePath(),
		// The log and the run-state file describe *this machine's* runs.
		// Pushing them would mean the copy on Drive claims a history the
		// files beside it do not have.
		"--exclude", "*.log", "--exclude", "*.log.1", "--exclude", stateFile,
		// Staging directories are named .partial-*; a run in progress must
		// not be published as though it were finished.
		"--exclude", ".partial-*/**",
		// What a file manager leaves behind is not part of the backup. An
		// admin who opens the backup folder in Finder to check on it puts a
		// .DS_Store beside the archive, and `copy` would push that to Drive
		// too -- so the off-site copy gains files the run never wrote, and
		// anybody auditing what left the machine has to account for them.
		"--exclude", ".DS_Store", "--exclude", "._*",
		"--exclude", "Thumbs.db", "--exclude", "desktop.ini",
	}
	if req.Encrypted {
		// The readable CSVs are deliberately *not* encrypted, because the
		// whole point of them is that somebody can open one in Excel. That is
		// fine while they sit on the closet PC and wrong the moment they leave
		// it: an encrypted archive beside a plaintext roster of student
		// numbers protects nothing.
		args = append(args, "--exclude", archiveInventory, "--exclude", archiveAccounts)
	}

	var lastErr error
	for attempt := 0; ; attempt++ {
		_, err := runRclone(ctx, driveTimeout, args...)
		if err == nil {
			return d.remotePath(req.Day), nil
		}
		lastErr = err
		if attempt >= len(d.retries) || ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(d.retries[attempt]):
		}
	}
	return "", fmt.Errorf("push to Google Drive: %w", lastErr)
}

// rcloneEntry is the subset of `rclone lsjson` this reads.
type rcloneEntry struct {
	Path    string    `json:"Path"`
	Name    string    `json:"Name"`
	Size    int64     `json:"Size"`
	ModTime time.Time `json:"ModTime"`
	IsDir   bool      `json:"IsDir"`
}

// Versions lists the archives on Drive, newest first.
func (d *driveTarget) Versions(ctx context.Context) ([]BackupVersion, error) {
	out, err := runRclone(ctx, driveTimeout, "lsjson", d.remotePath(), "-R", "--files-only")
	if err != nil {
		return nil, fmt.Errorf("list Google Drive backups: %w", err)
	}
	var entries []rcloneEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Errorf("list Google Drive backups: %w", err)
	}

	versions := []BackupVersion{}
	for _, e := range entries {
		if e.IsDir || !strings.HasPrefix(e.Name, "backup-") {
			continue
		}
		versions = append(versions, BackupVersion{
			ID:    e.Path,
			Label: strings.TrimSuffix(path.Dir(e.Path), "."),
			At:    e.ModTime,
			Bytes: e.Size,
		})
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i].At.After(versions[j].At) })
	return versions, nil
}

// Fetch streams one archive back down. The bytes are the same zip the local
// export wrote, so the restore path does not care which target it came from.
func (d *driveTarget) Fetch(ctx context.Context, id string) (io.ReadCloser, error) {
	if err := safeRemoteID(id); err != nil {
		return nil, err
	}
	out, err := runRclone(ctx, driveTimeout, "cat", d.remotePath(id))
	if err != nil {
		return nil, fmt.Errorf("download from Google Drive: %w", err)
	}
	return io.NopCloser(bytes.NewReader(out)), nil
}

// Test is what the settings screen's button runs. Listing directories proves
// the token is live and the remote resolves, which is the pair of things that
// actually break.
func (d *driveTarget) Test(ctx context.Context) error {
	if _, err := runRclone(ctx, time.Minute, "lsd", d.remote+":"); err != nil {
		return fmt.Errorf("Google Drive: %w", err)
	}
	return nil
}

// safeRemoteID refuses an id that could climb out of the configured folder.
// Ids come from Versions, which this server produced -- but they arrive back
// as client text, and a path is a path.
func safeRemoteID(id string) error {
	if id == "" || strings.HasPrefix(id, "/") || strings.Contains(id, "..") || strings.Contains(id, ":") {
		return fmt.Errorf("%w: %q is not a backup on this remote", ErrInvalid, id)
	}
	return nil
}

// runRclone runs one rclone command and returns its stdout.
//
// A missing binary is ErrNotConfigured naming the install command, not a
// crash and not a 500: rclone is an installer step (§F.1), so the machine
// simply has not been set up yet, and the person reading the message is the
// one who would install it.
func runRclone(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	if _, err := exec.LookPath(rcloneBinary); err != nil {
		return nil, fmt.Errorf("%w: rclone is not installed. On this machine run `brew install rclone` (macOS) or `winget install Rclone.Rclone` (Windows), then try again", ErrNotConfigured)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, rcloneBinary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// rclone reads its own config, which holds the OAuth token. Inheriting the
	// environment is what lets RCLONE_CONFIG point somewhere else on a machine
	// that needs it to.
	cmd.Env = os.Environ()

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("rclone timed out after %s: %s", timeout, oneLine(msg))
		}
		return nil, fmt.Errorf("%s", oneLine(msg))
	}
	return stdout.Bytes(), nil
}

/* ------------------------------------------------------------- connect ---- */

// DriveConnectResult is the first half of signing in to Google from the admin
// panel (ConnectGoogle, google_admin.go): the URL to open and the handle that
// identifies this attempt.
type DriveConnectResult struct {
	URL string `json:"url"`
	// ID is the pending authorization this token will be handed back to.
	ID string `json:"id"`
	// PasteCommand is what to run on another machine when the link will not
	// open on this one; its output is the block the dialog's paste box takes.
	// It carries the scope, which is why the server supplies it rather than
	// the screen spelling it out.
	PasteCommand string `json:"paste_command"`
}
