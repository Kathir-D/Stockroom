package main

import (
	"errors"
	"net/http"

	"stockroom/internal/stockroom"
)

// The backup, restore, settings and photo-mirror routes (docs/design/backup.md
// §E.8). Every one is admin-only, and none of them checks that here:
// RequireAdmin lives inside internal/stockroom so the rule survives a router
// edit (CLAUDE.md §7). These handlers decode, call, encode.

// maxRestoreUploadBytes caps an uploaded archive. It matches the package's own
// cap; having it here too means a 300 MB upload is refused before it is read
// into memory rather than after.
const maxRestoreUploadBytes = 200 << 20

// GET /admin/settings
// The secrets come back blank with a boolean saying whether one is stored.
func (d deps) handleGetSettings(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	s, err := d.db.GetSettings(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// PUT /admin/settings  body: stockroom.SettingsInput
// A partial update: an omitted field is left alone, which is what lets one
// card on the settings screen save without carrying the others.
func (d deps) handleSaveSettings(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var in stockroom.SettingsInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	s, err := d.db.SaveSettings(r.Context(), actor, in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// POST /admin/settings/test  {"target": "drive"|"github"}
// Answers 200 with {ok:true} or the failure as a readable message, because
// "test connection" that says only "failed" is a button nobody presses twice.
func (d deps) handleTestTarget(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var in struct {
		Target string `json:"target"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	if err := d.db.TestBackupTarget(r.Context(), actor, in.Target); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /admin/drive/connect
// Starts `rclone authorize drive` and hands back the link to open.
func (d deps) handleDriveConnect(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	res, err := d.db.ConnectDrive(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// POST /admin/drive/finish  {"id", "code"?, "remote"?}
// Completes the connection: `code` is the block rclone printed, for the case
// where the browser callback did not reach it.
func (d deps) handleDriveFinish(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var in struct {
		ID     string `json:"id"`
		Code   string `json:"code"`
		Remote string `json:"remote"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	s, err := d.db.FinishDriveConnect(r.Context(), actor, in.ID, in.Code, in.Remote)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// GET /admin/backup/status
func (d deps) handleBackupStatus(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	res, err := d.db.BackupStatus(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// GET /admin/backup/versions?target=local|drive|github
func (d deps) handleBackupVersions(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	target := r.URL.Query().Get("target")
	if target == "" {
		target = "local"
	}
	versions, err := d.db.ListBackupVersions(r.Context(), actor, target)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, versions)
}

// POST /admin/restore
// multipart: `file` (the archive) + `confirm=RESTORE`, plus optional
// `passphrase` and `force`. Follows the POST /users/import pattern.
func (d deps) handleRestoreUpload(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRestoreUploadBytes)
	// 32 MB in memory, the rest spooled to a temp file: an archive is read
	// whole either way, and holding a 200 MB upload in RAM to then copy it is
	// twice the memory for no gain.
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, errors.Join(stockroom.ErrInvalid, err))
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, errors.Join(stockroom.ErrInvalid, errors.New(`multipart form needs a "file" part holding the backup archive`)))
		return
	}
	defer file.Close()

	opts := stockroom.RestoreOptions{
		Confirm:    r.FormValue("confirm"),
		Passphrase: r.FormValue("passphrase"),
		Force:      r.FormValue("force") == "true",
		Source:     "upload",
	}
	res, err := d.db.RestoreFromReader(r.Context(), actor, file, opts)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// POST /admin/restore/remote  {"target", "id", "confirm", "passphrase"?, "force"?}
// The same restore, with the bytes fetched from a target rather than uploaded.
func (d deps) handleRestoreRemote(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var in struct {
		Target     string `json:"target"`
		ID         string `json:"id"`
		Confirm    string `json:"confirm"`
		Passphrase string `json:"passphrase"`
		Force      bool   `json:"force"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	res, err := d.db.RestoreFromTarget(r.Context(), actor, in.Target, in.ID, stockroom.RestoreOptions{
		Confirm:    in.Confirm,
		Passphrase: in.Passphrase,
		Force:      in.Force,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// GET /admin/photos/generations
func (d deps) handlePhotoGenerations(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	status, err := d.db.GetPhotoMirrorStatus(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// POST /admin/photos/restore  {"generation"}
func (d deps) handleRestorePhotos(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var in struct {
		Generation string `json:"generation"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	restored, err := d.db.RestorePhotos(r.Context(), actor, in.Generation)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "restored": restored})
}

// DELETE /admin/photos/generations/{name}
// The only deletion in the photo mirror, and it is a person pressing a button:
// nothing is ever purged automatically, because auto-purging the only copy of
// a deleted photo defeats the mirror.
func (d deps) handleDeletePhotoGeneration(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	if err := d.db.DeletePhotoGeneration(r.Context(), actor, r.PathValue("name")); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
