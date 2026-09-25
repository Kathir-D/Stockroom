package stockroom

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Tests for the one Google connection (google.go, google_admin.go) and the
// photo wall it starts (photowall_google.go).
// rclone is replaced by a shell script on PATH that records its arguments, so
// nothing here reaches Google or touches the machine's real rclone.conf.

// The token the fake prints. Asserted absent from every status the screen
// receives: it belongs in rclone's config file and nowhere else.
const fakeRcloneToken = `{"access_token":"ya29.FAKE-ACCESS-SECRET","token_type":"Bearer","refresh_token":"1//FAKE-REFRESH-SECRET","expiry":"2030-01-01T00:00:00Z"}`

// fakeRcloneBlob is what a real `rclone authorize` prints when it was given an
// options blob, as the photo wall's read-only sign-in was until 2026-09-25:
// not the token, but the resulting config, JSON then unpadded URL base64, with
// the token as its "token" value (rclone fs/config/authorize.go). A fake that
// printed the bare token for both calls is what let every real photo wall
// sign-in fail while this suite passed. It is still what an admin may paste
// from another machine, so it is still read.
func fakeRcloneBlob() string {
	b, _ := json.Marshal(map[string]string{"token": fakeRcloneToken})
	return base64.RawURLEncoding.EncodeToString(b)
}

