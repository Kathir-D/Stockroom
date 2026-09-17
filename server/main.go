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

	db, err := stockroom.Open(ctx, cfg.DatabaseURL, stockroom.Options{
		SessionIdle: time.Duration(cfg.SessionIdleMinutes) * time.Minute,
		UploadsDir:  cfg.UploadsDir,
		BackupDir:   cfg.BackupDir,
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
	switch err := db.EnsureFailsafeAdmin(ctx, cfg.AdminStudentNumber, cfg.AdminPassword); {
	case errors.Is(err, stockroom.ErrFailsafeNotConfigured):
		log.Println("warning: ADMIN_STUDENT_NUMBER / ADMIN_PASSWORD not set; no failsafe admin")
	case err != nil:
		log.Printf("warning: no failsafe admin, check ADMIN_STUDENT_NUMBER / ADMIN_PASSWORD: %v", err)
	}

	// The sign-in photo wall (docs/design/signin-photo-wall.html). The remote
	// is the switch: with SIGNIN_PHOTOS_REMOTE unset no reel is built and no
	// goroutine starts, which is every existing .env. Nothing here is fatal,
	// for the same reason the failsafe admin is not: §9's invariant is that no
	// failure in this subsystem may delay, block or visibly break sign-in, and
	// a server that refuses to start over a decorative wall breaks it hardest.
	//
	// The reel has no source of photographs yet -- rclone (§3) and the
	// normalizer (§4) are not built -- so with the remote set it creates and
	// wipes its cache directory, idles empty, and hands out nothing.
	if cfg.SignInPhotosRemote != "" {
		wall, err := stockroom.NewPhotoWall(stockroom.PhotoWallOptions{
			Dir:   cfg.SignInPhotosDir,
			Count: cfg.SignInPhotosCount,
			Batch: cfg.SignInPhotosBatch,
			TTL:   time.Duration(cfg.SignInPhotosTTLMinutes) * time.Minute,
		})
		if err != nil {
			log.Printf("warning: sign-in photo wall disabled, check SIGNIN_PHOTOS_DIR: %v", err)
		} else {
			db.PhotoWall = wall
			// Stops with ctx, so Ctrl+C ends the reel with everything else.
			go wall.Run(ctx)
			log.Printf("sign-in photo wall caching in %s", cfg.SignInPhotosDir)
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
