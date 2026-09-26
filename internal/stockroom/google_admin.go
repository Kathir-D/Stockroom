package stockroom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// The admin panel's one Google surface: GET /admin/google and the
// /admin/google/* routes beneath it.
//
// Everything behind it is rclone -- `authorize`, `config create`, a remote
// with a name, a callback port -- and none of that is what an admin setting up
// a school's closet PC should have to read. What they see is one button, Sign
// in with Google, the account it is signed in to, and a folder picker. The
// same button sits on Settings and on Photo wall and does the same thing,
// because there is one connection (google.go).
//
// **One click.** The screen opens Google's page itself and then asks
// FinishGoogle every couple of seconds until the callback has landed; a
// "not yet" is `done: false`, not an error. The paste box survives only as
// the fallback for signing in on another machine.
//
// **Folders are named by handle, not by Drive id.** The picker lists Drive
// folders, and a folder id of one shared "anyone with the link" is a key to
// it -- the rule the photo wall's status is built around (photowall_admin.go).
// So each listed folder gets a random handle that means something only to
// this server for an hour, and choosing one sends the handle back.

// GoogleStatus is GET /admin/google: what the screen shows about the one
// connection. Names and an email address, never a token.
type GoogleStatus struct {
	// RcloneInstalled is false on a machine that has not had rclone installed;
	// the button cannot work until it is, and the screen says how.
	RcloneInstalled bool `json:"rclone_installed"`
	// Connected is whether rclone has the remote, asked of rclone itself.
	Connected bool `json:"connected"`
	// Account is the Google account signed in to, when known.
	Account string `json:"account"`
	// OwnClient is whether sign-ins use the school's own Google client.
	OwnClient bool `json:"own_client"`
	// BackupEnabled and BackupFolder are the Drive backup: on or off, and the
	// folder in My Drive it pushes to ("" until one is chosen).
	BackupEnabled bool   `json:"backup_enabled"`
	BackupFolder  string `json:"backup_folder"`
	// PhotoWall is whether this server runs the sign-in photo wall at all.
	PhotoWall bool `json:"photo_wall"`
}

// GoogleSignInResult is POST /admin/google/finish. Done is false while Google
// has not called back yet, which the screen polls on.
type GoogleSignInResult struct {
	Done   bool          `json:"done"`
	Google *GoogleStatus `json:"google,omitempty"`
}

// GetGoogleStatus is GET /admin/google.
func (db *DB) GetGoogleStatus(ctx context.Context, actor Actor) (GoogleStatus, error) {
	if err := RequireAdmin(actor); err != nil {
		return GoogleStatus{}, err
	}
	return db.googleStatus(ctx)
}

func (db *DB) googleStatus(ctx context.Context) (GoogleStatus, error) {
	s, err := db.loadSettings(ctx)
	if err != nil {
		return GoogleStatus{}, err
	}
	st := GoogleStatus{
		RcloneInstalled: rcloneInstalled(),
		OwnClient:       s.GoogleClientID != "",
		BackupEnabled:   s.DriveEnabled,
		BackupFolder:    s.DrivePath,
		PhotoWall:       db.photoWallConfig() != nil,
	}
	// newDriveTarget pushes to "stockroom" when no folder is set, so an
	// enabled backup with a blank path is going there, and the screen says so.
	if st.BackupEnabled && st.BackupFolder == "" {
		st.BackupFolder = "stockroom"
	}
	if st.RcloneInstalled {
		// A failure reads as not connected, which is what the button offers
		// to fix.
		st.Connected, _ = rcloneHasRemote(ctx, s.googleRemote())
	}
	if st.Connected {
		if err := db.Pool.QueryRow(ctx, `select coalesce(google_account, '') from app_settings where id = true`).
			Scan(&st.Account); err != nil {
			return GoogleStatus{}, fmt.Errorf("read the Google account: %w", err)
		}
	}
	return st, nil
}

// ConnectGoogle is POST /admin/google/connect: start `rclone authorize` and
// return the page to open.
func (db *DB) ConnectGoogle(ctx context.Context, actor Actor) (DriveConnectResult, error) {
	if err := RequireAdmin(actor); err != nil {
		return DriveConnectResult{}, err
	}
	return db.startGoogleSignIn(ctx)
}

