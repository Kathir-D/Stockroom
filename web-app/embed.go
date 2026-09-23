// Package webapp carries the built web UI into the Go binary.
//
// The deliverable for an installed Stockroom is one file: API, UI and
// migrations in a single executable, started by a service manager, with
// nothing to serve the frontend separately and nothing to keep in step with it
// (TEMPLATE-TODO Phase A). The desktop app has always worked this way --
// `desktop-app/main.go` embeds its own dist the same way -- so this is the
// existing pattern applied to the other host.
//
// Serving the UI from the same origin as the API is the second reason, and on
// a bad day the bigger one: it makes the CORS allow-list irrelevant for an
// install, and a blocked preflight is the single most confusing failure this
// system can produce, because the browser reports it identically to a server
// that is not running (CLAUDE.md §13, 2026-09-15).
//
// `dist` is a *tracked empty directory* on a fresh clone. `go:embed` fails the
// build when its directory is absent, and `go build ./...` has to work before
// anybody has run `npm run build` -- so `dist/.gitkeep` is committed and
// `Present` below is how the server tells "nobody has built the UI" apart from
// "the UI is missing", which are the same bytes and very different problems.
package webapp

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFiles embed.FS

// Dist is the built UI, rooted at what the browser sees as `/`.
var Dist fs.FS = mustSub("dist")

// Present reports whether there is actually a UI in here.
//
// A development binary built from a clean clone embeds one `.gitkeep` and
// nothing else, which is a perfectly valid embed.FS that answers 404 to every
// request. Left undetected that is a blank page with no explanation; the
// server uses this to say "this binary was built without the web UI; run
// scripts/dev.sh for the development loop" instead.
func Present() bool {
	_, err := fs.Stat(Dist, "index.html")
	return err == nil
}

// mustSub returns a subtree of the embedded UI or panics if it is absent.
func mustSub(dir string) fs.FS {
	sub, err := fs.Sub(distFiles, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
