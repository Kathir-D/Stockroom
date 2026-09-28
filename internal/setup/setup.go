package setup

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/joho/godotenv"

	"stockroom/internal/stockroom"
)

// Options are setup's flags.
type Options struct {
	NonInteractive    bool
	NoService         bool
	NoOpen            bool
	WithCamera        bool
	ServiceUser       string
	AdminNumber       string
	AdminPasswordFile string
	Addr              string
	// ConfigFile is --config: where the config is written and read. Empty
	// means the one the service already uses, else the platform's default.
	ConfigFile string
}

const defaultAddr = "127.0.0.1:8080"

// installer is one run of setup.
type installer struct {
	env  *Env
	opts Options
	pg   postgres

	existing   map[string]string // the config already on disk, if any
	addr       string
	svcUser    account
	dbPort     string
	dbPassword string
}

// Setup installs or repairs Stockroom on this machine. Running it again
// repairs rather than reinstalls: it never overwrites a config, never drops
// the database, and resets the database password only when the config that
// held it is gone.
func Setup(ctx context.Context, env *Env, opts Options) error {
	if err := useConfig(env, opts.ConfigFile); err != nil {
		return err
	}
	in := &installer{env: env, opts: opts}
	for _, step := range []struct {
		name string
		run  func(context.Context) error
	}{
		{"Platform", in.checkPlatform},
		{"PostgreSQL", in.startPostgres},
		{"Database", in.ensureDatabase},
		{"Service user", in.ensureServiceUser},
		{"Config", in.writeConfig},
		{"Service", in.startService},
		{"Camera", in.startCamera},
	} {
		env.say("== %s ==", step.name)
		if err := step.run(ctx); err != nil {
			return fmt.Errorf("%s: %w", strings.ToLower(step.name), err)
		}
	}
	in.finish()
	if !opts.NoOpen && env.Open != nil {
		if err := env.Open("http://" + in.addr); err != nil {
			env.warn("could not open a browser: %v", err)
		}
	}
	return nil
}

/* ------------------------------------------------------------ platform ---- */

