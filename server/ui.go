package main

import (
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"

	webapp "stockroom/web-app"
)

// uiPrefix is where the built bundles live inside dist, and therefore the one
// path below / that the UI owns.
//
// It is `static` rather than Vite's default `assets` because the API already
// owns `GET /assets` and `GET /assets/{id}`; see the comment on `assetsDir` in
// web-app/vite.config.ts. The constant is here as well as there so that
// changing one without the other is a compile-time-visible mistake rather than
// a 404 on a JavaScript file.
const uiPrefix = "/static/"

// uiHandler serves the embedded web UI.
//
// Three behaviours, in order:
//
//   - `/` and any path outside uiPrefix that is not a file in dist answer
//     with index.html. The
//     app routes on the hash (packages/ui/src/lib/stores/router), so this is
//     not really an SPA fallback -- it is what makes a bookmarked
//     `http://localhost:8080/#/kits` work, and what makes a mistyped path show
//     the app rather than a bare 404 page a student cannot act on.
//   - A missing file under uiPrefix is a plain 404. Nobody types those paths;
//     a browser asks for them from a script or link tag, and index.html in
//     reply is a 200 of HTML where JavaScript was expected -- a MIME error in
//     the console instead of a 404 that says which file is missing.
//   - A file that does exist is served as itself.
//   - A binary built without the UI says so, in words, rather than answering a
//     blank page. See webapp.Present.
//
// Note what this does *not* do: it never answers a JSON 404 for an unknown API
// path, because it never sees one. Every API route is registered on the mux
// with a more specific pattern, and Go's ServeMux prefers the specific one --
// so this handler only ever receives paths no endpoint claims.
func uiHandler() http.Handler {
	return uiHandlerFor(webapp.Dist, webapp.Present())
}

// uiHandlerFor is uiHandler over any filesystem, so the routing rules can be
// tested without a built UI.
func uiHandlerFor(dist fs.FS, present bool) http.Handler {
	if !present {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 503 rather than 404: the UI is not missing, it was never built
			// into this binary, and that is a build problem with an operator
			// who can fix it -- the same reasoning as ErrNotConfigured
			// (CLAUDE.md §8.1).
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, uiMissingMessage)
		})
	}

	files := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" || !fileExists(dist, name) {
			if strings.HasPrefix(r.URL.Path, uiPrefix) {
				http.NotFound(w, r)
				return
			}
			serveIndex(dist, w)
			return
		}
		// The bundles carry a content hash in their filename, so a cached copy
		// can never be the wrong one and a year is as safe as a minute. Only
		// under uiPrefix: favicon.svg and anything else dropped in public/ has
		// a stable name and must not be pinned for a year.
		if strings.HasPrefix(r.URL.Path, uiPrefix) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}

// serveIndex writes index.html, uncached.
//
// Uncached deliberately, and it is the whole upgrade story for the browser: the
// bundle filenames change on every build, so a browser holding a stale
// index.html asks for JavaScript that the new binary does not have. The closet
// PC's browser stays open for months, which is exactly long enough for that to
// happen across a version.
func serveIndex(dist fs.FS, w http.ResponseWriter) {
	body, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		http.Error(w, "web UI not available", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	// ServeContent would add Range and If-Modified-Since handling for a file
	// that is a few hundred bytes and must not be cached; Write is the honest
	// version of what is wanted here.
	_, _ = w.Write(body)
}

// fileExists reports whether name identifies a file in the embedded UI.
func fileExists(dist fs.FS, name string) bool {
	info, err := fs.Stat(dist, name)
	return err == nil && !info.IsDir()
}

const uiMissingMessage = `Stockroom: this binary was built without the web UI.

The UI is compiled in from web-app/dist, which was empty at build time. To fix:

    npm install            # once, at the repository root
    npm run build --workspace=web-app
    go build -o stockroom ./server

The API is running and is unaffected -- try http://127.0.0.1:8080/health.
For the development loop, use ./scripts/dev.sh instead; it serves the UI from
Vite on :5173 and does not need this.
`
