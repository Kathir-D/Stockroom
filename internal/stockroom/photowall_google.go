package stockroom

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

// Turning the sign-in photo wall on from the admin panel: a "Sign in with
// Google" button on Admin → Photo wall that creates the read-only rclone
// remote and starts the wall on the spot, with no terminal, no .env edit and
// no restart.
//
// **The sign-in is the switch.** Until 2026-09-24 the switch was
// SIGNIN_PHOTOS_REMOTE in .env, read once at boot, and the screen could only
// say "edit a file and restart the server" -- the one instruction the Phase 7
// rule (no admin ever edits a file) exists to rule out. Now the wall runs
// exactly when rclone has a remote for it, and the only thing that creates one
// is an admin signing in to Google. So "is it on?" and "has anybody connected
// a Google account?" are one question, answered by the credential itself
// rather than by a flag that could disagree with it: restore this database
// onto a new machine and the wall is off there, with the button asking for a
// sign-in, instead of on and failing against a remote that does not exist.
//
// The token goes where it always went -- rclone's own config file, outside
// the repository -- and nowhere else: not into app_settings, so it is never in
// a backup; not into any response; not into the log. What the screen learns
// is `rclone listremotes`, which prints names only.

// DefaultPhotoWallRemote is the rclone remote the sign-in button creates.
// SIGNIN_PHOTOS_REMOTE renames it, for a machine whose remote was made by hand
// under another name before the button existed.
const DefaultPhotoWallRemote = "gdrive-photos"

// PhotoWallConfig is the install-time half of the wall: where the tiles are
// cached, how many, and which rclone remote to read through. Everything an
// admin changes -- the folder -- lives in app_settings instead.
type PhotoWallConfig struct {
	Remote           string
	Dir              string
	Count            int
	Batch            int
	TTL              time.Duration
	ManifestInterval time.Duration
}

type photoWallParts struct {
	wall   *PhotoWall
	source *DrivePhotoSource
}

// photoWallParts returns the running reel and its source; both nil while the
// wall is off. Every method on either is nil-safe.
func (db *DB) photoWallParts() (*PhotoWall, *DrivePhotoSource) {
	if p := db.photoWall.Load(); p != nil {
		return p.wall, p.source
	}
	return nil, nil
}

// SignInPhotoWall is the running reel, or nil. For server/, whose two public
// routes hand out and serve its tiles.
func (db *DB) SignInPhotoWall() *PhotoWall {
	wall, _ := db.photoWallParts()
	return wall
}

// SetPhotoWall installs a reel and its source. StartPhotoWall is the
// production path; this is exported for tests that build a reel by hand.
func (db *DB) SetPhotoWall(wall *PhotoWall, source *DrivePhotoSource) {
	db.photoWall.Store(&photoWallParts{wall: wall, source: source})
}

// StartPhotoWall is called once at boot. It records the configuration, then
// starts the wall if Google has already been signed in to for it.
//
// ctx is the server's lifetime, and is kept: a sign-in finishing later starts
// the wall from an HTTP request, and a reel whose goroutines died with that
// request would stop a second after it started.
//
// Nothing here is fatal, and nothing is returned. §9's invariant is that no
// failure in this subsystem may delay, block or visibly break sign-in; every
// reason the wall is off is logged once and shown on the admin screen.
func (db *DB) StartPhotoWall(ctx context.Context, cfg PhotoWallConfig) {
	cfg.Remote = photoWallRemoteName(cfg.Remote)

	db.photoWallStart.Lock()
	defer db.photoWallStart.Unlock()
	db.photoWallCfg = &cfg
	db.photoWallCtx = ctx

	if !rcloneInstalled() {
		log.Printf("sign-in photo wall: off, rclone is not installed")
		return
	}
	connected, err := rcloneHasRemote(ctx, cfg.Remote)
	if err != nil {
		log.Printf("warning: sign-in photo wall: could not read rclone's remotes: %v", err)
		return
	}
	if !connected {
		log.Printf("sign-in photo wall: off until an admin signs in to Google for it in Admin → Photo wall")
		return
	}
	if err := db.startPhotoWallLocked(ctx); err != nil {
		log.Printf("warning: sign-in photo wall disabled: %v", err)
	}
}