func (in *installer) checkPlatform(ctx context.Context) error {
	e := in.env
	switch e.GOOS {
	case "linux":
		if !e.Root {
			return errors.New("run it with sudo: sudo stockroom setup")
		}
		if e.WSL && !e.Systemd && !in.opts.NoService {
			return errors.New(`systemd is not running in this WSL distribution, so there is nothing to start Stockroom at boot.
Add these two lines to /etc/wsl.conf:

  [boot]
  systemd=true

then run "wsl --shutdown" from Windows, open Ubuntu again and run setup again`)
		}
		if !e.Systemd && !in.opts.NoService {
			return errors.New("systemd is not running. Run setup with --no-service and start `stockroom serve` another way")
		}
		in.pg = &linuxPostgres{env: e, systemd: e.Systemd}
		e.ok("Linux%s", map[bool]string{true: " under WSL", false: ""}[e.WSL])
	case "darwin":
		if e.Root && (e.SudoUser == "" || e.SudoUser == "root") {
			return errors.New("run setup as the account that installed Homebrew, not as root. It asks for your password when it needs sudo")
		}
		if in.opts.ServiceUser != "" {
			return errors.New("--service-user is Linux only. On macOS Stockroom and PostgreSQL run as the account that installed Homebrew")
		}
		in.pg = &brewPostgres{env: e}
		e.ok("macOS, Homebrew at %s", e.Paths.BrewPrefix)
	default:
		return fmt.Errorf("setup supports Linux and macOS, not %s. On Windows, install inside WSL (docs/INSTALL.md)", e.GOOS)
	}

	if raw, err := os.ReadFile(e.Paths.ConfigFile); err == nil {
		in.existing, err = godotenv.UnmarshalBytes(raw)
		if err != nil {
			return fmt.Errorf("read %s: %w", e.Paths.ConfigFile, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	in.addr = in.opts.Addr
	if in.addr == "" {
		in.addr = in.existing["SERVER_ADDR"]
	}
	if in.addr == "" {
		in.addr = defaultAddr
	}
	if in.opts.Addr != "" && in.existing != nil && in.existing["SERVER_ADDR"] != "" && in.existing["SERVER_ADDR"] != in.opts.Addr {
		e.warn("--addr %s is ignored: %s already sets SERVER_ADDR=%s. Edit it there", in.opts.Addr, e.Paths.ConfigFile, in.existing["SERVER_ADDR"])
		in.addr = in.existing["SERVER_ADDR"]
	}
	return nil
}

/* ---------------------------------------------------------- postgresql ---- */

func (in *installer) startPostgres(ctx context.Context) error {
	if err := in.pg.start(ctx); err != nil {
		return err
	}
	out, err := in.pg.sql(ctx, "show listen_addresses; show port; show config_file;")
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 3 {
		return fmt.Errorf("unexpected answer from psql: %q", out)
	}
	listen, port, conf := strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1]), strings.TrimSpace(lines[2])
	if bad := nonLoopback(listen); bad != "" {
		return fmt.Errorf(`PostgreSQL listens on %q, which is reachable from the network, and the Stockroom
database role is a superuser. Change this line in %s:

  listen_addresses = 'localhost'

then restart PostgreSQL and run setup again`, bad, conf)
	}
	in.dbPort = port
	in.env.ok("PostgreSQL running on port %s, listening on %s", port, orDefault(listen, "no TCP address"))
	return nil
}

// nonLoopback returns the first entry in listen_addresses that isn't
// loopback, or "" when all are.
func nonLoopback(listen string) string {
	for _, a := range strings.Split(listen, ",") {
		switch strings.TrimSpace(a) {
		case "", "localhost", "127.0.0.1", "::1":
			continue
		default:
			return strings.TrimSpace(a)
		}
	}
	return ""
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

/* ------------------------------------------------------------ database ---- */

const roleName, dbName = "stockroom", "stockroom"

// ensureDatabase creates the stockroom role and database. A role that exists
// alongside a config is left alone. A role with no config gets a new
// password, since the old one is lost with the file. A config whose role is
// gone (a new Postgres cluster) gets the role back with the config's password.
func (in *installer) ensureDatabase(ctx context.Context) error {
	e := in.env
	exists := func(q string) (bool, error) {
		out, err := in.pg.sql(ctx, q)
		return strings.TrimSpace(out) == "1", err
	}
	roleExists, err := exists(fmt.Sprintf("select count(*) from pg_roles where rolname = '%s';", roleName))
	if err != nil {
		return err
	}
	dbExists, err := exists(fmt.Sprintf("select count(*) from pg_database where datname = '%s';", dbName))
	if err != nil {
		return err
	}

	switch {
	case in.existing != nil:
		conn, err := pgconn.ParseConfig(in.existing["DATABASE_URL"])
		if err != nil {
			return fmt.Errorf("the DATABASE_URL in %s doesn't parse: %w", e.Paths.ConfigFile, err)
		}
		if conn.User != roleName {
			e.ok("%s uses role %q; leaving the database alone", e.Paths.ConfigFile, conn.User)
			return nil
		}
		if !roleExists {
			if _, err := in.pg.sql(ctx, fmt.Sprintf("create role %s with login superuser password %s;", roleName, sqlString(conn.Password))); err != nil {
				return err
			}
			e.ok("recreated role %s with the password from %s", roleName, e.Paths.ConfigFile)
		} else {
			e.ok("role %s exists and %s has its password", roleName, e.Paths.ConfigFile)
		}
	case roleExists:
		in.dbPassword = randomPassword()
		if _, err := in.pg.sql(ctx, fmt.Sprintf("alter role %s with login superuser password %s;", roleName, sqlString(in.dbPassword))); err != nil {
			return err
		}
		e.ok("role %s exists but no config does; gave it a new password", roleName)
	default:
		in.dbPassword = randomPassword()
		if _, err := in.pg.sql(ctx, fmt.Sprintf("create role %s with login superuser password %s;", roleName, sqlString(in.dbPassword))); err != nil {
			return err
		}
		e.ok("created role %s", roleName)
	}

	if !dbExists {
		if _, err := in.pg.sql(ctx, fmt.Sprintf("create database %s owner %s;", dbName, roleName)); err != nil {
			return err
		}
		e.ok("created database %s", dbName)
	} else {
		e.ok("database %s exists", dbName)
	}
	return nil
}

// sqlString quotes s as an SQL string literal.
func sqlString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// randomPassword is 32 letters and digits from crypto/rand. Alphanumeric so
// it needs no escaping in a URL, a config line or SQL.
func randomPassword() string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 32)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			panic(err) // crypto/rand does not fail on a supported platform
		}
		b[i] = alphabet[n.Int64()]
	}
	return string(b)
}

