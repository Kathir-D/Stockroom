package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// postgres is the local PostgreSQL as setup drives it: start it and keep it
// starting at boot, then run SQL as its superuser over the local socket.
type postgres interface {
	start(ctx context.Context) error
	sql(ctx context.Context, sql string) (string, error)
}

// psqlArgs reads SQL from stdin, so a password in it never reaches the
// process list, and prints bare values one per line.
var psqlArgs = []string{"-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-d", "postgres", "-f", "-"}

// waitForSQL retries "select 1" for up to a minute while Postgres starts.
func waitForSQL(ctx context.Context, pg postgres) error {
	deadline := time.Now().Add(time.Minute)
	for {
		_, err := pg.sql(ctx, "select 1;")
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("PostgreSQL did not answer within a minute: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

/* ---------------------------------------------------------------- linux ---- */

// linuxPostgres is the distribution's postgresql package. Debian and Ubuntu
// authenticate the postgres account over the local socket by peer, so psql
// runs as that account and needs no password.
type linuxPostgres struct {
	env     *Env
	systemd bool
}

func (p *linuxPostgres) start(ctx context.Context) error {
	e := p.env
	if p.systemd {
		if _, err := e.Run.Run(ctx, Cmd{Name: "systemctl", Args: []string{"enable", "--now", "postgresql"}, Root: true}); err != nil {
			return fmt.Errorf("%w\nIs PostgreSQL installed? sudo apt install postgresql", err)
		}
	} else {
		// A container with no systemd. The init script starts every cluster.
		if _, err := e.Run.Run(ctx, Cmd{Name: "service", Args: []string{"postgresql", "start"}, Root: true}); err != nil {
			return fmt.Errorf("%w\nIs PostgreSQL installed? sudo apt install postgresql", err)
		}
	}
	return waitForSQL(ctx, p)
}

func (p *linuxPostgres) sql(ctx context.Context, sql string) (string, error) {
	return p.env.Run.Run(ctx, Cmd{Name: "psql", Args: psqlArgs, Stdin: sql, User: "postgres"})
}

/* ---------------------------------------------------------------- macOS ---- */

// brewPostgres is Homebrew's postgresql@17, run by a LaunchDaemon as the
// installing user so it starts at boot with nobody logged in. `brew services`
// can't do that: it writes a LaunchAgent, which waits for a login, and under
// sudo it would run Postgres as root, which Postgres refuses.
type brewPostgres struct {
	env *Env
}

func (p *brewPostgres) bin(name string) string {
	return filepath.Join(p.env.Paths.BrewPrefix, "opt", brewFormula, "bin", name)
}

func (p *brewPostgres) dataDir() string {
	return filepath.Join(p.env.Paths.BrewPrefix, "var", brewFormula)
}

// user is who runs Postgres: the Homebrew account.
func (p *brewPostgres) user() string {
	if p.env.SudoUser != "" {
		return p.env.SudoUser
	}
	return os.Getenv("USER")
}

// asUser is the account to name in a Cmd: only needed when setup is root.
func (p *brewPostgres) asUser() string {
	if p.env.Root {
		return p.user()
	}
	return ""
}

func (p *brewPostgres) start(ctx context.Context) error {
	e := p.env
	if _, err := os.Stat(p.bin("postgres")); err != nil {
		return fmt.Errorf("%s is not installed. Run: brew install %s", brewFormula, brewFormula)
	}
	if _, err := os.Stat(filepath.Join(p.dataDir(), "PG_VERSION")); errors.Is(err, os.ErrNotExist) {
		if _, err := e.Run.Run(ctx, Cmd{Name: p.bin("initdb"), Args: []string{"--locale=C", "-E", "UTF-8", p.dataDir()}, User: p.asUser()}); err != nil {
			return err
		}
		e.ok("created the PostgreSQL cluster in %s", p.dataDir())
	}

	// Homebrew's own agent and this daemon would both start Postgres on one
	// data directory. Stopping the agent also deletes its plist.
	brew := filepath.Join(e.Paths.BrewPrefix, "bin", "brew")
	if _, err := os.Stat(brew); err == nil {
		_, _ = e.Run.Run(ctx, Cmd{Name: brew, Args: []string{"services", "stop", brewFormula}, User: p.asUser()})
	}

	if err := os.MkdirAll(filepath.Dir(p.logFile()), 0o755); err != nil {
		return err
	}
	if err := loadDaemon(ctx, e, postgresLabel, p.plist()); err != nil {
		return err
	}
	if err := waitForSQL(ctx, p); err != nil {
		return fmt.Errorf("%w\n\nThe end of %s:\n%s", err, p.logFile(), tailFile(p.logFile(), 15))
	}
	return nil
}

func (p *brewPostgres) logFile() string {
	return filepath.Join(p.env.Paths.BrewPrefix, "var", "log", brewFormula+".log")
}

// plist is the LaunchDaemon. launchd starts it with no locale in the
// environment, and Postgres on macOS then dies at once with "postmaster became
// multithreaded during startup". Homebrew's own service sets LC_ALL=C for the
// same reason.
func (p *brewPostgres) plist() string {
	return launchDaemon(postgresLabel, p.user(),
		[]string{p.bin("postgres"), "-D", p.dataDir()}, p.logFile(),
		map[string]string{"LC_ALL": "C"})
}

// tailFile is the last n lines of a file, or why it couldn't be read.
func tailFile(path string, n int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "  (" + err.Error() + ")"
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return "  " + strings.Join(lines, "\n  ")
}

func (p *brewPostgres) sql(ctx context.Context, sql string) (string, error) {
	return p.env.Run.Run(ctx, Cmd{Name: p.bin("psql"), Args: psqlArgs, Stdin: sql, User: p.asUser()})
}
