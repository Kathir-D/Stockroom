package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"stockroom/internal/stockroom"
	"stockroom/supabase"
)

// Doctor checks an install and says how to fix what's wrong. Each check
// prints OK, WARN or FAIL with one line of fix. It returns the number of
// FAILs; the command exits non-zero when there is any.

type level int

const (
	levelOK level = iota
	levelWarn
	levelFail
)

type doctor struct {
	env   *Env
	fails int
	warns int
	// dev is a working copy's .env: no service is expected, and Postgres
	// is the Supabase container, which listens on * inside Docker.
	dev bool
}

func (d *doctor) report(l level, what, fix string) {
	tag := map[level]string{levelOK: "OK  ", levelWarn: "WARN", levelFail: "FAIL"}[l]
	fmt.Fprintf(d.env.Out, "  %s  %s\n", tag, what)
	if fix != "" && l != levelOK {
		fmt.Fprintf(d.env.Out, "        fix: %s\n", fix)
	}
	switch l {
	case levelWarn:
		d.warns++
	case levelFail:
		d.fails++
	}
}

// Low-disk thresholds for the data, backup and recordings folders.
const (
	diskWarnBytes = 5 << 30
	diskFailBytes = 1 << 30
)

// Doctor runs every check. configPath is the --config flag.
func Doctor(ctx context.Context, e *Env, configPath string) int {
	d := &doctor{env: e}

	e.say("== Config ==")
	cfg, err := stockroom.LoadConfigFrom(configPath)
	if err != nil {
		d.report(levelFail, "config: "+err.Error(), "correct the file, or run sudo stockroom setup to write one")
		return d.fails
	}
	switch {
	case cfg.EnvPath == "":
		d.report(levelFail, "no config file found", "run sudo stockroom setup, or pass --config <file>")
	case cfg.Installed():
		d.report(levelOK, cfg.Describe()+", data paths absolute", "")
	default:
		d.report(levelOK, cfg.Describe(), "")
	}

	d.dev = !cfg.Installed()

	e.say("== PostgreSQL ==")
	db := d.checkPostgres(ctx, cfg)
	if db != nil {
		defer db.Close()
	}

	e.say("== Service ==")
	d.checkService(ctx)
	d.checkPort(ctx, cfg.ServerAddr)

	e.say("== Tools ==")
	d.checkRclone(ctx, cfg)
	d.checkPGDump(cfg)

	e.say("== Disk and backups ==")
	d.checkDisks(ctx, cfg, db)
	d.checkBackups(ctx, db)

	if e.WSL {
		e.say("== WSL ==")
		d.checkWSL(ctx)
	}

	e.say("")
	switch {
	case d.fails > 0:
		e.say("%d problem(s) to fix, %d warning(s).", d.fails, d.warns)
	case d.warns > 0:
		e.say("No problems, %d warning(s).", d.warns)
	default:
		e.say("Everything checks out.")
	}
	return d.fails
}

