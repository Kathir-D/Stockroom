package stockroom

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The backup run (CLAUDE.md §11, docs/design/backup.md). One function does the
// whole night's work -- export, archive, prune, mirror the photos, push to
// every configured target -- and three callers share it: the admin panel's
// "Back up now", the in-server scheduler, and the scheduler's catch-up on boot.
//
// CSV rather than pg_dump because the point is a copy anyone can open and read
// after the closet PC dies, without a Postgres to restore into first. The
// restore (restore.go) loads the same files back with COPY, which is why the
// two halves of the format live together in archive.go.

// BackupResult is what one run did. Everything the backup screen shows comes
// from here, including the parts that failed: a target that could not be
// reached is a line in Targets with an error on it, not a failed run. The
// local archive is what a restore needs, and it was written before any target
// was contacted.
type BackupResult struct {
	Dir     string    `json:"dir"`
	Archive string    `json:"archive"`
	RanAt   time.Time `json:"ran_at"`
	Source  string    `json:"source"`

	Tables []TableExport `json:"tables"`
	Rows   int64         `json:"rows"`

	SchemaVersion string `json:"schema_version"`
	Encrypted     bool   `json:"encrypted"`

	// Skipped is set when another process held the backup lock, which is a
	// skip rather than a failure: the run that holds it is doing the work.
	Skipped bool `json:"skipped"`

	Targets []TargetResult     `json:"targets"`
	Photos  *PhotoMirrorResult `json:"photos"`
	Pruned  []string           `json:"pruned"`
}

// TableExport is one table's line of that result. File is the path *inside the
// archive*, not on disk: the dated folder holds the zip and the two readable
// CSVs, and the raw tables live in the zip where a restore reads them.
type TableExport struct {
	Table string `json:"table"`
	File  string `json:"file"`
	Rows  int64  `json:"rows"`
}

// TargetResult is one off-site push. A failure here never fails the run: the
// other target may have succeeded and the local archive certainly did.
type TargetResult struct {
	Target string `json:"target"`
	OK     bool   `json:"ok"`
	// Ref is what the target calls what it wrote: a commit sha, a folder.
	Ref   string `json:"ref"`
	Error string `json:"error"`
}

// Backup sources, recorded in the log so "why did this run at 09:14" has an
// answer.
const (
	BackupSourceManual    = "manual"
	BackupSourceScheduled = "scheduled"
	BackupSourceCatchUp   = "boot-catchup"
)

// exportRedactions names the columns the export must not write out.
//
// app_settings sits in the public schema, so publicTables sweeps it up like
// any other table -- which would put github_token into app_settings.csv and
// push it to the very repository that token grants write access to. GitHub's
// secret scanning would spot the prefix and revoke it, and backups would stop
// silently a few hours after the first successful push. archive_passphrase is
// on the list for a blunter reason: a passphrase written inside the archive it
// encrypts protects nothing at all.
//
// A secret column added to this schema later has to be added here too. The
// table's comment in the migration says so, because that is where somebody
// adding a column is looking.
// signin_photos_folder_id is on the list for the same reason as the other
// two, and it is the least obvious of the three: a Drive folder link is a
// capability, not a name (docs/design/signin-photo-wall.html §7). A folder
// shared "anyone with the link" is readable by whoever holds the id, so an
// unredacted export would push the key to the department's photographs into
// the backup repository -- the precise failure this map already anticipated
// for github_token. The folder's typed *label* is not redacted, because a
// label is what it is for: something a person can read that opens nothing.
var exportRedactions = map[string][]string{
	"app_settings": {"github_token", "archive_passphrase", "signin_photos_folder_id"},
}

// backupLockKey is the advisory-lock key the whole backup run holds. Any
// constant works as long as nothing else in this database uses it; this one
// spells "STKBKUP1" loosely enough to be recognisable in pg_locks.
const backupLockKey int64 = 0x53544B42_4B555031

// BackupNow runs a backup on demand from the admin panel. Admin-only, like
// every other admin-panel action.
func (db *DB) BackupNow(ctx context.Context, actor Actor) (BackupResult, error) {
	if err := RequireAdmin(actor); err != nil {
		return BackupResult{}, err
	}
	return db.RunBackup(ctx, BackupSourceManual)
}

