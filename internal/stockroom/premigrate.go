package stockroom

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The pre-migrate dump. An upgrade is a new binary applying its migrations at
// boot, and a migration that goes wrong on a school's data has no undo. So
// before the first pending migration runs, serve writes a pg_dump of the whole
// database. This used to be scripts/install.sh's job, which only helped the
// people who upgraded through it.

// SchemaOptions says how PrepareSchema protects the data before migrating.
type SchemaOptions struct {
	// Dump is PRE_MIGRATE_DUMP. Off skips the dump; required refuses to
	// migrate without one.
	Dump PreMigrateDumpMode
	// PGDump is PG_DUMP, an explicit path to pg_dump. Empty searches.
	PGDump string
	// Dir is the folder the dumps go in, <backups dir>/pre-migrate. Empty
	// uses the backup folder saved in the admin panel, when there is one.
	Dir string
	// DatabaseURL is the connection the dump uses. Its password travels in
	// PGPASSWORD, never in an argument, because every local user can read a
	// process's arguments through ps.
	DatabaseURL string
}

// preMigrateKeep is how many dumps survive pruning. Each one is the whole
// roster and inventory, and ten upgrades back is further than anybody will
// want to roll.
const preMigrateKeep = 10

// PrepareSchema brings the database up to the embedded migrations, dumping it
// first when opts asks for that. It returns the migrations applied and the
// dump written, either of which may be empty.
//
// A database without a profiles table has nothing in it worth dumping (a
// fresh install), so it migrates without one even when a dump is required.
func PrepareSchema(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, opts SchemaOptions) (applied []string, dump string, err error) {
	pending, err := PendingMigrations(ctx, pool, fsys)
	if err != nil {
		return nil, "", err
	}
	if len(pending) > 0 && opts.Dump == PreMigrateDumpRequired {
		var hasData bool
		if err := pool.QueryRow(ctx, `select to_regclass('public.profiles') is not null`).Scan(&hasData); err != nil {
			return nil, "", fmt.Errorf("pre-migrate dump: %w", err)
		}
		if hasData {
			if opts.Dir == "" {
				opts.Dir = savedPreMigrateDir(ctx, pool)
			}
			from, err := schemaVersion(ctx, pool)
			if err != nil {
				return nil, "", err
			}
			dump, err = writePreMigrateDump(ctx, opts, from, pending[len(pending)-1])
			if err != nil {
				return nil, "", fmt.Errorf("pre-migrate dump failed, so no migration ran: %w\n"+
					"Fix it and start again, or set PRE_MIGRATE_DUMP=off to migrate without a copy", err)
			}
		}
	}
	applied, err = Migrate(ctx, pool, fsys)
	return applied, dump, err
}

// savedPreMigrateDir is <app_settings.backup_dir>/pre-migrate, or "" when no
// folder is saved. Read before migrating, so it asks for nothing newer than
// the column itself.
func savedPreMigrateDir(ctx context.Context, pool *pgxpool.Pool) string {
	var dir *string
	if err := pool.QueryRow(ctx, `select backup_dir from app_settings limit 1`).Scan(&dir); err != nil || dir == nil || *dir == "" {
		return ""
	}
	return filepath.Join(*dir, "pre-migrate")
}

// writePreMigrateDump runs pg_dump into a new mode-600 file and prunes old ones.
func writePreMigrateDump(ctx context.Context, opts SchemaOptions, from, to string) (string, error) {
	if opts.Dir == "" {
		return "", fmt.Errorf("no backup folder to put it in: set BACKUP_DIR in the config, or choose a backup folder in Admin → Settings")
	}
	if !filepath.IsAbs(opts.Dir) {
		return "", fmt.Errorf("the dump folder %q is not an absolute path", opts.Dir)
	}
	bin, err := FindPGDump(opts.PGDump)
	if err != nil {
		return "", err
	}
	conn, err := pgconn.ParseConfig(opts.DatabaseURL)
	if err != nil {
		return "", fmt.Errorf("read DATABASE_URL: %w", err)
	}
	if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
		return "", err
	}
	if from == "" {
		from = "none"
	}
	name := fmt.Sprintf("pre-migrate-%s-to-%s-%s.sql", from, to, time.Now().Format("20060102-150405"))
	path := filepath.Join(opts.Dir, name)
	// O_EXCL: two starts in one second must not share a file, and a file
	// that already exists is not ours to truncate.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, bin,
		"--host", conn.Host,
		"--port", strconv.Itoa(int(conn.Port)),
		"--username", conn.User,
		"--dbname", conn.Database,
		"--no-password",
	)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+conn.Password)
	var stderr bytes.Buffer
	cmd.Stdout = f
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	closeErr := f.Close()
	if runErr != nil || closeErr != nil {
		_ = os.Remove(path)
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = errors.Join(runErr, closeErr).Error()
		}
		return "", fmt.Errorf("%s: %s", bin, oneLine(msg))
	}
	pruneDumps(opts.Dir, preMigrateKeep)
	return path, nil
}

// pruneDumps deletes all but the newest keep pre-migrate dumps in dir. A
// failure only leaves an old file behind, so it is not reported.
func pruneDumps(dir string, keep int) {
	matches, err := filepath.Glob(filepath.Join(dir, "pre-migrate-*.sql"))
	if err != nil || len(matches) <= keep {
		return
	}
	type dumpFile struct {
		path string
		mod  time.Time
	}
	var files []dumpFile
	for _, m := range matches {
		if st, err := os.Stat(m); err == nil {
			files = append(files, dumpFile{m, st.ModTime()})
		}
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].mod.Equal(files[j].mod) {
			return files[i].path > files[j].path
		}
		return files[i].mod.After(files[j].mod)
	})
	for _, f := range files[min(keep, len(files)):] {
		_ = os.Remove(f.path)
	}
}

// ErrPGDumpNotFound is a pre-migrate dump with no pg_dump to run.
var ErrPGDumpNotFound = errors.New("pg_dump not found")

// pgDumpFallbacks are the places pg_dump lives off PATH, highest Postgres
// version first. Debian and Ubuntu keep one bin directory per major version;
// Homebrew's postgresql@17 is keg-only, so it is never on PATH. A variable so
// tests can empty it on a machine that has a real pg_dump.
var pgDumpFallbacks = func() []string {
	var found []string
	matches, _ := filepath.Glob("/usr/lib/postgresql/*/bin/pg_dump")
	sort.Slice(matches, func(i, j int) bool { return pgMajor(matches[i]) > pgMajor(matches[j]) })
	found = append(found, matches...)
	return append(found, filepath.Join(BrewPrefix(), "opt", "postgresql@17", "bin", "pg_dump"))
}

var pgMajorDir = regexp.MustCompile(`/postgresql/([0-9]+)/`)

func pgMajor(path string) int {
	if m := pgMajorDir.FindStringSubmatch(path); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

// FindPGDump returns the pg_dump to run: explicit (PG_DUMP) when set, then
// PATH, then pgDumpFallbacks.
func FindPGDump(explicit string) (string, error) {
	if explicit != "" {
		if isExecutable(explicit) {
			return explicit, nil
		}
		return "", fmt.Errorf("%w: PG_DUMP is %q, which is not an executable file", ErrPGDumpNotFound, explicit)
	}
	if p, err := exec.LookPath("pg_dump"); err == nil {
		return p, nil
	}
	for _, p := range pgDumpFallbacks() {
		if isExecutable(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("%w on PATH or in the usual places. Install the PostgreSQL client "+
		"(apt install postgresql-client, or brew install postgresql@17) or set PG_DUMP", ErrPGDumpNotFound)
}

func isExecutable(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}
