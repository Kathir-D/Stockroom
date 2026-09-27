package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	"stockroom/internal/stockroom"
)

// testDeps opens the database with a long session idle timeout and a temp
// uploads dir, and returns the router plus the raw deps for fixtures.
func testDeps(t *testing.T) (http.Handler, deps) {
	t.Helper()
	db := openTestDB(t)
	t.Cleanup(db.Close)
	db.UploadsDir = t.TempDir()
	d := deps{db: db}
	return newRouter(d), d
}

// seedUser inserts an account with a fresh 9-digit student number that the
// seed never uses and deletes it when the test ends. password == "" leaves
// the hash null (the roster-import shape).
func seedUser(t *testing.T, d deps, isAdmin bool, password string) (id, studentNumber string) {
	t.Helper()
	ctx := context.Background()
	sn := fmt.Sprintf("9%08d", rand.IntN(100_000_000))
	var hash *string
	if password != "" {
		h, err := stockroom.HashPassword(password)
		if err != nil {
			t.Fatal(err)
		}
		hash = &h
	}
	err := d.db.Pool.QueryRow(ctx, `
		insert into profiles (student_number, first_name, last_name, full_name, is_admin, password_hash)
		values ($1, 'HTTP', 'Test', 'HTTP Test', $2, $3) returning id`, sn, isAdmin, hash).Scan(&id)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.db.Pool.Exec(ctx, `delete from profiles where id = $1`, id)
	})
	return id, sn
}

// call sends a JSON request with an optional bearer token and decodes the
// response body into a generic map.
func call(t *testing.T, h http.Handler, method, path, token string, body any) (int, map[string]any) {
	t.Helper()
	var buf io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		buf = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	out := map[string]any{}
	if rec.Body.Len() > 0 {
		var decoded any
		if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("%s %s: body is not JSON: %q", method, path, rec.Body.String())
		}
		if m, ok := decoded.(map[string]any); ok {
			out = m
		} else {
			out["_list"] = decoded
		}
	}
	return rec.Code, out
}

func login(t *testing.T, h http.Handler, sn, password string) string {
	t.Helper()
	code, body := call(t, h, http.MethodPost, "/auth/password", "", map[string]string{"student_number": sn, "password": password})
	if code != http.StatusOK {
		t.Fatalf("login: status %d body %v", code, body)
	}
	return body["token"].(string)
}

// The roster-import flow end to end: scan in with no password, get a limited
// token, be refused everywhere but /me and /auth/set-password, set one, then
// be a normal session.
func TestScanLoginThenSetPassword(t *testing.T) {
	h, d := testDeps(t)
	_, sn := seedUser(t, d, false, "")

	code, body := call(t, h, http.MethodPost, "/auth/scan", "", map[string]string{"student_number": sn})
	if code != http.StatusOK || body["needs_password"] != true {
		t.Fatalf("scan: status %d body %v; want 200 with needs_password", code, body)
	}
	token := body["token"].(string)
	if prof, ok := body["profile"].(map[string]any); !ok || prof["student_number"] != sn {
		t.Errorf("profile in login response = %v", body["profile"])
	}
	if _, leaked := body["profile"].(map[string]any)["password_hash"]; leaked {
		t.Error("login response leaks password_hash")
	}

	if code, _ := call(t, h, http.MethodGet, "/me", token, nil); code != http.StatusOK {
		t.Errorf("GET /me with a limited token = %d, want 200", code)
	}
	code, body = call(t, h, http.MethodGet, "/users", token, nil)
	if code != http.StatusForbidden || body["needs_password"] != true {
		t.Errorf("GET /users with a limited token = %d %v, want 403 needs_password", code, body)
	}

	if code, _ := call(t, h, http.MethodPost, "/auth/set-password", token, map[string]string{"password": "short"}); code != http.StatusBadRequest {
		t.Errorf("set-password(short) = %d, want 400", code)
	}
	if code, _ := call(t, h, http.MethodPost, "/auth/set-password", token, map[string]string{"password": "a real password"}); code != http.StatusOK {
		t.Errorf("set-password = %d, want 200", code)
	}
	if code, _ := call(t, h, http.MethodPost, "/auth/set-password", token, map[string]string{"password": "a real password"}); code != http.StatusConflict {
		t.Errorf("second set-password = %d, want 409", code)
	}
	// Same token, now a full session; /users is admin-only so 403 without
	// the needs_password hint.
	code, body = call(t, h, http.MethodGet, "/users", token, nil)
	if code != http.StatusForbidden || body["needs_password"] == true {
		t.Errorf("GET /users as a non-admin = %d %v, want plain 403", code, body)
	}
	if login(t, h, sn, "a real password") == "" {
		t.Error("typed login after set-password failed")
	}
}