// RunBackup is the whole night's work. It is unexported from the API's point
// of view -- BackupNow is the gated entry -- but exported here because the
// scheduler in server/ calls it with no actor at all: nobody is signed in at
// 2 a.m., and inventing a fake admin session to satisfy a check nobody is
// making would be theatre.
func (db *DB) RunBackup(ctx context.Context, source string) (BackupResult, error) {
	baseDir, settings, err := db.backupDir(ctx)
	if err != nil {
		return BackupResult{}, err
	}

	// One run at a time, across processes. Not a lock file with a timeout:
	// too short and it is torn off a slow-but-live backup, so a second process
	// starts writing the same dated folder; too long and a killed process
	// wedges backups until somebody deletes a file they have never heard of.
	// An advisory lock lives with its connection, so it survives a run of any
	// length and a kill -9 drops it the moment the socket closes.
	release, held, err := acquireBackupLock(ctx, db.Pool)
	if err != nil {
		return BackupResult{}, err
	}
	if !held {
		return BackupResult{Skipped: true, Source: source, RanAt: time.Now()}, nil
	}
	defer release()

	res, err := db.runBackupLocked(ctx, baseDir, settings, source)
	db.recordBackupRun(baseDir, res, err)
	return res, err
}

func (db *DB) runBackupLocked(ctx context.Context, baseDir string, settings Settings, source string) (BackupResult, error) {
	ranAt := time.Now()
	day := ranAt.Format("2006-01-02")
	dir := filepath.Join(baseDir, day)

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
	tablesDir := filepath.Join(staging, "tables")
	if err := os.MkdirAll(tablesDir, 0o755); err != nil {
		return BackupResult{}, fmt.Errorf("create backup dir: %w", err)
	}

	res := BackupResult{Dir: dir, RanAt: ranAt, Source: source, Encrypted: settings.Encrypted()}

	// One connection for the whole export: COPY TO STDOUT is a protocol-level
	// operation on a single connection, and holding one is cheaper than
	// borrowing a dozen in a row from a pool of eight.
	conn, err := db.Pool.Acquire(ctx)
	if err != nil {
		return BackupResult{}, fmt.Errorf("backup: %w", err)
	}
	defer conn.Release()

	// Repeatable read pins every query below to the snapshot the first one
	// takes, so the table list, the rows, the inventory and the accounts all
	// describe one instant. A run that read each table at its own moment could
	// export a custody_events row whose asset landed in assets.csv a moment
	// too early to be there, and the reload would fail on the foreign key.
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return BackupResult{}, fmt.Errorf("backup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tables, err := publicTables(ctx, tx)
	if err != nil {
		return BackupResult{}, err
	}

	builder := newArchiveBuilder()
	manifest := Manifest{RanAt: ranAt, Rows: map[string]int64{}, Encrypted: settings.Encrypted()}

	for _, table := range tables {
		file := filepath.Join(tablesDir, table+".csv")
		selectSQL, err := tableExportSQL(ctx, tx, table)
		if err != nil {
			return BackupResult{}, err
		}
		rows, err := copyQueryToFile(ctx, conn.Conn(), selectSQL, file)
		if err != nil {
			return BackupResult{}, fmt.Errorf("export %s: %w", table, err)
		}
		name := archiveTablesDir + table + ".csv"
		if err := builder.addFile(name, file); err != nil {
			return BackupResult{}, err
		}
		res.Tables = append(res.Tables, TableExport{Table: table, File: name, Rows: rows})
		res.Rows += rows
		manifest.Rows[table] = rows
	}

	// Sequence state, which the CSVs cannot carry: a restore that reloads
	// assets.csv but leaves assets_asset_tag_seq at 1 collides on the next
	// insert into a unique column.
	sequences, err := readSequences(ctx, tx)
	if err != nil {
		return BackupResult{}, err
	}
	manifest.Sequences = sequences
	seqCSV, err := sequencesCSV(sequences)
	if err != nil {
		return BackupResult{}, err
	}
	if err := builder.addBytes(archiveSequences, seqCSV); err != nil {
		return BackupResult{}, err
	}

	manifest.SchemaVersion, err = schemaVersion(ctx, tx)
	if err != nil {
		return BackupResult{}, err
	}
	res.SchemaVersion = manifest.SchemaVersion

	// The two readable files. They go in the archive *and* stay beside it in
	// the dated folder, because the whole point of them is that somebody can
	// double-click one without knowing what a zip of CSVs is for.
	inventory, err := buildInventoryCSV(ctx, tx)
	if err != nil {
		return BackupResult{}, err
	}
	accounts, err := buildAccountsCSV(ctx, tx)
	if err != nil {
		return BackupResult{}, err
	}
	if err := builder.addBytes(archiveInventory, inventory); err != nil {
		return BackupResult{}, err
	}
	if err := builder.addBytes(archiveAccounts, accounts); err != nil {
		return BackupResult{}, err
	}
	if err := builder.addBytes(archiveRestoreDoc, restoreDoc); err != nil {
		return BackupResult{}, err
	}

	// The manifest is added last because it carries the digests of everything
	// added before it, and it is the one file not listed in its own Files map:
	// nothing can carry its own hash.
	manifest.Files = builder.files
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return BackupResult{}, fmt.Errorf("write manifest: %w", err)
	}
	if err := builder.addBytes(archiveManifest, manifestJSON); err != nil {
		return BackupResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return BackupResult{}, fmt.Errorf("backup: %w", err)
	}

	archive, err := builder.finish()
	if err != nil {
		return BackupResult{}, err
	}
	if settings.Encrypted() {
		archive, err = encryptArchive(archive, settings.ArchivePassphrase)
		if err != nil {
			return BackupResult{}, err
		}
	}

	// The dated folder holds the archive and the two readable CSVs, and
	// nothing else: the raw tables are inside the zip, where the restore reads
	// them, and a second loose copy on disk would only be a second thing to
	// keep in step.
	if err := os.RemoveAll(tablesDir); err != nil {
		return BackupResult{}, fmt.Errorf("stage backup: %w", err)
	}
	if err := os.WriteFile(filepath.Join(staging, archiveInventory), inventory, 0o644); err != nil {
		return BackupResult{}, fmt.Errorf("write %s: %w", archiveInventory, err)
	}
	if err := os.WriteFile(filepath.Join(staging, archiveAccounts), accounts, 0o644); err != nil {
		return BackupResult{}, fmt.Errorf("write %s: %w", archiveAccounts, err)
	}
	name := archiveName(day, settings.Encrypted())
	if err := writeArchiveFile(filepath.Join(staging, name), archive); err != nil {
		return BackupResult{}, err
	}
	res.Archive = filepath.Join(dir, name)

	if err := replaceBackupDir(staging, dir); err != nil {
		return BackupResult{}, err
	}

	// Everything past this point is best-effort: the restorable copy is on
	// disk, so a failure to prune, mirror or push is reported rather than
	// thrown away by returning an error that hides a successful export.
	res.Pruned = pruneDatedFolders(baseDir, settings.KeepDays, day)
	if photos, err := db.MirrorPhotos(ctx, settings); err != nil {
		res.Photos = &PhotoMirrorResult{Error: err.Error()}
	} else {
		res.Photos = photos
	}
	res.Targets = db.pushToTargets(ctx, settings, dir, manifest)
	return res, nil
}

