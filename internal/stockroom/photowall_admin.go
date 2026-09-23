package stockroom

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Admin control of the sign-in photo wall
// (docs/design/signin-photo-wall.html §7): which Drive folder is live, and
// everything the admin screen needs to tell whether the right one is.
//
// The rule the whole file is shaped around is that **a Drive folder link is a
// credential, not a label**. A folder shared "anyone with the link" is
// readable by whoever holds the URL, so echoing it back into the admin panel
// would put the entire folder one copy-paste away from anybody who reaches
// that screen. The project already holds this rule for app_settings.
// github_token (CLAUDE.md §11); this is that rule reaching one more value.
//
// So: the field is write-only, the id is redacted from the backup export
// (exportRedactions), the activity_log row names the folder's label and never
// its id, and no status this file returns carries the id or a drive.google.com
// URL. There is a test asserting the last part, because the failure mode is a
// debug field somebody adds in a year.

// photoProbeTimeout bounds the reachability check a paste is validated
// against. One bounded listing of the folder's top level over a school uplink
// is seconds; a minute is the point at which something is wrong, and an admin
// is standing at the screen waiting for the answer.
const photoProbeTimeout = 60 * time.Second

// maxPhotoWallLabel caps the typed label. It is shown in a status line and
// written into activity_log; nothing needs a paragraph.
const maxPhotoWallLabel = 120

// driveFolderID matches a Google Drive file id: URL-safe base64-ish, and long.
// The bound is a paste guard rather than a format -- Drive ids have grown over
// the years and nothing documents a maximum -- but it is what stops a whole
// sentence, or a URL this parser failed to recognise, from being written to
// the column as if it were an id.
var driveFolderID = regexp.MustCompile(`^[A-Za-z0-9_-]{10,200}$`)

// driveLinkForms is the error message for anything unparseable, shown as
// written. §7: requiring a hand-extracted id would be the kind of demand that
// gets a folder misconfigured once and then avoided forever, so the refusal
// has to name what *will* work rather than say "invalid".
const driveLinkForms = `paste a Google Drive folder link, for example ` +
	`https://drive.google.com/drive/folders/1AbC…xyz (with or without ?usp=sharing), ` +
	`https://drive.google.com/drive/u/0/folders/1AbC…xyz, ` +
	`https://drive.google.com/open?id=1AbC…xyz, or the folder id on its own`

// ParseDriveFolderLink turns whatever Drive's Share button put on the
// clipboard into a folder id.
//
// A *file* link (…/file/d/…) and a Docs/Sheets/Slides link (…/document/d/…)
// are refused rather than silently accepted, even though both carry an id in
// the same shape: the wall lists a folder, and pointing it at a single file
// would produce an empty wall with nothing on any screen saying why.
func ParseDriveFolderLink(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("%w: %s", ErrInvalid, driveLinkForms)
	}

	// A bare id, pasted from somewhere other than the Share button.
	if !strings.Contains(s, "/") && !strings.Contains(s, ":") {
		if driveFolderID.MatchString(s) {
			return s, nil
		}
		return "", fmt.Errorf("%w: %q is not a Drive folder id — %s", ErrInvalid, truncate(s, 40), driveLinkForms)
	}

	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%w: %s", ErrInvalid, driveLinkForms)
	}
	host := strings.ToLower(u.Hostname())
	if host != "drive.google.com" {
		return "", fmt.Errorf("%w: %q is not a Google Drive link — %s", ErrInvalid, host, driveLinkForms)
	}

	// /drive/folders/<id> and /drive/u/0/folders/<id> differ only by the
	// account segment, so the parser looks for the "folders" segment rather
	// than counting from the front.
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, p := range parts {
		if p == "folders" && i+1 < len(parts) {
			return validFolderID(parts[i+1])
		}
	}
	// /open?id=<id>, the old share format, which is still what some Drive
	// clients copy.
	if id := u.Query().Get("id"); id != "" {
		return validFolderID(id)
	}
	if len(parts) > 0 && parts[0] == "file" {
		return "", fmt.Errorf("%w: that is a link to a single file, not to a folder. Open the folder in Drive and use its Share link", ErrInvalid)
	}
	return "", fmt.Errorf("%w: that Drive link does not name a folder — %s", ErrInvalid, driveLinkForms)
}

