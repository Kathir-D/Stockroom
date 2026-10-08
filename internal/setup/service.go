package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// service is the background service that runs `stockroom serve`: a systemd
// unit on Linux, a LaunchDaemon on macOS. Setup and `stockroom service` share
// it.
type service interface {
	install(ctx context.Context, a account) error
	uninstall(ctx context.Context) error
	start(ctx context.Context) error
	stop(ctx context.Context) error
	restart(ctx context.Context) error
	status(ctx context.Context) (string, error)
	// recentLog is the service's last lines of output.
	recentLog(ctx context.Context, lines int) string
}

func newService(e *Env) service {
	if e.GOOS == "darwin" {
		return &launchdService{env: e}
	}
	return &systemdService{env: e}
}

/* -------------------------------------------------------------- systemd ---- */

type systemdService struct{ env *Env }

func (s *systemdService) systemctl(ctx context.Context, args ...string) (string, error) {
	return s.env.Run.Run(ctx, Cmd{Name: "systemctl", Args: args, Root: true})
}

func (s *systemdService) install(ctx context.Context, a account) error {
	e := s.env
	// The packaged unit names linuxConfigFile. Any other config, from
	// setup --config, goes in the drop-in; a unit written here names it.
	override := ""
	if _, err := os.Stat(e.Paths.PackagedUnit); err == nil && e.Paths.ConfigFile != linuxConfigFile {
		override = e.Paths.ConfigFile
	}
	if _, err := os.Stat(e.Paths.PackagedUnit); err != nil {
		// Not installed from the package: write a unit that runs this binary.
		if err := os.MkdirAll(filepath.Dir(e.Paths.LocalUnit), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(e.Paths.LocalUnit, []byte(systemdUnit(e.Paths.Binary, e.Paths.ConfigFile)), 0o644); err != nil {
			return err
		}
		e.ok("wrote %s for %s", e.Paths.LocalUnit, e.Paths.Binary)
	}
	if err := os.MkdirAll(filepath.Dir(e.Paths.DropIn), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(e.Paths.DropIn, []byte(userDropIn(a.name, a.group, e.Paths.Binary, override)), 0o644); err != nil {
		return err
	}
	if _, err := s.systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	if _, err := s.systemctl(ctx, "enable", "stockroom"); err != nil {
		return err
	}
	e.ok("service enabled, running as %s and reading %s", a.name, e.Paths.ConfigFile)
	return nil
}

func (s *systemdService) uninstall(ctx context.Context) error {
	e := s.env
	_, _ = s.systemctl(ctx, "disable", "--now", "stockroom")
	for _, p := range []string{e.Paths.DropIn, e.Paths.LocalUnit} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	_ = os.Remove(filepath.Dir(e.Paths.DropIn))
	_, err := s.systemctl(ctx, "daemon-reload")
	return err
}

func (s *systemdService) start(ctx context.Context) error {
	_, err := s.systemctl(ctx, "start", "stockroom")
	return err
}

func (s *systemdService) stop(ctx context.Context) error {
	_, err := s.systemctl(ctx, "stop", "stockroom")
	return err
}

func (s *systemdService) restart(ctx context.Context) error {
	_, err := s.systemctl(ctx, "restart", "stockroom")
	return err
}

func (s *systemdService) status(ctx context.Context) (string, error) {
	// systemctl status exits non-zero for a stopped unit; its text is the
	// answer either way.
	out, err := s.systemctl(ctx, "status", "stockroom", "--no-pager", "--lines=0")
	if out != "" {
		return out, nil
	}
	return out, err
}

func (s *systemdService) recentLog(ctx context.Context, lines int) string {
	out, err := s.env.Run.Run(ctx, Cmd{Name: "journalctl", Args: []string{"-u", "stockroom", "-n", strconv.Itoa(lines), "--no-pager"}, Root: true})
	if err != nil {
		return "journalctl -u stockroom: " + err.Error()
	}
	return out
}

/* -------------------------------------------------------------- launchd ---- */

type launchdService struct{ env *Env }

func (s *launchdService) plistPath() string {
	return filepath.Join(s.env.Paths.LaunchDaemons, serverLabel+".plist")
}

func (s *launchdService) install(ctx context.Context, a account) error {
	e := s.env
	if err := os.MkdirAll(filepath.Dir(e.Paths.LogFile), 0o755); err != nil {
		return err
	}
	plist := launchDaemon(serverLabel, a.name,
		[]string{e.Paths.Binary, "serve", "--config", e.Paths.ConfigFile},
		e.Paths.LogFile,
		map[string]string{
			// A daemon's PATH is /usr/bin:/bin:/usr/sbin:/sbin. rclone and
			// pg_dump are Homebrew's.
			"PATH": filepath.Join(e.Paths.BrewPrefix, "bin") + ":/usr/bin:/bin:/usr/sbin:/sbin",
			// rclone keeps its Google sign-in under HOME.
			"HOME":            a.home,
			"HOMEBREW_PREFIX": e.Paths.BrewPrefix,
		})
	if err := loadDaemon(ctx, e, serverLabel, plist); err != nil {
		return err
	}
	e.ok("LaunchDaemon %s loaded, running as %s and reading %s", serverLabel, a.name, e.Paths.ConfigFile)
	return nil
}

func (s *launchdService) uninstall(ctx context.Context) error {
	e := s.env
	_, _ = e.Run.Run(ctx, Cmd{Name: "launchctl", Args: []string{"bootout", "system/" + serverLabel}, Root: true})
	_, err := e.Run.Run(ctx, Cmd{Name: "rm", Args: []string{"-f", s.plistPath()}, Root: true})
	return err
}

func (s *launchdService) loaded(ctx context.Context) bool {
	return daemonLoaded(ctx, s.env, serverLabel)
}

func (s *launchdService) start(ctx context.Context) error {
	if !s.loaded(ctx) {
		_, err := s.env.Run.Run(ctx, Cmd{Name: "launchctl", Args: []string{"bootstrap", "system", s.plistPath()}, Root: true})
		return err
	}
	return s.restart(ctx)
}

// stop unloads the daemon. KeepAlive would restart a process that was only
// killed, and the plist stays, so the next boot starts it again.
func (s *launchdService) stop(ctx context.Context) error {
	_, err := s.env.Run.Run(ctx, Cmd{Name: "launchctl", Args: []string{"bootout", "system/" + serverLabel}, Root: true})
	return err
}

func (s *launchdService) restart(ctx context.Context) error {
	_, err := s.env.Run.Run(ctx, Cmd{Name: "launchctl", Args: []string{"kickstart", "-k", "system/" + serverLabel}, Root: true})
	return err
}

func (s *launchdService) status(ctx context.Context) (string, error) {
	out, err := s.env.Run.Run(ctx, Cmd{Name: "launchctl", Args: []string{"print", "system/" + serverLabel}, Root: true})
	if err != nil {
		if _, statErr := os.Stat(s.plistPath()); statErr != nil {
			return "not installed", nil
		}
		return "installed, not loaded", nil
	}
	var parts []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "state = ") || strings.HasPrefix(line, "pid = ") || strings.HasPrefix(line, "last exit code = ") {
			parts = append(parts, line)
		}
	}
	return strings.Join(parts, "\n"), nil
}

