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
	// loaded is the LaunchDaemons launchd holds, by label. bootoutLag is how
	// many `launchctl print` calls still find one after its bootout.
	loaded     map[string]bool
	bootoutLag int
	leaving    map[string]int
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{listen: "localhost", fail: map[string]error{}, loaded: map[string]bool{}, leaving: map[string]int{}}
}

// launchctl answers like launchd: a label is loaded from its bootstrap until
// its bootout, and bootstrapping one that hasn't left yet fails.
func (f *fakeRunner) launchctl(c Cmd) (string, error) {
	if len(c.Args) < 2 {
		return "", nil
	}
	label := strings.TrimPrefix(c.Args[len(c.Args)-1], "system/")
	switch c.Args[0] {
	case "bootstrap":
		label = strings.TrimSuffix(filepath.Base(label), ".plist")
		if f.loaded[label] {
			return "", errors.New("launchctl: exit status 5: Bootstrap failed: 5: Input/output error")
		}
		f.loaded[label] = true
	case "bootout":
		if !f.loaded[label] {
			return "", errors.New("launchctl: exit status 3: Boot-out failed: 3: No such process")
		}
		if f.bootoutLag > 0 {
			f.leaving[label] = f.bootoutLag
			return "", errors.New("launchctl: exit status 36: Boot-out failed: 36: Operation now in progress")
		}
		delete(f.loaded, label)
	case "print":
		if n, ok := f.leaving[label]; ok {
			if n <= 1 {
				delete(f.leaving, label)
				delete(f.loaded, label)
			} else {
				f.leaving[label] = n - 1
			}
			return "", nil
		}
		if !f.loaded[label] {
			return "", errors.New("launchctl: exit status 113: Could not find service")
		}
	}
	return "", nil
}