func (d *doctor) checkPostgres(ctx context.Context, cfg stockroom.Config) *stockroom.DB {
	ctx5, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	db, err := stockroom.Open(ctx5, cfg.DatabaseURL, stockroom.Options{UploadsDir: cfg.UploadsDir})
	if err != nil {
		fix := "start PostgreSQL (sudo systemctl start postgresql on Linux) and check DATABASE_URL"
		if d.env.GOOS == "darwin" {
			fix = "sudo launchctl kickstart system/" + postgresLabel + ", and check DATABASE_URL"
		}
		d.report(levelFail, "cannot reach PostgreSQL: "+firstLine(err.Error()), fix)
		return nil
	}

	si, err := db.ServerInfo(ctx)
	if err != nil {
		d.report(levelFail, "PostgreSQL answered but a query failed: "+err.Error(), "")
		return db
	}
	version, num, listen := si.Version, si.VersionNum, si.ListenAddresses
	if num < 140000 {
		d.report(levelFail, "PostgreSQL "+version+" is older than 14, the oldest Stockroom is tested on", "upgrade PostgreSQL (docs/INSTALL.md, Upgrading PostgreSQL)")
	} else {
		d.report(levelOK, "PostgreSQL "+version+" reachable", "")
	}
	if bad := nonLoopback(listen); bad != "" && d.dev {
		d.report(levelWarn, fmt.Sprintf("PostgreSQL listens on %q; fine for the Supabase container, not for an install", bad), "")
	} else if bad != "" {
		d.report(levelFail, fmt.Sprintf("PostgreSQL listens on %q, reachable from the network", bad), "set listen_addresses = 'localhost' in postgresql.conf and restart PostgreSQL")
	} else {
		d.report(levelOK, "PostgreSQL listens on loopback only", "")
	}

	pending, err := stockroom.PendingMigrations(ctx, db.Pool, supabase.Migrations)
	switch {
	case errors.Is(err, stockroom.ErrDatabaseNewer):
		d.report(levelFail, err.Error(), "install the newer version of Stockroom")
	case err != nil:
		d.report(levelFail, "could not read the migrations: "+err.Error(), "")
	case len(pending) > 0:
		d.report(levelWarn, fmt.Sprintf("%d migration(s) not applied yet", len(pending)), "restart the service; serve applies them at start")
	default:
		d.report(levelOK, "database schema is current", "")
	}
	return db
}

func (d *doctor) checkService(ctx context.Context) {
	e := d.env
	switch {
	case d.dev:
		d.report(levelOK, "a working copy, so no service is expected", "")
	case e.GOOS == "linux" && !e.Systemd:
		d.report(levelWarn, "systemd is not running, so nothing starts Stockroom at boot", "under WSL, turn systemd on in /etc/wsl.conf; elsewhere, start stockroom serve another way")
	case e.GOOS == "linux":
		enabled, _ := e.Run.Run(ctx, Cmd{Name: "systemctl", Args: []string{"is-enabled", "stockroom"}})
		active, _ := e.Run.Run(ctx, Cmd{Name: "systemctl", Args: []string{"is-active", "stockroom"}})
		userOut, _ := e.Run.Run(ctx, Cmd{Name: "systemctl", Args: []string{"show", "-p", "User", "--value", "stockroom"}})
		enabled, active, runAs := strings.TrimSpace(enabled), strings.TrimSpace(active), strings.TrimSpace(userOut)
		switch {
		case enabled == "" || enabled == "not-found":
			d.report(levelFail, "the stockroom service is not installed", "sudo stockroom setup")
		case enabled != "enabled":
			d.report(levelFail, "the stockroom service is "+enabled+", so it won't start at boot", "sudo systemctl enable stockroom")
		case active != "active":
			d.report(levelFail, "the stockroom service is enabled but "+orDefault(active, "not running"), "sudo systemctl start stockroom; journalctl -u stockroom says why it stopped")
		case runAs == "" || runAs == "root":
			d.report(levelWarn, "the service runs as root", "sudo stockroom setup writes a service user")
		default:
			d.report(levelOK, "service enabled and running as "+runAs, "")
		}
	case e.GOOS == "darwin":
		out, err := e.Run.Run(ctx, Cmd{Name: "launchctl", Args: []string{"print", "system/" + serverLabel}})
		switch {
		case err != nil:
			d.report(levelFail, "the "+serverLabel+" LaunchDaemon is not loaded", "stockroom setup, or sudo launchctl bootstrap system /Library/LaunchDaemons/"+serverLabel+".plist")
		case !strings.Contains(out, "state = running"):
			d.report(levelFail, "the LaunchDaemon is loaded but not running", "check "+e.Paths.LogFile)
		default:
			d.report(levelOK, "LaunchDaemon "+serverLabel+" running", "")
		}
	}
}

