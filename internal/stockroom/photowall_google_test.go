package stockroom

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Tests for the one Google connection (google.go) and for signing in to it
// from Admin → Photo wall (photowall_google.go).
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
//   - lsjson answers an empty folder, so a wall that starts has nothing to do.
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
    echo "[]"
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
	var r, id, secret *string
	var enabled bool
	if err := db.Pool.QueryRow(ctx, `select drive_remote, drive_enabled, google_client_id, google_client_secret
		from app_settings where id = true`).Scan(&r, &enabled, &id, &secret); err != nil {
		t.Fatalf("read settings: %v", err)
	}
	t.Cleanup(func() {
		db.Pool.Exec(context.Background(), `update app_settings set drive_remote = $1, drive_enabled = $2,
			google_client_id = $3, google_client_secret = $4 where id = true`, r, enabled, id, secret)
	})
	if _, err := db.Pool.Exec(ctx, `update app_settings set drive_remote = $1, drive_enabled = false,
		google_client_id = $2, google_client_secret = $3 where id = true`,
		nullable(remote), nullable(clientID), nullable(clientSecret)); err != nil {
		t.Fatalf("set settings: %v", err)
	}
}

// The whole feature, end to end: off until somebody signs in, then a sign-in
// on the Photo wall screen writes the one shared Google remote and starts the
// wall on the running server with no restart. The token reaches rclone's
// config and no response.
func TestPhotoWallGoogleSignInStartsTheWall(t *testing.T) {
	argsLog := fakeRclone(t)
	db := requireTestDB(t)
	googleSettings(t, db, "", "", "")
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	captureLog(t)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { db.SetPhotoWall(nil, nil) })
	t.Cleanup(func() {
		db.Pool.Exec(context.Background(),
			`delete from activity_log where action = 'signin_photo_wall_google' and actor_id = $1`, admin.ID)
	})

	db.StartPhotoWall(ctx, PhotoWallConfig{Dir: t.TempDir()})
	if db.SignInPhotoWall() != nil {
		t.Fatal("the wall started with nobody signed in to Google")
	}
	st, err := db.GetPhotoWallStatus(ctx, admin)
	if err != nil {
		t.Fatalf("GetPhotoWallStatus: %v", err)
	}
	if st.Enabled || st.GoogleConnected || !st.CanConnectGoogle || st.Remote != DefaultGoogleRemote {
		t.Errorf("before sign-in: enabled=%v connected=%v can_connect=%v remote=%q; want off, not connected, connectable, %q",
			st.Enabled, st.GoogleConnected, st.CanConnectGoogle, st.Remote, DefaultGoogleRemote)
	}

	res, err := db.ConnectPhotoWallGoogle(ctx, admin)
	if err != nil {
		t.Fatalf("ConnectPhotoWallGoogle: %v", err)
	}
	if !strings.Contains(res.URL, "127.0.0.1:53682") {
		t.Errorf("sign-in link = %q, want rclone's local callback", res.URL)
	}

	// The fake prints its token straight after the link; Finish polls for it.
	st, err = db.FinishPhotoWallGoogle(ctx, admin, res.ID, "")
	if err != nil {
		t.Fatalf("FinishPhotoWallGoogle: %v", err)
	}
	if !st.Enabled || !st.GoogleConnected || !st.CanSetFolder {
		t.Errorf("after sign-in: enabled=%v connected=%v can_set_folder=%v; want all true, with no restart",
			st.Enabled, st.GoogleConnected, st.CanSetFolder)
	}
	if db.SignInPhotoWall() == nil {
		t.Error("the sign-in screen's reel is still nil after the wall started")
	}

	var sawAuthorize, sawCreate bool
	for _, call := range rcloneCalls(t, argsLog) {
		switch {
		case strings.HasPrefix(call, "authorize drive"):
			sawAuthorize = true
		case strings.HasPrefix(call, "config create "):
			sawCreate = true
			if !strings.HasPrefix(call, "config create "+DefaultGoogleRemote+" drive ") || !strings.Contains(call, "scope=drive ") {
				t.Errorf("config ran as %q, want the shared %s remote with scope=drive", call, DefaultGoogleRemote)
			}
			// The token rclone printed, unframed -- not the arrows around it.
			if !strings.Contains(call, "token="+fakeRcloneToken) {
				t.Errorf("config ran as %q, want the token JSON rclone authorize returned", call)
			}
			if strings.Contains(call, "root_folder_id") {
				t.Errorf("config ran as %q; the folder is per command, never pinned on the remote", call)
			}
		}
	}
	if !sawAuthorize || !sawCreate {
		t.Errorf("rclone calls %v, want an authorize and a config create", rcloneCalls(t, argsLog))
	}

	s, err := db.loadSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.DriveRemote != DefaultGoogleRemote || s.DriveEnabled {
		t.Errorf("after the photo wall's sign-in: drive_remote=%q drive_enabled=%v; want the shared remote recorded and backups left as they were",
			s.DriveRemote, s.DriveEnabled)
	}

	body, _ := json.Marshal(st)
	if strings.Contains(string(body), "SECRET") {
		t.Errorf("the status carries the token: %s", body)
	}

	var logged int
	if err := db.Pool.QueryRow(ctx,
		`select count(*) from activity_log where action = 'signin_photo_wall_google' and actor_id = $1`,
		admin.ID).Scan(&logged); err != nil || logged != 1 {
		t.Errorf("activity_log rows = %d (%v), want 1 naming who signed in", logged, err)
	}
}

