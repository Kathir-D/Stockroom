package main

import (
	"net/http"

	"stockroom/internal/stockroom"
)

// The one Google connection and the two folder pickers
// (internal/stockroom/google_admin.go, local_folders.go). Every route is
// admin-only, enforced inside the package.

// GET /admin/google
func (d deps) handleGoogleStatus(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	st, err := d.db.GetGoogleStatus(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// POST /admin/google/connect
// Starts `rclone authorize` and returns Google's page for the screen to open.
func (d deps) handleGoogleConnect(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	res, err := d.db.ConnectGoogle(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// POST /admin/google/finish  {"id", "code"?}
// {"done": false} while Google has not called back; the screen polls. `code`
// is the block rclone printed, for a sign-in done on another machine.
func (d deps) handleGoogleFinish(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var in struct {
		ID   string `json:"id"`
		Code string `json:"code"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	res, err := d.db.FinishGoogle(r.Context(), actor, in.ID, in.Code)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// GET /admin/google/folders?in=my-drive|shared|<handle>
func (d deps) handleGoogleFolders(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	folders, err := d.db.GoogleFolders(r.Context(), actor, r.URL.Query().Get("in"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"folders": folders})
}

// POST /admin/google/folders  {"in", "name"}
func (d deps) handleCreateGoogleFolder(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var in struct {
		In   string `json:"in"`
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	f, err := d.db.CreateGoogleFolder(r.Context(), actor, in.In, in.Name)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

// GET /admin/local-folders?path=
func (d deps) handleLocalFolders(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	list, err := d.db.LocalFolders(r.Context(), actor, r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// POST /admin/local-folders  {"parent", "name"}
func (d deps) handleCreateLocalFolder(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var in struct {
		Parent string `json:"parent"`
		Name   string `json:"name"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	f, err := d.db.CreateLocalFolder(r.Context(), actor, in.Parent, in.Name)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}
