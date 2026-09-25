package stockroom

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"crypto/rand"
)

// Connecting a Google account from the admin panel (docs/design/backup.md
// §E.3), so a revoked token never needs a terminal.
//
// `rclone authorize "drive"` is an interactive command with three phases: it
// prints a URL, listens on 127.0.0.1:53682 for Google's callback, then prints
// the token as a JSON blob. The browser is on the same machine as the server
// -- the closet PC runs both -- so the callback lands where rclone is
// listening and the token arrives on its own.
//
// The pasted-code path is kept as a fallback anyway, because the callback is
// the part that can fail (another rclone holding the port, a browser on a
// different machine), and "paste the blob rclone printed" is a recovery that
// needs no terminal on *this* machine.

// authorizeTimeout is how long a half-finished connection is held open. Long
// enough to find a password and get through Google's unverified-app screen,
// short enough that a forgotten tab does not leave a process running all term.
const authorizeTimeout = 15 * time.Minute

// driveAuthRetention is how long a *finished* authorization stays consumable
// after rclone exits.
//
// It has to be non-zero: the token is the last thing rclone prints, so the
// entry must outlive the process for the admin's Save -- a separate request,
// possibly seconds behind -- to find it. It has to be bounded: nothing else
// removes an entry for an attempt the admin never came back to finish, so
// without an expiry the map holds a live Google refresh token in memory for
// as long as the server runs.
const driveAuthRetention = 10 * time.Minute

// driveAuthURL matches the link rclone prints. Its wording has changed between
// releases, so the pattern matches the URL rather than the sentence around it.
var driveAuthURL = regexp.MustCompile(`https?://(127\.0\.0\.1|localhost):\d+/auth\S*`)

// googleClient is the OAuth client a sign-in uses: the school's own from
// Admin → Settings, or blank for rclone's shared one, which rclone is retiring
// during 2026 (https://rclone.org/drive/#making-your-own-client-id).
type googleClient struct {
	id, secret string
}

func (c googleClient) shared() bool { return c.id == "" }

// driveAuthorizeArgs is the command line for one sign-in. It asks for the
// full `drive` scope, which is rclone's default and what the one remote both
// Drive features share needs: the backup writes, the photo wall reads.
//
// The client travels in the environment (driveAuthorizeEnv), not here, so the
// secret is off the process list for as long as authorize waits (config
// create is another matter, google.go). rclone reads RCLONE_DRIVE_CLIENT_ID and
// _SECRET for the temporary remote `authorize` builds -- verified against
// v1.75: the consent URL carries the id from the environment.
func driveAuthorizeArgs() []string {
	return []string{"authorize", "drive", "--auth-no-open-browser"}
}

func driveAuthorizeEnv(c googleClient) []string {
	env := os.Environ()
	if !c.shared() {
		env = append(env, "RCLONE_DRIVE_CLIENT_ID="+c.id, "RCLONE_DRIVE_CLIENT_SECRET="+c.secret)
	}
	return env
}

// drivePasteCommand is the same authorize, as a person would type it on a
// machine whose browser can reach Google. With the school's own client it has
// to carry that client, since the token is bound to the client that asked for
// it; the secret is a placeholder because no response carries a secret.
func drivePasteCommand(c googleClient) string {
	if c.shared() {
		return "rclone authorize drive"
	}
	return "rclone authorize drive " + c.id + " YOUR-CLIENT-SECRET"
}

type driveAuth struct {
	id     string
	url    string
	cancel context.CancelFunc
	// exited is closed once rclone has exited, which is when its callback
	// port is free again.
	exited chan struct{}

	mu    sync.Mutex
	token string
	err   error
	done  bool
}

var pendingDriveAuth = struct {
	mu sync.Mutex
	m  map[string]*driveAuth
	// superseded holds the attempts the latest Connect stopped, so finishing
	// one says a newer sign-in replaced it rather than that it never existed.
	// Replaced wholesale on every Connect, so it stays a handful of ids.
	superseded map[string]bool
}{m: map[string]*driveAuth{}}