func (s *launchdService) recentLog(ctx context.Context, lines int) string {
	out, err := s.env.Run.Run(ctx, Cmd{Name: "tail", Args: []string{"-n", strconv.Itoa(lines), s.env.Paths.LogFile}})
	if err != nil {
		return "tail " + s.env.Paths.LogFile + ": " + err.Error()
	}
	return out
}

// unloadWait is how long loadDaemon gives launchd to let go of a daemon it
// booted out.
var unloadWait = 30 * time.Second

func daemonLoaded(ctx context.Context, e *Env, label string) bool {
	_, err := e.Run.Run(ctx, Cmd{Name: "launchctl", Args: []string{"print", "system/" + label}, Root: true})
	return err == nil
}

// loadDaemon writes a LaunchDaemon plist owned by root and (re)loads it.
func loadDaemon(ctx context.Context, e *Env, label, plist string) error {
	path := filepath.Join(e.Paths.LaunchDaemons, label+".plist")
	if err := writeRootFile(ctx, e, path, plist); err != nil {
		return err
	}
	// bootout fails when it isn't loaded, which is fine.
	_, _ = e.Run.Run(ctx, Cmd{Name: "launchctl", Args: []string{"bootout", "system/" + label}, Root: true})
	// bootout can return while the daemon is still shutting down (Postgres
	// takes a moment), and a bootstrap before launchd has let go of the label
	// fails with "Bootstrap failed: 5: Input/output error".
	deadline := time.Now().Add(unloadWait)
	for daemonLoaded(ctx, e, label) && time.Now().Before(deadline) {
		if err := sleep(ctx, 500*time.Millisecond); err != nil {
			return err
		}
	}
	if _, err := e.Run.Run(ctx, Cmd{Name: "launchctl", Args: []string{"bootstrap", "system", path}, Root: true}); err != nil {
		return fmt.Errorf("load %s: %w", path, err)
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// retireCheckoutAgent stops the LaunchAgent the older checkout install
// (scripts/install.sh) left in a's login session. It has the daemon's label
// and listens on the same address, so the daemon could never bind while it
// runs. The plist is renamed rather than deleted, so the agent stays off at
// the next login and the older install can still be put back.
func retireCheckoutAgent(ctx context.Context, e *Env, a account) {
	dir := e.Paths.LaunchAgents
	if dir == "" {
		dir = filepath.Join(a.home, "Library", "LaunchAgents")
	}
	plist := filepath.Join(dir, serverLabel+".plist")
	if _, err := os.Stat(plist); err != nil {
		return
	}
	// bootout fails when nobody is logged in to have loaded it, which is fine.
	_, _ = e.Run.Run(ctx, Cmd{Name: "launchctl", Args: []string{"bootout", "gui/" + strconv.Itoa(a.uid) + "/" + serverLabel}, Root: e.Root})
	if err := os.Rename(plist, plist+".replaced"); err != nil {
		e.warn("the older install's LaunchAgent %s is still in place: %v", plist, err)
		return
	}
	e.warn("stopped the older checkout install's service and renamed its LaunchAgent to %s.replaced. Its folder and its database are untouched, and this install starts with its own database", plist)
}

// writeRootFile writes a root-owned file with mode 644. Without root it goes
// through a temp file and `sudo install`, which asks for the password once.
func writeRootFile(ctx context.Context, e *Env, path, content string) error {
	if e.Root {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return err
		}
		return e.Chown(path, 0, 0)
	}
	tmp, err := os.CreateTemp("", filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	_, err = e.Run.Run(ctx, Cmd{Name: "install", Args: []string{"-m", "644", "-o", "root", "-g", "wheel", tmp.Name(), path}, Root: true})
	return err
}

// Service runs `stockroom service <action>`. install takes the account to run
// as: userName, else the one already in the drop-in, else whoever ran sudo.
func Service(ctx context.Context, e *Env, action, userName, configPath string) error {
	switch e.GOOS {
	case "linux":
		if !e.Root {
			return errors.New("run it with sudo")
		}
		if !e.Systemd {
			return errors.New("systemd is not running, so there is no service to manage")
		}
	case "darwin":
	default:
		return fmt.Errorf("services are managed on Linux and macOS, not %s", e.GOOS)
	}
	if err := useConfig(e, configPath); err != nil {
		return err
	}
	svc := newService(e)
	switch action {
	case "install":
		name := userName
		if name == "" && e.GOOS == "linux" {
			name = previousServiceUser(e.Paths.DropIn)
		}
		if name == "" {
			name = e.SudoUser
		}
		if name == "" && e.GOOS == "darwin" {
			name = os.Getenv("USER")
		}
		if name == "" || name == "root" {
			return errors.New("say which account runs the service: --user <name>")
		}
		a, err := e.account(name)
		if err != nil {
			return err
		}
		if err := svc.install(ctx, a); err != nil {
			return err
		}
		return svc.start(ctx)
	case "uninstall":
		if err := svc.uninstall(ctx); err != nil {
			return err
		}
		e.ok("service removed. The config, the data and the database are untouched")
		return nil
	case "start":
		return svc.start(ctx)
	case "stop":
		return svc.stop(ctx)
	case "restart":
		return svc.restart(ctx)
	case "status":
		out, err := svc.status(ctx)
		if err != nil {
			return err
		}
		e.say("%s", strings.TrimRight(out, "\n"))
		return nil
	}
	return fmt.Errorf("unknown action %q: want install, uninstall, start, stop, restart or status", action)
}
