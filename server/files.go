package main

import (
	"net/http"
	"strings"
)

// fileServer serves the files under dir at prefix, and nothing else.
//
// Two routes use it: /files/ for the profile and asset photos under
// UploadsDir (the prefix stockroom.FilesPrefix builds every photo_url from),
// and the sign-in photo wall's tiles (see photoTileServer). The prefix is a
// parameter rather than a constant because the second caller arrived and one
// implementation of "serve a directory, never enumerate it" is worth more than
// two that can drift apart.
//
// No session is required on either. The desktop app renders these in <img>
// tags, which cannot carry the Authorization header the Wails origin signs in
// with, so gating them would simply break every photo. The server listens on
// localhost only (CLAUDE.md §3) and the files are student photos, equipment
// pictures and departmental photography, not credentials.
//
// http.Dir already refuses paths that escape the root, including encoded
// "..". What it does not refuse is a directory listing, so requests that
// resolve to a directory are answered 404: these routes are for fetching a
// known file, not for enumerating the roster.
//
// An empty dir is a 404 handler, which is how "the feature is off" needs no
// branch at the call site -- see PhotoWall.TileDir.
func fileServer(prefix, dir string) http.Handler {
	if dir == "" {
		return http.NotFoundHandler()
	}
	files := http.FileServer(http.Dir(dir))
	return http.StripPrefix(prefix, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A directory reached without a trailing slash is redirected to one
		// by FileServer, and that request lands back here and 404s.
		if r.URL.Path == "" || strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		files.ServeHTTP(w, r)
	}))
}
