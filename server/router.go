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
