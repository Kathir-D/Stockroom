package main

import (
	"net/http"
	"strings"

	"stockroom/internal/stockroom"
)

// sessionCookie is set on login alongside the JSON token so a same-origin
// page can rely on the cookie while the Wails and Vite dev origins send the
// Authorization header instead. Both are accepted on every request.
const sessionCookie = "stockroom_session"

// sessionMode says whether a route accepts a limited session (scan login by
// an account with no password yet, see stockroom.Session.Limited).
type sessionMode int

const (
	fullOnly sessionMode = iota
	allowLimited
)

// withSession resolves the session token into an Actor and passes it to the
// handler. A missing or expired token is a 401; a limited session on a
// fullOnly route is a 403 with a message the UI can key on to send the user
// to the set-password screen.
func (d deps) withSession(next func(http.ResponseWriter, *http.Request, stockroom.Actor), mode sessionMode) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor, err := d.db.Resolve(r.Context(), tokenFrom(r))
		if err != nil {
			writeError(w, err)
			return
		}
		if actor.Limited && mode == fullOnly {
			writeJSON(w, http.StatusForbidden, map[string]any{
				"error":          "password not set",
				"needs_password": true,
			})
			return
		}
		next(w, r, actor)
	})
}

// tokenFrom reads the session token from "Authorization: Bearer <token>"
// first, then the cookie. An Authorization header in some other scheme is
// ignored rather than fatal, so a browser that always sends one still signs
// in on its cookie.
func tokenFrom(r *http.Request) string {
	if tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		if tok = strings.TrimSpace(tok); tok != "" {
			return tok
		}
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		return c.Value
	}
	return ""
}

// setSessionCookie mirrors the token into an HttpOnly cookie. The server is
// localhost-only, so Secure is off; SameSite=Lax keeps it off cross-site
// requests.
func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}
