package stockroom

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// The backup export (CLAUDE.md §11): every table to its own CSV under a dated
// folder. Two callers share it. The admin panel's "Backup Now" button goes
// through BackupNow, and Phase 7's nightly CLI will call
// ExportAllTablesToCSV directly and then hand the folder to rclone.
//
// CSV rather than pg_dump because the point is a copy anyone can open and read
// after the closet PC dies, without a Postgres to restore into first. The
// restore path (CLAUDE.md §11) loads these files back with COPY.

// BackupResult is what one export wrote: where it went, and how many rows
// landed in each file. The admin panel shows the folder and the total as the
// result of the last run.
type BackupResult struct {
	Dir    string        `json:"dir"`
	Tables []TableExport `json:"tables"`
	Rows   int64         `json:"rows"`
	RanAt  time.Time     `json:"ran_at"`
}

// TableExport is one table's line of that result.
type TableExport struct {
	Table string `json:"table"`
	File  string `json:"file"`
	Rows  int64  `json:"rows"`
}

// BackupNow runs the export into baseDir, which the server passes from
// BACKUP_DIR. Admin-only, like every other admin-panel action.
func (db *DB) BackupNow(ctx context.Context, actor Actor, baseDir string) (BackupResult, error) {
	if err := RequireAdmin(actor); err != nil {
		return BackupResult{}, err
	}
	return db.ExportAllTablesToCSV(ctx, baseDir)
}

// ExportAllTablesToCSV writes one CSV per table into
// <baseDir>/<yyyy-mm-dd>/<table>.csv and returns what it wrote. Running twice
// in a day replaces the day's folder rather than piling up copies.
//
// The table list comes from the database, not from a list in Go, so a table
// added by a later migration is backed up without anyone remembering to add
// it here. Views are skipped: active_custody and overdue_custody are derived
// from custody_events, which is already in the export.
//
// The whole run is one snapshot written to one side folder, and neither half
// of that is fussiness. A backup is judged by whether it restores, and a run
// that read each table at its own instant could put a custody_events row in
// the export whose asset landed in assets.csv a moment too early to be there;
// the reload fails on the foreign key. A run that wrote straight into the
// dated folder and then died would leave half the tables in a folder that
// looks exactly like a whole backup, which is worse than leaving yesterday's.
func (db *DB) ExportAllTablesToCSV(ctx context.Context, baseDir string) (BackupResult, error) {
	if baseDir == "" {
		return BackupResult{}, fmt.Errorf("%w: BACKUP_DIR is not set, so there is nowhere to write the backup", ErrNotConfigured)
	}

	ranAt := time.Now()
	dir := filepath.Join(baseDir, ranAt.Format("2006-01-02"))
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return BackupResult{}, fmt.Errorf("create backup dir: %w", err)
	}
	staging, err := os.MkdirTemp(baseDir, ".partial-")
	if err != nil {
		return BackupResult{}, fmt.Errorf("create backup dir: %w", err)
	}
	// A no-op once the export has succeeded and moved the folder into place.
	defer func() { _ = os.RemoveAll(staging) }()
	// MkdirTemp is 0700; the finished folder is read like any other backup.
	if err := os.Chmod(staging, 0o755); err != nil {
		return BackupResult{}, fmt.Errorf("create backup dir: %w", err)
	}

	// One connection for the whole run: COPY TO STDOUT is a protocol-level
	// operation on a single connection, and holding one is cheaper than
	// borrowing a dozen in a row from a pool of eight.
	conn, err := db.Pool.Acquire(ctx)
	if err != nil {
		return BackupResult{}, fmt.Errorf("backup: %w", err)
	}
	defer conn.Release()

	// One transaction on it, too. Repeatable read pins every query below to
	// the snapshot the first one takes, so the table list and all the rows
	// describe the same database; read-only says the run will never write,
	// which is both true and cheaper for Postgres to hold open.
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return BackupResult{}, fmt.Errorf("backup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tables, err := publicTables(ctx, tx)
	if err != nil {
		return BackupResult{}, err
	}

	out := BackupResult{Dir: dir, Tables: make([]TableExport, 0, len(tables)), RanAt: ranAt}
	for _, table := range tables {
		// COPY goes through the same connection, so it reads the snapshot the
		// transaction is holding.
		rows, err := copyTableToFile(ctx, conn.Conn(), table, filepath.Join(staging, table+".csv"))
		if err != nil {
			return BackupResult{}, err
		}
		// The result names where the file ends up, not where it was written:
		// the staging folder is gone by the time a caller reads this.
		out.Tables = append(out.Tables, TableExport{Table: table, File: filepath.Join(dir, table+".csv"), Rows: rows})
		out.Rows += rows
	}
	if err := tx.Commit(ctx); err != nil {
		return BackupResult{}, fmt.Errorf("backup: %w", err)
	}
	if err := replaceBackupDir(staging, dir); err != nil {
		return BackupResult{}, err
	}
	return out, nil
}

// backupReplace serialises the swap below. Clearing the old folder and moving
// the new one in is two steps rather than one, because a rename will not
// replace a directory with anything in it, and two runs interleaving those
// steps would leave a dated folder holding half of each.
var backupReplace sync.Mutex

// replaceBackupDir puts staging where dir is. Replacing a folder from earlier
// the same day is the point -- what must not happen is the two runs mixing.
func replaceBackupDir(staging, dir string) error {
	backupReplace.Lock()
	defer backupReplace.Unlock()

	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("replace %s: %w", dir, err)
	}
	if err := os.Rename(staging, dir); err != nil {
		return fmt.Errorf("replace %s: %w", dir, err)
	}
	return nil
}

// copyTableToFile streams one table out through COPY. The rows never pass
// through Go's memory as a slice, so the size of the table doesn't matter.
func copyTableToFile(ctx context.Context, conn *pgx.Conn, table, file string) (int64, error) {
	f, err := os.Create(file)
	if err != nil {
		return 0, fmt.Errorf("create %s: %w", file, err)
	}
	defer f.Close()

	// The table name comes from information_schema, but it is still an
	// identifier being pasted into SQL, so it goes through pgx's quoting.
	name := pgx.Identifier{"public", table}.Sanitize()
	tag, err := conn.PgConn().CopyTo(ctx,
		f, `copy (select * from `+name+`) to stdout with (format csv, header)`)
	if err != nil {
		return 0, fmt.Errorf("export %s: %w", table, err)
	}
	if err := f.Sync(); err != nil {
		return 0, fmt.Errorf("write %s: %w", file, err)
	}
	return tag.RowsAffected(), nil
}

// publicTables lists the base tables in the public schema, alphabetically so
// two runs write the same files in the same order. It reads through the
// caller's querier so the export can take the list from the same snapshot it
// takes the rows from.
func publicTables(ctx context.Context, q querier) ([]string, error) {
	rows, err := q.Query(ctx, `
		select table_name from information_schema.tables
		where table_schema = 'public' and table_type = 'BASE TABLE'
		order by table_name`)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	defer rows.Close()

	tables := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan table name: %w", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	return tables, nil
}
