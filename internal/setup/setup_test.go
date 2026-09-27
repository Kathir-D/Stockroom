package setup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeRunner records every command and answers psql like a fresh Debian
// PostgreSQL: loopback only, port 5432, no stockroom role or database.
type fakeRunner struct {
	cmds       []Cmd
	listen     string
	roleExists bool
	dbExists   bool
	fail       map[string]error // by command name
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{listen: "localhost", fail: map[string]error{}}
}

func (f *fakeRunner) Run(ctx context.Context, c Cmd) (string, error) {
	f.cmds = append(f.cmds, c)
	if err := f.fail[filepath.Base(c.Name)]; err != nil {
		return "", err
	}
	if filepath.Base(c.Name) != "psql" {
		return "", nil
	}
	sql := c.Stdin
	switch {
	case strings.HasPrefix(sql, "show listen_addresses"):
		return f.listen + "\n5432\n/etc/postgresql/16/main/postgresql.conf\n", nil
	case strings.Contains(sql, "from pg_roles"):
		return boolCount(f.roleExists), nil
	case strings.Contains(sql, "from pg_database"):
		return boolCount(f.dbExists), nil
	case strings.HasPrefix(sql, "create role"):
		f.roleExists = true
	case strings.HasPrefix(sql, "create database"):
		f.dbExists = true
	}
	return "", nil
}

func boolCount(b bool) string {
	if b {
		return "1\n"
	}
	return "0\n"
}

// ran reports whether a command whose String() contains want was run.
func (f *fakeRunner) ran(want string) bool {
	for _, c := range f.cmds {
		if strings.Contains(c.String(), want) || strings.Contains(c.Stdin, want) {
			return true
		}
	}
	return false
}

// testEnv is a Linux machine rooted in a temp directory, run as root by sudo
// from the account running the test.
func testEnv(t *testing.T) (*Env, *fakeRunner, *bytes.Buffer) {
	t.Helper()
	root := t.TempDir()
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	run := newFakeRunner()
	out := &bytes.Buffer{}
	env := &Env{
		GOOS:     "linux",
		Systemd:  true,
		Root:     true,
		SudoUser: me.Username,
		Run:      run,
		Out:      out,
		Prompt:   fakePrompter{},
		Paths: Paths{
			ConfigFile:   filepath.Join(root, "etc", "stockroom", "stockroom.env"),
			DataDir:      filepath.Join(root, "var", "lib", "stockroom"),
			Binary:       "/usr/bin/stockroom",
			PackagedUnit: filepath.Join(root, "lib", "systemd", "system", "stockroom.service"),
			LocalUnit:    filepath.Join(root, "etc", "systemd", "system", "stockroom.service"),
			DropIn:       filepath.Join(root, "etc", "systemd", "system", "stockroom.service.d", "user.conf"),
			CameraDir:    filepath.Join(root, "usr", "share", "stockroom", "camera"),
		},
		LookupUser:    func(name string) (*user.User, error) { return user.Lookup(name) },
		Chown:         func(string, int, int) error { return nil },
		Health:        func(context.Context, string) error { return nil },
		HealthTimeout: time.Second,
		Stdin:         strings.NewReader(""),
	}
	return env, run, out
}

type fakePrompter struct{}

func (fakePrompter) Line(string) (string, error)   { return "", errNoTTY }
func (fakePrompter) Secret(string) (string, error) { return "", errNoTTY }

func nonInteractive() Options {
	return Options{NonInteractive: true, NoOpen: true}
}

var dbURL = regexp.MustCompile(`DATABASE_URL='postgresql://stockroom:([A-Za-z0-9]{32})@127\.0\.0\.1:5432/stockroom'`)