// checkPort asks /health whether the server's address answers as Stockroom,
// and names whatever holds the port when it doesn't.
func (d *doctor) checkPort(ctx context.Context, addr string) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/health", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		d.report(levelFail, "nothing answers at http://"+addr, "start the service; if it keeps stopping, its log says why")
		return
	}
	defer res.Body.Close()
	var body struct {
		OK      bool   `json:"ok"`
		Version string `json:"version"`
	}
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<16))
	if json.Unmarshal(raw, &body) == nil && body.OK {
		d.report(levelOK, fmt.Sprintf("Stockroom %s answers at http://%s", orDefault(body.Version, "(unknown version)"), addr), "")
		return
	}
	_, port, _ := net.SplitHostPort(addr)
	holder := d.portHolder(ctx, port)
	d.report(levelFail, fmt.Sprintf("http://%s answers, but not as Stockroom (%s)%s", addr, res.Status, holder),
		"stop the other program, or change SERVER_ADDR in the config")
}

func (d *doctor) portHolder(ctx context.Context, port string) string {
	if port == "" {
		return ""
	}
	out, err := d.env.Run.Run(ctx, Cmd{Name: "lsof", Args: []string{"-nP", "-iTCP:" + port, "-sTCP:LISTEN"}})
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return ""
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 2 {
		return ""
	}
	return fmt.Sprintf("; port %s is held by %s (pid %s)", port, fields[0], fields[1])
}

func (d *doctor) checkRclone(ctx context.Context, cfg stockroom.Config) {
	info := stockroom.ResolveRclone(ctx, cfg.RcloneBinary)
	switch {
	case !info.Found:
		d.report(levelWarn, "rclone: "+info.Error, "only the Google Drive backup and the photo wall need it")
	case info.Error != "":
		d.report(levelWarn, "rclone at "+info.Path+": "+info.Error, "")
	case info.TooOld:
		d.report(levelWarn, fmt.Sprintf("rclone %s at %s is older than %s", info.Version, info.Path, info.MinVersion), "install a newer rclone from https://rclone.org/install/")
	default:
		d.report(levelOK, fmt.Sprintf("rclone %s at %s", info.Version, info.Path), "")
	}
}

func (d *doctor) checkPGDump(cfg stockroom.Config) {
	path, err := stockroom.FindPGDump(cfg.PGDump)
	switch {
	case cfg.PreMigrateDump != stockroom.PreMigrateDumpRequired && err != nil:
		d.report(levelOK, "pg_dump not found, and PRE_MIGRATE_DUMP is off", "")
	case cfg.PreMigrateDump != stockroom.PreMigrateDumpRequired:
		d.report(levelOK, "pg_dump at "+path+" (PRE_MIGRATE_DUMP is off)", "")
	case err != nil:
		d.report(levelFail, err.Error(), "the next upgrade will refuse to migrate until pg_dump is installed")
	default:
		d.report(levelOK, "pg_dump at "+path+" for the pre-migrate dump", "")
	}
}

func (d *doctor) checkDisks(ctx context.Context, cfg stockroom.Config, db *stockroom.DB) {
	folders := []struct{ name, dir string }{{"uploads", cfg.UploadsDir}}
	if dir := cfg.PreMigrateDumpDir(); dir != "" {
		folders = append(folders, struct{ name, dir string }{"pre-migrate dumps", dir})
	}
	if db != nil {
		if f, err := db.SavedFolders(ctx); err == nil {
			for _, x := range []struct{ name, dir string }{
				{"backups", f.Backup}, {"photo mirror", f.PhotoBackup}, {"camera recordings", f.Recordings},
			} {
				if x.dir != "" {
					folders = append(folders, x)
				}
			}
		}
	}
	for _, f := range folders {
		dir := existingParent(f.dir)
		free, err := stockroom.FreeSpace(dir)
		switch {
		case err != nil:
			d.report(levelWarn, fmt.Sprintf("%s (%s): cannot measure free space: %v", f.name, f.dir, err), "")
		case free < diskFailBytes:
			d.report(levelFail, fmt.Sprintf("%s (%s): %s free", f.name, f.dir, gigabytes(free)), "free some space on that disk")
		case free < diskWarnBytes:
			d.report(levelWarn, fmt.Sprintf("%s (%s): %s free", f.name, f.dir, gigabytes(free)), "free some space on that disk soon")
		default:
			d.report(levelOK, fmt.Sprintf("%s (%s): %s free", f.name, f.dir, gigabytes(free)), "")
		}
	}
}

