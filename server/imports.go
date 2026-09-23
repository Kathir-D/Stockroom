package main

import (
	"errors"
	"io"
	"mime"
	"net/http"

	"stockroom/internal/stockroom"
)

// Bulk ways in (CLAUDE.md §13, Phase B): the category tree, the asset list,
// and N numbered units of one model. All admin-only inside the package.

// uploadBody reads an import the way POST /users/import always has: either a
// multipart form with a "file" part, which is what the admin panel's file
// picker sends, or the file itself as the body, which is what curl sends.
// `what` names the thing in the refusal, so the message says "category tree"
// rather than "file".
//
// The returned closer is nil for a raw body; the caller defers it otherwise.
func uploadBody(w http.ResponseWriter, r *http.Request, limit int64, what string) (io.Reader, io.Closer, error) {
	r.Body = http.MaxBytesReader(w, r.Body, limit)

	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch ct {
	case "multipart/form-data":
		if err := r.ParseMultipartForm(limit); err != nil {
			return nil, nil, errors.Join(stockroom.ErrInvalid, err)
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			return nil, nil, errors.Join(stockroom.ErrInvalid, errors.New(`multipart form needs a "file" part`))
		}
		return f, f, nil
	case "text/csv", "text/plain", "text/markdown":
		// text/plain and text/markdown because the category import's other
		// shape is an indented outline, and a .md or .txt file is what an
		// admin will have saved one as.
		return r.Body, nil, nil
	default:
		return nil, nil, errors.Join(stockroom.ErrInvalid,
			errors.New("send the "+what+" as multipart/form-data, text/csv or text/plain"))
	}
}

// POST /categories/import
// An indented outline or a type,category,model CSV; the package sniffs which.
func (d deps) handleImportCategories(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	body, closer, err := uploadBody(w, r, maxRosterBytes, "category tree")
	if err != nil {
		writeError(w, err)
		return
	}
	if closer != nil {
		defer closer.Close()
	}
	res, err := d.db.ImportCategories(r.Context(), actor, body)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// POST /assets/import
// A CSV upserted by serial. Per-row failures are in the result, not an error.
func (d deps) handleImportAssets(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	body, closer, err := uploadBody(w, r, maxRosterBytes, "asset list")
	if err != nil {
		writeError(w, err)
		return
	}
	if closer != nil {
		defer closer.Close()
	}
	res, err := d.db.ImportAssets(r.Context(), actor, body)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// POST /assets/bulk-preview
// The serials a bulk add would create, and which of them already exist.
// Writes nothing: the screen shows this list before anything is committed.
func (d deps) handleBulkPreview(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var in stockroom.BulkAddInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	res, err := d.db.BulkPreviewSerials(r.Context(), actor, in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// POST /assets/bulk
// Creates the units the preview listed, in one transaction.
func (d deps) handleBulkAdd(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var in stockroom.BulkAddInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	res, err := d.db.BulkAddAssets(r.Context(), actor, in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

// GET /signin/config
// What the sign-in screen needs before anybody has a session: the character
// class its field filters out. Unauthenticated for the same reason
// /signin/photos is -- the screen that asks for a session cannot hold one --
// and it reveals nothing a sign-in attempt would not.
//
// needs_setup is true while there are no accounts at all, which turns the
// sign-in screen into "create the first admin" (internal/stockroom/setup.go).
// A database error reads as false: the ordinary sign-in screen is the safe
// thing to show when the answer is unknown.
func (d deps) handleSignInConfig(w http.ResponseWriter, r *http.Request) {
	format, _ := stockroom.StudentNumberRule()
	needs, err := d.db.NeedsFirstAdmin(r.Context())
	if err != nil {
		needs = false
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"student_number_format": string(format),
		"student_number_filter": stockroom.StudentNumberFilterPattern(),
		"needs_setup":           needs,
	})
}
