package main

import (
	"log"
	"net/http"
	"net/url"
	"sync"
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

	// The sign-in photo wall (docs/design/signin-photo-wall.html §5). No
	// session: this is what the sign-in screen renders behind the card, and
	// there is no session to require yet. Both routes are nil-safe on a reel
	// that was never built, which is the common case -- see server/photowall.go.
	mux.HandleFunc("GET /signin/photos", d.handleSignInPhotos)
	mux.HandleFunc("GET /signin/config", d.handleSignInConfig)
	mux.Handle("GET "+stockroom.PhotoWallPrefix, photoTileServer(d.db.PhotoWall))

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

	// Kits: a named bundle of units. Reading one is any full session, because
	// a student puts a kit in the cart the way they put a unit in it; building
	// one is admin. Both are enforced in internal/stockroom. There is no kit
	// checkout route -- a kit reaches POST /checkout as its asset ids, so one
	// code path commits every cart (server/kits.go).
	mux.Handle("GET /kits", d.withSession(d.handleListKits, fullOnly))
	mux.Handle("GET /kits/{id}", d.withSession(d.handleGetKit, fullOnly))
	mux.Handle("POST /kits", d.withSession(d.handleCreateKit, fullOnly))
	mux.Handle("PUT /kits/{id}", d.withSession(d.handleUpdateKit, fullOnly))
	mux.Handle("DELETE /kits/{id}", d.withSession(d.handleDeleteKit, fullOnly))
	mux.Handle("POST /kits/{id}/items", d.withSession(d.handleAddKitItem, fullOnly))
	mux.Handle("DELETE /kits/{id}/items/{assetId}", d.withSession(d.handleRemoveKitItem, fullOnly))
	mux.Handle("POST /kits/{id}/checkin", d.withSession(d.handleCheckInKit, fullOnly))

	// Custody reads. The two lists and the asset trail are admin-only,
	// enforced inside internal/stockroom; a user's own history is not.
	mux.Handle("GET /custody/active", d.withSession(d.handleActiveCustody, fullOnly))
	mux.Handle("GET /custody/overdue", d.withSession(d.handleOverdueCustody, fullOnly))
	mux.Handle("GET /assets/{id}/history", d.withSession(d.handleAssetHistory, fullOnly))
	mux.Handle("GET /users/{id}/history", d.withSession(d.handleUserHistory, fullOnly))

	// Photos, served straight off UPLOADS_DIR. Unauthenticated on purpose;
	// see fileServer.
	mux.Handle("GET /files/", fileServer("/files/", d.db.UploadsDir))

	// The admin panel's writes: the asset table, the category tree, and the
	// backup button. Admin-only, enforced inside internal/stockroom.
	mux.Handle("POST /assets", d.withSession(d.handleCreateAsset, fullOnly))
	mux.Handle("PUT /assets/{id}", d.withSession(d.handleUpdateAsset, fullOnly))
	mux.Handle("DELETE /assets/{id}", d.withSession(d.handleDeleteAsset, fullOnly))
	mux.Handle("POST /assets/{id}/status", d.withSession(d.handleSetAssetStatus, fullOnly))
	mux.Handle("POST /assets/{id}/photo", d.withSession(d.handleSetAssetPhoto, fullOnly))
	// Barcodes and printable sheets (TEMPLATE-TODO Phase B). Admin-only,
	// enforced inside internal/stockroom. `labels.pdf` and `cards.pdf` are
	// POSTs because the id list can be hundreds of UUIDs; see server/labels.go.
	//
	// `GET /assets/{id}/barcode.png` is registered before nothing in
	// particular -- Go's mux prefers the most specific pattern, so it wins over
	// `GET /assets/{id}` without either needing to know about the other.
	mux.Handle("GET /labels/layouts", d.withSession(d.handleLabelLayouts, fullOnly))
	mux.Handle("POST /assets/labels.pdf", d.withSession(d.handlePrintAssetLabels, fullOnly))
	mux.Handle("POST /users/cards.pdf", d.withSession(d.handlePrintUserCards, fullOnly))
	mux.Handle("GET /assets/{id}/barcode.png", d.withSession(d.handleAssetBarcodePNG, fullOnly))

	mux.Handle("POST /assets/import", d.withSession(d.handleImportAssets, fullOnly))
	mux.Handle("POST /assets/bulk-preview", d.withSession(d.handleBulkPreview, fullOnly))
	mux.Handle("POST /assets/bulk", d.withSession(d.handleBulkAdd, fullOnly))
	mux.Handle("POST /categories/import", d.withSession(d.handleImportCategories, fullOnly))
	mux.Handle("POST /categories", d.withSession(d.handleCreateCategory, fullOnly))
	mux.Handle("PUT /categories/{id}", d.withSession(d.handleUpdateCategory, fullOnly))
	mux.Handle("DELETE /categories/{id}", d.withSession(d.handleDeleteCategory, fullOnly))
	mux.Handle("POST /admin/backup", d.withSession(d.handleBackupNow, fullOnly))

	// Backup configuration, status, restore and the photo mirror
	// (docs/design/backup.md §E.8). Admin-only, enforced inside
	// internal/stockroom. The restore routes are the destructive ones and each
	// carries its own typed confirmation, checked in the package rather than
	// here so the CLI is held to it too.
	mux.Handle("GET /admin/settings", d.withSession(d.handleGetSettings, fullOnly))
	mux.Handle("PUT /admin/settings", d.withSession(d.handleSaveSettings, fullOnly))
	mux.Handle("POST /admin/settings/test", d.withSession(d.handleTestTarget, fullOnly))
	mux.Handle("POST /admin/drive/connect", d.withSession(d.handleDriveConnect, fullOnly))
	mux.Handle("POST /admin/drive/finish", d.withSession(d.handleDriveFinish, fullOnly))
	mux.Handle("GET /admin/backup/status", d.withSession(d.handleBackupStatus, fullOnly))
	mux.Handle("GET /admin/backup/versions", d.withSession(d.handleBackupVersions, fullOnly))
	mux.Handle("POST /admin/restore", d.withSession(d.handleRestoreUpload, fullOnly))
	mux.Handle("POST /admin/restore/remote", d.withSession(d.handleRestoreRemote, fullOnly))
	mux.Handle("GET /admin/photos/generations", d.withSession(d.handlePhotoGenerations, fullOnly))
	mux.Handle("POST /admin/photos/restore", d.withSession(d.handleRestorePhotos, fullOnly))
	mux.Handle("DELETE /admin/photos/generations/{name}", d.withSession(d.handleDeletePhotoGeneration, fullOnly))

	// The sign-in photo wall's Drive folder
	// (docs/design/signin-photo-wall.html §7). Admin-only, enforced inside
	// internal/stockroom. None of these four ever returns the folder id: the
	// link is a capability, so the field is write-only and the screen is given
	// a label, counts and a preview strip instead.
	mux.Handle("GET /admin/photo-wall", d.withSession(d.handlePhotoWallStatus, fullOnly))
	mux.Handle("PUT /admin/photo-wall", d.withSession(d.handleSetPhotoWallFolder, fullOnly))
	mux.Handle("POST /admin/photo-wall/rebuild", d.withSession(d.handleRebuildPhotoWall, fullOnly))
	mux.Handle("GET /admin/photo-wall/preview", d.withSession(d.handlePhotoWallPreview, fullOnly))

	// User management. Admin-only, enforced inside internal/stockroom.
	mux.Handle("GET /users", d.withSession(d.handleListUsers, fullOnly))
	mux.Handle("POST /users", d.withSession(d.handleCreateUser, fullOnly))
	mux.Handle("POST /users/import", d.withSession(d.handleImportRoster, fullOnly))
	mux.Handle("GET /users/{id}", d.withSession(d.handleGetUser, fullOnly))
	mux.Handle("PUT /users/{id}", d.withSession(d.handleUpdateUser, fullOnly))
	mux.Handle("DELETE /users/{id}", d.withSession(d.handleDeleteUser, fullOnly))
	mux.Handle("POST /users/{id}/password", d.withSession(d.handleSetUserPassword, fullOnly))

	// The web UI, last and least specific. Go's ServeMux prefers the most
	// specific pattern, so every route above still wins over this one and the
	// catch-all only ever sees paths no endpoint claims (server/ui.go).
	//
	// Serving the UI from the same origin as the API is what makes an install
	// one process and one port, and it takes withCORS out of the picture
	// entirely for that deployment: a same-origin request is not subject to
	// CORS. The allow-list stays because the dev loop and the Wails window are
	// still separate origins.
	ui := uiHandler()
	mux.Handle("GET "+uiPrefix, ui)
	mux.Handle("GET /", ui)

	return logRequests(withCORS(mux))
}

