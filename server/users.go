package main

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"stockroom/internal/stockroom"
)

// GET /users
func (d deps) handleListUsers(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	users, err := d.db.ListUsers(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

// GET /users/{id}
func (d deps) handleGetUser(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	p, err := d.db.GetUser(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// POST /users  body: stockroom.UserInput
// The account starts with no password; it sets one at its first scan login,
// or an admin sets one with POST /users/{id}/password.
func (d deps) handleCreateUser(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var in stockroom.UserInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	p, err := d.db.CreateUser(r.Context(), actor, in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

// PUT /users/{id}  body: stockroom.UserInput
func (d deps) handleUpdateUser(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var in stockroom.UserInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	p, err := d.db.UpdateUser(r.Context(), actor, r.PathValue("id"), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// DELETE /users/{id}
func (d deps) handleDeleteUser(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	if err := d.db.DeleteUser(r.Context(), actor, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /users/{id}/password {"password": "..."}
// Admin reset. SetUserPassword drops that user's sessions itself, so an old
// password cannot keep one alive.
func (d deps) handleSetUserPassword(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var in struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	if err := d.db.SetUserPassword(r.Context(), actor, r.PathValue("id"), in.Password); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// maxRosterBytes caps the CSV upload. A roster is a few hundred lines; the
// photos are referenced by path, not embedded.
const maxRosterBytes = 8 << 20

// POST /users/import
// Accepts either a multipart form with a "file" part (the CSV) and an
// optional "photo_dir" field, or the CSV itself as the body with
// Content-Type text/csv. Relative photo paths in the CSV resolve against
// photo_dir, so a roster with photos comes in as multipart; absolute paths
// are used as they are.
func (d deps) handleImportRoster(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRosterBytes)

	var csvBody io.Reader
	photoDir := ""

	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch {
	case ct == "multipart/form-data":
		if err := r.ParseMultipartForm(maxRosterBytes); err != nil {
			writeError(w, errors.Join(stockroom.ErrInvalid, err))
			return
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			writeError(w, errors.Join(stockroom.ErrInvalid, errors.New(`multipart form needs a "file" part`)))
			return
		}
		defer f.Close()
		csvBody = f
		photoDir = strings.TrimSpace(r.FormValue("photo_dir"))
	case ct == "text/csv":
		csvBody = r.Body
	default:
		writeError(w, errors.Join(stockroom.ErrInvalid, errors.New("send the roster as multipart/form-data or text/csv")))
		return
	}

	res, err := d.db.ImportRoster(r.Context(), actor, csvBody, photoDir)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
