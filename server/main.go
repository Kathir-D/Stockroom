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

	db, err := stockroom.Open(ctx, cfg.DatabaseURL)
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

	auth := stockroom.NewAuth(db, time.Duration(cfg.SessionIdleMinutes)*time.Minute)

	srv := &http.Server{
		Addr:              cfg.ServerAddr,
		Handler:           newRouter(deps{db: db, auth: auth, uploadsDir: cfg.UploadsDir, backupDir: cfg.BackupDir}),
		ReadHeaderTimeout: 5 * time.Second,
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
