package stockroom

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
// in a day overwrites the day's folder rather than piling up copies.
//
// The table list comes from the database, not from a list in Go, so a table
// added by a later migration is backed up without anyone remembering to add
// it here. Views are skipped: active_custody and overdue_custody are derived
// from custody_events, which is already in the export.
func (db *DB) ExportAllTablesToCSV(ctx context.Context, baseDir string) (BackupResult, error) {
	if baseDir == "" {
		return BackupResult{}, fmt.Errorf("%w: BACKUP_DIR is not set, so there is nowhere to write the backup", ErrNotConfigured)
	}

	ranAt := time.Now()
	dir := filepath.Join(baseDir, ranAt.Format("2006-01-02"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return BackupResult{}, fmt.Errorf("create backup dir: %w", err)
	}

	tables, err := db.publicTables(ctx)
	if err != nil {
		return BackupResult{}, err
	}

	// One connection for the whole run: COPY TO STDOUT is a protocol-level
	// operation on a single connection, and holding one is cheaper than
	// borrowing a dozen in a row from a pool of eight.
	conn, err := db.Pool.Acquire(ctx)
	if err != nil {
		return BackupResult{}, fmt.Errorf("backup: %w", err)
	}
	defer conn.Release()

	out := BackupResult{Dir: dir, Tables: make([]TableExport, 0, len(tables)), RanAt: ranAt}
	for _, table := range tables {
		file := filepath.Join(dir, table+".csv")
		rows, err := copyTableToFile(ctx, conn.Conn(), table, file)
		if err != nil {
			return BackupResult{}, err
		}
		out.Tables = append(out.Tables, TableExport{Table: table, File: file, Rows: rows})
		out.Rows += rows
	}
	return out, nil
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
// two runs write the same files in the same order.
func (db *DB) publicTables(ctx context.Context) ([]string, error) {
	rows, err := db.Pool.Query(ctx, `
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