// fakeRclone puts a stand-in rclone first on PATH and returns the file its
// arguments are appended to, one invocation per line.
//
//   - authorize logs the client it was handed through the environment,
//     prints the sign-in link, then the token framed as real rclone frames
//     it -- or, with
//     FAKE_RCLONE_HANG set, waits the way a real one does for a browser that
//     never comes back. `exec` so a cancel kills the process holding the
//     pipes, as it would a real rclone.
//   - config create records that the remote now exists; listremotes then
//     names it.
//   - lsjson answers $FAKE_RCLONE_LSJSON, or an empty folder, so a wall that
//     starts has nothing to do.
func fakeRclone(t *testing.T) (argsLog string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake rclone is a shell script")
	}
	dir := t.TempDir()
	argsLog = filepath.Join(dir, "args.log")
	script := `#!/bin/sh
echo "$@" >> "$FAKE_RCLONE_LOG"
case "$1" in
  authorize)
    echo "NOTICE: Please go to the following link: http://127.0.0.1:53682/auth?state=fake" >&2
    if [ -n "$FAKE_RCLONE_HANG" ]; then exec sleep 30; fi
    echo "client=$RCLONE_DRIVE_CLIENT_ID secret=$RCLONE_DRIVE_CLIENT_SECRET" >> "$FAKE_RCLONE_LOG"
    echo "Paste the following into your remote machine --->"
    echo '` + fakeRcloneToken + `'
    echo "<---End paste"
    ;;
  listremotes)
    if [ -f "$FAKE_RCLONE_DIR/remote" ]; then echo "$(cat "$FAKE_RCLONE_DIR/remote"):"; fi
    ;;
  config)
    echo "$3" > "$FAKE_RCLONE_DIR/remote"
    ;;
  lsjson)
    echo "${FAKE_RCLONE_LSJSON:-[]}"
    ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "rclone"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_RCLONE_LOG", argsLog)
	t.Setenv("FAKE_RCLONE_DIR", dir)
	t.Cleanup(stopUnfinishedDriveAuthorize)
	return argsLog
}

func rcloneCalls(t *testing.T, argsLog string) []string {
	t.Helper()
	b, err := os.ReadFile(argsLog)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// The sign-in asks for rclone's default, full scope -- one remote serves the
// backup's writes and the photo wall's reads -- and the school's own Google
// client travels in the environment, so its secret is never on a process list.
func TestDriveAuthorizeArgsAndClient(t *testing.T) {
	if got := strings.Join(driveAuthorizeArgs(), " "); got != "authorize drive --auth-no-open-browser" {
		t.Errorf("authorize = %q, want no options blob and no client on the command line", got)
	}

	shared := googleClient{}
	for _, kv := range driveAuthorizeEnv(shared) {
		if strings.HasPrefix(kv, "RCLONE_DRIVE_CLIENT_") && os.Getenv(strings.SplitN(kv, "=", 2)[0]) == "" {
			t.Errorf("the shared client added %q to the environment", kv)
		}
	}
	if got := drivePasteCommand(shared); got != "rclone authorize drive" {
		t.Errorf("paste command with the shared client = %q", got)
	}

	own := googleClient{id: "123-abc.apps.googleusercontent.com", secret: "GOCSPX-shh"}
	env := strings.Join(driveAuthorizeEnv(own), "\n")
	if !strings.Contains(env, "RCLONE_DRIVE_CLIENT_ID="+own.id) || !strings.Contains(env, "RCLONE_DRIVE_CLIENT_SECRET="+own.secret) {
		t.Error("the school's own client is not in rclone's environment")
	}
	cmd := drivePasteCommand(own)
	if !strings.Contains(cmd, own.id) || strings.Contains(cmd, own.secret) {
		t.Errorf("paste command = %q, want the client id and a placeholder for the secret", cmd)
	}
}

func TestValidateGoogleClient(t *testing.T) {
	const id = "123-abc.apps.googleusercontent.com"
	for _, ok := range [][2]string{{"", ""}, {id, "GOCSPX-shh"}} {
		if err := validateGoogleClient(ok[0], ok[1]); err != nil {
			t.Errorf("validateGoogleClient(%q, %q) = %v, want nil", ok[0], ok[1], err)
		}
	}
	for _, bad := range [][2]string{{id, ""}, {"", "GOCSPX-shh"}, {"not-a-client-id", "GOCSPX-shh"}, {id, "has space"}} {
		if err := validateGoogleClient(bad[0], bad[1]); !errors.Is(err, ErrInvalid) {
			t.Errorf("validateGoogleClient(%q, %q) = %v, want ErrInvalid", bad[0], bad[1], err)
		}
	}
}

// Both shapes `rclone authorize` prints reduce to the token JSON, whether read
// off its output a line at a time or pasted whole, arrows and all.
func TestAuthorizeTokenReadsBothShapes(t *testing.T) {
	pastedBlock := "Paste the following into your remote machine --->\n" + fakeRcloneBlob() + "\n<---End paste\n"
	quoted, _ := json.Marshal(fakeRcloneToken)
	for name, in := range map[string]string{
		"bare token":             fakeRcloneToken,
		"config blob":            fakeRcloneBlob(),
		"padded blob":            base64.URLEncoding.EncodeToString([]byte(`{"token":` + string(quoted) + `}`)),
		"pasted block, arrows":   pastedBlock,
		"surrounding whitespace": "  " + fakeRcloneBlob() + "\n",
	} {
		if got, ok := authorizeToken(in); !ok || got != fakeRcloneToken {
			t.Errorf("%s: authorizeToken = %q, %v; want the token", name, got, ok)
		}
	}
	for _, in := range []string{"", "Paste the following into your remote machine --->", "<---End paste", "NOTICE: waiting for code...", "eyJmb28iOiJiYXIifQ", `{"foo":"bar"}`} {
		if got, ok := authorizeToken(in); ok {
			t.Errorf("authorizeToken(%q) = %q, want no token", in, got)
		}
	}
}

// googleSettings pins the settings these tests read and puts back whatever
// the shared development database held, so a real Google connection somebody
// made on this machine neither breaks the test nor is lost to it.
func googleSettings(t *testing.T, db *DB, remote, clientID, clientSecret string) {
	t.Helper()
	ctx := context.Background()
	var r, id, secret, account *string
	var enabled bool
	if err := db.Pool.QueryRow(ctx, `select drive_remote, drive_enabled, google_client_id, google_client_secret, google_account
		from app_settings where id = true`).Scan(&r, &enabled, &id, &secret, &account); err != nil {
		t.Fatalf("read settings: %v", err)
	}
	t.Cleanup(func() {
		db.Pool.Exec(context.Background(), `update app_settings set drive_remote = $1, drive_enabled = $2,
			google_client_id = $3, google_client_secret = $4, google_account = $5 where id = true`, r, enabled, id, secret, account)
	})
	if _, err := db.Pool.Exec(ctx, `update app_settings set drive_remote = $1, drive_enabled = false,
		google_client_id = $2, google_client_secret = $3 where id = true`,
		nullable(remote), nullable(clientID), nullable(clientSecret)); err != nil {
		t.Fatalf("set settings: %v", err)
	}
}

// fakeGoogleAbout stands in for Drive's "who is this token for".
func fakeGoogleAbout(t *testing.T, email string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ya29.FAKE-ACCESS-SECRET" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		fmt.Fprintf(w, `{"user":{"emailAddress":%q}}`, email)
	}))
	t.Cleanup(srv.Close)
	old := googleAboutURL
	googleAboutURL = srv.URL
	t.Cleanup(func() { googleAboutURL = old })
}

// The whole feature, end to end: off until somebody signs in, then one
// sign-in writes the shared Google remote, records which account it is, and
// starts the wall on the running server with no restart. It does not turn
// backups on -- choosing a backup folder does. The token reaches rclone's
// config and no response.
func TestGoogleSignInStartsTheWall(t *testing.T) {
	argsLog := fakeRclone(t)
	fakeGoogleAbout(t, "media@example.org")
	db := requireTestDB(t)
	googleSettings(t, db, "", "", "")
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	captureLog(t)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { db.SetPhotoWall(nil, nil) })
	t.Cleanup(func() {
		db.Pool.Exec(context.Background(),
			`delete from activity_log where action = 'google_sign_in' and actor_id = $1`, admin.ID)
	})

	db.StartPhotoWall(ctx, PhotoWallConfig{Dir: t.TempDir()})
	if db.SignInPhotoWall() != nil {
		t.Fatal("the wall started with nobody signed in to Google")
	}
	g, err := db.GetGoogleStatus(ctx, admin)
	if err != nil {
		t.Fatalf("GetGoogleStatus: %v", err)
	}
	if g.Connected || !g.RcloneInstalled || !g.PhotoWall {
		t.Errorf("before sign-in: %+v; want not connected, rclone installed, a photo wall", g)
	}

	res, err := db.ConnectGoogle(ctx, admin)
	if err != nil {
		t.Fatalf("ConnectGoogle: %v", err)
	}
	if !strings.Contains(res.URL, "127.0.0.1:53682") {
		t.Errorf("sign-in link = %q, want rclone's local callback", res.URL)
	}

	// The fake prints its token straight after the link; the screen polls.
	var done GoogleSignInResult
	for deadline := time.Now().Add(10 * time.Second); !done.Done && time.Now().Before(deadline); {
		if done, err = db.FinishGoogle(ctx, admin, res.ID, ""); err != nil {
			t.Fatalf("FinishGoogle: %v", err)
		}
	}
	if !done.Done || done.Google == nil || !done.Google.Connected || done.Google.Account != "media@example.org" {
		t.Fatalf("after sign-in: %+v; want done, connected, as media@example.org", done)
	}
	if done.Google.BackupEnabled {
		t.Error("signing in turned Drive backups on; choosing a backup folder is what does that")
	}
	if db.SignInPhotoWall() == nil {
		t.Error("the sign-in screen's reel is still nil after signing in")
	}
	st, err := db.GetPhotoWallStatus(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Enabled || !st.GoogleConnected || !st.CanSetFolder || st.Remote != DefaultGoogleRemote {
		t.Errorf("photo wall after sign-in: enabled=%v connected=%v can_set_folder=%v remote=%q",
			st.Enabled, st.GoogleConnected, st.CanSetFolder, st.Remote)
	}

	var sawCreate bool
	for _, call := range rcloneCalls(t, argsLog) {
		if strings.HasPrefix(call, "config create ") {
			sawCreate = true
			if !strings.HasPrefix(call, "config create "+DefaultGoogleRemote+" drive ") || !strings.Contains(call, "scope=drive ") {
				t.Errorf("config ran as %q, want the shared %s remote with scope=drive", call, DefaultGoogleRemote)
			}
			if !strings.Contains(call, "token="+fakeRcloneToken) {
				t.Errorf("config ran as %q, want the token JSON rclone authorize returned", call)
			}
			if strings.Contains(call, "root_folder_id") {
				t.Errorf("config ran as %q; the folder is per command, never pinned on the remote", call)
			}
		}
	}
	if !sawCreate {
		t.Errorf("rclone calls %v, want a config create", rcloneCalls(t, argsLog))
	}

	for _, v := range []any{done, st} {
		body, _ := json.Marshal(v)
		if strings.Contains(string(body), "SECRET") {
			t.Errorf("a response carries the token: %s", body)
		}
	}
	var logged int
	if err := db.Pool.QueryRow(ctx,
		`select count(*) from activity_log where action = 'google_sign_in' and actor_id = $1`,
		admin.ID).Scan(&logged); err != nil || logged != 1 {
		t.Errorf("activity_log rows = %d (%v), want 1 naming who signed in", logged, err)
	}
}

// While Google has not called back, finishing is "not yet", not an error:
// the screen polls on it after opening Google's page itself.
func TestGoogleSignInWaitsForGoogle(t *testing.T) {
	fakeRclone(t)
	t.Setenv("FAKE_RCLONE_HANG", "1")
	db := requireTestDB(t)
	googleSettings(t, db, "", "", "")
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	ctx := context.Background()

	res, err := db.ConnectGoogle(ctx, admin)
	if err != nil {
		t.Fatalf("ConnectGoogle: %v", err)
	}
	got, err := db.FinishGoogle(ctx, admin, res.ID, "")
	if err != nil || got.Done {
		t.Errorf("FinishGoogle before Google called back = %+v, %v; want done=false and no error", got, err)
	}
}

// The school's own client reaches both `rclone authorize` (through the
// environment, never the command line) and the remote, and its secret reaches
// no response.
func TestGoogleSignInUsesTheSchoolsClient(t *testing.T) {
	argsLog := fakeRclone(t)
	fakeGoogleAbout(t, "media@example.org")
	db := requireTestDB(t)
	const clientID, clientSecret = "123-abc.apps.googleusercontent.com", "GOCSPX-FAKE-CLIENT-SECRET"
	googleSettings(t, db, "", clientID, clientSecret)
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	captureLog(t)
	ctx := context.Background()

	res, err := db.ConnectGoogle(ctx, admin)
	if err != nil {
		t.Fatalf("ConnectGoogle: %v", err)
	}
	if !strings.Contains(res.PasteCommand, clientID) || strings.Contains(res.PasteCommand, clientSecret) {
		t.Errorf("paste command = %q, want the client id and not its secret", res.PasteCommand)
	}
	// The pasted-block path, as from another machine.
	got, err := db.FinishGoogle(ctx, admin, res.ID, fakeRcloneBlob())
	if err != nil || !got.Done || !got.Google.OwnClient {
		t.Fatalf("FinishGoogle with a pasted block = %+v, %v", got, err)
	}
	body, _ := json.Marshal(got)
	if strings.Contains(string(body), clientSecret) {
		t.Errorf("the response carries the client secret: %s", body)
	}
	calls := strings.Join(rcloneCalls(t, argsLog), "\n")
	if !strings.Contains(calls, "client_id="+clientID) || !strings.Contains(calls, "client_secret="+clientSecret) {
		t.Errorf("the remote was written without the school's client:\n%s", calls)
	}
	if strings.Contains(calls, "authorize drive "+clientID) {
		t.Errorf("the client went onto authorize's command line:\n%s", calls)
	}
}

// The Drive picker hands out handles, never Drive ids, and a handle sent back
// reaches rclone as the folder it stood for.
func TestGoogleFolderPickerUsesHandles(t *testing.T) {
	argsLog := fakeRclone(t)
	t.Setenv("FAKE_RCLONE_LSJSON", `[{"Path":"Photos","Name":"Photos","IsDir":true,"ID":"1FAKEFOLDERIDxyz"},{"Path":"a.jpg","Name":"a.jpg","IsDir":false,"ID":"1FAKEFILEIDxyz"},{"Path":"Backups","Name":"Backups","IsDir":true,"ID":"1FAKEBACKUPSxyz"}]`)
	db := requireTestDB(t)
	googleSettings(t, db, "", "", "")
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	ctx := context.Background()

	folders, err := db.GoogleFolders(ctx, admin, GoogleSharedWithMe)
	if err != nil {
		t.Fatalf("GoogleFolders: %v", err)
	}
	if len(folders) != 2 || folders[0].Name != "Backups" || folders[1].Name != "Photos" {
		t.Fatalf("folders = %+v, want Backups and Photos, sorted, and no file", folders)
	}
	body, _ := json.Marshal(folders)
	if strings.Contains(string(body), "FAKE") {
		t.Errorf("the picker's response carries a Drive id: %s", body)
	}
	if _, err := db.GoogleFolders(ctx, admin, folders[1].Handle); err != nil {
		t.Fatalf("GoogleFolders(handle): %v", err)
	}
	calls := strings.Join(rcloneCalls(t, argsLog), "\n")
	if !strings.Contains(calls, "lsjson "+DefaultGoogleRemote+": --drive-shared-with-me") ||
		!strings.Contains(calls, "--drive-root-folder-id 1FAKEFOLDERIDxyz") {
		t.Errorf("rclone calls:\n%s\nwant Shared with me, then the Photos folder by its id", calls)
	}
	if _, err := db.GoogleFolders(ctx, admin, "not-a-handle"); !errors.Is(err, ErrNotFound) {
		t.Errorf("an unknown handle = %v, want ErrNotFound", err)
	}
	if _, err := db.CreateGoogleFolder(ctx, admin, GoogleSharedWithMe, "x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("a new folder in Shared with me = %v, want ErrInvalid", err)
	}
}

// The local picker lists folders only, skips hidden ones, and makes one.
func TestLocalFolderPicker(t *testing.T) {
	db := requireTestDB(t)
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	ctx := context.Background()
	dir := t.TempDir()
	for _, d := range []string{"b", "A", ".hidden"} {
		os.Mkdir(filepath.Join(dir, d), 0o755)
	}
	os.WriteFile(filepath.Join(dir, "file.txt"), nil, 0o644)

	list, err := db.LocalFolders(ctx, admin, dir)
	if err != nil {
		t.Fatalf("LocalFolders: %v", err)
	}
	if len(list.Folders) != 2 || list.Folders[0].Name != "A" || list.Folders[1].Name != "b" || list.Parent != filepath.Dir(dir) {
		t.Errorf("list = %+v, want A and b under %s", list, filepath.Dir(dir))
	}
	made, err := db.CreateLocalFolder(ctx, admin, dir, "Stockroom Backups")
	if err != nil {
		t.Fatalf("CreateLocalFolder: %v", err)
	}
	if info, err := os.Stat(made.Path); err != nil || !info.IsDir() {
		t.Errorf("made %q: %v", made.Path, err)
	}
	if _, err := db.LocalFolders(ctx, admin, "relative/path"); !errors.Is(err, ErrInvalid) {
		t.Errorf("a relative path = %v, want ErrInvalid", err)
	}
	if _, err := db.CreateLocalFolder(ctx, admin, dir, "../escape"); !errors.Is(err, ErrInvalid) {
		t.Errorf("a name with a slash = %v, want ErrInvalid", err)
	}
	student := actorFor(insertTestProfile(t, db, false, "student-pw"))
	if _, err := db.LocalFolders(ctx, student, dir); !errors.Is(err, ErrForbidden) {
		t.Errorf("a student listing folders = %v, want ErrForbidden", err)
	}
}

// Every `rclone authorize` listens on the same port, so an attempt left
// waiting -- Connect pressed twice, or pressed on Settings and then on Photo
// wall -- made the next one fail to bind and time out after thirty seconds.
// Starting a new one stops the old, and finishing the old one then says to
// a newer sign-in replaced it (409) rather than answering a 500 or a 404.
func TestStartingASignInStopsTheOneWaiting(t *testing.T) {
	fakeRclone(t)
	t.Setenv("FAKE_RCLONE_HANG", "1")
	ctx := context.Background()

	first, err := startDriveAuthorize(ctx, googleClient{})
	if err != nil {
		t.Fatalf("first startDriveAuthorize: %v", err)
	}
	began := time.Now()
	if _, err := startDriveAuthorize(ctx, googleClient{}); err != nil {
		t.Fatalf("second startDriveAuthorize: %v", err)
	}
	if waited := time.Since(began); waited > 5*time.Second {
		t.Errorf("the second sign-in took %s to start", waited)
	}

	pendingDriveAuth.mu.Lock()
	_, stillThere := pendingDriveAuth.m[first.ID]
	pendingDriveAuth.mu.Unlock()
	if stillThere {
		t.Error("the first sign-in is still pending, holding rclone's callback port")
	}
	if _, err := finishDriveAuthorize(ctx, first.ID, ""); !errors.Is(err, ErrConflict) {
		t.Errorf("finishing the stopped sign-in = %v, want a 409 saying a newer sign-in replaced it", err)
	}
}
