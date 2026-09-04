// Command server is the Stockroom HTTP API. It is the only process that talks
// to Postgres; both frontends call it over localhost. All logic lives in
// internal/stockroom — handlers here only decode requests, call the package,
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

func main() {
	cfg, err := stockroom.LoadConfig()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := stockroom.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer db.Close()

	srv := &http.Server{
		Addr:              cfg.ServerAddr,
		Handler:           newRouter(db),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("stockroom server listening on http://%s", cfg.ServerAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
