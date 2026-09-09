package main

import (
	"net/http"

	"stockroom/internal/stockroom"
)

// POST /auth/scan {"student_number": "..."}
// The frontend calls this when the keystroke burst looked like a scan
// (CLAUDE.md §10). Responds with the session token; needs_password = true
// means the token is limited and the UI must show the set-password screen.
func (d deps) handleLoginByScan(w http.ResponseWriter, r *http.Request) {
	var in struct {
		StudentNumber string `json:"student_number"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	res, err := d.auth.LoginByScan(r.Context(), in.StudentNumber)
	if err != nil {
		writeError(w, err)
		return
	}
	setSessionCookie(w, res.Token)
	writeJSON(w, http.StatusOK, res)
}

// POST /auth/password {"student_number": "...", "password": "..."}
func (d deps) handleLoginByPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		StudentNumber string `json:"student_number"`
		Password      string `json:"password"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	res, err := d.auth.LoginByPassword(r.Context(), in.StudentNumber, in.Password)
	if err != nil {
		writeError(w, err)
		return
	}
	setSessionCookie(w, res.Token)
	writeJSON(w, http.StatusOK, res)
}

// POST /auth/set-password {"password": "..."}
// Only for an account with no password yet; upgrades the limited session.
func (d deps) handleSetInitialPassword(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var in struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	if err := d.auth.SetInitialPassword(r.Context(), actor, in.Password); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /auth/logout
func (d deps) handleLogout(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	d.auth.Logout(actor)
	clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// GET /me
func (d deps) handleMe(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	res, err := d.auth.Me(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