/* -------------------------------------------------------- service user ---- */

func (in *installer) ensureServiceUser(ctx context.Context) error {
	e := in.env
	name := in.opts.ServiceUser
	if e.GOOS == "darwin" {
		name = e.SudoUser
		if name == "" {
			name = os.Getenv("USER")
		}
	} else if name == "" {
		name = e.SudoUser
		if name == "" || name == "root" {
			return errors.New("run setup with sudo from your own account, so the service runs as you, or pass --service-user stockroom for a dedicated account")
		}
	}
	if name == "root" {
		return errors.New("the service must not run as root. Pick another --service-user")
	}

	if _, err := e.LookupUser(name); err != nil {
		if e.GOOS != "linux" || in.opts.ServiceUser == "" {
			return fmt.Errorf("no account named %q: %w", name, err)
		}
		if _, err := e.Run.Run(ctx, Cmd{Name: "useradd", Args: []string{
			"--system", "--home-dir", e.Paths.DataDir, "--no-create-home",
			"--shell", "/usr/sbin/nologin", "--user-group", name,
		}}); err != nil {
			return err
		}
		e.ok("created system account %s", name)
	}
	a, err := e.account(name)
	if err != nil {
		return err
	}
	in.svcUser = a

	if e.GOOS == "linux" {
		if prev := previousServiceUser(e.Paths.DropIn); prev != "" && prev != a.name {
			e.warn("the service user changes from %s to %s. rclone keeps its Google sign-in in the service user's ~/.config/rclone/rclone.conf, so sign in to Google again in Admin → Settings", prev, a.name)
		}
	}

	// The data directory itself belongs to root on Linux (and to the
	// Homebrew user on macOS); its subdirectories belong to the service.
	if err := os.MkdirAll(e.Paths.DataDir, 0o755); err != nil {
		return err
	}
	if in.opts.ServiceUser != "" {
		// A dedicated account's home is the data directory.
		if err := e.Chown(e.Paths.DataDir, a.uid, a.gid); err != nil {
			return err
		}
	}
	for _, d := range dataDirs {
		if err := e.mkdirOwned(filepath.Join(e.Paths.DataDir, d), 0o750, a); err != nil {
			return err
		}
	}
	e.ok("service runs as %s; data in %s", a.name, e.Paths.DataDir)
	return nil
}

// previousServiceUser reads User= from an existing drop-in.
func previousServiceUser(dropIn string) string {
	f, err := os.Open(dropIn)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "User="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// previousServiceConfig is the config a drop-in from an earlier setup
// --config points the service at, or "".
func previousServiceConfig(dropIn string) string {
	f, err := os.Open(dropIn)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "ExecStart=") {
			continue
		}
		if _, cfg, ok := strings.Cut(line, " --config "); ok {
			return strings.TrimSpace(cfg)
		}
	}
	return ""
}

// useConfig settles e.Paths.ConfigFile for setup and service: the --config
// flag, else the config an earlier setup --config chose (so a repair run
// without the flag keeps it), else the platform's default.
func useConfig(e *Env, flagPath string) error {
	if flagPath == "" {
		if e.GOOS == "linux" {
			if prev := previousServiceConfig(e.Paths.DropIn); prev != "" {
				e.Paths.ConfigFile = prev
			}
		}
		return nil
	}
	abs, err := checkConfigPath(flagPath)
	if err != nil {
		return err
	}
	e.Paths.ConfigFile = abs
	return nil
}

/* -------------------------------------------------------------- config ---- */

