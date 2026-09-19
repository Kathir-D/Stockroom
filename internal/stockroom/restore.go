package stockroom

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// The restore (docs/design/backup.md §E.5).
//
// One implementation, four entry points: an upload in the admin panel, a date
// picked from Drive, a date picked from GitHub, and cmd/restore for when the
// database has no accounts to sign in with. A second, emergency-only restore
// would be code first exercised during an emergency, which is the one time
// nobody wants to find out whether it works.
//
// The shape of the transaction is the whole design, and each step is load
// bearing:
//
//   - `set local session_replication_role = replica` makes a CSV reload work
//     at all. Foreign keys stop being checked, so the alphabetical file order
//     stops mattering (assets.csv sorts before the categories.csv it
//     references), and trg_asset_status_log stays quiet, so activity_log
//     restores clean instead of gaining a junk row per asset.
//   - That is also why the restore has to validate before it commits: with FK
//     enforcement off, nothing on the way in rejects an assets row pointing at
//     a category the archive does not contain. Unchecked, replica mode turns a
//     clean failure into a permanently inconsistent database, which is worse
//     than the ordering problem it solves.
//   - Sequences are written *after* every check, because setval is not
//     transactional. Verified against the live stack: a setval inside an
//     explicit rollback survives it. Written first, a validation failure would
//     roll the tables back while leaving every sequence advanced -- a database
//     that looks untouched and hands out colliding keys.

// maxRestoreBytes caps an uploaded archive. A backup of this database is a few
// hundred KB; 200 MB is far past any plausible growth and well short of
// letting a bad request exhaust memory.
const maxRestoreBytes = 200 << 20

// restoreConfirmation is the word the request has to carry. Restoring
// overwrites every table, so it is the one destructive action in the app that
// asks for something to be typed.
const restoreConfirmation = "RESTORE"

// RestoreOptions is everything the caller decides about a restore.
type RestoreOptions struct {
	// Confirm must equal restoreConfirmation.
	Confirm string
	// Force skips the schema-version check. An admin who knows the archive
	// predates a migration can still load it; nobody does it by accident.
	Force bool
	// Passphrase decrypts an encrypted archive. Empty falls back to the one in
	// settings, which is the common case: the same machine wrote it.
	Passphrase string
	// Source is recorded in the log: "upload", "drive", "github", "cli".
	Source string
}

// RestoreResult is what a restore did, and it is deliberately detailed: "who
// restored the database, from what, and how many rows landed" is the first
// thing anybody asks afterwards.
type RestoreResult struct {
	RanAt  time.Time `json:"ran_at"`
	Source string    `json:"source"`
	By     string    `json:"by"`
	// ByName is who that is, in words. By is an account id, which is the
	// right thing in a log line and the wrong thing on a screen: the panel
	// would otherwise tell the admin who just pressed Restore that it was
	// performed by 00000000-0000-0000-0000-000000000020. Resolved *before*
	// the tables are replaced, because afterwards the row it comes from may
	// belong to somebody else or not exist at all.
	ByName        string        `json:"by_name"`
	SchemaVersion string        `json:"schema_version"`
	ArchiveRanAt  time.Time     `json:"archive_ran_at"`
	Tables        []TableExport `json:"tables"`
	Rows          int64         `json:"rows"`
	Sequences     int           `json:"sequences"`
	// Warnings names what could not be checked rather than pretending it was.
	Warnings []string `json:"warnings"`
}

// RestoreFromReader reads an archive with a cap and restores it.
func (db *DB) RestoreFromReader(ctx context.Context, actor Actor, r io.Reader, opts RestoreOptions) (RestoreResult, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxRestoreBytes+1))
	if err != nil {
		return RestoreResult{}, fmt.Errorf("read the uploaded archive: %w", err)
	}
	if len(data) > maxRestoreBytes {
		return RestoreResult{}, fmt.Errorf("%w: that file is larger than %d MB, which is not a Stockroom backup", ErrInvalid, maxRestoreBytes>>20)
	}
	return db.RestoreFromZip(ctx, actor, data, opts)
}