func validFolderID(id string) (string, error) {
	if !driveFolderID.MatchString(id) {
		return "", fmt.Errorf("%w: %q is not a Drive folder id — %s", ErrInvalid, truncate(id, 40), driveLinkForms)
	}
	return id, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

/* -------------------------------------------------------------- status ---- */

// PhotoWallStatus is the admin screen's one read.
//
// Nothing here is the folder id and nothing here is a URL. What an admin
// actually needs to know is whether the right photographs are showing, and
// the preview strip answers that better than any identifier could -- while
// revealing nothing the sign-in screen does not already show to everyone who
// walks up to the machine.
type PhotoWallStatus struct {
	// Enabled is whether a reel exists at all: SIGNIN_PHOTOS_REMOTE set, the
	// cache directory usable. False is the common case and not an error.
	Enabled bool `json:"enabled"`
	// RcloneInstalled is reported separately because it is the one failure an
	// admin can fix without leaving the machine, and because a wall that is
	// enabled with no rclone looks exactly like one that is simply empty.
	RcloneInstalled bool `json:"rclone_installed"`
	// CanSetFolder is whether SetPhotoWallFolder would get past its first
	// gate, and exists so the screen can refuse the paste before the admin
	// types it rather than after.
	//
	// It is a field rather than something the screen infers from Enabled and
	// RcloneInstalled, because those two do not add up to the condition the
	// write actually checks -- a reel can exist with no source, and rclone can
	// be installed with no remote configured. Two ways of asking the same
	// question is two things to keep in step, and the one that drifts is
	// always the copy in the UI: the form would stay enabled and every press
	// would be a 503 the admin has no way to predict.
	CanSetFolder bool `json:"can_set_folder"`

	// FolderSet is whether a Drive folder has been chosen. Unset is normal on
	// a fresh install: the wall stays empty and sign-in is unaffected.
	FolderSet bool `json:"folder_set"`
	// FolderLabel is the name a person typed for it. Never the id.
	FolderLabel string     `json:"folder_label"`
	ChangedAt   *time.Time `json:"changed_at"`
	ChangedBy   string     `json:"changed_by"`

	// Listing and ListedSoFar are §3's streamed count. A large folder takes
	// minutes to list and the wall is empty for all of them, which is correct
	// but indistinguishable from a broken feature unless the screen can say
	// "rebuilding — 12,400 files listed so far".
	Listing     bool       `json:"listing"`
	ListedSoFar int        `json:"listed_so_far"`
	PhotoCount  int        `json:"photo_count"`
	BuiltAt     *time.Time `json:"built_at"`

	// Ready and Served are the reel's counts: how many tiles are waiting and
	// how many are out in a browser somewhere waiting to expire.
	Ready  int `json:"ready"`
	Served int `json:"served"`

	LastError   string     `json:"last_error"`
	LastErrorAt *time.Time `json:"last_error_at"`
}

// photoWallFolder is the settings row's half of the status.
type photoWallFolder struct {
	id        string
	label     string
	changedAt *time.Time
	changedBy string
}

// PhotoWallFolder reads the live folder out of app_settings. Exported for
// server/main.go, which needs the id to build the source at boot and is the
// only caller outside this package -- the id crosses no other boundary.
//
// .env's SIGNIN_PHOTOS_FOLDER_ID seeds this column on first boot
// (EnsureSettings) and is ignored afterwards, so a folder an admin pasted is
// never out-voted by an environment variable on the next restart (§8).
func (db *DB) PhotoWallFolder(ctx context.Context) (id, label string, err error) {
	f, err := db.loadPhotoWallFolder(ctx)
	if err != nil {
		return "", "", err
	}
	return f.id, f.label, nil
}

func (db *DB) loadPhotoWallFolder(ctx context.Context) (photoWallFolder, error) {
	var f photoWallFolder
	var id, label, changedBy *string
	err := db.Pool.QueryRow(ctx, `
		select s.signin_photos_folder_id, s.signin_photos_label, s.signin_photos_changed_at,
		       coalesce(nullif(trim(coalesce(p.first_name,'') || ' ' || coalesce(p.last_name,'')), ''), p.full_name)
		from app_settings s
		left join profiles p on p.id = s.signin_photos_changed_by
		where s.id = true`).Scan(&id, &label, &f.changedAt, &changedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		// The migration inserts the row; a database that has had it deleted by
		// hand gets the same treatment loadSettings gives it -- an unset
		// folder, not a failure.
		return photoWallFolder{}, nil
	}
	if err != nil {
		return photoWallFolder{}, fmt.Errorf("read the photo wall folder: %w", err)
	}
	f.id, f.label, f.changedBy = deref(id), deref(label), deref(changedBy)
	return f, nil
}

// GetPhotoWallStatus is GET /admin/photo-wall.
func (db *DB) GetPhotoWallStatus(ctx context.Context, actor Actor) (PhotoWallStatus, error) {
	if err := RequireAdmin(actor); err != nil {
		return PhotoWallStatus{}, err
	}
	f, err := db.loadPhotoWallFolder(ctx)
	if err != nil {
		return PhotoWallStatus{}, err
	}
	return db.photoWallStatus(f), nil
}

// photoWallStatus composes the three places the answer lives: the settings
// row, the source's manifest progress, and the reel's counts.
func (db *DB) photoWallStatus(f photoWallFolder) PhotoWallStatus {
	src := db.PhotoWallSource.Status()
	ready, served := db.PhotoWall.Counts()

	st := PhotoWallStatus{
		Enabled:         db.PhotoWall != nil,
		RcloneInstalled: rcloneInstalled(),
		// Deliberately the same expression SetPhotoWallFolder guards on, and
		// the only one, so the screen and the write cannot disagree.
		CanSetFolder: db.PhotoWallSource != nil,
		FolderSet:    f.id != "",
		FolderLabel:  f.label,
		ChangedAt:    f.changedAt,
		ChangedBy:    f.changedBy,
		Listing:      src.Listing,
		ListedSoFar:  src.ListedSoFar,
		PhotoCount:   src.PhotoCount,
		Ready:        ready,
		Served:       served,
		LastError:    src.LastError,
	}
	if !src.BuiltAt.IsZero() {
		at := src.BuiltAt
		st.BuiltAt = &at
	}
	if !src.LastErrorAt.IsZero() {
		at := src.LastErrorAt
		st.LastErrorAt = &at
	}
	// The reel's failures, which until §10's manual pass reached no screen at
	// all: the source only knows about listings, so a wall that had a manifest
	// of two thousand photographs and was resting after 25 failed downloads
	// reported no error while showing nothing. Newer wins, because the older
	// of the two has usually been overtaken by the newer.
	if err, at := db.PhotoWall.fillFailure(); err != nil && (st.LastErrorAt == nil || at.After(*st.LastErrorAt)) {
		st.LastError = describeFillFailure(err)
		st.LastErrorAt = &at
	}
	// A folder set in the database but not in the running source means the
	// server was started before the folder was chosen, or the source failed to
	// build. Say so rather than showing a configured folder and an idle wall.
	//
	// Not when rclone is missing, though: the screen already has a line about
	// that, and this one would be the same fact in a second wording -- the
	// thing the backup banner was corrected for (CLAUDE.md §13). A missing
	// binary is *why* there is no source, so naming both reads as two
	// problems.
	if st.FolderSet && !src.Configured && st.LastError == "" && st.RcloneInstalled {
		st.LastError = "the running server has no Drive source for this folder; check that SIGNIN_PHOTOS_REMOTE is set in .env, then restart"
	}
	return st
}

// describeFillFailure words a resting reel's last failure for the admin.
//
// A streak of photographs the normalizer refused gets §9's sentence, naming
// the gate, because that one is fixed by what goes into the folder and not
// by anything on this machine: a folder of phone portraits is two thousand
// valid photographs and an empty wall. The gate's bounds are 4:3 and 16:10,
// so "close to 3:2" is how it is put to a person rather than as two decimals.
func describeFillFailure(err error) string {
	if errors.Is(err, errPhotoUnusable) {
		return fmt.Sprintf("no usable photographs found in the last %d tries. The wall only uses landscape photographs close to 3:2 — anything outside 4:3 to 16:10, which includes every portrait shot, is skipped. The last one: %v",
			photoWallFailureCap, err)
	}
	return fmt.Sprintf("paused after %d failed tries in a row; trying again every %s. The last: %v",
		photoWallFailureCap, photoWallBackoff, err)
}

// rcloneInstalled reports whether the binary is on PATH. Checked per request
// rather than cached: an admin who installs rclone in response to this screen
// saying it is missing should see the answer change on a reload, not after a
// restart.
func rcloneInstalled() bool {
	_, err := exec.LookPath(rcloneBinary)
	return err == nil
}

/* -------------------------------------------------------------- switch ---- */

// SetPhotoWallFolder is PUT /admin/photo-wall: parse, probe, write, invalidate.
//
// The probe is the most valuable part of the feature and the reason the write
// is not simply a settings field. Without it a typo or an unshared folder
// returns a success message and the wall silently empties ten minutes later,
// with nothing on any screen connecting the two events (§7).
//
// The order below is not interchangeable. A switch is a config write *plus* a
// teardown, and doing only the first leaves the wall showing photographs from
// the folder that was just replaced -- the exact outcome this screen exists to
// prevent.
func (db *DB) SetPhotoWallFolder(ctx context.Context, actor Actor, link, label string) (PhotoWallStatus, error) {
	if err := RequireAdmin(actor); err != nil {
		return PhotoWallStatus{}, err
	}
	folderID, err := ParseDriveFolderLink(link)
	if err != nil {
		return PhotoWallStatus{}, err
	}
	label = strings.TrimSpace(label)
	if label == "" {
		return PhotoWallStatus{}, fmt.Errorf("%w: give the folder a short name. It is what this screen and the log show in place of the link, which is never displayed", ErrInvalid)
	}
	if len(label) > maxPhotoWallLabel {
		return PhotoWallStatus{}, fmt.Errorf("%w: that name is %d characters; keep it under %d", ErrInvalid, len(label), maxPhotoWallLabel)
	}
	if db.PhotoWallSource == nil {
		return PhotoWallStatus{}, fmt.Errorf("%w: the sign-in photo wall has no Drive source on this server. Set SIGNIN_PHOTOS_REMOTE, install rclone, and restart", ErrNotConfigured)
	}

	// Nothing is written until Drive has answered for this exact id. A failure
	// here leaves the previous folder live and the reel untouched.
	if err := db.PhotoWallSource.Probe(ctx, folderID); err != nil {
		return PhotoWallStatus{}, err
	}

	if err := db.savePhotoWallFolder(ctx, actor, folderID, label); err != nil {
		return PhotoWallStatus{}, err
	}

	// The source discards the manifest and nudges its refresher; the reel
	// advances the generation and deletes every ready tile. Together they are
	// the teardown: a fetch already in flight carries the old generation and
	// its result is written nowhere, so a slow `rclone cat` cannot deposit a
	// photograph from the replaced folder into the new reel minutes later.
	//
	// Tiles already *served* are deliberately left to expire on their TTL.
	// Their URLs sit in a browser that has already rendered them, and 404ing a
	// live page to save fifteen minutes is the worse trade.
	db.PhotoWallSource.SetFolder(folderID)
	db.PhotoWall.Invalidate()

	f, err := db.loadPhotoWallFolder(ctx)
	if err != nil {
		return PhotoWallStatus{}, err
	}
	return db.photoWallStatus(f), nil
}

// savePhotoWallFolder writes the row and the audit line in one transaction, so
// a folder can never be live with nothing in the log saying who put it there.
//
// The activity_log row records the **label, never the id** (§7). The log is
// itself exported in backups, and not echoing the link to the screen buys
// nothing if it is sitting in activity_log.csv. The accepted cost is that the
// log can say a folder was replaced, by whom and when, but cannot be used to
// recover the link that was replaced -- that has to come from Drive again.
func (db *DB) savePhotoWallFolder(ctx context.Context, actor Actor, folderID, label string) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("save the photo wall folder: %w", err)
	}
	defer tx.Rollback(ctx)

	// A trustedCLI actor has no profiles row, so the column stays null rather
	// than failing the foreign key. Nothing calls this from the CLI today; the
	// coalesce is what keeps that from being a crash if something ever does.
	var actorID *string
	if actor.ID != "" && !actor.trustedCLI {
		id := actor.ID
		actorID = &id
	}

	if _, err := tx.Exec(ctx, `
		update app_settings set
			signin_photos_folder_id = $1,
			signin_photos_label = $2,
			signin_photos_changed_at = now(),
			signin_photos_changed_by = $3
		where id = true`, folderID, label, actorID); err != nil {
		return mapPgError("save the photo wall folder", err)
	}
	if _, err := tx.Exec(ctx, `
		insert into activity_log (asset_id, actor_id, action, details)
		values (null, $1, 'signin_photo_wall_folder', jsonb_build_object('label', $2::text))`,
		actorID, label); err != nil {
		return mapPgError("log the photo wall folder change", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("save the photo wall folder: %w", err)
	}
	return nil
}