func (f *fakeRunner) Run(ctx context.Context, c Cmd) (string, error) {
	f.cmds = append(f.cmds, c)
	if err := f.fail[filepath.Base(c.Name)]; err != nil {
		return "", err
	}
	if filepath.Base(c.Name) == "launchctl" {
		return f.launchctl(c)
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
			LaunchAgents: filepath.Join(root, "LaunchAgents"),
		},
		LookupUser:    func(name string) (*user.User, error) { return user.Lookup(name) },
		Chown:         func(string, int, int) error { return nil },
		Health:        func(context.Context, string) error { return nil },
		HealthTimeout: time.Second,
		Listening:     func(string) bool { return false },
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
	if err != nil || !strings.Contains(string(unit), "ExecStart=/usr/bin/stockroom serve --config "+env.Paths.ConfigFile+"\n") {
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
	os.WriteFile(env.Paths.PackagedUnit, []byte(systemdUnit("/usr/bin/stockroom", linuxConfigFile)), 0o644)
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

// TestSetupWithAConfigFlag: setup --config writes the config there and points
// the packaged unit at it through the drop-in, and a repair run without the
// flag keeps using it.
func TestSetupWithAConfigFlag(t *testing.T) {
	env, _, out := testEnv(t)
	os.MkdirAll(filepath.Dir(env.Paths.PackagedUnit), 0o755)
	os.WriteFile(env.Paths.PackagedUnit, []byte(systemdUnit("/usr/bin/stockroom", linuxConfigFile)), 0o644)
	defaultConfig := env.Paths.ConfigFile
	cfg := filepath.Join(t.TempDir(), "school", "stockroom.env")
	opts := nonInteractive()
	opts.ConfigFile = cfg
	if err := Setup(context.Background(), env, opts); err != nil {
		t.Fatalf("Setup: %v\n%s", err, out)
	}
	if _, err := os.Stat(cfg); err != nil {
		t.Fatalf("no config at --config: %v", err)
	}
	if _, err := os.Stat(defaultConfig); err == nil {
		t.Error("wrote the default config as well")
	}
	want := "ExecStart=\nExecStart=/usr/bin/stockroom serve --config " + cfg + "\nReadWritePaths=" + filepath.Dir(cfg) + "\n"
	if dropIn, _ := os.ReadFile(env.Paths.DropIn); !strings.Contains(string(dropIn), want) {
		t.Errorf("drop-in = %q, want it to contain %q", dropIn, want)
	}

	env.Paths.ConfigFile = defaultConfig
	if err := Setup(context.Background(), env, nonInteractive()); err != nil {
		t.Fatalf("repair Setup: %v\n%s", err, out)
	}
	if env.Paths.ConfigFile != cfg {
		t.Errorf("the repair run used %s, want the --config file %s", env.Paths.ConfigFile, cfg)
	}
	if _, err := os.Stat(defaultConfig); err == nil {
		t.Error("the repair run wrote the default config")
	}
}

func TestCheckConfigPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("setup runs on Linux and macOS")
	}
	if _, err := checkConfigPath("/srv/my school/stockroom.env"); err == nil {
		t.Error("accepted a path with a space, which ExecStart would split")
	}
	got, err := checkConfigPath("stockroom.env")
	if err != nil || !filepath.IsAbs(got) {
		t.Errorf("checkConfigPath(relative) = %q, %v; want an absolute path", got, err)
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

// TestSetupRefusesATakenAddress guards the two-minute wait a second program
// on the server's port used to cause: the service restarted on "address
// already in use" while the other program answered /health.
func TestSetupRefusesATakenAddress(t *testing.T) {
	env, run, _ := testEnv(t)
	env.Listening = func(string) bool { return true }
	addrFreeWait = 0
	t.Cleanup(func() { addrFreeWait = 10 * time.Second })
	err := Setup(context.Background(), env, nonInteractive())
	if err == nil || !strings.Contains(err.Error(), "already listening on "+defaultAddr) {
		t.Fatalf("err = %v", err)
	}
	if !run.ran("ss -ltnpH sport = :8080") {
		t.Error("setup never asked which program holds the port")
	}
	if run.ran("systemctl restart stockroom") {
		t.Error("the service was started on a taken address")
	}
}

// TestSetupReplacesItsOwnRunningService is a second setup on a working
// install: the listener is this install's service, which stops and restarts.
func TestSetupReplacesItsOwnRunningService(t *testing.T) {
	env, run, out := testEnv(t)
	env.Listening = func(string) bool { return !run.ran("systemctl stop stockroom") }
	if err := Setup(context.Background(), env, nonInteractive()); err != nil {
		t.Fatalf("Setup: %v\n%s", err, out)
	}
	if !run.ran("systemctl restart stockroom") {
		t.Error("the service was not started again")
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
	if want := systemdUnit("/usr/bin/stockroom", linuxConfigFile); string(raw) != want {
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
	env.Paths.LaunchAgents = filepath.Join(prefix, "LaunchAgents")
	env.Paths.Binary = filepath.Join(prefix, "bin", "stockroom")
	os.MkdirAll(env.Paths.LaunchDaemons, 0o755)
	bin := filepath.Join(prefix, "opt", brewFormula, "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "postgres"), nil, 0o755)
	me, _ := user.Current()
	t.Setenv("USER", me.Username)
	// An earlier setup left Postgres running, and launchd takes a moment to
	// let go of it: bootstrapping at once is "Bootstrap failed: 5".
	run.loaded[postgresLabel] = true
	run.bootoutLag = 2
	// The older checkout install's LaunchAgent holds the server's port.
	agent := filepath.Join(env.Paths.LaunchAgents, serverLabel+".plist")
	os.MkdirAll(env.Paths.LaunchAgents, 0o755)
	os.WriteFile(agent, nil, 0o644)

	if err := Setup(context.Background(), env, nonInteractive()); err != nil {
		t.Fatalf("Setup: %v\n%s", err, out)
	}
	if !run.ran("launchctl bootout gui/" + me.Uid + "/" + serverLabel) {
		t.Error("the older install's LaunchAgent was not stopped")
	}
	if _, err := os.Stat(agent); err == nil {
		t.Error("the older install's LaunchAgent would start again at the next login")
	}
	if _, err := os.Stat(agent + ".replaced"); err != nil {
		t.Errorf("the older install's LaunchAgent was not kept: %v", err)
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

// TestBrewPostgresPlistSetsLocale guards the one-minute hang on a fresh Mac:
// Postgres started by launchd with no locale exits with "postmaster became
// multithreaded during startup".
func TestBrewPostgresPlistSetsLocale(t *testing.T) {
	env, _, _ := testEnv(t)
	env.Paths.BrewPrefix = "/opt/homebrew"
	env.SudoUser = "teacher"
	plist := (&brewPostgres{env: env}).plist()
	for _, want := range []string{
		"<key>EnvironmentVariables</key>",
		"<key>LC_ALL</key>\n    <string>C</string>",
		"<string>/opt/homebrew/opt/" + brewFormula + "/bin/postgres</string>",
		"<string>/opt/homebrew/var/log/" + brewFormula + ".log</string>",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist lacks %q:\n%s", want, plist)
		}
	}
}

func TestTailFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	os.WriteFile(path, []byte("a\nb\nc\nd\n"), 0o644)
	if got := tailFile(path, 2); got != "  c\n  d" {
		t.Errorf("tailFile = %q", got)
	}
	if got := tailFile(filepath.Join(t.TempDir(), "none"), 2); !strings.Contains(got, "no such file") {
		t.Errorf("tailFile of a missing file = %q", got)
	}
}

func runPlutil(path string) (string, error) {
	out, err := exec.Command("/usr/bin/plutil", "-lint", path).CombinedOutput()
	return string(out), err
}
