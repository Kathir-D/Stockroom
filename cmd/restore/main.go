// Command restore loads a Stockroom backup archive into the database.
//
// It exists for the one case the admin panel cannot cover: a database with no
// accounts in it. `EnsureFailsafeAdmin` recreates the .env admin on every
// server start, which is what normally makes "sign in and restore" work after
// a wipe -- but the failsafe is best-effort by decision (CLAUDE.md §7), so a
// site that never filled in .env restores into zero accounts and finds the
// admin panel unreachable at exactly the moment it is needed.
//
// This takes no session. Its trust boundary is shell access to the closet PC
// and the database URL, which is already strictly more access than any account
// grants. It satisfies RequireAdmin through stockroom.LocalCLIActor rather
// than skipping the check, and it calls the same RestoreFromZip the panel
// calls -- with the same checksum, row-count, foreign-key and sequence gates.
// A second, emergency-only restore would be code first exercised during an
// emergency.
//
// Usage:
//
//	stockroom-restore --yes backup-2026-09-14.zip
//	stockroom-restore --yes --passphrase 'the passphrase' backup-2026-09-14.zip.enc
//	stockroom-restore --list
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"stockroom/internal/stockroom"
)

func main() {
	log.SetFlags(0)

	yes := flag.Bool("yes", false, `confirm the restore. Without it nothing is written: restoring replaces every record in the database`)
	passphrase := flag.String("passphrase", "", "passphrase for an encrypted archive (.zip.enc). Omit to use the one saved in the admin panel")
	force := flag.Bool("force", false, "restore even when the backup was taken on a different database version")
	list := flag.Bool("list", false, "list the backups in the configured backup folder and exit")
	flag.Usage = usage
	flag.Parse()

	cfg, err := stockroom.LoadConfig()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := stockroom.Open(ctx, cfg.DatabaseURL, stockroom.Options{UploadsDir: cfg.UploadsDir})
	if err != nil {
		log.Fatalf("database: %v\n\nIs the database running? On this machine: `supabase start` in the Stockroom folder.", err)
	}
	defer db.Close()

	// Seed app_settings from .env if this is a fresh database, so --list knows
	// where to look even before the server has ever started.
	if _, err := db.EnsureSettings(ctx, cfg); err != nil {
		log.Printf("warning: could not load backup settings: %v", err)
	}

	actor := stockroom.LocalCLIActor()

	if *list {
		listBackups(ctx, db, actor)
		return
	}

	if flag.NArg() != 1 {
		usage()
		os.Exit(2)
	}
	path := flag.Arg(0)

	if !*yes {
		log.Fatalf(`Refusing to restore without --yes.

Restoring %s replaces every record in the database: every item, every account,
every checkout. Nothing is merged. Run it again with --yes when you are sure.`, path)
	}

	file, err := os.Open(path)
	if err != nil {
		log.Fatalf("cannot open %s: %v", path, err)
	}
	defer file.Close()

	start := time.Now()
	res, err := db.RestoreFromReader(ctx, actor, file, stockroom.RestoreOptions{
		// The typed confirmation the HTTP path asks for. The gate is in the
		// package, so the CLI has to satisfy it too -- --yes is this command's
		// way of asking the person the same question.
		Confirm:    "RESTORE",
		Force:      *force,
		Passphrase: *passphrase,
		Source:     "cli",
	})
	if err != nil {
		reportFailure(err)
		os.Exit(1)
	}

	fmt.Printf("Restored %d rows across %d tables in %s.\n", res.Rows, len(res.Tables), time.Since(start).Round(time.Millisecond))
	fmt.Printf("The backup was taken %s.\n", res.ArchiveRanAt.Local().Format("2006-01-02 15:04"))
	if res.Sequences > 0 {
		fmt.Printf("%d sequence(s) restored to where they left off.\n", res.Sequences)
	}
	for _, w := range res.Warnings {
		fmt.Printf("Note: %s\n", w)
	}
	fmt.Println("\nEverybody has been signed out. Start the server and sign in again.")
}

// reportFailure says what went wrong and, where the error is one of the known
// ones, what to do about it. A restore fails at the worst possible moment, and
// a bare sentinel message is not enough to act on.
func reportFailure(err error) {
	log.Printf("Restore failed: %v", err)
	switch {
	case errors.Is(err, stockroom.ErrInvalid) && strings.Contains(err.Error(), "encrypted"):
		log.Println("\nThis archive is encrypted. Run it again with --passphrase '<the passphrase>'.")
	case errors.Is(err, stockroom.ErrConflict) && strings.Contains(err.Error(), "database version"):
		log.Println("\nThe backup and this database are on different schema versions. Apply the migrations\n(`supabase db reset` applies all of them), or run again with --force if you know the\ndifference is safe.")
	case errors.Is(err, stockroom.ErrInvalid):
		log.Println("\nNothing was changed. The database is exactly as it was before this command ran.")
	}
}

func listBackups(ctx context.Context, db *stockroom.DB, actor stockroom.Actor) {
	versions, err := db.ListLocalBackups(ctx, actor)
	if err != nil {
		log.Fatalf("%v", err)
	}
	if len(versions) == 0 {
		fmt.Println("No backups found in the configured backup folder.")
		return
	}
	for _, v := range versions {
		fmt.Printf("%s  %8.1f KB  %s\n", v.At.Local().Format("2006-01-02 15:04"), float64(v.Bytes)/1024, v.ID)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `stockroom-restore — load a backup archive into the database.

  stockroom-restore --yes <archive.zip>
  stockroom-restore --yes --passphrase '<passphrase>' <archive.zip.enc>
  stockroom-restore --list

Restoring replaces every record in the database. Nothing is merged.

Flags:
`)
	flag.PrintDefaults()
}
