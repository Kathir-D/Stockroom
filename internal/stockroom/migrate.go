package stockroom

import (
	"context"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Migrate applies every migration in fsys that this database has not seen.
//
// It exists because an installed Stockroom has no Supabase CLI. Development
// still runs `supabase db reset`, which applies the same files from the same
// directory; this is the other reader of them, so a school's database moves
// forward when they replace the binary and nobody is ever asked to run SQL in
// an order. That upgrade path is the whole point: without it every release
// forever is a support conversation that starts "run these files, in this
// order, against this database".
//
// The bookkeeping table is *Supabase's own*,
// `supabase_migrations.schema_migrations`, and that is deliberate rather than
// lazy. Two tables would mean a database migrated by the CLI looks unmigrated
// to the server and vice versa, so the first production start after a
// development reset would replay `create table profiles` over a live schema.
// It is also the table `schemaVersion` already reads for the backup manifest
// (backup.go), so the restore's schema-version check keeps working unchanged
// on an install that has never had the CLI anywhere near it.
//
// Ordering is by filename, which is the CLI's rule too: the leading timestamp
// is the version and it sorts lexicographically. A file whose name does not
// start with digits is an error rather than a skip -- a migration silently not
// running is the failure this function exists to prevent.
//
// Each file runs in its own transaction, so a syntax error in the fourth
// migration leaves the first three applied and the database in a state the
// next run can continue from. The alternative -- one transaction for all of
// them -- reads safer and is not: several of these create types and indexes
// that a later migration depends on, and a half-applied *set* is what the
// version table is for.
func Migrate(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) ([]string, error) {
	files, err := migrationFilenames(fsys)
	if err != nil {
		return nil, err
	}

	// One connection for the whole run, because the advisory lock below is
	// session-scoped: taken on a pooled connection that is then returned, it
	// would be released at a moment nobody chose.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("migrate: acquire connection: %w", err)
	}
	defer conn.Release()

	// Two servers against one database is not the normal deployment, but a
	// restart that overlaps its predecessor's shutdown is, and both would
	// otherwise read "nothing applied" and race to create the same table. The
	// lock is held, not tried: the loser should wait and then find there is
	// nothing left to do, which is the correct outcome.
	if _, err := conn.Exec(ctx, `select pg_advisory_lock($1)`, migrateLockKey); err != nil {
		return nil, fmt.Errorf("migrate: lock: %w", err)
	}
	defer func() {
		// Best-effort: a failed unlock is survivable because the lock is
		// session-scoped and the connection is about to be released, which
		// drops it anyway.
		_, _ = conn.Exec(context.WithoutCancel(ctx), `select pg_advisory_unlock($1)`, migrateLockKey)
	}()

	if err := ensureMigrationsTable(ctx, conn); err != nil {
		return nil, err
	}

	applied, err := appliedVersions(ctx, conn)
	if err != nil {
		return nil, err
	}

	var ran []string
	for _, file := range files {
		if applied[file.version] {
			continue
		}
		if err := applyMigration(ctx, conn, fsys, file); err != nil {
			return ran, err
		}
		ran = append(ran, file.name)
	}
	return ran, nil
}

// migrateLockKey is the advisory-lock id this function takes. Arbitrary, but
// fixed: the value has no meaning beyond being the one every Stockroom binary
// agrees on. It is deliberately not the backup's key -- a nightly backup and a
// start-up migration blocking each other would be two unrelated subsystems
// sharing a mutex for no reason.
const migrateLockKey int64 = 0x53544B524D494752 // "STKRMIGR"

// migrationFile is one file on the way in: its sort key, its display name and
// the path to read it back from.
type migrationFile struct {
	version string
	name    string
	path    string
}

// versionPrefix matches the leading timestamp the CLI writes and sorts on.
var versionPrefix = regexp.MustCompile(`^([0-9]+)_(.+)\.sql$`)

func migrationFilenames(fsys fs.FS) ([]migrationFile, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("migrate: read migrations: %w", err)
	}

	var files []migrationFile
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		match := versionPrefix.FindStringSubmatch(entry.Name())
		if match == nil {
			return nil, fmt.Errorf("migrate: %q is not <version>_<name>.sql", entry.Name())
		}
		files = append(files, migrationFile{
			version: match[1],
			name:    match[2],
			path:    entry.Name(),
		})
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("migrate: no migrations found")
	}

	sort.Slice(files, func(i, j int) bool { return files[i].version < files[j].version })

	// Two files with one version is refused here, at boot, rather than left to
	// the test that checks the embedded set. The bookkeeping table is keyed on
	// the version, so a fresh database would run both files and record one,
	// while an upgraded database that already recorded the version would skip
	// the second -- two installs of the same release with different schemas.
	for i := 1; i < len(files); i++ {
		if files[i-1].version == files[i].version {
			return nil, fmt.Errorf("migrate: %q and %q share version %s",
				files[i-1].path, files[i].path, files[i].version)
		}
	}
	return files, nil
}

// ensureMigrationsTable creates Supabase's bookkeeping table if it is absent.
//
// The column list matches what the CLI creates, `statements text[]` included,
// so a database this function bootstraps is one the CLI can also write to --
// which matters on a developer's machine, where both tools touch the same
// database.
func ensureMigrationsTable(ctx context.Context, q querier) error {
	const ddl = `
		create schema if not exists supabase_migrations;
		create table if not exists supabase_migrations.schema_migrations (
			version text not null primary key,
			statements text[],
			name text
		)`
	if _, err := q.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("migrate: create schema_migrations: %w", err)
	}
	return nil
}

func appliedVersions(ctx context.Context, q querier) (map[string]bool, error) {
	rows, err := q.Query(ctx, `select version from supabase_migrations.schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("migrate: read applied versions: %w", err)
	}
	defer rows.Close()

	applied := map[string]bool{}
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("migrate: read applied versions: %w", err)
		}
		applied[version] = true
	}
	return applied, rows.Err()
}

// applyMigration runs one file and records it, in one transaction.
//
// The whole file goes to the server as a single Exec rather than being split
// on semicolons. pgx sends that as a simple query, which Postgres runs as an
// implicit transaction of its own -- and splitting SQL on `;` is wrong in a
// way that only shows up later, because these files contain dollar-quoted
// function bodies with semicolons inside them.
func applyMigration(ctx context.Context, conn *pgxpool.Conn, fsys fs.FS, file migrationFile) error {
	body, err := fs.ReadFile(fsys, file.path)
	if err != nil {
		return fmt.Errorf("migrate: read %s: %w", file.path, err)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migrate: begin %s: %w", file.path, err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, string(body)); err != nil {
		return fmt.Errorf("migrate: %s: %w", file.path, err)
	}
	if _, err := tx.Exec(ctx, `
		insert into supabase_migrations.schema_migrations (version, name, statements)
		values ($1, $2, $3)
		on conflict (version) do nothing`,
		file.version, file.name, []string{string(body)}); err != nil {
		return fmt.Errorf("migrate: record %s: %w", file.path, err)
	}
	return tx.Commit(ctx)
}