func TestSetupFreshLinuxInstall(t *testing.T) {
	env, run, out := testEnv(t)
	pwFile := filepath.Join(t.TempDir(), "pw")
	os.WriteFile(pwFile, []byte("password123\n"), 0o600)
	opts := nonInteractive()
	opts.AdminNumber, opts.AdminPasswordFile = "900100", pwFile

	if err := Setup(context.Background(), env, opts); err != nil {
		t.Fatalf("Setup: %v\n%s", err, out)
	}

	raw, err := os.ReadFile(env.Paths.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(raw)
	m := dbURL.FindStringSubmatch(cfg)
	if m == nil {
		t.Fatalf("no generated DATABASE_URL in the config:\n%s", cfg)
	}
	for _, want := range []string{
		"ADMIN_STUDENT_NUMBER='900100'", "ADMIN_PASSWORD='password123'",
		"SERVER_ADDR='127.0.0.1:8080'", "PRE_MIGRATE_DUMP=required",
		"UPLOADS_DIR='" + filepath.Join(env.Paths.DataDir, "uploads") + "'",
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config lacks %s", want)
		}
	}
	if st, _ := os.Stat(env.Paths.ConfigFile); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("config mode %v, want 600", st.Mode().Perm())
	}
	if st, _ := os.Stat(filepath.Dir(env.Paths.ConfigFile)); runtime.GOOS != "windows" && st.Mode().Perm() != 0o700 {
		t.Errorf("config directory mode %v, want 700", st.Mode().Perm())
	}

	// The password went to psql on stdin, never in an argument.
	for _, c := range run.cmds {
		if strings.Contains(strings.Join(c.Args, " "), m[1]) {
			t.Errorf("the database password is in the arguments of %s", c)
		}
	}
	for _, want := range []string{
		"systemctl enable --now postgresql",
		"create role stockroom with login superuser password '" + m[1] + "'",
		"create database stockroom owner stockroom",
		"systemctl daemon-reload", "systemctl enable stockroom", "systemctl restart stockroom",
	} {
		if !run.ran(want) {
			t.Errorf("setup never ran %q", want)
		}
	}

	unit, err := os.ReadFile(env.Paths.LocalUnit)
	if err != nil || !strings.Contains(string(unit), "ExecStart=/usr/bin/stockroom serve --config /etc/stockroom/stockroom.env") {
		t.Errorf("no local unit for a binary outside the package: %v\n%s", err, unit)
	}
	dropIn, _ := os.ReadFile(env.Paths.DropIn)
	if !strings.Contains(string(dropIn), "User="+env.SudoUser) {
		t.Errorf("drop-in = %q, want User=%s", dropIn, env.SudoUser)
	}
	for _, d := range dataDirs {
		if st, err := os.Stat(filepath.Join(env.Paths.DataDir, d)); err != nil || !st.IsDir() {
			t.Errorf("data directory %s missing", d)
		}
	}
}

// TestSetupRunsAgainAsARepair: a second run keeps the config and the role's
// password, and uses the packaged unit when there is one.
func TestSetupRunsAgainAsARepair(t *testing.T) {
	env, run, out := testEnv(t)
	if err := Setup(context.Background(), env, nonInteractive()); err != nil {
		t.Fatalf("first Setup: %v\n%s", err, out)
	}
	before, _ := os.ReadFile(env.Paths.ConfigFile)

	os.MkdirAll(filepath.Dir(env.Paths.PackagedUnit), 0o755)
	os.WriteFile(env.Paths.PackagedUnit, []byte(systemdUnit("/usr/bin/stockroom")), 0o644)
	os.Remove(env.Paths.LocalUnit)
	run.cmds = nil
	if err := Setup(context.Background(), env, nonInteractive()); err != nil {
		t.Fatalf("second Setup: %v\n%s", err, out)
	}
	after, _ := os.ReadFile(env.Paths.ConfigFile)
	if !bytes.Equal(before, after) {
		t.Error("the second run rewrote the config")
	}
	if run.ran("create role") || run.ran("alter role") || run.ran("create database") {
		t.Error("the second run touched the role or database")
	}
	if _, err := os.Stat(env.Paths.LocalUnit); err == nil {
		t.Error("wrote a local unit although the package installed one")
	}
}

func TestSetupResetsThePasswordWhenTheConfigIsGone(t *testing.T) {
	env, run, out := testEnv(t)
	run.roleExists, run.dbExists = true, true
	if err := Setup(context.Background(), env, nonInteractive()); err != nil {
		t.Fatalf("Setup: %v\n%s", err, out)
	}
	if !run.ran("alter role stockroom with login superuser password") {
		t.Error("an orphaned role kept its old, lost password")
	}
}

func TestSetupRecreatesARoleFromTheConfig(t *testing.T) {
	env, run, out := testEnv(t)
	os.MkdirAll(filepath.Dir(env.Paths.ConfigFile), 0o700)
	os.WriteFile(env.Paths.ConfigFile, []byte("DATABASE_URL='postgresql://stockroom:keepThisOne@127.0.0.1:5432/stockroom'\n"), 0o600)
	if err := Setup(context.Background(), env, nonInteractive()); err != nil {
		t.Fatalf("Setup: %v\n%s", err, out)
	}
	if !run.ran("create role stockroom with login superuser password 'keepThisOne'") {
		t.Error("the role was not recreated with the config's password")
	}
}

