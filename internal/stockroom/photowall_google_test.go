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

// Tests for signing in to Google from Admin → Photo wall (photowall_google.go).
// rclone is replaced by a shell script on PATH that records its arguments, so
// nothing here reaches Google or touches the machine's real rclone.conf.

// The token the fake prints. Asserted absent from every status the screen
// receives: it belongs in rclone's config file and nowhere else.
const fakeRcloneToken = `{"access_token":"ya29.FAKE-ACCESS-SECRET","token_type":"Bearer","refresh_token":"1//FAKE-REFRESH-SECRET","expiry":"2030-01-01T00:00:00Z"}`

// fakeRclone puts a stand-in rclone first on PATH and returns the file its
// arguments are appended to, one invocation per line.
//
//   - authorize prints the sign-in link, then the token -- or, with
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
    echo '` + fakeRcloneToken + `'
    ;;
  listremotes)
    if [ -f "$FAKE_RCLONE_DIR/remote" ]; then echo "gdrive-photos:"; fi
    ;;
  config)
    touch "$FAKE_RCLONE_DIR/remote"
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

// The read-only scope has to reach `rclone authorize`: the token's scope is
// fixed when Google issues it, so a `scope = drive.readonly` line written into
// rclone.conf afterwards changes nothing about what the token can do. The blob
// is unpadded URL base64, the only form rclone accepts.
func TestDriveAuthorizeArgsCarryTheScope(t *testing.T) {
	full := driveAuthorizeArgs(driveScopeFull)
	if strings.Join(full, " ") != "authorize drive --auth-no-open-browser" {
		t.Errorf("the backup's authorize = %v, want no options blob", full)
	}

	ro := driveAuthorizeArgs(driveScopeReadOnly)
	if len(ro) != 4 {
		t.Fatalf("the photo wall's authorize = %v, want an options blob", ro)
	}
	if strings.ContainsAny(ro[2], "=+/") {
		t.Errorf("options blob %q is not unpadded URL base64; rclone refuses it", ro[2])
	}
	raw, err := base64.RawURLEncoding.DecodeString(ro[2])
	if err != nil {
		t.Fatalf("decode %q: %v", ro[2], err)
	}
	var opts map[string]string
	if err := json.Unmarshal(raw, &opts); err != nil || opts["scope"] != "drive.readonly" {
		t.Errorf("options blob decodes to %s, want scope drive.readonly", raw)
	}
}

// The whole feature, end to end: off until somebody signs in, then a sign-in
// writes a read-only remote and starts the wall on the running server with no
// restart. The token reaches rclone's config and no response.
func TestPhotoWallGoogleSignInStartsTheWall(t *testing.T) {
	argsLog := fakeRclone(t)
	db := requireTestDB(t)
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
	if st.Enabled || st.GoogleConnected || !st.CanConnectGoogle || st.Remote != DefaultPhotoWallRemote {
		t.Errorf("before sign-in: enabled=%v connected=%v can_connect=%v remote=%q; want off, not connected, connectable, %q",
			st.Enabled, st.GoogleConnected, st.CanConnectGoogle, st.Remote, DefaultPhotoWallRemote)
	}

	res, err := db.ConnectPhotoWallGoogle(ctx, admin)
	if err != nil {
		t.Fatalf("ConnectPhotoWallGoogle: %v", err)
	}
	if !strings.Contains(res.URL, "127.0.0.1:53682") {
		t.Errorf("sign-in link = %q, want rclone's local callback", res.URL)
	}
	// The fallback the dialog offers has to keep the read-only grant too.
	if want := "rclone authorize drive " + driveAuthorizeArgs(driveScopeReadOnly)[2]; res.PasteCommand != want {
		t.Errorf("paste command = %q, want %q", res.PasteCommand, want)
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
		case strings.HasPrefix(call, "authorize drive "):
			sawAuthorize = true
			if call != strings.Join(driveAuthorizeArgs(driveScopeReadOnly), " ") {
				t.Errorf("authorize ran as %q, want the read-only scope", call)
			}
		case strings.HasPrefix(call, "config create "):
			sawCreate = true
			if !strings.HasPrefix(call, "config create gdrive-photos drive ") || !strings.Contains(call, "scope=drive.readonly") {
				t.Errorf("config ran as %q, want gdrive-photos with scope=drive.readonly", call)
			}
			if strings.Contains(call, "root_folder_id") {
				t.Errorf("config ran as %q; the folder is per command, never pinned on the remote", call)
			}
		}
	}
	if !sawAuthorize || !sawCreate {
		t.Errorf("rclone calls %v, want an authorize and a config create", rcloneCalls(t, argsLog))
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

// A sign-in the backup screen started asked Google for all of Drive. Finishing
// it here would put a write-capable token under a remote that says
// drive.readonly, so it is refused and nothing is written.
func TestPhotoWallGoogleRefusesTheBackupsSignIn(t *testing.T) {
	argsLog := fakeRclone(t)
	captureLog(t)
	db := &DB{}
	admin := Actor{ID: "admin", IsAdmin: true}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	db.StartPhotoWall(ctx, PhotoWallConfig{Dir: t.TempDir()})

	res, err := startDriveAuthorize(ctx, driveScopeFull)
	if err != nil {
		t.Fatalf("startDriveAuthorize: %v", err)
	}
	if _, err := db.FinishPhotoWallGoogle(ctx, admin, res.ID, ""); !errors.Is(err, ErrConflict) {
		t.Errorf("finishing the backup's sign-in into the photo wall = %v, want ErrConflict", err)
	}
	for _, call := range rcloneCalls(t, argsLog) {
		if strings.HasPrefix(call, "config ") {
			t.Errorf("a remote was written anyway: %q", call)
		}
	}
}

// Every `rclone authorize` listens on the same port, so an attempt left
// waiting -- Connect pressed twice, or pressed on the backup screen and then
// here -- made the next one fail to bind and time out after thirty seconds.
// Starting a new one stops the old.
func TestStartingASignInStopsTheOneWaiting(t *testing.T) {
	fakeRclone(t)
	t.Setenv("FAKE_RCLONE_HANG", "1")
	ctx := context.Background()

	first, err := startDriveAuthorize(ctx, driveScopeFull)
	if err != nil {
		t.Fatalf("first startDriveAuthorize: %v", err)
	}
	began := time.Now()
	if _, err := startDriveAuthorize(ctx, driveScopeReadOnly); err != nil {
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
}
