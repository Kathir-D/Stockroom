// Package cli holds the stockroom subcommands that aren't the server itself:
// restore, version and open. server/main.go dispatches to them, and
// cmd/restore keeps working as a thin wrapper around Restore.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"stockroom/internal/stockroom"
)

// Restore loads a Stockroom backup archive into the database and returns the
// process exit code.
//
// It exists for the one case the admin panel cannot cover: a database with no
// accounts in it. EnsureFailsafeAdmin recreates the config's admin on every
// server start, which is what normally makes "sign in and restore" work after
// a wipe, but the failsafe is best-effort (CLAUDE.md §7), so a site that never
// set one restores into zero accounts and finds the admin panel unreachable.
//
// It takes no session. Its trust boundary is shell access to the closet PC and
// the database URL, which is already more access than any account grants. It
// satisfies RequireAdmin through stockroom.LocalCLIActor rather than skipping
// the check, and it calls the same RestoreFromZip the panel calls, with the
// same checksum, row-count, foreign-key and sequence gates.
//
// name is how the usage text refers to the command: "stockroom restore" from
// the binary, "go run ./cmd/restore" from a working copy.
func Restore(name string, args []string) int {
	log.SetFlags(0)

	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	configPath := fs.String("config", "", "config file to read (default: STOCKROOM_CONFIG, a .env above the working directory, then the system path)")
	yes := fs.Bool("yes", false, `confirm the restore. Without it nothing is written: restoring replaces every record in the database`)
	passphrase := fs.String("passphrase", "", "passphrase for an encrypted archive (.zip.enc). Omit to use the one saved in the admin panel")
	force := fs.Bool("force", false, "restore even when the backup was taken on a different database version")
	list := fs.Bool("list", false, "list the backups in the configured backup folder and exit")
	fs.Usage = func() { restoreUsage(fs.Output(), name, fs) }
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := stockroom.LoadConfigFrom(*configPath)
	if err != nil {
		log.Printf("config: %v", err)
		return 1
	}
	log.Print(cfg.Describe())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := stockroom.Open(ctx, cfg.DatabaseURL, stockroom.Options{UploadsDir: cfg.UploadsDir})
	if err != nil {
		log.Printf("database: %v\n\nIs the database running? `stockroom doctor` checks, and in a working copy `supabase start` starts it.", err)
		return 1
	}
	defer db.Close()

	// Seed app_settings from the config if this is a fresh database, so --list
	// knows where to look even before the server has ever started.
	if _, err := db.EnsureSettings(ctx, cfg); err != nil {
		log.Printf("warning: could not load backup settings: %v", err)
	}

	actor := stockroom.LocalCLIActor()

	if *list {
		return listBackups(ctx, db, actor)
	}

	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	path := fs.Arg(0)

	if !*yes {
		log.Printf(`Refusing to restore without --yes.

Restoring %s replaces every record in the database: every item, every account,
every checkout. Nothing is merged. Run it again with --yes when you are sure.`, path)
		return 1
	}

	file, err := os.Open(path)
	if err != nil {
		log.Printf("cannot open %s: %v", path, err)
		return 1
	}
	defer file.Close()

	start := time.Now()
	res, err := db.RestoreFromReader(ctx, actor, file, stockroom.RestoreOptions{
		// The typed confirmation the HTTP path asks for. The gate is in the
		// package, so the CLI has to satisfy it too; --yes is this command's
		// way of asking the person the same question.
		Confirm:    "RESTORE",
		Force:      *force,
		Passphrase: *passphrase,
		Source:     "cli",
	})
	if err != nil {
		reportRestoreFailure(err)
		return 1
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
	return 0
}

// reportRestoreFailure says what went wrong and, where the error is one of the
// known ones, what to do about it.
func reportRestoreFailure(err error) {
	log.Printf("Restore failed: %v", err)
	switch {
	case errors.Is(err, stockroom.ErrInvalid) && strings.Contains(err.Error(), "encrypted"):
		log.Println("\nThis archive is encrypted. Run it again with --passphrase '<the passphrase>'.")
	case errors.Is(err, stockroom.ErrConflict) && strings.Contains(err.Error(), "database version"):
		log.Println("\nThe backup and this database are on different schema versions. Start the server once\nso it applies its migrations, or run again with --force if you know the difference is safe.")
	case errors.Is(err, stockroom.ErrInvalid):
		log.Println("\nNothing was changed. The database is exactly as it was before this command ran.")
	}
}

func listBackups(ctx context.Context, db *stockroom.DB, actor stockroom.Actor) int {
	versions, err := db.ListLocalBackups(ctx, actor)
	if err != nil {
		log.Printf("%v", err)
		return 1
	}
	if len(versions) == 0 {
		fmt.Println("No backups found in the configured backup folder.")
		return 0
	}
	for _, v := range versions {
		fmt.Printf("%s  %8.1f KB  %s\n", v.At.Local().Format("2006-01-02 15:04"), float64(v.Bytes)/1024, v.ID)
	}
	return 0
}

func restoreUsage(w io.Writer, name string, fs *flag.FlagSet) {
	fmt.Fprintf(w, `%[1]s loads a backup archive into the database.

  %[1]s --yes <archive.zip>
  %[1]s --yes --passphrase '<passphrase>' <archive.zip.enc>
  %[1]s --list

Restoring replaces every record in the database. Nothing is merged.

Flags:
`, name)
	fs.PrintDefaults()
}