func TestSetupRefusesANetworkFacingPostgres(t *testing.T) {
	env, run, _ := testEnv(t)
	run.listen = "localhost, 0.0.0.0"
	err := Setup(context.Background(), env, nonInteractive())
	if err == nil || !strings.Contains(err.Error(), "0.0.0.0") || !strings.Contains(err.Error(), "postgresql.conf") {
		t.Fatalf("err = %v, want a refusal naming the address and postgresql.conf", err)
	}
	if run.ran("create role") {
		t.Error("created the role before refusing")
	}
}

func TestSetupNeedsRootAndSystemd(t *testing.T) {
	env, _, _ := testEnv(t)
	env.Root = false
	if err := Setup(context.Background(), env, nonInteractive()); err == nil || !strings.Contains(err.Error(), "sudo") {
		t.Errorf("not root: err = %v", err)
	}

	env, _, _ = testEnv(t)
	env.WSL, env.Systemd = true, false
	if err := Setup(context.Background(), env, nonInteractive()); err == nil || !strings.Contains(err.Error(), "systemd=true") {
		t.Errorf("WSL without systemd: err = %v, want the wsl.conf fix", err)
	}

	env, run, _ := testEnv(t)
	env.Systemd = false
	opts := nonInteractive()
	opts.NoService = true
	if err := Setup(context.Background(), env, opts); err != nil {
		t.Errorf("--no-service without systemd: %v", err)
	}
	if !run.ran("service postgresql start") || run.ran("systemctl") {
		t.Error("without systemd, setup should start Postgres with service and never call systemctl")
	}
}

func TestSetupReportsAServerThatNeverStarts(t *testing.T) {
	env, run, _ := testEnv(t)
	env.Health = func(context.Context, string) error { return errors.New("connection refused") }
	err := Setup(context.Background(), env, nonInteractive())
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("err = %v", err)
	}
	if !run.ran("journalctl -u stockroom -n 40") {
		t.Error("the log was not shown")
	}
}

func TestSetupReadsThePasswordFromStdin(t *testing.T) {
	env, _, out := testEnv(t)
	env.Stdin = strings.NewReader("from stdin $HOME\n")
	opts := nonInteractive()
	opts.AdminNumber, opts.AdminPasswordFile = "42", "-"
	if err := Setup(context.Background(), env, opts); err != nil {
		t.Fatalf("Setup: %v\n%s", err, out)
	}
	raw, _ := os.ReadFile(env.Paths.ConfigFile)
	if !strings.Contains(string(raw), "ADMIN_PASSWORD='from stdin $HOME'") {
		t.Errorf("config:\n%s", raw)
	}
}

func TestSetupDedicatedServiceUser(t *testing.T) {
	env, run, out := testEnv(t)
	created := false
	env.LookupUser = func(name string) (*user.User, error) {
		if name == "stockroom" && !created {
			return nil, user.UnknownUserError(name)
		}
		me, _ := user.Current()
		u := *me
		u.Username = name
		return &u, nil
	}
	run.fail = map[string]error{}
	opts := nonInteractive()
	opts.ServiceUser = "stockroom"
	// useradd "creates" the account.
	env.Run = runnerFunc(func(ctx context.Context, c Cmd) (string, error) {
		if c.Name == "useradd" {
			created = true
		}
		return run.Run(ctx, c)
	})
	if err := Setup(context.Background(), env, opts); err != nil {
		t.Fatalf("Setup: %v\n%s", err, out)
	}
	if !run.ran("useradd --system --home-dir " + env.Paths.DataDir) {
		t.Error("no system account was created")
	}
	dropIn, _ := os.ReadFile(env.Paths.DropIn)
	if !strings.Contains(string(dropIn), "User=stockroom") {
		t.Errorf("drop-in = %q", dropIn)
	}
}

type runnerFunc func(ctx context.Context, c Cmd) (string, error)

func (f runnerFunc) Run(ctx context.Context, c Cmd) (string, error) { return f(ctx, c) }

func TestCheckFailsafe(t *testing.T) {
	for _, c := range []struct {
		f  failsafe
		ok bool
	}{
		{failsafe{}, true},
		{failsafe{"900100", "password123"}, true},
		{failsafe{"900100", ""}, false},
		{failsafe{"", "password123"}, false},
		{failsafe{"90a100", "password123"}, false},
		{failsafe{"900100", "short"}, false},
		{failsafe{"900100", strings.Repeat("x", 73)}, false},
		{failsafe{"900100", "it's mine!"}, false},
	} {
		if err := checkFailsafe(c.f); (err == nil) != c.ok {
			t.Errorf("checkFailsafe(%+v) = %v, want ok=%v", c.f, err, c.ok)
		}
	}
}