// existingParent walks up from dir to the first directory that exists, so a
// folder not created yet is measured on the disk it will be created on.
func existingParent(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	for {
		if _, err := stockroom.FreeSpace(abs); err == nil {
			return abs
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return abs
		}
		abs = parent
	}
}

func gigabytes(b int64) string {
	return strconv.FormatFloat(float64(b)/(1<<30), 'f', 1, 64) + " GB"
}

func (d *doctor) checkBackups(ctx context.Context, db *stockroom.DB) {
	if db == nil {
		return
	}
	st, err := db.BackupStatus(ctx, stockroom.LocalCLIActor())
	switch {
	case err != nil:
		d.report(levelWarn, "could not read the backup status: "+err.Error(), "")
	case !st.Configured:
		d.report(levelFail, "no backup folder is set, so nothing is backed up", "choose one in Admin → Settings")
	case st.Stale && st.WorstAgeHours == nil:
		// A fresh install: the first run is tonight, or at the next start.
		d.report(levelWarn, "no backup has succeeded yet", "Admin → Backup → Back up now runs one")
	case st.Stale:
		d.report(levelFail, fmt.Sprintf("the oldest target's last success was %.0f hours ago", *st.WorstAgeHours),
			"Admin → Backup shows which target failed and why")
	default:
		age := "just now"
		if st.WorstAgeHours != nil {
			age = fmt.Sprintf("%.0f hours ago", *st.WorstAgeHours)
		}
		d.report(levelOK, "last successful backup "+age, "")
	}
}

func (d *doctor) checkWSL(ctx context.Context) {
	e := d.env
	if e.Systemd {
		d.report(levelOK, "systemd is on", "")
	} else {
		d.report(levelFail, "systemd is off", "add [boot] systemd=true to /etc/wsl.conf, then wsl --shutdown from Windows")
	}
	if _, err := e.Run.Run(ctx, Cmd{Name: "schtasks.exe", Args: []string{"/query", "/tn", WSLTaskName}}); err != nil {
		d.report(levelFail, "no Windows startup task named "+WSLTaskName+", so WSL won't start at boot", "run scripts/get.ps1 again from an administrator PowerShell")
	} else {
		d.report(levelOK, "Windows startup task "+WSLTaskName+" exists", "")
	}
	out, err := e.Run.Run(ctx, Cmd{Name: "powershell.exe", Args: []string{"-NoProfile", "-Command", "[DateTimeOffset]::UtcNow.ToUnixTimeSeconds()"}})
	if err != nil {
		d.report(levelWarn, "could not read the Windows clock: "+firstLine(err.Error()), "")
		return
	}
	win, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		d.report(levelWarn, "could not read the Windows clock: "+strings.TrimSpace(out), "")
		return
	}
	skew := time.Since(time.Unix(win, 0))
	if skew < 0 {
		skew = -skew
	}
	if skew > time.Minute {
		d.report(levelFail, fmt.Sprintf("the WSL clock is %s off Windows's", skew.Round(time.Second)), "wsl --shutdown from Windows resets it; set Windows never to sleep")
	} else {
		d.report(levelOK, "the WSL clock matches Windows", "")
	}
}

// WSLTaskName is the Task Scheduler task scripts/get.ps1 registers to start
// WSL at boot.
const WSLTaskName = "Stockroom WSL"

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