// One connection, from the other side: Connect under Settings → Google Drive
// writes the same remote, turns backups on, and starts a photo wall that was
// waiting for a sign-in. The school's own client reaches both `rclone
// authorize` (through the environment) and the remote, and its secret reaches
// no response.
func TestBackupConnectIsTheOneGoogleConnection(t *testing.T) {
	argsLog := fakeRclone(t)
	db := requireTestDB(t)
	const clientID, clientSecret = "123-abc.apps.googleusercontent.com", "GOCSPX-FAKE-CLIENT-SECRET"
	googleSettings(t, db, "", clientID, clientSecret)
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	captureLog(t)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { db.SetPhotoWall(nil, nil) })
	db.StartPhotoWall(ctx, PhotoWallConfig{Dir: t.TempDir()})

	res, err := db.ConnectDrive(ctx, admin)
	if err != nil {
		t.Fatalf("ConnectDrive: %v", err)
	}
	if !strings.Contains(res.PasteCommand, clientID) || strings.Contains(res.PasteCommand, clientSecret) {
		t.Errorf("paste command = %q, want the client id and not its secret", res.PasteCommand)
	}
	s, err := db.FinishDriveConnect(ctx, admin, res.ID, "", "")
	if err != nil {
		t.Fatalf("FinishDriveConnect: %v", err)
	}
	if s.DriveRemote != DefaultGoogleRemote || !s.DriveEnabled {
		t.Errorf("drive_remote=%q drive_enabled=%v, want %q and on", s.DriveRemote, s.DriveEnabled, DefaultGoogleRemote)
	}
	if s.GoogleClientSecret != "" || !s.GoogleClientSecretSet || s.GoogleClientID != clientID {
		t.Errorf("settings response: client_id=%q secret=%q secret_set=%v; want the id, a blank secret and _set", s.GoogleClientID, s.GoogleClientSecret, s.GoogleClientSecretSet)
	}
	if db.SignInPhotoWall() == nil {
		t.Error("the backup's Connect did not start the photo wall, which reads through the same remote")
	}

	calls := strings.Join(rcloneCalls(t, argsLog), "\n")
	if !strings.Contains(calls, "client="+clientID+" secret="+clientSecret) {
		t.Errorf("rclone authorize did not get the school's client in its environment:\n%s", calls)
	}
	if !strings.Contains(calls, "client_id="+clientID) || !strings.Contains(calls, "client_secret="+clientSecret) {
		t.Errorf("the remote was written without the school's client:\n%s", calls)
	}
	if strings.Contains(calls, "authorize drive "+clientID) {
		t.Errorf("the client went onto authorize's command line:\n%s", calls)
	}

	st, err := db.GetPhotoWallStatus(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if !st.GoogleConnected || st.Remote != DefaultGoogleRemote {
		t.Errorf("photo wall after the backup's Connect: connected=%v remote=%q", st.GoogleConnected, st.Remote)
	}
}

// Every `rclone authorize` listens on the same port, so an attempt left
// waiting -- Connect pressed twice, or pressed on Settings and then on Photo
// wall -- made the next one fail to bind and time out after thirty seconds.
// Starting a new one stops the old, and finishing the old one then says to
// press Connect again rather than answering a 500.
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
	if _, err := finishDriveAuthorize(ctx, first.ID, ""); !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrConflict) {
		t.Errorf("finishing the stopped sign-in = %v, want a 404 or 409 that says to press Connect again", err)
	}
}
