package main

import (
	"net/http"
	"strconv"

	"stockroom/internal/stockroom"
)

// The sign-in photo wall's two unauthenticated routes
// (docs/design/signin-photo-wall.html §5): the batch endpoint the component
// calls once on mount, and the static mount the resulting <img> tags fetch
// from.
//
// Neither takes a session, and that is not an oversight. The wall renders on
// the sign-in screen, which exists precisely because nobody is signed in yet;
// there is no session to require. §0 is where the consequences of that are
// argued through -- the short version is that the tiles are departmental
// photography, they carry no Drive identifier, the directory cannot be
// enumerated, and the server listens on loopback only.

// signInPhotosResponse is what the component gets. Both fields are always
// present: an empty list is a normal answer, not an error condition, so the
// frontend never has to distinguish a missing key from an empty one.
type signInPhotosResponse struct {
	Photos []string `json:"photos"`
	// TTLSeconds is how long the URLs above stay fetchable. The component does
	// not currently need it -- it fetches once on mount and never polls -- but
	// it is the one number that makes the response self-describing, and a
	// client that ever does want to re-fetch should read it rather than
	// hard-code fifteen minutes.
	TTLSeconds int `json:"ttl_seconds"`
}

// handleSignInPhotos hands out a batch of tile URLs.
//
// It always answers 200, including when there is nothing to give. Not
// configured, rclone missing, the manifest still building, Drive unreachable,
// or a burst of sign-ins draining the reel faster than it refills all produce
// the same empty list, because they all mean the same thing to the only
// caller: draw no columns. §9's invariant is that no failure in this subsystem
// may delay, block or visibly break sign-in, and an error status here would be
// a red herring in the log on the one screen that must never look broken.
func (d deps) handleSignInPhotos(w http.ResponseWriter, r *http.Request) {
	urls, ttl := d.db.PhotoWall.TakePhotos(0)
	if urls == nil {
		// json.Marshal writes a nil slice as null, and the component would
		// then have to guard against it. An empty list is the honest shape.
		urls = []string{}
	}

	// A cached list points at files that have already been deleted, and the
	// tiles it names were marked served the moment this ran -- so replaying it
	// is a wall of 404s rather than a saved request.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, signInPhotosResponse{
		Photos:     urls,
		TTLSeconds: int(ttl.Seconds()),
	})
}

// photoTileServer serves the tiles themselves off local disk.
//
// dir is PhotoWall.TileDir(), never the cache directory: the ownership marker
// and §3's manifest.json live in the root, and the manifest is a listing of
// every photograph's path inside the Drive folder, which §0's second barrier
// says no client may ever receive. http.FileServer refuses to enumerate a
// directory but serves any file in one by name, so the separation is what
// makes that barrier hold -- not this function remembering to exclude a
// filename.
//
// A nil reel returns "" from TileDir, and fileServer answers an empty dir with
// a 404 handler, so the off state needs no branch here or in the router.
func photoTileServer(wall *stockroom.PhotoWall) http.Handler {
	files := fileServer(stockroom.PhotoWallPrefix, wall.TileDir())
	maxAge := strconv.Itoa(int(wall.TTL().Seconds()))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Tile names are random and single-use, so the bytes at a given URL
		// can never change -- immutable is exactly true.
		//
		// The lifetime is the reel's TTL rather than the year a
		// permanently-immutable asset would get, because the server *deletes*
		// the file at the end of that window. A year-long max-age would leave
		// a photograph in the browser's disk cache long after the copy on disk
		// was reaped, which is the opposite of what "delete after use" (§2) is
		// for, and it would buy nothing: the component fetches each URL once
		// and is handed a fresh batch on the next mount.
		//
		// private, because the machine is shared and there is no shared cache
		// on loopback for public to be about.
		w.Header().Set("Cache-Control", "private, max-age="+maxAge+", immutable")
		files.ServeHTTP(w, r)
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

// PUT /admin/photo-wall  {"link": "...", "label": "..."}
// Parse the link, probe Drive, write the row, tear the reel down. A failed
// probe writes nothing and leaves the previous folder live.
func (d deps) handleSetPhotoWallFolder(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var in struct {
		Link  string `json:"link"`
		Label string `json:"label"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	status, err := d.db.SetPhotoWallFolder(r.Context(), actor, in.Link, in.Label)
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

// GET /admin/photo-wall/preview
// Up to six tile URLs that are *not* marked served: a preview must not
// consume the buffer the sign-in screen is about to draw from.
func (d deps) handlePhotoWallPreview(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	urls, err := d.db.PhotoWallPreview(r.Context(), actor, 0)
	if err != nil {
		writeError(w, err)
		return
	}
	if urls == nil {
		urls = []string{}
	}
	// The tiles behind these URLs are still ready, so they may be handed to a
	// real sign-in and reaped a TTL later. Caching the list would point the
	// screen at files that are gone.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"photos": urls})
}
