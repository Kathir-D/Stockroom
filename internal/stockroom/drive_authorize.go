package stockroom

import (
	"bufio"
	"context"
	"encoding/hex"
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

type driveAuth struct {
	id     string
	url    string
	cancel context.CancelFunc

	mu    sync.Mutex
	token string
	err   error
	done  bool
}

var pendingDriveAuth = struct {
	mu sync.Mutex
	m  map[string]*driveAuth
}{m: map[string]*driveAuth{}}

// startDriveAuthorize launches rclone and returns as soon as it has printed a
// URL to open. The process keeps running in the background waiting for
// Google's callback.
func startDriveAuthorize(parent context.Context) (DriveConnectResult, error) {
	// Deliberately not derived from the request's context: the request that
	// starts this returns in a second, and the authorization it started has to
	// outlive it by as long as it takes somebody to sign in to Google.
	ctx, cancel := context.WithTimeout(context.Background(), authorizeTimeout)

	cmd := exec.CommandContext(ctx, rcloneBinary, "authorize", "drive", "--auth-no-open-browser")
	cmd.Env = os.Environ()
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
	auth := &driveAuth{id: id, cancel: cancel}

	urlFound := make(chan string, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); auth.scan(stdout, urlFound) }()
	go func() { defer wg.Done(); auth.scan(stderr, urlFound) }()
	go func() {
		wg.Wait()
		err := cmd.Wait()
		auth.finish(err)
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

	return DriveConnectResult{URL: auth.url, ID: id}, nil
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
		if strings.HasPrefix(line, "{") && strings.Contains(line, "access_token") {
			a.mu.Lock()
			a.token = line
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
			a.err = fmt.Errorf("the Google connection did not complete: %w", err)
		} else {
			a.err = fmt.Errorf("%w: the Google connection did not complete. Open the link again, or paste the code rclone printed", ErrConflict)
		}
	}
}

// finishDriveAuthorize returns the token for a pending authorization: the one
// the callback produced, or the blob the admin pasted.
func finishDriveAuthorize(ctx context.Context, id, pasted string) (string, error) {
	if pasted != "" {
		// The admin pasted rclone's blob. It is the token itself, so nothing
		// is waiting on: this is the path that works when the callback did
		// not.
		if !strings.HasPrefix(pasted, "{") {
			return "", fmt.Errorf("%w: paste the whole block rclone printed, starting with {", ErrInvalid)
		}
		stopDriveAuthorize(id)
		return pasted, nil
	}

	pendingDriveAuth.mu.Lock()
	auth := pendingDriveAuth.m[id]
	pendingDriveAuth.mu.Unlock()
	if auth == nil {
		return "", fmt.Errorf("%w: that connection attempt has expired. Press Connect again", ErrNotFound)
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
			return "", fmt.Errorf("%w: Google has not sent the code back yet. Finish signing in, then press Save again -- or paste the block rclone printed", ErrConflict)
		}
		time.Sleep(100 * time.Millisecond)
	}
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