// An admin's card answers 401 with password_required, which is what moves
// the sign-in screen to the password field (CLAUDE.md §7).
func TestAdminScanAsksForPassword(t *testing.T) {
	h, d := testDeps(t)
	_, sn := seedUser(t, d, true, "admin password")
	code, body := call(t, h, http.MethodPost, "/auth/scan", "", map[string]string{"student_number": sn})
	if code != http.StatusUnauthorized || body["password_required"] != true || body["token"] != nil {
		t.Fatalf("admin scan = %d %v, want 401 password_required and no token", code, body)
	}
}

// The logins refuse a non-JSON body, so another site's form cannot post at
// them, and share a rate limit.
func TestSignInRoutesAreGuarded(t *testing.T) {
	h, _ := testDeps(t)
	req := httptest.NewRequest(http.MethodPost, "/auth/scan", bytes.NewReader([]byte(`{"student_number":"1"}`)))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain login = %d, want 415", rec.Code)
	}

	limited := 0
	for range 40 {
		code, _ := call(t, h, http.MethodPost, "/auth/scan", "", map[string]string{"student_number": "999999999999"})
		if code == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited == 0 {
		t.Error("40 scan logins in a row were never rate limited")
	}
}

// A request addressed to a foreign host name is refused, which is what stops
// a DNS-rebinding page using the API as its own origin.
func TestHostCheck(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := withHostCheck(ok, "127.0.0.1:8080")
	for host, want := range map[string]int{
		"127.0.0.1:8080":    http.StatusNoContent,
		"localhost:8080":    http.StatusNoContent,
		"[::1]:8080":        http.StatusNoContent,
		"evil.example:8080": http.StatusMisdirectedRequest,
		"127.0.0.1.nip.io":  http.StatusMisdirectedRequest,
	} {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("Host %s = %d, want %d", host, rec.Code, want)
		}
	}
}

// A card switch ends the old session by its bearer token while the cookie
// already holds the new one, and the logout must leave that cookie alone.
func TestLogoutKeepsAnotherSessionsCookie(t *testing.T) {
	h, d := testDeps(t)
	_, oldSN := seedUser(t, d, false, "old password")
	_, newSN := seedUser(t, d, false, "new password")
	_, oldBody := call(t, h, http.MethodPost, "/auth/scan", "", map[string]string{"student_number": oldSN})
	_, newBody := call(t, h, http.MethodPost, "/auth/scan", "", map[string]string{"student_number": newSN})
	oldTok, _ := oldBody["token"].(string)
	newTok, _ := newBody["token"].(string)
	if oldTok == "" || newTok == "" {
		t.Fatalf("scan logins = %v / %v", oldBody, newBody)
	}

	logout := func(cookie string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
		req.Header.Set("Authorization", "Bearer "+oldTok)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := logout(newTok); rec.Code != http.StatusOK || rec.Header().Get("Set-Cookie") != "" {
		t.Errorf("logout of the old session = %d, Set-Cookie %q; want 200 and no cookie change",
			rec.Code, rec.Header().Get("Set-Cookie"))
	}
	if code, _ := call(t, h, http.MethodGet, "/me", newTok, nil); code != http.StatusOK {
		t.Errorf("new session after the old logout = %d, want 200", code)
	}
}