/* ---------------------------------------------------------------- lock ---- */

// acquireBackupLock takes the cross-process backup lock on a dedicated
// connection and returns the function that gives it back. ok = false means
// another process is mid-backup, which is a skip and not an error.
func acquireBackupLock(ctx context.Context, pool *pgxpool.Pool) (func(), bool, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("backup: %w", err)
	}
	var got bool
	if err := conn.QueryRow(ctx, `select pg_try_advisory_lock($1)`, backupLockKey).Scan(&got); err != nil {
		conn.Release()
		return nil, false, fmt.Errorf("backup lock: %w", err)
	}
	if !got {
		conn.Release()
		return nil, false, nil
	}
	return func() {
		// Best-effort: releasing the connection drops a session-level advisory
		// lock anyway, which is exactly why this design has no way to wedge.
		// A fresh context because the run's may already be cancelled.
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(unlockCtx, `select pg_advisory_unlock($1)`, backupLockKey)
		conn.Release()
	}, true, nil
}

/* -------------------------------------------------------------- export ---- */

// tableExportSQL is the query one table is copied out through: everything,
// except that the columns in exportRedactions come out null.
//
// The column list is read from the catalogue rather than written here, so a
// column added by a later migration is exported without anyone remembering to
// add it -- the same property publicTables gives the table list.
func tableExportSQL(ctx context.Context, q querier, table string) (string, error) {
	name := pgx.Identifier{"public", table}.Sanitize()
	redact := exportRedactions[table]
	if len(redact) == 0 {
		return `select * from ` + name, nil
	}

	rows, err := q.Query(ctx, `
		select column_name from information_schema.columns
		where table_schema = 'public' and table_name = $1
		order by ordinal_position`, table)
	if err != nil {
		return "", fmt.Errorf("list columns of %s: %w", table, err)
	}
	defer rows.Close()

	cols := []string{}
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			return "", fmt.Errorf("list columns of %s: %w", table, err)
		}
		quoted := pgx.Identifier{col}.Sanitize()
		if slicesContains(redact, col) {
			// Typed null, so COPY writes an empty field rather than failing to
			// infer a type for a bare NULL in a union-free select.
			cols = append(cols, `null::text as `+quoted)
		} else {
			cols = append(cols, quoted)
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("list columns of %s: %w", table, err)
	}
	if len(cols) == 0 {
		return "", fmt.Errorf("table %s has no columns", table)
	}
	return `select ` + strings.Join(cols, ", ") + ` from ` + name, nil
}

