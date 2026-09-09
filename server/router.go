package main

import (
	"log"
	"net/http"
	"time"

	"stockroom/internal/stockroom"
)

// deps is everything the handlers need. main builds one from the config;
// tests build one with whatever the case under test requires.
type deps struct {
	db         *stockroom.DB
	auth       *stockroom.Auth
	uploadsDir string
}

// newRouter registers every HTTP route and wraps the mux in shared
// middleware. Routes use Go 1.22+ method-and-path patterns ("GET /health").
// Handlers stay thin and push logic into internal/stockroom; the only
// decisions made here are which routes need a session and which accept a
// limited one.
func newRouter(d deps) http.Handler {
	mux := http.NewServeMux()

	// Health check used by the start scripts and by humans to confirm the
	// server is up and can reach Postgres.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if err := d.db.Ping(r.Context()); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":   true,
			"db":   "ok",
			"time": time.Now().UTC(),
		})
	})

	// Sign-in. No session needed.
	mux.HandleFunc("POST /auth/scan", d.handleLoginByScan)
	mux.HandleFunc("POST /auth/password", d.handleLoginByPassword)

	// A limited session (scan login, no password yet) may only set its
	// password, sign out, or ask who it is.
	mux.Handle("POST /auth/set-password", d.withSession(d.handleSetInitialPassword, allowLimited))
	mux.Handle("POST /auth/logout", d.withSession(d.handleLogout, allowLimited))
	mux.Handle("GET /me", d.withSession(d.handleMe, allowLimited))

	// User management. Admin-only, enforced inside internal/stockroom.
	mux.Handle("GET /users", d.withSession(d.handleListUsers, fullOnly))
	mux.Handle("POST /users", d.withSession(d.handleCreateUser, fullOnly))
	mux.Handle("POST /users/import", d.withSession(d.handleImportRoster, fullOnly))
	mux.Handle("GET /users/{id}", d.withSession(d.handleGetUser, fullOnly))
	mux.Handle("PUT /users/{id}", d.withSession(d.handleUpdateUser, fullOnly))
	mux.Handle("DELETE /users/{id}", d.withSession(d.handleDeleteUser, fullOnly))
	mux.Handle("POST /users/{id}/password", d.withSession(d.handleSetUserPassword, fullOnly))

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
