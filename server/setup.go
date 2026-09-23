package main

import (
	"log"
	"net/http"

	"stockroom/examples"
	"stockroom/internal/stockroom"
)

// The first-run wizard (internal/stockroom/setup.go). One route with no
// session -- creating the first admin, which refuses once anyone exists --
// and the rest admin-only inside the package like everything else.

// POST /setup/admin
func (d deps) handleCreateFirstAdmin(w http.ResponseWriter, r *http.Request) {
	var in stockroom.FirstAdminInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	res, err := d.db.CreateFirstAdmin(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	setSessionCookie(w, res.Token)
	writeJSON(w, http.StatusCreated, res)
}

// GET /setup
func (d deps) handleGetSetup(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	st, err := d.db.GetSetup(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// PUT /setup {"step": n, "completed": bool}
func (d deps) handleSaveSetup(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var in struct {
		Step      int  `json:"step"`
		Completed bool `json:"completed"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	st, err := d.db.SaveSetupProgress(r.Context(), actor, in.Step, in.Completed)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// POST /setup/failsafe {"student_number", "password"}
func (d deps) handleConfigureFailsafe(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var in struct {
		StudentNumber string `json:"student_number"`
		Password      string `json:"password"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	if err := d.db.ConfigureFailsafe(r.Context(), actor, in.StudentNumber, in.Password); err != nil {
		writeError(w, err)
		return
	}
	// Logged without the number: the log is readable by more people than the
	// .env it was written into.
	log.Printf("setup: failsafe admin written to the settings file by %s", actor.ID)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// POST /setup/examples
func (d deps) handleLoadExamples(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	res, err := d.db.LoadExamples(r.Context(), actor, examples.FS)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// DELETE /setup/examples
func (d deps) handleRemoveExamples(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	res, err := d.db.RemoveExamples(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