// startDriveAuthorize launches rclone and returns as soon as it has printed a
// URL to open. The process keeps running in the background waiting for
// Google's callback.
func startDriveAuthorize(parent context.Context, client googleClient) (DriveConnectResult, error) {
	// Every `rclone authorize` listens on the same port, 127.0.0.1:53682, so a
	// second one cannot start while an earlier one is still waiting -- it fails
	// to bind and never prints a link, which surfaced as a 30-second timeout
	// naming `rclone version`. The earlier attempt is one the admin walked
	// away from (pressed Connect twice, or on Settings and then on Photo
	// wall), so it is the one to stop.
	stopUnfinishedDriveAuthorize()

	// Deliberately not derived from the request's context: the request that
	// starts this returns in a second, and the authorization it started has to
	// outlive it by as long as it takes somebody to sign in to Google.
	ctx, cancel := context.WithTimeout(context.Background(), authorizeTimeout)

	cmd := exec.CommandContext(ctx, rcloneBinary, driveAuthorizeArgs()...)
	cmd.Env = driveAuthorizeEnv(client)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return DriveConnectResult{}, fmt.Errorf("start the Google connection: %w", err)
	}
	// rclone prints the link on stderr in some versions and stdout in others,
	// so both are read and both are searched.
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return DriveConnectResult{}, fmt.Errorf("start the Google connection: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return DriveConnectResult{}, fmt.Errorf("start the Google connection: %w", err)
	}

	id, err := randomID()
	if err != nil {
		cancel()
		return DriveConnectResult{}, err
	}
	auth := &driveAuth{id: id, cancel: cancel, exited: make(chan struct{})}

	urlFound := make(chan string, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); auth.scan(stdout, urlFound) }()
	go func() { defer wg.Done(); auth.scan(stderr, urlFound) }()
	go func() {
		wg.Wait()
		err := cmd.Wait()
		auth.finish(err)
		close(auth.exited)
		cancel()
		// The entry deliberately survives rclone exiting, so a Save that
		// arrives after the token was printed still finds it. Reaping it later
		// is what keeps that from being a leak: finishDriveAuthorize removes
		// the entry when it consumes one, and this removes the ones nobody
		// ever came back for.
		time.AfterFunc(driveAuthRetention, func() { stopDriveAuthorize(id) })
	}()

	select {
	case url := <-urlFound:
		auth.url = url
	case <-time.After(30 * time.Second):
		cancel()
		return DriveConnectResult{}, fmt.Errorf("%w: rclone did not offer a sign-in link within 30 seconds. Check that `rclone version` works on this machine", ErrNotConfigured)
	}

	pendingDriveAuth.mu.Lock()
	pendingDriveAuth.m[id] = auth
	pendingDriveAuth.mu.Unlock()

	return DriveConnectResult{URL: auth.url, ID: id, PasteCommand: drivePasteCommand(client)}, nil
}

// scan reads one of rclone's streams, publishing the sign-in URL when it
// appears and keeping the token blob when it does.
func (a *driveAuth) scan(r io.Reader, urlFound chan<- string) {
	sc := bufio.NewScanner(r)
	// The token blob is one long JSON line; the default 64 KB scanner buffer
	// is ample, but the line is long enough to be worth saying so.
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if url := driveAuthURL.FindString(line); url != "" {
			select {
			case urlFound <- url:
			default:
			}
			continue
		}
		if token, ok := authorizeToken(line); ok {
			a.mu.Lock()
			a.token = token
			a.mu.Unlock()
		}
	}
}

func (a *driveAuth) finish(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.done = true
	if a.token == "" && a.err == nil {
		if err != nil {
			// Usually this attempt was stopped: a newer Connect frees the
			// callback port by stopping it, and so does the 15-minute limit.
			// Either way the fix is the same button, so it is a conflict the
			// admin can read, not a 500 with the reason hidden in the log.
			a.err = fmt.Errorf("%w: the Google sign-in stopped before it finished (%v). Press Sign in with Google again", ErrConflict, err)
		} else {
			a.err = fmt.Errorf("%w: the Google connection did not complete. Open the link again, or paste the code rclone printed", ErrConflict)
		}
	}
}

// errGoogleWaiting is finishDriveAuthorize's "not yet": the sign-in page is
// open and Google has not called back. The screen polls on it, so it is a
// state rather than a failure, and FinishGoogle turns it into done=false.
var errGoogleWaiting = fmt.Errorf("%w: Google has not sent the sign-in back yet. Finish signing in in the Google window", ErrConflict)

