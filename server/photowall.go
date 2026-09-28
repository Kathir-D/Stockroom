package main

import (
	"net/http"
	"strconv"

	"stockroom/internal/stockroom"
)

// The sign-in photo wall's two unauthenticated routes
// (docs/design/signin-photo-wall.html §5): the set the component polls, and
// the tiles its <img> tags fetch, served from memory.
//
// Neither takes a session, and that is not an oversight. The wall renders on
// the sign-in screen, which exists precisely because nobody is signed in yet;
// there is no session to require. §0 is where the consequences of that are
// argued through -- the short version is that the tiles are departmental
// photography, they carry no Drive identifier, their names are random, and
// the server listens on loopback only.

// signInPhotosResponse is what the component gets. Both fields are always
// present: an empty list is a normal answer, not an error condition.
type signInPhotosResponse struct {
	// Photos is the set's tile URLs in strip order. New photographs arrive
	// at the end, so a strip that appends what it hasn't seen never jumps.
	Photos []string `json:"photos"`
	// Size is how many the set is filling to, so the strip can tell a set
	// still filling from a full one and poll faster while it fills.
	Size int `json:"size"`
}

// handleSignInPhotos returns the set.
//
// It always answers 200, including when there is nothing to give. Not
// configured, rclone missing, the manifest still building or Drive
// unreachable all produce the same empty list, because they all mean the
// same thing to the only caller: draw no strips. §9's invariant is that no
// failure in this subsystem may delay, block or visibly break sign-in.
func (d deps) handleSignInPhotos(w http.ResponseWriter, r *http.Request) {
	wall := d.db.SignInPhotoWall()
	_, size := wall.Counts()
	// The set changes every few minutes, so a cached copy is soon wrong.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, signInPhotosResponse{Photos: wall.Photos(), Size: size})
}

// photoTileServer serves one tile from the set's memory. Nothing on disk is
// reachable here, so the manifest (a listing of the Drive folder, which §0
// says no client may receive) can't be served by any name.
//
// The set is looked up per request rather than captured when the router is
// built, because it can start after that: an admin signing in to Google on
// the Photo wall screen starts it on a running server (photowall_google.go).
func photoTileServer(currentWall func() *stockroom.PhotoWall) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := currentWall().Tile(r.URL.Path)
		if !ok {
			http.NotFound(w, r)
			return
		}
		// A tile's name is random and never reused, so the bytes at a URL
		// never change and immutable is exactly true. A day rather than a
		// year: long enough that a strip scrolling all day never fetches a
		// tile twice, short enough that a photograph dropped from the set
		// leaves the browser's cache soon after. private, because the
		// machine is shared and there is no shared cache on loopback.
		w.Header().Set("Cache-Control", "private, max-age=86400, immutable")
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		if r.Method != http.MethodHead {
			_, _ = w.Write(data)
		}
	})
}

/* --------------------------------------------------------------- admin ---- */

// The Drive folder the wall reads from, controlled from the admin panel
// (§7). Admin-only, enforced by RequireAdmin inside internal/stockroom rather
// than here, like every other admin route.
//
// The one rule that runs through all four: **no response carries the folder
// id or a Drive URL**. The field is write-only, and there is a test asserting
// these bodies contain neither, because the way that property regresses is a
// debug field somebody adds in a year.

// GET /admin/photo-wall
func (d deps) handlePhotoWallStatus(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	status, err := d.db.GetPhotoWallStatus(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// PUT /admin/photo-wall  {"link": "...", "label"?} or {"folder": handle, "label"?}
// A pasted link, or a folder chosen in the picker (GET /admin/google/folders).
// Either way: probe Drive, write the row, empty the set. A failed probe
// writes nothing and leaves the previous folder live.
func (d deps) handleSetPhotoWallFolder(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var in struct {
		Link   string `json:"link"`
		Folder string `json:"folder"`
		Label  string `json:"label"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	var status stockroom.PhotoWallStatus
	var err error
	if in.Folder != "" {
		status, err = d.db.ChoosePhotoWallFolder(r.Context(), actor, in.Folder, in.Label)
	} else {
		status, err = d.db.SetPhotoWallFolder(r.Context(), actor, in.Link, in.Label)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// POST /admin/photo-wall/rebuild
// Re-list the folder that is already live, rather than waiting out the weekly
// refresh. The listing runs in the background; the response is the status
// with Listing about to turn true, which is what the screen polls on.
func (d deps) handleRebuildPhotoWall(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	status, err := d.db.RebuildPhotoWallManifest(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// PUT /admin/photo-wall/size  {"size": 150}
// How many photographs the set holds.
func (d deps) handleSetPhotoWallSize(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var in struct {
		Size int `json:"size"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	status, err := d.db.SetPhotoWallSize(r.Context(), actor, in.Size)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// POST /admin/photo-wall/reshuffle
// Replace every photograph in the set, one at a time, without emptying it.
func (d deps) handleReshufflePhotoWall(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	status, err := d.db.ReshufflePhotoWall(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// GET /admin/photo-wall/preview
// The first six photographs in the set. Reading the set takes nothing from
// it, so the preview costs the sign-in screen nothing.
func (d deps) handlePhotoWallPreview(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	urls, err := d.db.PhotoWallPreview(r.Context(), actor, 0)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"photos": urls})
}