// RestoreFromZip is the one restore. Every entry point ends up here.
func (db *DB) RestoreFromZip(ctx context.Context, actor Actor, data []byte, opts RestoreOptions) (RestoreResult, error) {
	if err := RequireAdmin(actor); err != nil {
		return RestoreResult{}, err
	}
	if opts.Confirm != restoreConfirmation {
		return RestoreResult{}, fmt.Errorf("%w: restoring replaces every record in the database. Type %s to confirm", ErrInvalid, restoreConfirmation)
	}

	settings, err := db.loadSettings(ctx)
	if err != nil {
		return RestoreResult{}, err
	}
	passphrase := opts.Passphrase
	if passphrase == "" {
		passphrase = settings.ArchivePassphrase
	}

	// Checksums and the manifest first, before anything is locked or touched.
	archive, err := readArchive(data, passphrase)
	if err != nil {
		return RestoreResult{}, err
	}

	live, err := schemaVersion(ctx, db.Pool)
	if err != nil {
		return RestoreResult{}, err
	}
	// An override is not a clean restore, and the report is the only place
	// anybody will ever see that it happened: the refusal below is a 409 the
	// admin actively dismissed by pressing again with force, and without this
	// the result reads exactly like a restore that needed no override at all.
	var forced []string
	if archive.manifest.SchemaVersion != "" && live != "" && archive.manifest.SchemaVersion != live {
		if !opts.Force {
			return RestoreResult{}, fmt.Errorf("%w: this backup was taken on database version %s and this database is on %s. The columns may no longer match. Restore it anyway only if you know the difference is safe",
				ErrConflict, archive.manifest.SchemaVersion, live)
		}
		forced = append(forced, fmt.Sprintf(
			"Restored across a database version change: the backup was taken on %s and this database is on %s. Check anything a migration added since.",
			archive.manifest.SchemaVersion, live))
	}

	// The same lock a backup takes, so a restore and a backup can never
	// interleave -- a backup reading a half-loaded database would produce an
	// archive that passes every check and describes nothing that ever existed.
	release, held, err := acquireBackupLock(ctx, db.Pool)
	if err != nil {
		return RestoreResult{}, err
	}
	if !held {
		return RestoreResult{}, fmt.Errorf("%w: a backup is running right now. Wait for it to finish and try again", ErrConflict)
	}
	defer release()

	res, err := db.restoreLocked(ctx, actor, archive, opts)
	res.Warnings = append(forced, res.Warnings...)
	if err != nil {
		log.Printf("restore FAILED (source=%s by=%s): %v", opts.Source, actorLabel(actor), err)
		return res, err
	}
	log.Printf("restore ok (source=%s by=%s): %d rows across %d tables from a backup taken %s",
		opts.Source, actorLabel(actor), res.Rows, len(res.Tables), res.ArchiveRanAt.Format(time.RFC3339))
	return res, nil
}

// actorLabel is what the log records about who did this: "cli" for the local
// command, the account id otherwise. The distinction is the whole reason
// trustedCLI exists as a field rather than just an is-admin flag.
func actorLabel(a Actor) string {
	if a.trustedCLI {
		return "cli"
	}
	return a.ID
}

// actorName resolves the actor to something worth showing a person. It never
// fails the restore: a name is a courtesy on a report, and falling back to the
// id costs nothing anybody needs.
func (db *DB) actorName(ctx context.Context, a Actor) string {
	if a.trustedCLI {
		return "the command line"
	}
	var name *string
	err := db.Pool.QueryRow(ctx, `
		select coalesce(nullif(trim(coalesce(first_name,'') || ' ' || coalesce(last_name,'')), ''), full_name)
		from profiles where id = $1`, a.ID).Scan(&name)
	if err != nil || strings.TrimSpace(deref(name)) == "" {
		return a.ID
	}
	return strings.TrimSpace(deref(name))
}