// finishDriveAuthorize returns the token for a pending authorization: the one
// the callback produced, or the blob the admin pasted.
func finishDriveAuthorize(ctx context.Context, id, pasted string) (string, error) {
	pendingDriveAuth.mu.Lock()
	auth := pendingDriveAuth.m[id]
	superseded := pendingDriveAuth.superseded[id]
	pendingDriveAuth.mu.Unlock()

	if pasted != "" {
		// The admin pasted rclone's blob. It is the token itself, so nothing
		// is waiting on: this is the path that works when the callback did
		// not.
		token, ok := authorizeToken(pasted)
		if !ok {
			return "", fmt.Errorf("%w: paste the whole block rclone printed between the two arrows, and nothing else", ErrInvalid)
		}
		stopDriveAuthorize(id)
		return token, nil
	}

	if superseded {
		return "", fmt.Errorf("%w: a newer Google sign-in replaced this one. Finish that one, or press Sign in with Google again", ErrConflict)
	}
	if auth == nil {
		return "", fmt.Errorf("%w: that sign-in has expired. Press Sign in with Google again", ErrNotFound)
	}

	// Poll rather than block on a channel: the token arrives on a background
	// goroutine and the caller is an HTTP request with its own deadline, so
	// what matters is answering promptly either way.
	deadline := time.Now().Add(2 * time.Second)
	for {
		auth.mu.Lock()
		token, err, done := auth.token, auth.err, auth.done
		auth.mu.Unlock()
		if token != "" {
			stopDriveAuthorize(id)
			return token, nil
		}
		if err != nil {
			stopDriveAuthorize(id)
			return "", err
		}
		if done || time.Now().After(deadline) || ctx.Err() != nil {
			return "", errGoogleWaiting
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// stopUnfinishedDriveAuthorize stops every attempt still waiting on Google,
// which is what frees rclone's callback port for a new one. An attempt that
// already has its token is left alone: its rclone has exited, it holds no
// port, and the admin's Finish may be a second behind.
func stopUnfinishedDriveAuthorize() {
	pendingDriveAuth.mu.Lock()
	var waiting []*driveAuth
	for _, auth := range pendingDriveAuth.m {
		auth.mu.Lock()
		if !auth.done && auth.token == "" {
			waiting = append(waiting, auth)
		}
		auth.mu.Unlock()
	}
	superseded := make(map[string]bool, len(waiting))
	for _, auth := range waiting {
		superseded[auth.id] = true
	}
	pendingDriveAuth.superseded = superseded
	pendingDriveAuth.mu.Unlock()
	for _, auth := range waiting {
		stopDriveAuthorize(auth.id)
		// Cancelling signals the process; it has not necessarily let go of
		// the port by the time the new one tries to bind. Bounded, because a
		// wedged process must not hold up the admin's button press forever.
		if auth.exited != nil {
			select {
			case <-auth.exited:
			case <-time.After(3 * time.Second):
			}
		}
	}
}

// authorizeToken finds the token in one line of what `rclone authorize`
// printed, or in the block an admin pasted from it, and returns it as the
// JSON `rclone config create ... token=` takes.
//
// It comes in two shapes, and which one depends on the command line, not on
// the rclone version. Called with no options blob, as Stockroom calls it,
// rclone prints the token JSON itself. Called with one -- as rclone's own
// remote setup does, and as the photo wall's read-only sign-in did until
// 2026-09-25 -- it answers in kind: the whole resulting config, JSON-encoded
// then base64'd, with the token JSON as its "token" value
// (fs/config/authorize.go, "If received a config blob, then return one").
// Only the first shape used to be recognised, so every photo wall sign-in
// reached Google, succeeded, and was reported as not having completed. Both
// are read, because a block pasted from another machine may be either.
func authorizeToken(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "{") {
		return s, strings.Contains(s, "access_token")
	}
	// A pasted block may carry rclone's "--->" and "<---End paste" lines too.
	if strings.Contains(s, "\n") {
		for _, line := range strings.Split(s, "\n") {
			if token, ok := authorizeToken(line); ok {
				return token, true
			}
		}
		return "", false
	}
	if s == "" || strings.ContainsAny(s, " \t") {
		return "", false
	}
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding} {
		b, err := enc.DecodeString(s)
		if err != nil {
			continue
		}
		var blob map[string]string
		if json.Unmarshal(b, &blob) != nil {
			return "", false
		}
		token := strings.TrimSpace(blob["token"])
		return token, strings.HasPrefix(token, "{") && strings.Contains(token, "access_token")
	}
	return "", false
}

func stopDriveAuthorize(id string) {
	pendingDriveAuth.mu.Lock()
	auth := pendingDriveAuth.m[id]
	delete(pendingDriveAuth.m, id)
	pendingDriveAuth.mu.Unlock()
	if auth != nil && auth.cancel != nil {
		auth.cancel()
	}
}

func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate an id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