// RebuildPhotoWallManifest is POST /admin/photo-wall/rebuild: re-list the
// folder that is already live.
//
// It exists because the manifest is refreshed weekly (§3), which is right for
// a folder that gains a batch of photographs after an event -- and wrong for
// the ten minutes right after somebody has uploaded that batch and wants to
// see it on the wall.
func (db *DB) RebuildPhotoWallManifest(ctx context.Context, actor Actor) (PhotoWallStatus, error) {
	if err := RequireAdmin(actor); err != nil {
		return PhotoWallStatus{}, err
	}
	f, err := db.loadPhotoWallFolder(ctx)
	if err != nil {
		return PhotoWallStatus{}, err
	}
	if f.id == "" {
		return PhotoWallStatus{}, fmt.Errorf("%w: no Drive folder has been chosen for the sign-in photo wall yet", ErrNotConfigured)
	}
	if db.PhotoWallSource == nil {
		return PhotoWallStatus{}, fmt.Errorf("%w: the sign-in photo wall has no Drive source on this server", ErrNotConfigured)
	}
	db.PhotoWallSource.Rebuild()
	return db.photoWallStatus(f), nil
}

// PhotoWallPreview is GET /admin/photo-wall/preview: up to n tile URLs that
// are *not* marked served.
//
// A preview must not consume the buffer the sign-in screen is about to draw
// from -- an admin refreshing this screen a few times would otherwise empty
// the wall for the next person to walk up. The cost is that a URL here can go
// stale: the tile it names is still ready, so it may be handed to a real
// sign-in and deleted a TTL later. The screen treats a 404 tile the way the
// wall does, by hiding it.
func (db *DB) PhotoWallPreview(ctx context.Context, actor Actor, n int) ([]string, error) {
	if err := RequireAdmin(actor); err != nil {
		return nil, err
	}
	return db.PhotoWall.PreviewPhotos(n), nil
}
