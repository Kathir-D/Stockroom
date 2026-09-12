package main

import (
	"net/http"
	"strings"
)

// fileServer serves the photos under UPLOADS_DIR at /files/ (the prefix
// stockroom.FilesPrefix builds every photo_url from).
//
// No session is required. The desktop app renders these in <img> tags, which
// cannot carry the Authorization header the Wails origin signs in with, so
// gating them would simply break every photo. The server listens on
// localhost only (CLAUDE.md §3) and the files are student photos and
// equipment pictures, not credentials.
//
// http.Dir already refuses paths that escape the root, including encoded
// "..". What it does not refuse is a directory listing, so requests that
// resolve to a directory are answered 404: /files/ is for fetching a known
// photo, not for enumerating the roster.
func fileServer(dir string) http.Handler {
	if dir == "" {
		return http.NotFoundHandler()
	}
	files := http.FileServer(http.Dir(dir))
	return http.StripPrefix("/files/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A directory reached without a trailing slash is redirected to one
		// by FileServer, and that request lands back here and 404s.
		if r.URL.Path == "" || strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		files.ServeHTTP(w, r)
	}))
}
