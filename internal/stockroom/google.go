package stockroom

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

// One Google connection for everything that talks to Drive.
//
// The backup pushes through an rclone remote and the sign-in photo wall reads
// through one, and until 2026-09-25 they were two remotes with two sign-ins:
// the backup's full-scope `stockroom-drive`, and the photo wall's read-only
// `gdrive-photos`. Two sign-ins meant two tokens to expire, two places to
// press Reconnect, and two `rclone authorize` flows fighting over the same
// callback port -- and the second flow was the one that broke (it asked for
// its scope through an options blob, and rclone answers such a call in a
// shape the reader did not recognise; see authorizeToken). The read-only
// scope bought little on a machine whose rclone.conf already held a
// write-capable token for the same account. So there is one remote, named by
// app_settings.drive_remote, and either screen's Connect writes it.
//
// The OAuth client comes from app_settings too. rclone's shared client id is
// being retired during 2026 (https://rclone.org/drive/#making-your-own-client-id),
// so a school enters its own in Admin → Settings and every sign-in and every
// remote written from then on uses it.

// DefaultGoogleRemote is the rclone remote Connect writes when none is named.
const DefaultGoogleRemote = "stockroom-drive"

// googleRemote is the one remote's name: drive_remote, or the default.
func (s Settings) googleRemote() string {
	if r := strings.TrimSuffix(strings.TrimSpace(s.DriveRemote), ":"); r != "" {
		return r
	}
	return DefaultGoogleRemote
}

func (s Settings) googleClient() googleClient {
	return googleClient{id: s.GoogleClientID, secret: s.GoogleClientSecret}
}

// validateGoogleClient checks the pair an admin typed. Both or neither: an id
// with no secret fails at Google's token exchange, after the admin has already
// signed in, with an error that names neither field.
func validateGoogleClient(id, secret string) error {
	switch {
	case id == "" && secret == "":
		return nil
	case id == "":
		return fmt.Errorf("%w: a Google client secret needs its client ID beside it", ErrInvalid)
	case secret == "":
		return fmt.Errorf("%w: a Google client ID needs its client secret too; both are on the page Google showed when the OAuth client was created", ErrInvalid)
	case !strings.HasSuffix(id, ".apps.googleusercontent.com") || strings.ContainsAny(id, " \t"):
		return fmt.Errorf("%w: a Google client ID ends in .apps.googleusercontent.com; copy it from Google Cloud → APIs & Services → Credentials", ErrInvalid)
	case strings.ContainsAny(secret, " \t"):
		return fmt.Errorf("%w: a Google client secret has no spaces in it", ErrInvalid)
	}
	return nil
}

// startGoogleSignIn launches `rclone authorize` with the configured client.
// The caller has checked the actor.
func (db *DB) startGoogleSignIn(ctx context.Context) (DriveConnectResult, error) {
	if !rcloneInstalled() {
		return DriveConnectResult{}, fmt.Errorf("%w: rclone is not installed. On this machine run `brew install rclone` (macOS) or `winget install Rclone.Rclone` (Windows), then reload this page", ErrNotConfigured)
	}
	s, err := db.loadSettings(ctx)
	if err != nil {
		return DriveConnectResult{}, err
	}
	return startDriveAuthorize(ctx, s.googleClient())
}

// finishGoogleSignIn writes the one remote with the token Google sent back
// and returns its name. remote may be blank, meaning the one already
// configured. The caller has checked the actor and saves the name.
func (db *DB) finishGoogleSignIn(ctx context.Context, id, code, remote string) (string, error) {
	s, err := db.loadSettings(ctx)
	if err != nil {
		return "", err
	}
	remote = strings.TrimSuffix(strings.TrimSpace(remote), ":")
	if remote == "" {
		remote = s.googleRemote()
	}
	if strings.ContainsAny(remote, ` :/\`) {
		return "", fmt.Errorf("%w: a remote name cannot contain spaces, colons or slashes", ErrInvalid)
	}
	token, err := finishDriveAuthorize(ctx, id, strings.TrimSpace(code))
	if err != nil {
		return "", err
	}
	// The client is written explicitly even when it is rclone's shared one:
	// a reconnect replaces the remote, and a client id left over from an
	// earlier sign-in would sit beside a token issued to a different client,
	// which Google refuses at the first refresh. root_folder_id stays blank so
	// the photo wall can name its folder per command (photowall_drive.go).
	c := s.googleClient()
	if _, err := runRclone(ctx, time.Minute, "config", "create", remote, "drive",
		"config_is_local=false", "scope=drive", "token="+token,
		"client_id="+c.id, "client_secret="+c.secret); err != nil {
		// rclone quotes what it could not parse, and what it was given is the
		// token. The message is going to a screen and a log.
		msg := strings.ReplaceAll(err.Error(), token, "<token>")
		if c.secret != "" {
			msg = strings.ReplaceAll(msg, c.secret, "<client secret>")
		}
		return "", fmt.Errorf("save the Google sign-in: %s", msg)
	}
	return remote, nil
}

// googleSignedIn is what either screen's Connect does last: start the photo
// wall if this server has one and it is not running yet, or point a running
// one at the remote that was just written. Best-effort from the backup's
// side -- a backup connection must not fail because the decorative wall could
// not start -- so FinishDriveConnect logs the error and FinishPhotoWallGoogle
// returns it.
func (db *DB) googleSignedIn(ctx context.Context) error {
	db.photoWallStart.Lock()
	wall, source := db.photoWallParts()
	err := db.startPhotoWallLocked(ctx)
	db.photoWallStart.Unlock()
	if err != nil {
		return err
	}
	// A reconnect is usually the answer to a listing that failed on an
	// expired token. Without this the source would wait out its five-minute
	// retry while the admin stares at the error they just fixed.
	if wall != nil && source != nil && source.Status().LastError != "" {
		source.Rebuild()
	}
	return nil
}

// sharedClientWarning is the admin-facing sentence while rclone's retiring
// client id is in use, or "" once the school has its own.
func (s Settings) sharedClientWarning() string {
	if s.GoogleClientID != "" || !s.DriveEnabled {
		return ""
	}
	return "Google Drive is signed in with rclone's shared Google client, which rclone is retiring during 2026; once it stops, backups to Drive and the sign-in photo wall stop with it. Add your own Google client under Settings → Google sign-in, then press Reconnect."
}

func logGoogleWallError(err error) {
	if err != nil {
		log.Printf("warning: signed in to Google, but the sign-in photo wall could not start: %v", err)
	}
}
