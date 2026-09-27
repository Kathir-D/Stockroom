// Package setup installs Stockroom on the machine it runs on: `stockroom
// setup`, `stockroom service` and `stockroom doctor`. Linux (systemd) and
// macOS (launchd over Homebrew) are supported. Windows runs the Linux package
// inside WSL.
//
// Everything that touches the machine goes through Env: commands through a
// Runner, ownership through Chown, prompts through a Prompter, and every path
// through Paths. The tests swap each for a fake rooted in a temp directory,
// so none of this needs root to test. Section 5's CI job runs it for real.
package setup

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"time"

	"stockroom/internal/platform"
	"stockroom/internal/stockroom"
)

// Paths is every place setup reads or writes.
type Paths struct {
	// ConfigFile is the config setup writes and serve reads.
	ConfigFile string
	// DataDir holds uploads, backups, the photo mirror, the photo wall's
	// cache and the camera's recordings.
	DataDir string
	// Binary is the stockroom the service runs.
	Binary string

	// Linux.
	PackagedUnit string // the unit the .deb installs
	LocalUnit    string // where `service install` writes one otherwise
	DropIn       string // the service user
	CameraDir    string // the .deb's copy of deploy/camera

	// macOS.
	BrewPrefix    string
	LaunchDaemons string
	LogFile       string
}

// Data directories under DataDir, created and owned by the service user.
var dataDirs = []string{"uploads", "backups", "photo-backups", "cache"}

const (
	serverLabel   = "com.stockroom.server"
	postgresLabel = "com.stockroom.postgresql"
	// brewPostgres is the formula macOS setup runs. Homebrew keeps it
	// keg-only, so its binaries are under opt/, never on PATH.
	brewFormula = "postgresql@17"
)

// DefaultPaths returns the real locations for goos.
func DefaultPaths(goos string) Paths {
	bin, err := os.Executable()
	if err != nil {
		bin = "/usr/bin/stockroom"
	}
	switch goos {
	case "darwin":
		prefix := stockroom.BrewPrefix()
		// A Homebrew binary runs from its Cellar, whose path changes with
		// every upgrade. The daemon points at the stable link instead.
		if rel, err := filepath.Rel(filepath.Join(prefix, "Cellar"), bin); err == nil && !startsWithDotDot(rel) {
			bin = filepath.Join(prefix, "bin", "stockroom")
		}
		return Paths{
			ConfigFile:    filepath.Join(prefix, "var", "stockroom", "stockroom.env"),
			DataDir:       filepath.Join(prefix, "var", "stockroom"),
			Binary:        bin,
			CameraDir:     filepath.Join(prefix, "share", "stockroom", "camera"),
			BrewPrefix:    prefix,
			LaunchDaemons: "/Library/LaunchDaemons",
			LogFile:       filepath.Join(prefix, "var", "log", "stockroom.log"),
		}
	default:
		return Paths{
			ConfigFile:   "/etc/stockroom/stockroom.env",
			DataDir:      "/var/lib/stockroom",
			Binary:       bin,
			PackagedUnit: "/lib/systemd/system/stockroom.service",
			LocalUnit:    "/etc/systemd/system/stockroom.service",
			DropIn:       "/etc/systemd/system/stockroom.service.d/user.conf",
			CameraDir:    "/usr/share/stockroom/camera",
		}
	}
}

func startsWithDotDot(rel string) bool {
	return rel == ".." || len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator)
}

// Env is the machine as setup sees it.
type Env struct {
	GOOS    string
	WSL     bool
	Systemd bool
	// Root is whether setup runs as root. Linux setup must; macOS setup
	// runs as the installing user and uses sudo for the LaunchDaemons.
	Root bool
	// SudoUser is who ran sudo, when Root.
	SudoUser string

	Run    Runner
	Out    io.Writer
	Prompt Prompter
	Paths  Paths

	LookupUser func(name string) (*user.User, error)
	Chown      func(path string, uid, gid int) error
	// Health answers whether the server at addr is up.
	Health func(ctx context.Context, addr string) error
	// HealthTimeout is how long setup waits for /health after starting the
	// service.
	HealthTimeout time.Duration
	// Stdin is what --admin-password-file - reads.
	Stdin io.Reader
	// Open opens a URL in the desktop's browser, or is nil where there is
	// no desktop to open it on.
	Open func(url string) error
}

// NewEnv describes the machine this process runs on.
func NewEnv() *Env {
	root := os.Geteuid() == 0
	sudoUser := ""
	if root {
		sudoUser = os.Getenv("SUDO_USER")
	}
	return &Env{
		GOOS:     runtime.GOOS,
		WSL:      platform.IsWSL(),
		Systemd:  platform.SystemdRunning(),
		Root:     root,
		SudoUser: sudoUser,
		Run:      ExecRunner{},
		Out:      os.Stdout,
		Prompt:   ttyPrompter{},
		Paths:    DefaultPaths(runtime.GOOS),
		LookupUser: func(name string) (*user.User, error) {
			return user.Lookup(name)
		},
		Chown: func(path string, uid, gid int) error {
			if !root {
				return nil
			}
			return os.Lchown(path, uid, gid)
		},
		Health:        httpHealth,
		HealthTimeout: 2 * time.Minute,
		Stdin:         os.Stdin,
		Open:          openFunc(),
	}
}

func openFunc() func(string) error {
	if !platform.HasDesktop() {
		return nil
	}
	return platform.OpenURL
}

func httpHealth(ctx context.Context, addr string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/health", nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("/health answered %s", res.Status)
	}
	return nil
}

func (e *Env) say(format string, args ...any) {
	fmt.Fprintf(e.Out, format+"\n", args...)
}

func (e *Env) ok(format string, args ...any) {
	fmt.Fprintf(e.Out, "  [OK] "+format+"\n", args...)
}

func (e *Env) warn(format string, args ...any) {
	fmt.Fprintf(e.Out, "  [WARN] "+format+"\n", args...)
}

// account is a resolved user: name, ids and home.
type account struct {
	name     string
	uid, gid int
	group    string
	home     string
}

func (e *Env) account(name string) (account, error) {
	u, err := e.LookupUser(name)
	if err != nil {
		return account{}, err
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	group := name
	if g, err := user.LookupGroupId(u.Gid); err == nil {
		group = g.Name
	}
	return account{name: u.Username, uid: uid, gid: gid, group: group, home: u.HomeDir}, nil
}

// mkdirOwned creates dir with mode and gives it to a.
func (e *Env) mkdirOwned(dir string, mode os.FileMode, a account) error {
	if err := os.MkdirAll(dir, mode); err != nil {
		return err
	}
	if err := os.Chmod(dir, mode); err != nil {
		return err
	}
	return e.Chown(dir, a.uid, a.gid)
}

// waitHealthy polls /health until it answers or the timeout passes.
func (e *Env) waitHealthy(ctx context.Context, addr string) error {
	deadline := time.Now().Add(e.HealthTimeout)
	var last error
	for {
		if last = e.Health(ctx, addr); last == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no answer from http://%s/health after %s: %w", addr, e.HealthTimeout, last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