func slicesContains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// copyQueryToFile streams a query out through COPY. The rows never pass
// through Go's memory as a slice, so the size of the table doesn't matter.
func copyQueryToFile(ctx context.Context, conn *pgx.Conn, selectSQL, file string) (int64, error) {
	f, err := os.Create(file)
	if err != nil {
		return 0, fmt.Errorf("create %s: %w", file, err)
	}
	defer f.Close()

	tag, err := conn.PgConn().CopyTo(ctx, f, `copy (`+selectSQL+`) to stdout with (format csv, header)`)
	if err != nil {
		return 0, err
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

// readSequences captures every public sequence, reading the shape from
// pg_sequences and last_value/is_called from the relation itself.
//
// Neither source alone is enough, and this is the gap that makes a restore
// silently wrong rather than loudly broken: pg_sequences has increment_by and
// cycle but no is_called, and reports a *null* last_value for a sequence that
// has never been read, while the relation holds the real start value and the
// flag that says it has not been handed out yet.
func readSequences(ctx context.Context, q querier) ([]SequenceState, error) {
	rows, err := q.Query(ctx, `
		select sequencename, increment_by, min_value, max_value, cycle
		from pg_sequences where schemaname = 'public'
		order by sequencename`)
	if err != nil {
		return nil, fmt.Errorf("list sequences: %w", err)
	}
	defer rows.Close()

	out := []SequenceState{}
	for rows.Next() {
		var s SequenceState
		if err := rows.Scan(&s.Name, &s.IncrementBy, &s.MinValue, &s.MaxValue, &s.Cycle); err != nil {
			return nil, fmt.Errorf("scan sequence: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list sequences: %w", err)
	}

	for i := range out {
		name := pgx.Identifier{"public", out[i].Name}.Sanitize()
		if err := q.QueryRow(ctx, `select last_value, is_called from `+name).
			Scan(&out[i].LastValue, &out[i].IsCalled); err != nil {
			return nil, fmt.Errorf("read sequence %s: %w", out[i].Name, err)
		}
	}
	return out, nil
}

func sequencesCSV(seqs []SequenceState) ([]byte, error) {
	var buf strings.Builder
	w := csv.NewWriter(&buf)
	if err := w.Write(sequenceCSVHeader); err != nil {
		return nil, fmt.Errorf("write sequences.csv: %w", err)
	}
	for _, s := range seqs {
		if err := w.Write(s.csvRecord()); err != nil {
			return nil, fmt.Errorf("write sequences.csv: %w", err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("write sequences.csv: %w", err)
	}
	return []byte(buf.String()), nil
}

// schemaVersion is the last applied migration. It lives outside the public
// schema, so the table export never picks it up and the restore would have
// nothing to compare against.
//
// A database with no supabase_migrations table at all -- a plain Postgres, a
// test fixture -- reports an empty version rather than failing: the point of
// the field is to catch a restore into the *wrong* schema, and "unknown" is
// not wrong, it is unknown.
//
// The table's absence is established with to_regclass *before* the table is
// named in a query, not by catching the error afterwards. The export runs
// inside a transaction, and in Postgres a failed statement aborts the whole
// transaction: querying a missing table and then swallowing the error left
// buildInventoryCSV, buildAccountsCSV and Commit to fail with "current
// transaction is aborted", which is the opposite of the tolerance this
// function is trying to provide. to_regclass answers with null instead of
// raising, so nothing is poisoned.
func schemaVersion(ctx context.Context, q querier) (string, error) {
	var present bool
	if err := q.QueryRow(ctx, `
		select to_regclass('supabase_migrations.schema_migrations') is not null`).Scan(&present); err != nil {
		return "", fmt.Errorf("read schema version: %w", err)
	}
	if !present {
		return "", nil
	}
	var version *string
	if err := q.QueryRow(ctx, `
		select max(version) from supabase_migrations.schema_migrations`).Scan(&version); err != nil {
		return "", fmt.Errorf("read schema version: %w", err)
	}
	return deref(version), nil
}

/* ------------------------------------------------------ readable files ---- */

var inventoryCSVHeader = []string{
	"name", "serial_number", "asset_tag", "type", "category", "model",
	"status", "condition", "held_by", "student_number",
	"checked_out_at", "due_at", "overdue", "photo_path",
}

// buildInventoryCSV is the human-readable half of a backup: one row per item,
// the category path spelled out, and who has it right now.
//
// It reuses loadCategoryTree and holdersByAsset rather than writing a
// recursive CTE and a second definition of "out". Two definitions of who holds
// an item is exactly the drift CLAUDE.md §13 spent a phase removing.
func buildInventoryCSV(ctx context.Context, q querier) ([]byte, error) {
	tree, err := loadCategoryTree(ctx, q)
	if err != nil {
		return nil, err
	}

	rows, err := q.Query(ctx, `select `+assetColumns+` from assets a order by a.name, a.asset_tag`)
	if err != nil {
		return nil, fmt.Errorf("read inventory: %w", err)
	}
	defer rows.Close()

	assets := []Asset{}
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		assets = append(assets, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read inventory: %w", err)
	}

	ids := make([]string, 0, len(assets))
	for _, a := range assets {
		ids = append(ids, a.ID)
	}
	// The export is an admin-level read by definition -- inventory.csv carries
	// student numbers because the file is a backup of the database, not a
	// browse screen -- so the viewer is the local process itself.
	held, err := holdersByAsset(ctx, q, ids, LocalCLIActor())
	if err != nil {
		return nil, err
	}

	var buf strings.Builder
	w := csv.NewWriter(&buf)
	if err := w.Write(inventoryCSVHeader); err != nil {
		return nil, fmt.Errorf("write inventory.csv: %w", err)
	}
	for _, a := range assets {
		path := tree.pathOf(a.CategoryID)
		record := []string{
			a.Name,
			deref(a.SerialNumber),
			a.AssetTag,
			pathLevel(path, 0),
			pathLevel(path, 1),
			pathLevel(path, 2),
			string(a.Status),
			deref(a.Condition),
			"", "", "", "", "",
			deref(a.PhotoPath),
		}
		if c := held[a.ID]; c != nil {
			record[8] = c.CustodianName
			record[9] = deref(c.StudentNumber)
			record[10] = c.CheckedOutAt.Format(time.RFC3339)
			if c.DueAt != nil {
				record[11] = c.DueAt.Format(time.RFC3339)
			}
			record[12] = fmt.Sprint(c.Overdue)
		}
		if err := w.Write(record); err != nil {
			return nil, fmt.Errorf("write inventory.csv: %w", err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("write inventory.csv: %w", err)
	}
	return []byte(buf.String()), nil
}

// pathLevel is one level of a category path, blank when the branch stops short
// -- a unit filed under a Type with no Model below it is a shape the tree
// allows (docs/adr/0001), so the column is empty rather than the row missing.
func pathLevel(path []CategoryRef, i int) string {
	if i < len(path) {
		return path[i].Name
	}
	return ""
}

var accountsCSVHeader = []string{
	"student_number", "first_name", "last_name", "full_name", "email",
	"is_admin", "password_hash", "has_password", "created_at", "photo_path",
}

// buildAccountsCSV writes one row per account, password hash included.
//
// The hash is in here because restoring it is what makes a restore invisible
// to the user: their existing password keeps working. Plaintext is not
// recoverable anywhere in this system -- bcrypt is one-way and the server
// never stores what was typed -- so this is the most the file *can* carry.
// Dropping it was considered and rejected: the student number sitting in the
// next column is itself a working password-free login (CLAUDE.md §7), so
// omitting the hash would protect the weaker credential while leaving the file
// just as sensitive and making every restore worse. See §C.5 for the opt-in
// encryption that is the real answer for a site that needs one.
func buildAccountsCSV(ctx context.Context, q querier) ([]byte, error) {
	rows, err := q.Query(ctx, `
		select student_number, first_name, last_name, full_name, email,
		       is_admin, password_hash, created_at, photo_path
		from profiles
		order by student_number nulls last, created_at`)
	if err != nil {
		return nil, fmt.Errorf("read accounts: %w", err)
	}
	defer rows.Close()

	var buf strings.Builder
	w := csv.NewWriter(&buf)
	if err := w.Write(accountsCSVHeader); err != nil {
		return nil, fmt.Errorf("write accounts.csv: %w", err)
	}
	for rows.Next() {
		var sn, first, last, full, email, hash, photo *string
		var isAdmin bool
		var created time.Time
		if err := rows.Scan(&sn, &first, &last, &full, &email, &isAdmin, &hash, &created, &photo); err != nil {
			return nil, fmt.Errorf("scan account: %w", err)
		}
		record := []string{
			deref(sn), deref(first), deref(last), deref(full), deref(email),
			fmt.Sprint(isAdmin), deref(hash), fmt.Sprint(deref(hash) != ""),
			created.Format(time.RFC3339), deref(photo),
		}
		if err := w.Write(record); err != nil {
			return nil, fmt.Errorf("write accounts.csv: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read accounts: %w", err)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("write accounts.csv: %w", err)
	}
	return []byte(buf.String()), nil
}

/* ------------------------------------------------------------ on disk ---- */

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

// datedFolder matches the folders a run writes, and nothing else. Pruning
// walks a directory an admin chose, which may hold anything, so what gets
// deleted is matched rather than assumed.
var datedFolder = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// pruneDatedFolders removes local dated folders older than keepDays, and
// returns what it removed.
//
// Local folders are pruned automatically where photo generations are not
// (§E.2), and the difference is whether a second copy exists: a dated database
// folder is redundant with whatever was pushed off-site, and a photo
// generation is the only copy of a photo somebody may have deleted.
//
// today is never pruned, whatever the arithmetic says, so a keep_days of 1 on
// a clock that has just crossed midnight cannot delete the run in progress.
func pruneDatedFolders(baseDir string, keepDays int, today string) []string {
	if keepDays < 1 {
		return nil
	}
	cutoff := time.Now().AddDate(0, 0, -keepDays)
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil
	}
	pruned := []string{}
	for _, e := range entries {
		if !e.IsDir() || !datedFolder.MatchString(e.Name()) || e.Name() == today {
			continue
		}
		day, err := time.ParseInLocation("2006-01-02", e.Name(), time.Local)
		if err != nil || !day.Before(cutoff) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(baseDir, e.Name())); err == nil {
			pruned = append(pruned, e.Name())
		}
	}
	sort.Strings(pruned)
	return pruned
}

// ListLocalBackups returns the dated archives still on disk, newest first.
// The restore-by-date picker uses it for the local target, and the backup
// screen shows it so an admin can see what is actually there rather than
// trusting that a nightly job has been running.
func (db *DB) ListLocalBackups(ctx context.Context, actor Actor) ([]BackupVersion, error) {
	if err := RequireAdmin(actor); err != nil {
		return nil, err
	}
	baseDir, _, err := db.backupDir(ctx)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []BackupVersion{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", baseDir, err)
	}
	out := []BackupVersion{}
	for _, e := range entries {
		if !e.IsDir() || !datedFolder.MatchString(e.Name()) {
			continue
		}
		dir := filepath.Join(baseDir, e.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if !strings.HasPrefix(f.Name(), "backup-") {
				continue
			}
			info, err := f.Info()
			if err != nil {
				continue
			}
			out = append(out, BackupVersion{
				ID:    filepath.Join(e.Name(), f.Name()),
				Label: e.Name(),
				At:    info.ModTime(),
				Bytes: info.Size(),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out, nil
}

// openLocalBackup opens an archive in the local backup folder by the id
// ListLocalBackups handed out (<date>/<file>).
//
// The id round-trips through the client, so it is validated rather than
// trusted: two path segments, both of a shape this code writes, and nothing
// that could climb out of the backup directory.
func (db *DB) openLocalBackup(ctx context.Context, id string) (io.ReadCloser, error) {
	baseDir, _, err := db.backupDir(ctx)
	if err != nil {
		return nil, err
	}
	day, file, ok := strings.Cut(filepath.ToSlash(id), "/")
	if !ok || !datedFolder.MatchString(day) || !strings.HasPrefix(file, "backup-") ||
		strings.ContainsAny(file, `/\`) || strings.Contains(file, "..") {
		return nil, fmt.Errorf("%w: %q is not a backup in this folder", ErrInvalid, id)
	}
	f, err := os.Open(filepath.Join(baseDir, day, file))
	if err != nil {
		return nil, fmt.Errorf("%w: there is no backup at %s", ErrNotFound, id)
	}
	return f, nil
}