func TestNonLoopback(t *testing.T) {
	for listen, want := range map[string]string{
		"localhost":           "",
		"":                    "",
		"127.0.0.1, ::1":      "",
		"*":                   "*",
		"localhost,192.0.2.1": "192.0.2.1",
	} {
		if got := nonLoopback(listen); got != want {
			t.Errorf("nonLoopback(%q) = %q, want %q", listen, got, want)
		}
	}
}

// TestPackagedUnitMatches keeps packaging/linux/stockroom.service equal to the
// unit `service install` writes.
func TestPackagedUnitMatches(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "packaging", "linux", "stockroom.service"))
	if err != nil {
		t.Fatal(err)
	}
	if want := systemdUnit("/usr/bin/stockroom"); string(raw) != want {
		t.Errorf("packaging/linux/stockroom.service differs from systemdUnit:\n%s\nwant:\n%s", raw, want)
	}
}

func TestLaunchDaemonPlist(t *testing.T) {
	plist := launchDaemon(serverLabel, "teacher", []string{"/opt/homebrew/bin/stockroom", "serve", "--config", "/opt/homebrew/var/stockroom/stockroom.env"},
		"/opt/homebrew/var/log/stockroom.log", map[string]string{"HOME": "/Users/teacher", "PATH": "/opt/homebrew/bin:/usr/bin"})
	for _, want := range []string{"<string>com.stockroom.server</string>", "<key>UserName</key>\n  <string>teacher</string>", "<key>RunAtLoad</key>\n  <true/>", "<key>KeepAlive</key>\n  <true/>"} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist lacks %q", want)
		}
	}
	if _, err := os.Stat("/usr/bin/plutil"); err == nil {
		path := filepath.Join(t.TempDir(), "x.plist")
		os.WriteFile(path, []byte(plist), 0o644)
		if out, err := runPlutil(path); err != nil {
			t.Errorf("plutil -lint: %v\n%s", err, out)
		}
	}
}

// TestSetupMacOS runs the macOS path as the Homebrew user, with sudo only for
// the LaunchDaemons.
func TestSetupMacOS(t *testing.T) {
	env, run, out := testEnv(t)
	prefix := t.TempDir()
	env.GOOS, env.Root, env.SudoUser, env.Systemd = "darwin", false, "", false
	env.Paths = DefaultPaths("darwin")
	env.Paths.BrewPrefix = prefix
	env.Paths.ConfigFile = filepath.Join(prefix, "var", "stockroom", "stockroom.env")
	env.Paths.DataDir = filepath.Join(prefix, "var", "stockroom")
	env.Paths.LogFile = filepath.Join(prefix, "var", "log", "stockroom.log")
	env.Paths.LaunchDaemons = filepath.Join(prefix, "LaunchDaemons")
	env.Paths.Binary = filepath.Join(prefix, "bin", "stockroom")
	os.MkdirAll(env.Paths.LaunchDaemons, 0o755)
	bin := filepath.Join(prefix, "opt", brewFormula, "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "postgres"), nil, 0o755)
	me, _ := user.Current()
	t.Setenv("USER", me.Username)

	if err := Setup(context.Background(), env, nonInteractive()); err != nil {
		t.Fatalf("Setup: %v\n%s", err, out)
	}
	for _, want := range []string{
		"initdb --locale=C -E UTF-8 " + filepath.Join(prefix, "var", brewFormula),
		"[root] install -m 644 -o root -g wheel",
		"[root] launchctl bootstrap system " + filepath.Join(env.Paths.LaunchDaemons, postgresLabel+".plist"),
		"[root] launchctl bootstrap system " + filepath.Join(env.Paths.LaunchDaemons, serverLabel+".plist"),
		"[root] launchctl kickstart -k system/" + serverLabel,
		"create role stockroom",
	} {
		if !run.ran(want) {
			t.Errorf("setup never ran %q", want)
		}
	}
	for _, c := range run.cmds {
		if filepath.Base(c.Name) == "psql" && (c.Root || c.User != "") {
			t.Errorf("psql ran as another account on macOS: %s", c)
		}
	}
	if _, err := os.Stat(env.Paths.ConfigFile); err != nil {
		t.Error(err)
	}
}

func runPlutil(path string) (string, error) {
	out, err := exec.Command("/usr/bin/plutil", "-lint", path).CombinedOutput()
	return string(out), err
}