// startPhotoWallLocked builds and starts the reel and its source, once. The
// caller holds photoWallStart. A wall already running is left alone: rclone
// reads its config on every call, so a reconnected token is picked up by the
// next listing or download with nothing here restarting.
func (db *DB) startPhotoWallLocked(ctx context.Context) error {
	cfg, runCtx := db.photoWallCfg, db.photoWallCtx
	if cfg == nil || runCtx == nil {
		return fmt.Errorf("%w: this server was started without the sign-in photo wall", ErrNotConfigured)
	}
	if wall, _ := db.photoWallParts(); wall != nil {
		return nil
	}

	// The live folder comes from app_settings, never from .env (§8), so a
	// folder chosen before the sign-in -- or kept from an earlier one -- is
	// read straight away.
	folderID, folderLabel, err := db.PhotoWallFolder(ctx)
	if err != nil {
		return err
	}
	source, err := NewDrivePhotoSource(DrivePhotoSourceOptions{
		Remote:          cfg.Remote,
		FolderID:        folderID,
		Dir:             cfg.Dir,
		RefreshInterval: cfg.ManifestInterval,
	})
	if err != nil {
		return err
	}
	wall, err := NewPhotoWall(PhotoWallOptions{
		Dir:    cfg.Dir,
		Count:  cfg.Count,
		Batch:  cfg.Batch,
		TTL:    cfg.TTL,
		Source: source,
	})
	if err != nil {
		return fmt.Errorf("check SIGNIN_PHOTOS_DIR: %w", err)
	}
	db.SetPhotoWall(wall, source)

	go wall.Run(runCtx)
	// After the reel, because NewPhotoWall wipes the cache directory and the
	// source reads its manifest back out of it.
	go source.Run(runCtx)

	log.Printf("sign-in photo wall: on, reading Google Drive through rclone remote %q, caching in %s", cfg.Remote, cfg.Dir)
	if folderID == "" {
		log.Printf("sign-in photo wall: no Drive folder set, so the wall stays empty until one is chosen in Admin → Photo wall")
	} else {
		log.Printf("sign-in photo wall reading %q", folderLabel)
	}
	return nil
}

// photoWallConfig is what StartPhotoWall recorded, or nil before it ran.
func (db *DB) photoWallConfig() *PhotoWallConfig {
	db.photoWallStart.Lock()
	defer db.photoWallStart.Unlock()
	return db.photoWallCfg
}

// photoWallRemoteName normalises the configured remote name. A trailing
// colon is how rclone *prints* a remote, so it is a natural thing to type
// into .env; `rclone config create` wants the bare name.
func photoWallRemoteName(remote string) string {
	remote = strings.TrimSuffix(strings.TrimSpace(remote), ":")
	if remote == "" {
		return DefaultPhotoWallRemote
	}
	return remote
}

// rcloneHasRemote reports whether rclone has a remote by this name.
// `listremotes` prints names only -- never a token -- which is why it is the
// question asked, and not `config show`.
func rcloneHasRemote(ctx context.Context, remote string) (bool, error) {
	out, err := runRclone(ctx, 15*time.Second, "listremotes")
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSuffix(strings.TrimSpace(line), ":") == remote {
			return true, nil
		}
	}
	return false, nil
}

/* ------------------------------------------------------------ sign-in ---- */

