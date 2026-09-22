// Command server is the Stockroom HTTP API. It is the only process that talks
// to Postgres; both frontends call it over localhost. All logic lives in
// internal/stockroom. Handlers here only decode requests, call the package,
// and encode responses.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"stockroom/internal/stockroom"
	"stockroom/supabase"
)

// main wires the server together in order: load config, connect to Postgres,
// start listening, then block until Ctrl+C / SIGTERM and shut down gracefully
// so in-flight requests finish before the process exits.
func main() {
	cfg, err := stockroom.LoadConfig()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// ctx is cancelled on Ctrl+C / SIGTERM; everything below hangs off it so
	// the start scripts can stop the server cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// BackupDir and PhotoBackupDir are deliberately *not* passed here. They
	// live in app_settings now (docs/design/backup.md §C.2), seeded from .env
	// below on first boot only; passing the environment through as an override
	// would mean every restart quietly out-voting the settings screen.
	db, err := openWithRetry(ctx, cfg.DatabaseURL, stockroom.Options{
		SessionIdle: time.Duration(cfg.SessionIdleMinutes) * time.Minute,
		UploadsDir:  cfg.UploadsDir,
	}, dbConnectBudget)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer db.Close()

	// The schema, before anything reads it. An installed Stockroom has no
	// Supabase CLI, so this is the only thing that applies a migration on that
	// machine -- and it is also the upgrade path: a new binary carries the
	// files, the server applies whatever is pending, and nobody is ever asked
	// to run SQL by hand (TEMPLATE-TODO Phase A).
	//
	// Fatal, unlike the failsafe admin and the backup settings below. Those
	// two are features that can be absent; a schema that is not the one this
	// binary was built against is a server that will fail on its first real
	// query, at a counter, with a student holding a camera. Failing here says
	// so once, in the log, with the migration that broke named.
	//
	// Development is untouched: `supabase db reset` has already applied these,
	// they are recorded in the same table, and this finds nothing to do.
	switch applied, err := stockroom.Migrate(ctx, db.Pool, supabase.Migrations); {
	case err != nil:
		log.Fatalf("database schema: %v", err)
	case len(applied) > 0:
		log.Printf("database schema: applied %d migration(s): %s",
			len(applied), strings.Join(applied, ", "))
	}

	// Settings live in the database; .env seeds the backup ones the first
	// time this runs against a fresh one. A failure here is logged and not
	// fatal, for the same reason the failsafe admin is not: a backup that
	// cannot be configured must not stop students borrowing cameras.
	//
	// Loaded BEFORE the failsafe admin, because the settings row carries the
	// student-number format and the failsafe's number is validated against
	// it. The other order checks an `AB12345` failsafe against the digits
	// default and silently starts the server with no way in.
	if settings, err := db.EnsureSettings(ctx, cfg); err != nil {
		log.Printf("warning: could not load settings: %v", err)
	} else if err := stockroom.SetStudentNumberFormat(
		stockroom.StudentNumberFormat(settings.StudentNumberFormat), settings.StudentNumberPattern,
	); err != nil {
		// SaveSettings refuses a pattern that does not compile, so this is a
		// row edited by hand. Digits stays in force, which is the historical
		// rule, and the log names what was ignored.
		log.Printf("warning: student number format %q ignored, using digits: %v",
			settings.StudentNumberFormat, err)
	}

	// The failsafe admin (CLAUDE.md §7) is re-applied on every start so a
	// forgotten password or a bad roster import can never lock out the admin
	// panel. Nothing here is fatal. The failsafe exists to prevent a lockout,
	// so a missing or malformed .env value must not take the whole API down
	// with it -- log it and serve without one.
	//
	// Whether it worked is recorded rather than only logged, because the
	// consequence of it not working is invisible until the morning the
	// database is lost: with no failsafe admin, a restored-from-empty database
	// has nobody to sign in as and the admin panel -- the whole documented
	// restore route -- is unreachable. The backup screen says so while there
	// is still somebody signed in to read it (docs/design/backup.md §C.1).
	switch err := db.EnsureFailsafeAdmin(ctx, cfg.AdminStudentNumber, cfg.AdminPassword); {
	case errors.Is(err, stockroom.ErrFailsafeNotConfigured):
		log.Println("warning: ADMIN_STUDENT_NUMBER / ADMIN_PASSWORD not set; no failsafe admin")
	case err != nil:
		log.Printf("warning: no failsafe admin, check ADMIN_STUDENT_NUMBER / ADMIN_PASSWORD: %v", err)
	default:
		stockroom.SetFailsafeAdminConfigured(true)
	}

	// The nightly backup, scheduled in-process rather than by the operating
	// system (docs/design/backup.md §E.6). It also runs immediately on boot
	// when the last successful run is stale, which is how "back up first thing
	// when the machine is available" is met on a closet PC that gets unplugged.
	db.StartBackupScheduler(ctx)

	// The sign-in photo wall (docs/design/signin-photo-wall.html). The remote
	// is the switch: with SIGNIN_PHOTOS_REMOTE unset no reel is built and no
	// goroutine starts, which is every existing .env. Nothing here is fatal,
	// for the same reason the failsafe admin is not: §9's invariant is that no
	// failure in this subsystem may delay, block or visibly break sign-in, and
	// a server that refuses to start over a decorative wall breaks it hardest.
	//
	// Two goroutines, not one, and each is stopped by ctx: the reel fills and
	// reaps tiles on a ten-second tick, while the source re-lists the Drive
	// folder about once a week. Keeping them apart is what preserves the
	// reel's one-writer rule over the tile files (§2) while a listing that
	// takes minutes runs beside it.
	//
	// A source that cannot be built -- rclone not installed, no remote -- is a
	// warning and a reel that idles empty, not a reason to skip the reel:
	// §5's endpoint and §7's screen are written against a wall that may have
	// nothing to hand out, because that is the state they spend most of their
	// life in.
	if cfg.SignInPhotosRemote != "" {
		// The live folder comes from app_settings, not from .env (§8).
		// SIGNIN_PHOTOS_FOLDER_ID seeded that column on first boot, above, and
		// is ignored from then on -- otherwise a folder an admin pasted in the
		// panel would be silently out-voted by the environment on the next
		// restart, which is the direction the Phase 7 decision rules out.
		folderID, folderLabel, err := db.PhotoWallFolder(ctx)
		if err != nil {
			log.Printf("warning: could not read the sign-in photo wall folder: %v", err)
		}

		source, err := stockroom.NewDrivePhotoSource(stockroom.DrivePhotoSourceOptions{
			Remote:          cfg.SignInPhotosRemote,
			FolderID:        folderID,
			Dir:             cfg.SignInPhotosDir,
			RefreshInterval: time.Duration(cfg.SignInPhotosManifestHours) * time.Hour,
		})
		if err != nil {
			log.Printf("warning: sign-in photo wall has no source: %v", err)
		}

		wall, wallErr := stockroom.NewPhotoWall(stockroom.PhotoWallOptions{
			Dir:   cfg.SignInPhotosDir,
			Count: cfg.SignInPhotosCount,
			Batch: cfg.SignInPhotosBatch,
			TTL:   time.Duration(cfg.SignInPhotosTTLMinutes) * time.Minute,
			// A nil *DrivePhotoSource in a non-nil PhotoSource interface would
			// be a source the reel calls and that always errors, so the nil
			// case is kept out of the interface entirely.
			Source: photoSource(source),
		})
		if wallErr != nil {
			log.Printf("warning: sign-in photo wall disabled, check SIGNIN_PHOTOS_DIR: %v", wallErr)
		} else {
			db.PhotoWall = wall
			// The source is handed to DB as well as to the reel, because §7's
			// admin screen needs the folder switch, the listing progress and
			// the probe -- none of which the PhotoSource interface has.
			db.PhotoWallSource = source
			go wall.Run(ctx)
			if source != nil {
				// Started after the reel, because NewPhotoWall wipes the cache
				// directory and the source reads its manifest back out of it.
				go source.Run(ctx)
			}
			log.Printf("sign-in photo wall caching in %s", cfg.SignInPhotosDir)
			if folderID == "" {
				log.Printf("sign-in photo wall: no Drive folder set, so the wall stays empty until one is chosen in Admin → Photo wall")
			} else {
				log.Printf("sign-in photo wall reading %q", folderLabel)
			}
		}
	}

	// ReadTimeout bounds the body as well as the headers. Without it a photo
	// upload that trickles in a byte at a time holds a connection and its
	// goroutine open forever, and ReadHeaderTimeout alone does not touch that
	// because the headers arrived fine. A minute is far longer than a 10 MB
	// picture needs over loopback and far shorter than forever.
	srv := &http.Server{
		Addr:              cfg.ServerAddr,
		Handler:           newRouter(deps{db: db}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       60 * time.Second,
	}

	// Serve in the background so main can wait on the signal context below.
	go func() {
		log.Printf("stockroom server listening on http://%s", cfg.ServerAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	// Block until a stop signal arrives, then give in-flight requests a few
	// seconds to complete before closing the listener and the DB pool.
	<-ctx.Done()
	log.Println("shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

// photoSource wraps a concrete source so a nil one stays a nil interface.
// Assigning a typed nil pointer into an interface produces a value that is not
// nil, and the reel's "no source means idle quietly" check would miss it.
func photoSource(s *stockroom.DrivePhotoSource) stockroom.PhotoSource {
	if s == nil {
		return nil
	}
	return s
}

// dbConnectBudget is how long the server waits for Postgres to answer before
// giving up at start-up.
//
// Two minutes rather than the single attempt this used to make, because the
// closet PC's failure mode is a reboot, not a misconfiguration: the service
// starts in seconds while Docker Desktop takes the better part of a minute to
// have a container ready, so the database is *reliably* absent at exactly the
// moment the server first asks for it. Exiting there is worse than it looks --
// the nightly backup is a goroutine inside this process (docs/design/backup.md
// §E.6), so "the server did not come back" and "the machine stopped backing
// up" are the same event, and neither is visible until somebody needs the
// backup. A wrong DATABASE_URL still fails, two minutes later, with every
// attempt in the log saying so.
const dbConnectBudget = 2 * time.Minute

// openWithRetry is stockroom.Open with a deadline instead of one attempt.
//
// It lives here rather than inside Open because the other three callers want
// the opposite behaviour: both test helpers should fail immediately against a
// database that is not running, and cmd/restore is a person at a terminal
// during a disaster, for whom a fast, legible error beats two minutes of
// silence. Only the long-running server benefits from waiting.
func openWithRetry(ctx context.Context, url string, opts stockroom.Options, budget time.Duration) (*stockroom.DB, error) {
	deadline := time.Now().Add(budget)
	// Backoff starts short and caps low: Docker usually appears within a
	// minute, and a five-second ceiling keeps the log readable without
	// turning a ready database into a five-second wait.
	const (
		firstWait = time.Second
		maxWait   = 5 * time.Second
	)

	wait := firstWait
	for attempt := 1; ; attempt++ {
		db, err := stockroom.Open(ctx, url, opts)
		if err == nil {
			if attempt > 1 {
				log.Printf("database: connected on attempt %d", attempt)
			}
			return db, nil
		}

		// A cancelled context is Ctrl+C or SIGTERM, not a database that is
		// still starting. Retrying it would ignore the signal for two minutes.
		if ctx.Err() != nil {
			return nil, err
		}
		if !time.Now().Add(wait).Before(deadline) {
			return nil, fmt.Errorf("after %s: %w", budget, err)
		}

		// Logged every time rather than once, so somebody reading the log
		// during a slow boot can see it is waiting rather than wedged.
		log.Printf("database not ready (attempt %d), retrying in %s: %v", attempt, wait, err)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
		if wait < maxWait {
			wait *= 2
			if wait > maxWait {
				wait = maxWait
			}
		}
	}
}
