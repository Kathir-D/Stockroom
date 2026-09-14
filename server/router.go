package main

import (
	"log"
	"net/http"
	"time"

	"stockroom/internal/stockroom"
)

// deps is everything the handlers need: the one DB handle, which carries the
// pool, the session store and the two directories (stockroom.Options).
type deps struct {
	db *stockroom.DB
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

	// Browse. Any full session may read the catalogue; nothing here is
	// admin-only (CLAUDE.md §7).
	mux.Handle("GET /categories/tree", d.withSession(d.handleCategoryTree, fullOnly))
	mux.Handle("GET /assets", d.withSession(d.handleListAssets, fullOnly))
	mux.Handle("GET /assets/{id}", d.withSession(d.handleGetAsset, fullOnly))

	// The core loop: scan, cart checkout, check-in. Any full session may
	// reach all three -- anyone signed in can return any item (CLAUDE.md §7)
	// -- and the rules about who may check out what live in the package.
	mux.Handle("POST /scan", d.withSession(d.handleScan, fullOnly))
	mux.Handle("POST /checkout", d.withSession(d.handleCheckout, fullOnly))
	mux.Handle("POST /assets/{id}/checkin", d.withSession(d.handleCheckIn, fullOnly))
	mux.Handle("POST /custody/{id}/note", d.withSession(d.handleAnnotateCustody, fullOnly))

	// Custody reads. The two lists and the asset trail are admin-only,
	// enforced inside internal/stockroom; a user's own history is not.
	mux.Handle("GET /custody/active", d.withSession(d.handleActiveCustody, fullOnly))
	mux.Handle("GET /custody/overdue", d.withSession(d.handleOverdueCustody, fullOnly))
	mux.Handle("GET /assets/{id}/history", d.withSession(d.handleAssetHistory, fullOnly))
	mux.Handle("GET /users/{id}/history", d.withSession(d.handleUserHistory, fullOnly))

	// Photos, served straight off UPLOADS_DIR. Unauthenticated on purpose;
	// see fileServer.
	mux.Handle("GET /files/", fileServer(d.db.UploadsDir))

	// The admin panel's writes: the asset table, the category tree, and the
	// backup button. Admin-only, enforced inside internal/stockroom.
	mux.Handle("POST /assets", d.withSession(d.handleCreateAsset, fullOnly))
	mux.Handle("PUT /assets/{id}", d.withSession(d.handleUpdateAsset, fullOnly))
	mux.Handle("DELETE /assets/{id}", d.withSession(d.handleDeleteAsset, fullOnly))
	mux.Handle("POST /assets/{id}/status", d.withSession(d.handleSetAssetStatus, fullOnly))
	mux.Handle("POST /assets/{id}/photo", d.withSession(d.handleSetAssetPhoto, fullOnly))
	mux.Handle("POST /categories", d.withSession(d.handleCreateCategory, fullOnly))
	mux.Handle("PUT /categories/{id}", d.withSession(d.handleUpdateCategory, fullOnly))
	mux.Handle("DELETE /categories/{id}", d.withSession(d.handleDeleteCategory, fullOnly))
	mux.Handle("POST /admin/backup", d.withSession(d.handleBackupNow, fullOnly))

	// User management. Admin-only, enforced inside internal/stockroom.
	mux.Handle("GET /users", d.withSession(d.handleListUsers, fullOnly))
	mux.Handle("POST /users", d.withSession(d.handleCreateUser, fullOnly))
	mux.Handle("POST /users/import", d.withSession(d.handleImportRoster, fullOnly))
	mux.Handle("GET /users/{id}", d.withSession(d.handleGetUser, fullOnly))
	mux.Handle("PUT /users/{id}", d.withSession(d.handleUpdateUser, fullOnly))
	mux.Handle("DELETE /users/{id}", d.withSession(d.handleDeleteUser, fullOnly))
	mux.Handle("POST /users/{id}/password", d.withSession(d.handleSetUserPassword, fullOnly))

	return logRequests(withCORS(mux))
}

// localOrigins are the browser origins allowed to call this server.
//
// The Wails webview and the two Vite dev servers are each a *different origin*
// from 127.0.0.1:8080, so without this a browser refuses every fetch before it
// leaves the page — including the preflight on any request carrying an
// Authorization header. This is not a step toward LAN access: every entry is a
// loopback address, and CLAUDE.md §2 keeps the app localhost-only.
var localOrigins = map[string]bool{
	"http://localhost:5173":  true, // web-app, vite dev
	"http://127.0.0.1:5173":  true,
	"http://localhost:34115": true, // wails dev
	"http://127.0.0.1:34115": true,
	"wails://wails":          true, // wails production webview
	"http://wails.localhost": true,
}

// withCORS answers preflights and echoes an allowed origin back.
//
// Credentials are allowed because the server also sets an HttpOnly session
// cookie, and the spec forbids pairing that with a wildcard origin — so the
// origin is echoed from the allow-list rather than starred, and anything not on
// the list simply gets no CORS headers and is refused by the browser.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if localOrigins[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			// Without Vary, a cache could hand one origin's response to another.
			w.Header().Add("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
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