// ConnectPhotoWallGoogle is POST /admin/photo-wall/google/connect: start
// `rclone authorize` for a read-only token and return the link to open.
//
// The same flow the backup's Connect button uses (drive_authorize.go), with
// one difference that matters: it asks Google for drive.readonly. The
// backup's token can write to the whole of Drive; this one can read and do
// nothing else, so a machine left open on the photo wall can never be used to
// change or delete a photograph.
func (db *DB) ConnectPhotoWallGoogle(ctx context.Context, actor Actor) (DriveConnectResult, error) {
	if err := RequireAdmin(actor); err != nil {
		return DriveConnectResult{}, err
	}
	if db.photoWallConfig() == nil {
		return DriveConnectResult{}, fmt.Errorf("%w: this server was started without the sign-in photo wall", ErrNotConfigured)
	}
	if !rcloneInstalled() {
		return DriveConnectResult{}, fmt.Errorf("%w: rclone is not installed. On this machine run `brew install rclone` (macOS) or `winget install Rclone.Rclone` (Windows), then reload this page", ErrNotConfigured)
	}
	return startDriveAuthorize(ctx, driveScopeReadOnly)
}

// FinishPhotoWallGoogle is POST /admin/photo-wall/google/finish: write the
// rclone remote with the token Google sent back, log who did it, and start
// the wall.
//
// `rclone config create` on an existing name replaces it, so Reconnect is the
// same call -- and it is the fix for an expired sign-in that until now needed
// `rclone config reconnect` typed into a terminal (§9).
func (db *DB) FinishPhotoWallGoogle(ctx context.Context, actor Actor, id, code string) (PhotoWallStatus, error) {
	if err := RequireAdmin(actor); err != nil {
		return PhotoWallStatus{}, err
	}
	cfg := db.photoWallConfig()
	if cfg == nil {
		return PhotoWallStatus{}, fmt.Errorf("%w: this server was started without the sign-in photo wall", ErrNotConfigured)
	}
	token, err := finishDriveAuthorize(ctx, id, strings.TrimSpace(code), driveScopeReadOnly)
	if err != nil {
		return PhotoWallStatus{}, err
	}
	// root_folder_id is left blank on purpose (§3): the folder is supplied per
	// command, so the remote can read whichever folder is pasted next.
	if _, err := runRclone(ctx, time.Minute, "config", "create", cfg.Remote, "drive",
		"config_is_local=false", "token="+token, "scope="+driveScopeReadOnly); err != nil {
		// rclone quotes what it could not parse, and what it was given is the
		// token. The message is going to a screen and a log.
		msg := strings.ReplaceAll(err.Error(), token, "<token>")
		return PhotoWallStatus{}, fmt.Errorf("save the Google sign-in: %s", msg)
	}
	if err := db.logPhotoWallGoogle(ctx, actor, cfg.Remote); err != nil {
		return PhotoWallStatus{}, err
	}

	db.photoWallStart.Lock()
	_, running := db.photoWallParts()
	startErr := db.startPhotoWallLocked(ctx)
	db.photoWallStart.Unlock()
	if startErr != nil {
		return PhotoWallStatus{}, fmt.Errorf("signed in to Google, but the photo wall could not start: %w", startErr)
	}
	// A reconnect is usually the answer to a listing that failed on an
	// expired token. Without this the source would wait out its five-minute
	// retry while the admin stares at the error they just fixed.
	if running != nil && running.Status().LastError != "" {
		running.Rebuild()
	}

	f, err := db.loadPhotoWallFolder(ctx)
	if err != nil {
		return PhotoWallStatus{}, err
	}
	return db.photoWallStatus(ctx, f), nil
}

// logPhotoWallGoogle records who connected the account. The remote's name
// only; which Google account it is, rclone does not say and the token is not
// written anywhere but rclone's own file.
func (db *DB) logPhotoWallGoogle(ctx context.Context, actor Actor, remote string) error {
	var actorID *string
	if actor.ID != "" && !actor.trustedCLI {
		id := actor.ID
		actorID = &id
	}
	if _, err := db.Pool.Exec(ctx, `
		insert into activity_log (asset_id, actor_id, action, details)
		values (null, $1, 'signin_photo_wall_google', jsonb_build_object('remote', $2::text))`,
		actorID, remote); err != nil {
		return mapPgError("log the photo wall's Google sign-in", err)
	}
	return nil
}