// localOrigins are the browser origins allowed to call this server.
//
// The Wails webview and the two Vite dev servers are each a *different origin*
// from 127.0.0.1:8080, so without this a browser refuses every fetch before it
// leaves the page — including the preflight on any request carrying an
// Authorization header. This is not a step toward LAN access: every entry is a
// loopback address, and CLAUDE.md §2 keeps the app localhost-only.
//
// The Wails webview is *not* in this map; see originAllowed, which has to match
// four different origins across two platforms and two build modes.
// 5173 is the only web-app port, and deliberately: `web-app/package.json` runs
// Vite with --strictPort, so a busy port makes it refuse to start instead of
// quietly taking the next one. Chasing the drift here — 5174, 5175, a range —
// was the other option and is worse. Vite's fallback port is unbounded in
// principle, so any range still has an edge past which the app loads normally
// and every request fails as "cannot reach the server": a blocked preflight and
// a dead server raise the same fetch error, so the UI blames the server. A
// refusal to start names the actual problem; a wider allow-list only moves the
// silent failure further out.
var localOrigins = map[string]bool{
	"http://localhost:5173":  true, // web-app, vite dev (--strictPort)
	"http://127.0.0.1:5173":  true,
	"http://localhost:34115": true, // wails dev, viewed in a normal browser
	"http://127.0.0.1:34115": true,
}

