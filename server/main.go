// Command server is the Stockroom HTTP API. It is the only process that talks
// to Postgres; both frontends call it over localhost. All logic lives in
// internal/stockroom. Handlers here only decode requests, call the package,
// and encode responses.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"stockroom/internal/stockroom"
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
	db, err := stockroom.Open(ctx, cfg.DatabaseURL, stockroom.Options{
		SessionIdle: time.Duration(cfg.SessionIdleMinutes) * time.Minute,
		UploadsDir:  cfg.UploadsDir,
	})
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer db.Close()

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

	// Backup settings live in the database; .env seeds them the first time
	// this runs against a fresh one. A failure here is logged and not fatal,
	// for the same reason the failsafe admin is not: a backup that cannot be
	// configured must not stop students borrowing cameras.
	if _, err := db.EnsureSettings(ctx, cfg); err != nil {
		log.Printf("warning: could not load backup settings: %v", err)
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
		source, err := stockroom.NewDrivePhotoSource(stockroom.DrivePhotoSourceOptions{
			Remote:          cfg.SignInPhotosRemote,
			FolderID:        cfg.SignInPhotosFolderID,
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
			go wall.Run(ctx)
			if source != nil {
				// Started after the reel, because NewPhotoWall wipes the cache
				// directory and the source reads its manifest back out of it.
				go source.Run(ctx)
			}
			log.Printf("sign-in photo wall caching in %s", cfg.SignInPhotosDir)
			if cfg.SignInPhotosFolderID == "" {
				log.Printf("sign-in photo wall: no Drive folder set, so the wall stays empty")
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
