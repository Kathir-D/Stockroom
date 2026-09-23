package main

import (
	"errors"
	"fmt"
	"net/http"

	"stockroom/internal/stockroom"
)

// The admin-panel routes (TODO Phase 5): asset CRUD and photos, the category
// tree's writes, and Backup Now. Every one of them is admin-only, and none of
// them checks that here: RequireAdmin lives inside internal/stockroom so the
// rule survives a router edit (CLAUDE.md §7). These handlers decode, call,
// encode.

// maxPhotoBytes caps an asset photo upload. Big enough for a phone picture of
// a camera body, small enough that a mistaken video upload fails fast.
const maxPhotoBytes = 10 << 20

// POST /assets  body: stockroom.AssetInput
// Creates one unit, available, and answers with the same shape GET
// /assets/{id} returns so the admin table can insert the row directly.
func (d deps) handleCreateAsset(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var in stockroom.AssetInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	asset, err := d.db.CreateAsset(r.Context(), actor, in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, asset)
}

// PUT /assets/{id}  body: stockroom.AssetInput
func (d deps) handleUpdateAsset(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var in stockroom.AssetInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	asset, err := d.db.UpdateAsset(r.Context(), actor, r.PathValue("id"), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, asset)
}

// DELETE /assets/{id}
// Refused with 409 for anything that has ever been checked out; the message
// says to mark it unavailable instead, and the UI should show it as written.
func (d deps) handleDeleteAsset(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	if err := d.db.DeleteAsset(r.Context(), actor, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /assets/{id}/status {"status": "unavailable"}
// The available/unavailable toggle. checked_out is not an accepted value.
func (d deps) handleSetAssetStatus(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var in struct {
		Status stockroom.AssetStatus `json:"status"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	asset, err := d.db.SetAssetStatus(r.Context(), actor, r.PathValue("id"), in.Status)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, asset)
}

// POST /assets/{id}/photo
// A multipart form with the picture in a "photo" part. The response is the
// asset, whose photo_url points at the copy just written under /files/.
func (d deps) handleSetAssetPhoto(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	r.Body = http.MaxBytesReader(w, r.Body, maxPhotoBytes)
	if err := r.ParseMultipartForm(maxPhotoBytes); err != nil {
		writeError(w, errors.Join(stockroom.ErrInvalid, err))
		return
	}
	file, header, err := r.FormFile("photo")
	if err != nil {
		writeError(w, errors.Join(stockroom.ErrInvalid, errors.New(`multipart form needs a "photo" part`)))
		return
	}
	defer file.Close()

	asset, err := d.db.SetAssetPhoto(r.Context(), actor, r.PathValue("id"), header.Filename, file)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, asset)
}

// POST /categories  body: stockroom.CategoryInput
func (d deps) handleCreateCategory(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var in stockroom.CategoryInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	c, err := d.db.CreateCategory(r.Context(), actor, in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

// PUT /categories/{id}  body: stockroom.CategoryInput
// Rename, move, or renumber. Omitting sort_order leaves the node where it is.
func (d deps) handleUpdateCategory(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var in stockroom.CategoryInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	c, err := d.db.UpdateCategory(r.Context(), actor, r.PathValue("id"), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// DELETE /categories/{id}
func (d deps) handleDeleteCategory(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	if err := d.db.DeleteCategory(r.Context(), actor, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /admin/backup
// Runs the CSV export into BACKUP_DIR and answers with the folder it wrote and
// a row count per table. With BACKUP_DIR unset the answer is 503 naming the
// variable, because that is a line missing from .env, not a bad request.
func (d deps) handleBackupNow(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	res, err := d.db.BackupNow(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// GET /admin/export
// The whole database as the backup's zip, downloaded rather than written to
// the backup folder. Works with backups never configured, which is the point:
// leaving Stockroom must not depend on having set it up properly.
func (d deps) handleExport(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	exp, err := d.db.ExportEverything(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", exp.Filename))
	// It holds every student number beside its password hash. No cache, on
	// this machine or any proxy, may keep a copy.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(exp.Archive)
}
