package stockroom

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"
)

// The export (CLAUDE.md §13, Phase C). "We want to stop using Stockroom"
// should be a button, not a rescue operation, and it is also what makes
// adopting it low-risk: whatever goes in can come out as files anybody can
// open.
//
// It is the backup's archive, byte for byte the same format, taken through
// the same snapshot. Nothing about the data differs, so nothing about the
// code should: a second exporter would be a second definition of "every
// table" that drifts the first time a migration adds one. What differs is
// everything around the archive. An export needs no backup folder, takes no
// backup lock, pushes nowhere, is never encrypted (the person leaving needs to
// read it, and the passphrase is for archives that leave the machine
// unattended), and goes to the browser rather than to disk.

// Export is one download: the zip and the name to save it under.
type Export struct {
	Filename string
	Archive  []byte
}

// exportNamePattern names the download. "export", not "backup", so a teacher
// looking for the word export in their Downloads folder finds it, and so
// nobody mistakes a one-off download for the nightly run.
const exportNamePattern = "stockroom-export-%s.zip"

// ExportEverything packs the whole database into the backup's zip format and
// hands it back. Admin-only: the zip holds accounts.csv, which is every
// student number (a working scan login, CLAUDE.md §7) beside its password
// hash.
//
// The zip also restores. It carries the same manifest, digests and sequences
// a backup does, so RestoreFromZip accepts it, and "move Stockroom to another
// PC" is this plus an upload on the new machine.
func (db *DB) ExportEverything(ctx context.Context, actor Actor) (Export, error) {
	if err := RequireAdmin(actor); err != nil {
		return Export{}, err
	}
	workDir, err := os.MkdirTemp("", "stockroom-export-")
	if err != nil {
		return Export{}, fmt.Errorf("export: %w", err)
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	now := time.Now()
	snap, err := db.takeSnapshot(ctx, workDir, now, false)
	if err != nil {
		return Export{}, err
	}
	if err := db.logNow(ctx, LogEntry{Category: LogAdmin, Action: "export", ActorID: actorLogID(actor),
		Summary: "Downloaded Export everything"}); err != nil {
		return Export{}, err
	}
	return Export{
		Filename: fmt.Sprintf(exportNamePattern, now.Format("2006-01-02")),
		Archive:  snap.archive,
	}, nil
}

// snapshot is one consistent read of the whole database, packed.
type snapshot struct {
	archive   []byte
	inventory []byte
	accounts  []byte
	manifest  Manifest
	tables    []TableExport
	rows      int64
}

// takeSnapshot exports every public table, the sequences, the two readable
// CSVs and RESTORE.md into one zip with its manifest. workDir holds the raw
// table CSVs on the way in (COPY streams to a file, so a large table never
// sits in memory as rows); the caller owns it and removes it. The returned
// archive is always plaintext. Encrypting it is the backup's decision.
func (db *DB) takeSnapshot(ctx context.Context, workDir string, ranAt time.Time, encrypted bool) (snapshot, error) {
	tablesDir := filepath.Join(workDir, "tables")
	if err := os.MkdirAll(tablesDir, 0o755); err != nil {
		return snapshot{}, fmt.Errorf("create snapshot dir: %w", err)
	}

	// One connection for the whole export: COPY TO STDOUT is a protocol-level
	// operation on a single connection, and holding one is cheaper than
	// borrowing a dozen in a row from a pool of eight.
	conn, err := db.Pool.Acquire(ctx)
	if err != nil {
		return snapshot{}, fmt.Errorf("snapshot: %w", err)
	}
	defer conn.Release()

	// Repeatable read pins every query below to the snapshot the first one
	// takes, so the table list, the rows, the inventory and the accounts all
	// describe one instant. A run that read each table at its own moment could
	// export a custody_events row whose asset landed in assets.csv a moment
	// too early to be there, and the reload would fail on the foreign key.
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return snapshot{}, fmt.Errorf("snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tables, err := publicTables(ctx, tx)
	if err != nil {
		return snapshot{}, err
	}

	builder := newArchiveBuilder()
	var snap snapshot
	manifest := Manifest{RanAt: ranAt, Rows: map[string]int64{}, Encrypted: encrypted}

	for _, table := range tables {
		file := filepath.Join(tablesDir, table+".csv")
		selectSQL, err := tableExportSQL(ctx, tx, table)
		if err != nil {
			return snapshot{}, err
		}
		rows, err := copyQueryToFile(ctx, conn.Conn(), selectSQL, file)
		if err != nil {
			return snapshot{}, fmt.Errorf("export %s: %w", table, err)
		}
		name := archiveTablesDir + table + ".csv"
		if err := builder.addFile(name, file); err != nil {
			return snapshot{}, err
		}
		snap.tables = append(snap.tables, TableExport{Table: table, File: name, Rows: rows})
		snap.rows += rows
		manifest.Rows[table] = rows
	}

	// Sequence state, which the CSVs cannot carry: a restore that reloads
	// assets.csv but leaves assets_asset_tag_seq at 1 collides on the next
	// insert into a unique column.
	sequences, err := readSequences(ctx, tx)
	if err != nil {
		return snapshot{}, err
	}
	manifest.Sequences = sequences
	seqCSV, err := sequencesCSV(sequences)
	if err != nil {
		return snapshot{}, err
	}
	if err := builder.addBytes(archiveSequences, seqCSV); err != nil {
		return snapshot{}, err
	}

	manifest.SchemaVersion, err = schemaVersion(ctx, tx)
	if err != nil {
		return snapshot{}, err
	}

	// The two readable files. They go in the archive *and* stay beside it in
	// the dated folder, because the whole point of them is that somebody can
	// double-click one without knowing what a zip of CSVs is for.
	inventory, err := buildInventoryCSV(ctx, tx)
	if err != nil {
		return snapshot{}, err
	}
	accounts, err := buildAccountsCSV(ctx, tx)
	if err != nil {
		return snapshot{}, err
	}
	if err := builder.addBytes(archiveInventory, inventory); err != nil {
		return snapshot{}, err
	}
	if err := builder.addBytes(archiveAccounts, accounts); err != nil {
		return snapshot{}, err
	}
	if err := builder.addBytes(archiveRestoreDoc, restoreDoc); err != nil {
		return snapshot{}, err
	}

	// The manifest is added last because it carries the digests of everything
	// added before it, and it is the one file not listed in its own Files map:
	// nothing can carry its own hash.
	manifest.Files = builder.files
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return snapshot{}, fmt.Errorf("write manifest: %w", err)
	}
	if err := builder.addBytes(archiveManifest, manifestJSON); err != nil {
		return snapshot{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return snapshot{}, fmt.Errorf("snapshot: %w", err)
	}

	archive, err := builder.finish()
	if err != nil {
		return snapshot{}, err
	}
	snap.archive, snap.inventory, snap.accounts, snap.manifest = archive, inventory, accounts, manifest
	return snap, nil
}