func (in *installer) writeConfig(ctx context.Context) error {
	e := in.env
	cfgDir := filepath.Dir(e.Paths.ConfigFile)
	// The directory belongs to the service user, because the setup wizard
	// rewrites the file through a temp file beside it (envfile.go).
	if err := e.mkdirOwned(cfgDir, 0o700, in.svcUser); err != nil {
		return err
	}
	if in.existing != nil {
		if err := e.Chown(e.Paths.ConfigFile, in.svcUser.uid, in.svcUser.gid); err != nil {
			return err
		}
		e.ok("keeping %s", e.Paths.ConfigFile)
		return nil
	}

	admin, err := in.askFailsafe()
	if err != nil {
		return err
	}
	backupDir := filepath.Join(e.Paths.DataDir, "backups")
	if e.WSL {
		if dir, err := in.windowsBackupDir(ctx); err != nil {
			e.warn("could not find the Windows Documents folder (%v); backups stay in %s", err, backupDir)
		} else {
			backupDir = dir
		}
	}

	body, err := renderConfig(configValues{
		databaseURL:   fmt.Sprintf("postgresql://%s:%s@127.0.0.1:%s/%s", roleName, in.dbPassword, in.dbPort, dbName),
		addr:          in.addr,
		admin:         admin,
		dataDir:       e.Paths.DataDir,
		backupDir:     backupDir,
		preMigrateDir: filepath.Join(e.Paths.DataDir, "backups", "pre-migrate"),
	}, time.Now())
	if err != nil {
		return err
	}
	if err := stockroom.CreateEnvFile(e.Paths.ConfigFile, body, 0o600); err != nil {
		return err
	}
	if err := e.Chown(e.Paths.ConfigFile, in.svcUser.uid, in.svcUser.gid); err != nil {
		return err
	}
	e.ok("wrote %s with a generated database password", e.Paths.ConfigFile)
	if admin.number == "" {
		e.warn("no failsafe admin. Set one in the web setup wizard, or add ADMIN_STUDENT_NUMBER and ADMIN_PASSWORD to %s", e.Paths.ConfigFile)
	}
	return nil
}

// askFailsafe gets the failsafe admin from the flags, or asks.
func (in *installer) askFailsafe() (failsafe, error) {
	f := failsafe{number: in.opts.AdminNumber}
	switch in.opts.AdminPasswordFile {
	case "":
	case "-":
		line, err := bufio.NewReader(in.env.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return f, fmt.Errorf("read the admin password from stdin: %w", err)
		}
		f.password = strings.TrimRight(line, "\r\n")
	default:
		raw, err := os.ReadFile(in.opts.AdminPasswordFile)
		if err != nil {
			return f, err
		}
		f.password = strings.TrimRight(strings.SplitN(string(raw), "\n", 2)[0], "\r")
	}

	if f.number == "" && f.password == "" && !in.opts.NonInteractive {
		in.env.say("The failsafe admin is an account that always works, even after the database is lost.\nLeave the number blank to skip it and set one in the web wizard instead.")
		for {
			n, err := in.env.Prompt.Line("  Failsafe admin student number (digits): ")
			if err != nil {
				return f, err
			}
			if n == "" {
				return failsafe{}, nil
			}
			p, err := in.env.Prompt.Secret("  Password (8 to 72 characters): ")
			if err != nil {
				return f, err
			}
			again, err := in.env.Prompt.Secret("  Password again: ")
			if err != nil {
				return f, err
			}
			if p != again {
				in.env.warn("the two passwords differ; try again")
				continue
			}
			cand := failsafe{number: n, password: p}
			if err := checkFailsafe(cand); err != nil {
				in.env.warn("%v", err)
				continue
			}
			return cand, nil
		}
	}
	return f, checkFailsafe(f)
}