// FinishGoogle is POST /admin/google/finish: once Google has called back (or
// the admin pasted rclone's block), write the one remote, record which account
// it is, and start the photo wall. It does not turn Drive backups on -- the
// Settings screen's folder picker does that, because a backup needs a folder
// and "on" with no folder chosen is a surprise.
func (db *DB) FinishGoogle(ctx context.Context, actor Actor, id, code string) (GoogleSignInResult, error) {
	if err := RequireAdmin(actor); err != nil {
		return GoogleSignInResult{}, err
	}
	remote, token, err := db.finishGoogleSignIn(ctx, id, code)
	if errors.Is(err, errGoogleWaiting) {
		return GoogleSignInResult{Done: false}, nil
	}
	if err != nil {
		return GoogleSignInResult{}, err
	}
	if _, err := db.SaveSettings(ctx, actor, SettingsInput{DriveRemote: &remote}); err != nil {
		return GoogleSignInResult{}, err
	}
	// From here the sign-in has worked: rclone holds the token and the
	// setting names the remote. The account label and the audit line are
	// logged on failure rather than returned, or the screen would report a
	// failed sign-in for one that is already live.
	account := googleAccountEmail(ctx, token)
	if _, err := db.Pool.Exec(ctx, `update app_settings set google_account = $1 where id = true`, nullable(account)); err != nil {
		log.Printf("warning: signed in to Google, but could not record which account: %v", err)
	}
	if err := db.logGoogleSignIn(ctx, actor, remote); err != nil {
		log.Printf("warning: signed in to Google, but could not write the activity log: %v", err)
	}
	if err := db.googleSignedIn(ctx); err != nil && !errors.Is(err, ErrNotConfigured) {
		logGoogleWallError(err)
	}
	st, err := db.googleStatus(ctx)
	if err != nil {
		return GoogleSignInResult{}, err
	}
	return GoogleSignInResult{Done: true, Google: &st}, nil
}

// googleAboutURL is Drive's "who is this token for". A variable so tests can
// point it at a stand-in rather than at Google.
var googleAboutURL = "https://www.googleapis.com/drive/v3/about?fields=user(emailAddress)"

// googleAccountEmail asks Drive once, with the token just issued, which account
// it belongs to. The one Drive API call this package makes itself: rclone has
// no command that prints it, and "Connected as teacher@school.org" is what
// tells an admin they signed in to the right account. Best-effort -- a blank
// answer only means the screen says "Connected" without the address.
func googleAccountEmail(ctx context.Context, tokenJSON string) string {
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if json.Unmarshal([]byte(tokenJSON), &tok) != nil || tok.AccessToken == "" || googleAboutURL == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleAboutURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("warning: signed in to Google, but could not ask which account: %v", err)
		return ""
	}
	defer res.Body.Close()
	var about struct {
		User struct {
			EmailAddress string `json:"emailAddress"`
		} `json:"user"`
	}
	if res.StatusCode != http.StatusOK || json.NewDecoder(res.Body).Decode(&about) != nil {
		return ""
	}
	return about.User.EmailAddress
}

// logGoogleSignIn records who connected the account and the remote's name --
// never the token, which is written nowhere but rclone's own file.
func (db *DB) logGoogleSignIn(ctx context.Context, actor Actor, remote string) error {
	var actorID *string
	if actor.ID != "" && !actor.trustedCLI {
		id := actor.ID
		actorID = &id
	}
	return db.logNow(ctx, LogEntry{Category: LogAdmin, Action: "google_sign_in", ActorID: deref(actorID),
		Summary: "Signed in to Google for Drive", Details: map[string]any{"remote": remote}})
}

/* ------------------------------------------------------------- folders ---- */

// The picker's two starting points. Neither is a folder id.
const (
	GoogleMyDrive      = "my-drive"
	GoogleSharedWithMe = "shared"
)

// folderHandleTTL is how long a listed folder stays choosable: long enough to
// browse and pick, short enough that the map does not grow for a term.
const folderHandleTTL = time.Hour

type folderHandle struct {
	id, name string
	expires  time.Time
}

var folderHandles = struct {
	mu sync.Mutex
	m  map[string]folderHandle
}{m: map[string]folderHandle{}}

func rememberFolder(id, name string) (string, error) {
	h, err := randomID()
	if err != nil {
		return "", err
	}
	now := time.Now()
	folderHandles.mu.Lock()
	defer folderHandles.mu.Unlock()
	for k, v := range folderHandles.m {
		if now.After(v.expires) {
			delete(folderHandles.m, k)
		}
	}
	folderHandles.m[h] = folderHandle{id: id, name: name, expires: now.Add(folderHandleTTL)}
	return h, nil
}

func lookupFolder(h string) (folderHandle, error) {
	folderHandles.mu.Lock()
	f, ok := folderHandles.m[h]
	folderHandles.mu.Unlock()
	if !ok || time.Now().After(f.expires) {
		return folderHandle{}, fmt.Errorf("%w: that folder list has expired. Open the folder picker again", ErrNotFound)
	}
	return f, nil
}

// GoogleFolder is one folder in the picker.
type GoogleFolder struct {
	Handle string `json:"handle"`
	Name   string `json:"name"`
}

// GoogleFolders is GET /admin/google/folders?in=: the folders directly inside
// My Drive, Shared with me, or a folder listed earlier.
func (db *DB) GoogleFolders(ctx context.Context, actor Actor, in string) ([]GoogleFolder, error) {
	if err := RequireAdmin(actor); err != nil {
		return nil, err
	}
	args, id, err := db.googleFolderArgs(ctx, "lsjson", in, "")
	if err != nil {
		return nil, err
	}
	args = append(args, "--dirs-only", "--no-modtime", "--no-mimetype")
	out, err := runRclone(ctx, time.Minute, args...)
	if err != nil {
		return nil, googleFolderError(err, id)
	}
	var entries []struct {
		ID    string `json:"ID"`
		Name  string `json:"Name"`
		IsDir bool   `json:"IsDir"`
	}
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Errorf("read Google Drive's folder list: %w", err)
	}
	folders := []GoogleFolder{}
	for _, e := range entries {
		if !e.IsDir || e.ID == "" {
			continue
		}
		h, err := rememberFolder(e.ID, e.Name)
		if err != nil {
			return nil, err
		}
		folders = append(folders, GoogleFolder{Handle: h, Name: e.Name})
	}
	sort.Slice(folders, func(i, j int) bool {
		return strings.ToLower(folders[i].Name) < strings.ToLower(folders[j].Name)
	})
	return folders, nil
}