// wailsWebViewHost is the hostname Wails gives the window it loads the app into.
//
// On Windows the page is served over http from this host; on macOS and Linux it
// is served over the custom wails:// scheme, whose host is either "wails" or
// this, depending on build mode.
const wailsWebViewHost = "wails.localhost"

// originAllowed reports whether a browser origin may call this server.
//
// The Wails webview needs four cases, not one, and getting them wrong looks
// exactly like a server that is down — which is how an afternoon went missing:
//
//	macOS / Linux, wails build  wails://wails
//	macOS / Linux, wails dev    wails://wails.localhost:34115
//	Windows, wails build        http://wails.localhost
//	Windows, wails dev          http://wails.localhost:34115
//
// The port is the asset server's and is only present in dev, where it is also
// configurable (`wails dev -devserver`), so it is deliberately not matched on:
// a hard-coded 34115 would break the moment someone moved it, for a value that
// buys nothing. What is checked is the host, and for the custom scheme the
// scheme alone — a page can only carry a wails:// origin by being loaded inside
// a Wails webview on this machine, which is the app itself.
//
// None of this is an authorization boundary. CORS constrains browsers, not
// clients: curl sends whatever Origin it likes and always has. The session
// token is what protects the API (CLAUDE.md §7); this decides which *local UIs*
// the browser will let talk to it.
func originAllowed(origin string) bool {
	if localOrigins[origin] {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	switch parsed.Scheme {
	case "wails":
		return true
	case "http":
		// Hostname() drops the port, and the comparison is exact: a prefix test
		// would also have matched wails.localhost.example.com.
		return parsed.Hostname() == wailsWebViewHost
	}
	return false
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
		if originAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			// Without Vary, a cache could hand one origin's response to another.
			w.Header().Add("Vary", "Origin")
		} else if origin != "" {
			// A blocked origin is otherwise invisible: the browser rejects the
			// response before any JavaScript sees it, and the frontend reports
			// "cannot reach the server" — indistinguishable from a server that
			// is down. Naming the origin turns a confusing afternoon into one
			// log line. Logged once per origin, and only for the first
			// maxBlockedOrigins of them, so neither a reload loop nor a caller
			// inventing a header value can flood the log.
			logBlockedOrigin(origin)
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// maxBlockedOrigins caps how many distinct refused origins are remembered.
// The set exists only to log each one once; it is keyed by a header the caller
// controls, so without a bound a caller sending a fresh Origin per request
// would grow the map — and the log — for the life of the process. Past the cap
// the origin is neither stored nor logged: the first hundred have already told
// whoever is reading the log what is misconfigured, and the alternative
// (logging without storing) is the flood the set was added to prevent.
const maxBlockedOrigins = 100

var blockedOrigins = struct {
	mu   sync.Mutex
	seen map[string]struct{}
}{seen: make(map[string]struct{})}

func logBlockedOrigin(origin string) {
	blockedOrigins.mu.Lock()
	_, seen := blockedOrigins.seen[origin]
	full := len(blockedOrigins.seen) >= maxBlockedOrigins
	if !seen && !full {
		blockedOrigins.seen[origin] = struct{}{}
	}
	blockedOrigins.mu.Unlock()

	if seen || full {
		return
	}
	log.Printf("warning: refused a request from origin %q; it is not allowed (see originAllowed in server/router.go)", origin)
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