func (db *DB) restoreLocked(ctx context.Context, actor Actor, archive *openArchive, opts RestoreOptions) (RestoreResult, error) {
	res := RestoreResult{
		RanAt:         time.Now(),
		Source:        opts.Source,
		By:            actorLabel(actor),
		ByName:        db.actorName(ctx, actor),
		SchemaVersion: archive.manifest.SchemaVersion,
		ArchiveRanAt:  archive.manifest.RanAt,
		Warnings:      []string{},
	}

	liveTables, err := publicTables(ctx, db.Pool)
	if err != nil {
		return res, err
	}
	archiveTables := archive.tables()
	if len(archiveTables) == 0 {
		return res, fmt.Errorf("%w: the archive holds no tables", ErrInvalid)
	}
	liveSet := map[string]bool{}
	for _, t := range liveTables {
		liveSet[t] = true
	}
	for _, t := range archiveTables {
		if !liveSet[t] {
			return res, fmt.Errorf("%w: the archive holds a table this database does not have (%s). Apply the migrations first", ErrConflict, t)
		}
	}
	// A live table the archive does not carry would be *emptied* by the
	// truncate and never refilled, which is data loss disguised as a restore.
	// Refuse rather than silently wipe it.
	archiveSet := map[string]bool{}
	for _, t := range archiveTables {
		archiveSet[t] = true
	}
	for _, t := range liveTables {
		if archiveSet[t] {
			continue
		}
		if !opts.Force {
			return res, fmt.Errorf("%w: this database has a table the backup does not (%s), so restoring would empty it. Restore anyway only if that is what you want", ErrConflict, t)
		}
		// Forced past it: the table was emptied and nothing refilled it, which
		// is the one outcome of a restore that loses data. It has to be on the
		// report.
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"%s was emptied: this database has that table and the backup does not.", t))
	}

	// Secrets the export redacted are not in the archive, so they have to be
	// carried across rather than loaded: restoring would otherwise null the
	// GitHub token and silently stop the backups the restore was meant to
	// protect.
	secrets, err := db.captureSecrets(ctx)
	if err != nil {
		return res, err
	}

	// Every sequence as it stands now, so a failed commit can put them back:
	// setval is not transactional, and this is the compensation for the one
	// window the transaction cannot cover.
	before, err := readSequences(ctx, db.Pool)
	if err != nil {
		return res, err
	}

	conn, err := db.Pool.Acquire(ctx)
	if err != nil {
		return res, fmt.Errorf("restore: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return res, fmt.Errorf("restore: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	if _, err := tx.Exec(ctx, `set local session_replication_role = replica`); err != nil {
		return res, fmt.Errorf("restore: %w", err)
	}

	// One truncate for every table, so the FK graph never has to be ordered.
	names := make([]string, 0, len(archiveTables))
	for _, t := range archiveTables {
		names = append(names, pgx.Identifier{"public", t}.Sanitize())
	}
	if _, err := tx.Exec(ctx, `truncate table `+strings.Join(names, ", ")+` cascade`); err != nil {
		return res, fmt.Errorf("restore: clear the existing rows: %w", err)
	}

	for _, table := range archiveTables {
		content, err := archive.read(archiveTablesDir + table + ".csv")
		if err != nil {
			return res, fmt.Errorf("%w: the archive is missing %s", ErrInvalid, table+".csv")
		}
		rows, err := copyCSVIntoTable(ctx, conn.Conn(), table, content)
		if err != nil {
			return res, err
		}
		res.Tables = append(res.Tables, TableExport{Table: table, File: archiveTablesDir + table + ".csv", Rows: rows})
		res.Rows += rows
	}

	if err := db.restoreSecrets(ctx, tx, secrets); err != nil {
		return res, err
	}

	// ---- the three checks, before anything irreversible -------------------

	if err := checkRowCounts(res.Tables, archive.manifest); err != nil {
		return res, err
	}
	if err := checkForeignKeys(ctx, tx); err != nil {
		return res, err
	}
	warnings, err := checkSequences(ctx, tx, archive.manifest.Sequences)
	if err != nil {
		return res, err
	}
	res.Warnings = append(res.Warnings, warnings...)

	// ---- only now, the one non-transactional write ------------------------

	applied, err := applySequences(ctx, tx, archive.manifest.Sequences)
	if err != nil {
		return res, err
	}
	res.Sequences = applied

	if err := tx.Commit(ctx); err != nil {
		// The tables roll back on their own; the sequences do not, so put
		// them back by hand. That compensation is itself non-transactional,
		// so a failure to compensate is named in the error rather than
		// swallowed: a restore that reports failure while leaving sequences in
		// a third state is worse than one that says what happened.
		if cerr := compensateSequences(ctx, db.Pool, before); cerr != nil {
			log.Printf("restore: could not put the sequences back after a failed commit: %v", cerr)
			return res, fmt.Errorf("restore failed and the sequences could not be put back (%v): %w", cerr, err)
		}
		return res, fmt.Errorf("restore: %w", err)
	}
	committed = true

	// Every live token now points at a profile row that may no longer exist,
	// or at an account whose admin flag just changed underneath it.
	db.Sessions.Clear()
	invalidateBackupWarning()
	return res, nil
}

/* ------------------------------------------------------------- loading ---- */

// copyCSVIntoTable loads one file with COPY FROM STDIN.
//
// The column list comes from the CSV's own header rather than from the live
// table, so a backup taken before a column was added still loads: the missing
// column takes its default. A column in the file that the table does not have
// is a real mismatch and is named.
func copyCSVIntoTable(ctx context.Context, conn *pgx.Conn, table string, content []byte) (int64, error) {
	header, err := csv.NewReader(bytes.NewReader(content)).Read()
	if err != nil {
		return 0, fmt.Errorf("%w: %s.csv has no header row", ErrInvalid, table)
	}
	cols := make([]string, 0, len(header))
	for _, c := range header {
		cols = append(cols, pgx.Identifier{c}.Sanitize())
	}
	sql := fmt.Sprintf(`copy %s (%s) from stdin with (format csv, header)`,
		pgx.Identifier{"public", table}.Sanitize(), strings.Join(cols, ", "))

	tag, err := conn.PgConn().CopyFrom(ctx, bytes.NewReader(content), sql)
	if err != nil {
		return 0, fmt.Errorf("load %s: %w", table, mapPgError("load "+table, err))
	}
	return tag.RowsAffected(), nil
}

// captureSecrets reads the columns the export redacts, so the restore can put
// them back rather than loading the archive's nulls over a working token.
func (db *DB) captureSecrets(ctx context.Context) (map[string]map[string]*string, error) {
	out := map[string]map[string]*string{}
	for table, cols := range exportRedactions {
		quoted := make([]string, 0, len(cols))
		for _, c := range cols {
			quoted = append(quoted, pgx.Identifier{c}.Sanitize())
		}
		row := db.Pool.QueryRow(ctx, `select `+strings.Join(quoted, ", ")+
			` from `+pgx.Identifier{"public", table}.Sanitize()+` limit 1`)
		values := make([]*string, len(cols))
		targets := make([]any, len(cols))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := row.Scan(targets...); err != nil {
			// No row to carry across (an empty table, a fresh database) is
			// normal, not a failure.
			continue
		}
		kept := map[string]*string{}
		for i, c := range cols {
			kept[c] = values[i]
		}
		out[table] = kept
	}
	return out, nil
}

// restoreSecrets writes the captured values back over the archive's nulls.
// Only where the archive had nothing: an archive that somehow carries a value
// is not second-guessed.
func (db *DB) restoreSecrets(ctx context.Context, tx pgx.Tx, secrets map[string]map[string]*string) error {
	for table, cols := range secrets {
		sets := []string{}
		args := []any{}
		for col, value := range cols {
			if value == nil {
				continue
			}
			args = append(args, *value)
			sets = append(sets, fmt.Sprintf("%s = coalesce(%s, $%d)",
				pgx.Identifier{col}.Sanitize(), pgx.Identifier{col}.Sanitize(), len(args)))
		}
		if len(sets) == 0 {
			continue
		}
		if _, err := tx.Exec(ctx, `update `+pgx.Identifier{"public", table}.Sanitize()+
			` set `+strings.Join(sets, ", "), args...); err != nil {
			return fmt.Errorf("restore: keep the existing %s secrets: %w", table, err)
		}
	}
	return nil
}

/* -------------------------------------------------------------- checks ---- */

func checkRowCounts(loaded []TableExport, m Manifest) error {
	for _, t := range loaded {
		want, ok := m.Rows[t.Table]
		if !ok {
			continue
		}
		if t.Rows != want {
			return fmt.Errorf("%w: %s loaded %d rows but the backup recorded %d. Nothing was changed",
				ErrInvalid, t.Table, t.Rows, want)
		}
	}
	return nil
}

// foreignKey is one constraint, read from the catalogue rather than listed in
// Go, so a foreign key added by a later migration is checked without anybody
// remembering to add it here.
type foreignKey struct {
	Name       string
	Child      string
	Parent     string
	ChildCols  []string
	ParentCols []string
}

// checkForeignKeys re-enforces referential integrity inside the transaction.
//
// session_replication_role is reset to origin first, which is what makes this
// meaningful: replica mode is the reason nothing rejected a bad reference on
// the way in. One anti-join per constraint, with MATCH SIMPLE semantics -- a
// row with any null in the key satisfies the constraint, which is why the
// query only looks at rows where every key column is present.
func checkForeignKeys(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `set local session_replication_role = origin`); err != nil {
		return fmt.Errorf("restore: %w", err)
	}

	rows, err := tx.Query(ctx, `
		select con.conname, child.relname, parent.relname,
		       (select array_agg(a.attname order by k.ord)
		          from unnest(con.conkey) with ordinality k(attnum, ord)
		          join pg_attribute a on a.attrelid = con.conrelid and a.attnum = k.attnum),
		       (select array_agg(a.attname order by k.ord)
		          from unnest(con.confkey) with ordinality k(attnum, ord)
		          join pg_attribute a on a.attrelid = con.confrelid and a.attnum = k.attnum)
		from pg_constraint con
		join pg_class child on child.oid = con.conrelid
		join pg_class parent on parent.oid = con.confrelid
		join pg_namespace n on n.oid = child.relnamespace
		where con.contype = 'f' and n.nspname = 'public'
		order by con.conname`)
	if err != nil {
		return fmt.Errorf("restore: list foreign keys: %w", err)
	}
	defer rows.Close()

	keys := []foreignKey{}
	for rows.Next() {
		var k foreignKey
		if err := rows.Scan(&k.Name, &k.Child, &k.Parent, &k.ChildCols, &k.ParentCols); err != nil {
			return fmt.Errorf("restore: list foreign keys: %w", err)
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("restore: list foreign keys: %w", err)
	}

	for _, k := range keys {
		if len(k.ChildCols) == 0 || len(k.ChildCols) != len(k.ParentCols) {
			continue
		}
		notNull := make([]string, 0, len(k.ChildCols))
		match := make([]string, 0, len(k.ChildCols))
		for i, col := range k.ChildCols {
			c := pgx.Identifier{col}.Sanitize()
			p := pgx.Identifier{k.ParentCols[i]}.Sanitize()
			notNull = append(notNull, "c."+c+" is not null")
			match = append(match, "p."+p+" = c."+c)
		}
		var orphans int64
		err := tx.QueryRow(ctx, fmt.Sprintf(
			`select count(*) from %s c where %s and not exists (select 1 from %s p where %s)`,
			pgx.Identifier{"public", k.Child}.Sanitize(), strings.Join(notNull, " and "),
			pgx.Identifier{"public", k.Parent}.Sanitize(), strings.Join(match, " and ")),
		).Scan(&orphans)
		if err != nil {
			return fmt.Errorf("restore: check %s: %w", k.Name, err)
		}
		if orphans > 0 {
			return fmt.Errorf("%w: the backup is inconsistent -- %d row(s) in %s point at a %s that is not in the backup (%s). Nothing was changed",
				ErrInvalid, orphans, k.Child, k.Parent, k.Name)
		}
	}
	return nil
}

// sequenceOwner is the column a sequence hands values to, which is what its
// next value has to stay ahead of.
type sequenceOwner struct {
	Table  string
	Column string
	Kind   string // the column's type category: "int", "text", or "" for anything else
}

// checkSequences proves that every sequence in the archive will hand out a
// value the restored table does not already hold.
//
// It reads the archive's numbers rather than the database's, because nothing
// has been written yet -- that is the point of the ordering. The invariant is
// on the *effective* next value:
//
//	next = is_called ? last_value + increment_by : last_value
//
// and for an ascending sequence it must be strictly greater than the maximum
// already in the owning column, or the next insert collides on a unique index.
// Two details a naive `last_value + 1` gets wrong: is_called decides whether
// to add anything at all, and the step is increment_by rather than 1.
//
// A descending or cycling sequence is refused outright. Nothing in this schema
// is either, so writing two more branches would mean shipping code that is
// never exercised -- and a check that quietly passes on a sequence it was
// never designed for is the exact bug this step exists to prevent.
func checkSequences(ctx context.Context, tx pgx.Tx, seqs []SequenceState) ([]string, error) {
	warnings := []string{}
	owners, err := sequenceOwners(ctx, tx)
	if err != nil {
		return nil, err
	}

	for _, s := range seqs {
		if s.Cycle {
			return nil, fmt.Errorf("%w: the sequence %s cycles, and this restore cannot reason about a cycling sequence. Refusing rather than skipping the check", ErrInvalid, s.Name)
		}
		if s.IncrementBy < 0 {
			return nil, fmt.Errorf("%w: the sequence %s counts downwards, and this restore cannot reason about a descending sequence. Refusing rather than skipping the check", ErrInvalid, s.Name)
		}
		next := s.LastValue
		if s.IsCalled {
			next += s.IncrementBy
		}

		owner, ok := owners[s.Name]
		if !ok {
			// A sequence nothing draws from constrains nothing. That is a
			// pass, and saying so here is what keeps it from being confused
			// with a check that was skipped.
			continue
		}
		expr, ok := sequenceMaxExpr(owner)
		if !ok {
			warnings = append(warnings, fmt.Sprintf(
				"could not check that %s stays ahead of %s.%s: the column is not a kind this restore knows how to compare. The rows were restored; watch for duplicate-key errors on the next insert",
				s.Name, owner.Table, owner.Column))
			continue
		}

		var max *int64
		err := tx.QueryRow(ctx, `select `+expr+` from `+pgx.Identifier{"public", owner.Table}.Sanitize()).Scan(&max)
		if err != nil {
			return nil, fmt.Errorf("restore: check the sequence %s: %w", s.Name, err)
		}
		// An empty table has no maximum, so it constrains nothing. A pass,
		// not a skip.
		if max == nil {
			continue
		}
		if next <= *max {
			return nil, fmt.Errorf("%w: the backup's %s would next hand out %d, but %s.%s already holds %d. Restoring would produce duplicate keys, so nothing was changed",
				ErrInvalid, s.Name, next, owner.Table, owner.Column, *max)
		}
	}
	return warnings, nil
}

// sequenceMaxExpr is how to read "the largest number already handed out" from
// the owning column.
//
// An integer column is the plain max. A text column is the one shape this
// schema actually has -- assets.asset_tag is 'AST-000089', generated from
// assets_asset_tag_seq -- so the trailing digits are what the sequence's value
// has to stay ahead of, exactly as the migration that created it computed its
// starting point. Anything else returns false and is reported rather than
// quietly passed.
func sequenceMaxExpr(o sequenceOwner) (string, bool) {
	col := pgx.Identifier{o.Column}.Sanitize()
	switch o.Kind {
	case "int":
		return "max(" + col + ")::bigint", true
	case "text":
		return "max(substring(" + col + " from '[0-9]+$')::bigint)", true
	}
	return "", false
}

// sequenceOwners maps each sequence to the column it feeds.
//
// Two shapes, because Postgres records them differently: a serial or identity
// column owns its sequence directly (pg_depend from the sequence to the
// column), while a sequence named in a column *default* -- which is how
// assets.asset_tag is generated -- is recorded as the default expression
// depending on the sequence. Reading only the first would miss the one
// sequence this schema actually has.
func sequenceOwners(ctx context.Context, q querier) (map[string]sequenceOwner, error) {
	rows, err := q.Query(ctx, `
		with owned as (
			select s.relname as seq, t.relname as tbl, a.attname as col
			from pg_depend d
			join pg_class s on s.oid = d.objid and s.relkind = 'S'
			join pg_class t on t.oid = d.refobjid
			join pg_attribute a on a.attrelid = t.oid and a.attnum = d.refobjsubid
			where d.classid = 'pg_class'::regclass and d.refclassid = 'pg_class'::regclass
			  and d.deptype in ('a','i') and s.relnamespace = 'public'::regnamespace
		), defaulted as (
			select s.relname as seq, t.relname as tbl, a.attname as col
			from pg_depend d
			join pg_class s on s.oid = d.refobjid and s.relkind = 'S'
			join pg_attrdef ad on ad.oid = d.objid
			join pg_class t on t.oid = ad.adrelid
			join pg_attribute a on a.attrelid = ad.adrelid and a.attnum = ad.adnum
			where d.classid = 'pg_attrdef'::regclass and s.relnamespace = 'public'::regnamespace
		), owners as (select * from owned union select * from defaulted)
		select owners.seq, owners.tbl, owners.col,
		       case
		         when at.typcategory = 'N' and at.typname in ('int2','int4','int8') then 'int'
		         when at.typcategory = 'S' then 'text'
		         else ''
		       end
		from owners
		join pg_class t on t.relname = owners.tbl and t.relnamespace = 'public'::regnamespace
		join pg_attribute a on a.attrelid = t.oid and a.attname = owners.col
		join pg_type at on at.oid = a.atttypid`)
	if err != nil {
		return nil, fmt.Errorf("restore: read sequence owners: %w", err)
	}
	defer rows.Close()

	out := map[string]sequenceOwner{}
	for rows.Next() {
		var seq string
		var o sequenceOwner
		if err := rows.Scan(&seq, &o.Table, &o.Column, &o.Kind); err != nil {
			return nil, fmt.Errorf("restore: read sequence owners: %w", err)
		}
		out[seq] = o
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("restore: read sequence owners: %w", err)
	}
	return out, nil
}

// applySequences replays each captured sequence, is_called and all.
//
// Replaying the flag rather than defaulting it is the difference between a
// never-read sequence handing out its start value and skipping it: setval's
// default is is_called = true, so setval('s', 5) on a fresh `start 5` sequence
// makes nextval return 6.
//
// A sequence in the database but not in the archive is left alone rather than
// guessed at -- it was created by a migration applied after the backup, and
// its declared start is a better answer than any number this code could invent.
func applySequences(ctx context.Context, tx pgx.Tx, seqs []SequenceState) (int, error) {
	applied := 0
	for _, s := range seqs {
		if _, err := tx.Exec(ctx, `select setval($1, $2, $3)`,
			"public."+s.Name, s.LastValue, s.IsCalled); err != nil {
			return applied, fmt.Errorf("restore: set the sequence %s: %w", s.Name, err)
		}
		applied++
	}
	return applied, nil
}

// compensateSequences puts the sequences back after a failed commit. Best
// effort by nature -- it is non-transactional too -- so its own failure is
// reported rather than hidden.
func compensateSequences(ctx context.Context, q querier, before []SequenceState) error {
	var failed []string
	for _, s := range before {
		if _, err := q.Exec(ctx, `select setval($1, $2, $3)`, "public."+s.Name, s.LastValue, s.IsCalled); err != nil {
			failed = append(failed, s.Name)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("could not reset %s", strings.Join(failed, ", "))
	}
	return nil
}

/* --------------------------------------------------------- from remote ---- */

// RestoreFromTarget downloads one past backup and restores it. Upload, Drive
// and GitHub all end at the same RestoreFromZip; only the bytes arrive
// differently.
func (db *DB) RestoreFromTarget(ctx context.Context, actor Actor, target, id string, opts RestoreOptions) (RestoreResult, error) {
	if err := RequireAdmin(actor); err != nil {
		return RestoreResult{}, err
	}
	settings, err := db.loadSettings(ctx)
	if err != nil {
		return RestoreResult{}, err
	}

	var body io.ReadCloser
	if target == localTarget {
		body, err = db.openLocalBackup(ctx, id)
	} else {
		var t BackupTarget
		t, err = targetByName(settings, target)
		if err != nil {
			return RestoreResult{}, err
		}
		fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		body, err = t.Fetch(fetchCtx, id)
	}
	if err != nil {
		return RestoreResult{}, err
	}
	defer body.Close()

	opts.Source = target
	return db.RestoreFromReader(ctx, actor, body, opts)
}