// CreateGoogleFolder is POST /admin/google/folders: a new folder inside My
// Drive or a folder listed earlier, so a backup folder can be made from the
// picker rather than in another tab.
func (db *DB) CreateGoogleFolder(ctx context.Context, actor Actor, in, name string) (GoogleFolder, error) {
	if err := RequireAdmin(actor); err != nil {
		return GoogleFolder{}, err
	}
	name = strings.TrimSpace(name)
	if err := validFolderName(name); err != nil {
		return GoogleFolder{}, err
	}
	if in == GoogleSharedWithMe {
		return GoogleFolder{}, fmt.Errorf("%w: a new folder cannot go directly in Shared with me. Open a folder first, or make it in My Drive", ErrInvalid)
	}
	args, id, err := db.googleFolderArgs(ctx, "mkdir", in, name)
	if err != nil {
		return GoogleFolder{}, err
	}
	if _, err := runRclone(ctx, time.Minute, args...); err != nil {
		return GoogleFolder{}, googleFolderError(err, id)
	}
	// mkdir prints nothing, so the new folder's handle comes from listing its
	// parent. It also means a folder that already existed is simply chosen.
	folders, err := db.GoogleFolders(ctx, actor, in)
	if err != nil {
		return GoogleFolder{}, err
	}
	// Drive allows two folders with one name, and mkdir then picks one of
	// them without saying which. Choosing for the admin could point a backup
	// at the wrong folder, so more than one match is refused.
	var match []GoogleFolder
	for _, f := range folders {
		if f.Name == name {
			match = append(match, f)
		}
	}
	switch len(match) {
	case 0:
		return GoogleFolder{}, fmt.Errorf("made the folder %q, but Google Drive does not list it yet. Open the picker again in a moment", name)
	case 1:
		return match[0], nil
	}
	return GoogleFolder{}, fmt.Errorf("%w: there are %d folders called %q here already. Pick one from the list, or use a different name", ErrConflict, len(match), name)
}

// googleFolderArgs is `rclone <verb> <remote>:[name]` pointed at the folder
// `in` names, plus the Drive id it resolved to ("" for the two roots).
func (db *DB) googleFolderArgs(ctx context.Context, verb, in, name string) ([]string, string, error) {
	s, err := db.loadSettings(ctx)
	if err != nil {
		return nil, "", err
	}
	args := []string{verb, s.googleRemote() + ":" + name}
	switch in {
	case "", GoogleMyDrive:
		return args, "", nil
	case GoogleSharedWithMe:
		return append(args, "--drive-shared-with-me"), "", nil
	}
	f, err := lookupFolder(in)
	if err != nil {
		return nil, "", err
	}
	return append(args, "--drive-root-folder-id", f.id), f.id, nil
}

// googleFolderError words a failed Drive call for the picker. rclone quotes
// its request, whose query carries the folder id, so it goes through the same
// scrubbing as the photo wall's errors.
func googleFolderError(err error, id string) error {
	msg := scrubDriveError(err, id)
	if strings.Contains(msg, "invalid_grant") || strings.Contains(msg, "token expired") {
		return fmt.Errorf("%w: the Google sign-in has expired. Press Sign in with Google again", ErrConflict)
	}
	return fmt.Errorf("%w: Google Drive did not answer: %s", ErrNotConfigured, msg)
}

func validFolderName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("%w: give the new folder a name", ErrInvalid)
	case utf8.RuneCountInString(name) > 100:
		return fmt.Errorf("%w: keep a folder name under 100 characters", ErrInvalid)
	case name == "." || name == ".." || strings.ContainsAny(name, `/\`):
		return fmt.Errorf("%w: a folder name cannot contain slashes", ErrInvalid)
	}
	return nil
}

// ChoosePhotoWallFolder is PUT /admin/photo-wall with a picked folder rather
// than a pasted link: the same probe, write and teardown, named after the
// folder unless the admin typed a name.
func (db *DB) ChoosePhotoWallFolder(ctx context.Context, actor Actor, handle, label string) (PhotoWallStatus, error) {
	if err := RequireAdmin(actor); err != nil {
		return PhotoWallStatus{}, err
	}
	f, err := lookupFolder(handle)
	if err != nil {
		return PhotoWallStatus{}, err
	}
	if strings.TrimSpace(label) == "" {
		label = f.name
	}
	return db.SetPhotoWallFolder(ctx, actor, f.id, label)
}
