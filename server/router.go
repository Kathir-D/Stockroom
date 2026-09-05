package main

import (
	"log"
	"net/http"
	"time"

	"stockroom/internal/stockroom"
)

// newRouter registers every HTTP route and wraps the mux in shared
// middleware. Routes use Go 1.22+ method-and-path patterns ("GET /health").
// Later phases add their handlers here; keep them thin and push logic into
// internal/stockroom.
func newRouter(db *stockroom.DB) http.Handler {
	mux := http.NewServeMux()

	// Health check used by the start scripts and by humans to confirm the
	// server is up and can reach Postgres.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if err := db.Ping(r.Context()); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":   true,
			"db":   "ok",
			"time": time.Now().UTC(),
		})
	})

	return logRequests(mux)
}

// logRequests prints one line per request. Localhost-only, low traffic, so a
// simple log is enough.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}
