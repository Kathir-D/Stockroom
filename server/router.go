package main

import (
	"log"
	"net/http"
	"time"

	"stockroom/internal/stockroom"
)

func newRouter(db *stockroom.DB) http.Handler {
	mux := http.NewServeMux()

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