// windowsBackupDir is "Documents/Stockroom Backups" in the Windows user's
// profile, where a teacher finds it in Explorer and where it survives the
// WSL distribution being removed.
func (in *installer) windowsBackupDir(ctx context.Context) (string, error) {
	out, err := in.env.Run.Run(ctx, Cmd{Name: "cmd.exe", Args: []string{"/c", "echo %USERNAME%"}})
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(out)
	if name == "" || strings.Contains(name, "%") {
		return "", errors.New("cmd.exe gave no user name")
	}
	dir := filepath.Join("/mnt/c/Users", name, "Documents", "Stockroom Backups")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

type configValues struct {
	databaseURL, addr                 string
	admin                             failsafe
	dataDir, backupDir, preMigrateDir string
}

// renderConfig is the config setup writes. Values are quoted by
// stockroom.EnvQuote, which godotenv reads literally, so a `$` in a password
// stays a `$`.
func renderConfig(v configValues, now time.Time) (string, error) {
	var bad error
	q := func(s string) string {
		quoted, err := stockroom.EnvQuote(s)
		if err != nil && bad == nil {
			bad = err
		}
		return quoted
	}
	body := fmt.Sprintf(`# Written by stockroom setup on %s.
#
# Most settings live in the database and are changed in the admin panel.
# BACKUP_DIR and PHOTO_BACKUP_DIR only seed the admin panel's values the first
# time the server starts (CLAUDE.md §9).
#
# This file holds the database password. Keep it at mode 600.

DATABASE_URL=%s
SERVER_ADDR=%s

# The failsafe admin (CLAUDE.md §7). The web setup wizard can set it too.
ADMIN_STUDENT_NUMBER=%s
ADMIN_PASSWORD=%s

UPLOADS_DIR=%s
SIGNIN_PHOTOS_DIR=%s
BACKUP_DIR=%s
PHOTO_BACKUP_DIR=%s

# A pg_dump of the whole database before every upgrade that migrates it.
PRE_MIGRATE_DUMP=required
PRE_MIGRATE_DIR=%s

SESSION_IDLE_MINUTES=10
`,
		now.Format("2006-01-02 15:04"),
		q(v.databaseURL), q(v.addr),
		q(v.admin.number), q(v.admin.password),
		q(filepath.Join(v.dataDir, "uploads")),
		q(filepath.Join(v.dataDir, "cache")),
		q(v.backupDir),
		q(filepath.Join(v.dataDir, "photo-backups")),
		q(v.preMigrateDir),
	)
	return body, bad
}

/* ------------------------------------------------------------- service ---- */

func (in *installer) startService(ctx context.Context) error {
	e := in.env
	if in.opts.NoService {
		e.ok("skipped (--no-service). Start it with: %s serve --config %s", e.Paths.Binary, e.Paths.ConfigFile)
		return nil
	}
	svc := newService(e)
	if err := svc.install(ctx, in.svcUser); err != nil {
		return err
	}
	if err := svc.restart(ctx); err != nil {
		return err
	}
	e.say("  waiting for http://%s/health ...", in.addr)
	if err := e.waitHealthy(ctx, in.addr); err != nil {
		e.say("%s", svc.recentLog(ctx, 40))
		return err
	}
	e.ok("Stockroom answers at http://%s", in.addr)
	return nil
}

/* -------------------------------------------------------------- camera ---- */

func (in *installer) startCamera(ctx context.Context) error {
	e := in.env
	if !in.opts.WithCamera {
		e.ok("not requested (--with-camera turns it on)")
		return nil
	}
	if e.WSL {
		e.warn("the closet camera isn't supported under WSL; skipped")
		return nil
	}
	if _, err := e.Run.Run(ctx, Cmd{Name: "docker", Args: []string{"info", "--format", "{{.ServerVersion}}"}}); err != nil {
		e.warn("the camera runs Frigate in Docker, and Docker isn't installed or running. Install it (https://docs.docker.com/engine/install/ on Linux, Docker Desktop on macOS), then run setup --with-camera again")
		return nil
	}
	script := filepath.Join(e.Paths.CameraDir, "camera.sh")
	if _, err := os.Stat(script); err != nil {
		e.warn("%s is missing, so the camera can't be started from here", script)
		return nil
	}
	rec := filepath.Join(e.Paths.DataDir, "recordings")
	if err := e.mkdirOwned(rec, 0o750, in.svcUser); err != nil {
		return err
	}
	if _, err := e.Run.Run(ctx, Cmd{Name: script, Args: []string{"up", "--source", "webcam"}, Env: []string{"CAMERA_DIR=" + rec}}); err != nil {
		e.warn("the camera didn't start: %v", err)
		return nil
	}
	e.ok("camera detector started; recordings in %s. Turn it on in Admin → Settings → Closet camera", rec)
	return nil
}

/* -------------------------------------------------------------- finish ---- */

func (in *installer) finish() {
	e := in.env
	url := "http://" + in.addr
	e.say(`
Stockroom is installed.

  Open        %s
  Config      %s
  Data        %s

Next:
  1. Open %s and follow the setup wizard.
  2. In Admin → Settings, sign in to Google and choose a backup folder.
  3. Reboot without logging in, then check that %s/health answers
     (from another machine, or after logging in: stockroom doctor).
`, url, e.Paths.ConfigFile, e.Paths.DataDir, url, url)
}
