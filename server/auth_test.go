package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"stockroom/internal/stockroom"
)

// testDeps opens the database, builds an Auth with a long idle timeout and a
// temp uploads dir, and returns the router plus the raw deps for fixtures.
func testDeps(t *testing.T) (http.Handler, deps) {
	t.Helper()
	db := openTestDB(t)
	t.Cleanup(db.Close)
	d := deps{db: db, auth: stockroom.NewAuth(db, time.Hour), uploadsDir: t.TempDir()}
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
		var any_ any
		if err := json.Unmarshal(rec.Body.Bytes(), &any_); err != nil {
			t.Fatalf("%s %s: body is not JSON: %q", method, path, rec.Body.String())
		}
		if m, ok := any_.(map[string]any); ok {
			out = m
		} else {
			out["_list"] = any_
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

func TestLoginErrors(t *testing.T) {
	h, d := testDeps(t)
	_, sn := seedUser(t, d, false, "right-password")
	_, noPw := seedUser(t, d, false, "")

	cases := []struct {
		name string
		path string
		body map[string]string
		want int
	}{
		{"scan unknown", "/auth/scan", map[string]string{"student_number": "900000000"}, http.StatusNotFound},
		{"scan bad number", "/auth/scan", map[string]string{"student_number": "abc"}, http.StatusBadRequest},
		{"scan unknown field", "/auth/scan", map[string]string{"student": sn}, http.StatusBadRequest},
		{"password wrong", "/auth/password", map[string]string{"student_number": sn, "password": "nope"}, http.StatusUnauthorized},
		{"password unknown", "/auth/password", map[string]string{"student_number": "900000000", "password": "nope"}, http.StatusUnauthorized},
		{"password not set", "/auth/password", map[string]string{"student_number": noPw, "password": "nope"}, http.StatusUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if code, body := call(t, h, http.MethodPost, c.path, "", c.body); code != c.want {
				t.Errorf("status %d body %v, want %d", code, body, c.want)
			}
		})
	}
	// "not set" and "wrong" share a status but the message differs, which
	// is what the UI keys on to say "scan your card first".
	_, body := call(t, h, http.MethodPost, "/auth/password", "", map[string]string{"student_number": noPw, "password": "x"})
	if body["error"] != "password not set" {
		t.Errorf("error = %q, want \"password not set\"", body["error"])
	}
}

func TestSessionTokenSources(t *testing.T) {
	h, d := testDeps(t)
	_, sn := seedUser(t, d, false, "cookie-password")

	rec := httptest.NewRecorder()
	b, _ := json.Marshal(map[string]string{"student_number": sn, "password": "cookie-password"})
	req := httptest.NewRequest(http.MethodPost, "/auth/password", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: %d %s", rec.Code, rec.Body.String())
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.Value == "" {
		t.Fatalf("login did not set an HttpOnly session cookie: %+v", cookie)
	}

	// Cookie alone works.
	req = httptest.NewRequest(http.MethodGet, "/me", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("GET /me via cookie = %d", rec.Code)
	}
	// No credentials at all is a 401, as is a garbage bearer.
	if code, _ := call(t, h, http.MethodGet, "/me", "", nil); code != http.StatusUnauthorized {
		t.Errorf("GET /me anonymous = %d, want 401", code)
	}
	if code, _ := call(t, h, http.MethodGet, "/me", "not-a-token", nil); code != http.StatusUnauthorized {
		t.Errorf("GET /me bad token = %d, want 401", code)
	}

	// Logout clears the cookie and kills the token.
	req = httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("logout = %d", rec.Code)
	}
	cleared := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("logout did not clear the cookie")
	}
	if code, _ := call(t, h, http.MethodGet, "/me", cookie.Value, nil); code != http.StatusUnauthorized {
		t.Errorf("GET /me after logout = %d, want 401", code)
	}
}

func TestUsersCRUDOverHTTP(t *testing.T) {
	h, d := testDeps(t)
	_, adminSN := seedUser(t, d, true, "admin-password")
	admin := login(t, h, adminSN, "admin-password")
	newSN := fmt.Sprintf("9%08d", rand.IntN(100_000_000))
	t.Cleanup(func() {
		_, _ = d.db.Pool.Exec(context.Background(), `delete from profiles where student_number = $1`, newSN)
	})

	code, body := call(t, h, http.MethodPost, "/users", admin, map[string]any{
		"student_number": newSN, "first_name": "New", "last_name": "User", "email": nil, "photo_path": nil, "is_admin": false,
	})
	if code != http.StatusCreated {
		t.Fatalf("POST /users = %d %v", code, body)
	}
	id := body["id"].(string)

	if code, _ := call(t, h, http.MethodPost, "/users", admin, map[string]any{
		"student_number": newSN, "first_name": "Dup", "last_name": "", "email": nil, "photo_path": nil, "is_admin": false,
	}); code != http.StatusConflict {
		t.Errorf("duplicate POST /users = %d, want 409", code)
	}

	code, body = call(t, h, http.MethodGet, "/users/"+id, admin, nil)
	if code != http.StatusOK || body["first_name"] != "New" {
		t.Errorf("GET /users/{id} = %d %v", code, body)
	}
	if code, _ := call(t, h, http.MethodGet, "/users/not-a-uuid", admin, nil); code != http.StatusBadRequest {
		t.Errorf("GET /users/not-a-uuid = %d, want 400", code)
	}

	code, body = call(t, h, http.MethodPut, "/users/"+id, admin, map[string]any{
		"student_number": newSN, "first_name": "Renamed", "last_name": "User", "email": "n@school.edu", "photo_path": nil, "is_admin": false,
	})
	if code != http.StatusOK || body["first_name"] != "Renamed" || body["email"] != "n@school.edu" {
		t.Errorf("PUT /users/{id} = %d %v", code, body)
	}

	code, body = call(t, h, http.MethodGet, "/users", admin, nil)
	if code != http.StatusOK {
		t.Fatalf("GET /users = %d", code)
	}
	if list, ok := body["_list"].([]any); !ok || len(list) < 2 {
		t.Errorf("GET /users returned %v, want a list", body)
	}

	// Password reset signs the user out everywhere.
	if code, _ := call(t, h, http.MethodPost, "/users/"+id+"/password", admin, map[string]string{"password": "reset by admin"}); code != http.StatusOK {
		t.Errorf("POST /users/{id}/password = %d", code)
	}
	userTok := login(t, h, newSN, "reset by admin")
	if code, _ := call(t, h, http.MethodPost, "/users/"+id+"/password", admin, map[string]string{"password": "reset again!"}); code != http.StatusOK {
		t.Errorf("second reset = %d", code)
	}
	if code, _ := call(t, h, http.MethodGet, "/me", userTok, nil); code != http.StatusUnauthorized {
		t.Errorf("session survived a password reset: %d", code)
	}

	// Non-admins get 403 on every user route.
	userTok = login(t, h, newSN, "reset again!")
	for _, r := range []struct{ m, p string }{
		{http.MethodGet, "/users"}, {http.MethodGet, "/users/" + id}, {http.MethodPut, "/users/" + id},
		{http.MethodDelete, "/users/" + id}, {http.MethodPost, "/users/" + id + "/password"}, {http.MethodPost, "/users"},
	} {
		var b any
		switch {
		case strings.HasSuffix(r.p, "/password"):
			b = map[string]any{"password": "long enough"}
		case r.m == http.MethodPost || r.m == http.MethodPut:
			b = map[string]any{"student_number": "1", "first_name": "x"}
		}
		if code, _ := call(t, h, r.m, r.p, userTok, b); code != http.StatusForbidden {
			t.Errorf("%s %s as non-admin = %d, want 403", r.m, r.p, code)
		}
	}

	if code, _ := call(t, h, http.MethodDelete, "/users/"+id, admin, nil); code != http.StatusOK {
		t.Errorf("DELETE /users/{id} = %d", code)
	}
	if code, _ := call(t, h, http.MethodGet, "/me", userTok, nil); code != http.StatusUnauthorized {
		t.Errorf("deleted user's session still works: %d", code)
	}
	if code, _ := call(t, h, http.MethodDelete, "/users/"+id, admin, nil); code != http.StatusNotFound {
		t.Errorf("second DELETE = %d, want 404", code)
	}
}

func TestImportRosterOverHTTP(t *testing.T) {
	h, d := testDeps(t)
	_, adminSN := seedUser(t, d, true, "admin-password")
	admin := login(t, h, adminSN, "admin-password")
	sn := fmt.Sprintf("9%08d", rand.IntN(100_000_000))
	t.Cleanup(func() {
		_, _ = d.db.Pool.Exec(context.Background(), `delete from profiles where student_number = $1`, sn)
	})
	photoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(photoDir, "s.jpg"), []byte("jpg"), 0o644); err != nil {
		t.Fatal(err)
	}
	csv := "first_name,last_name,student_number,photo_path\nRoster,Kid," + sn + ",s.jpg\nbad,row,xx,\n"

	// Multipart with photo_dir.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "roster.csv")
	fw.Write([]byte(csv))
	mw.WriteField("photo_dir", photoDir)
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/users/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+admin)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("multipart import = %d %s", rec.Code, rec.Body.String())
	}
	var res stockroom.RosterResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Created != 1 || res.Failed != 1 || len(res.Rows) != 2 {
		t.Errorf("result = %+v", res)
	}
	if _, err := os.Stat(filepath.Join(d.uploadsDir, "profiles", sn+".jpg")); err != nil {
		t.Errorf("photo not copied into the configured uploads dir: %v", err)
	}

	// Raw text/csv body, re-import updates.
	req = httptest.NewRequest(http.MethodPost, "/users/import?photo_dir="+photoDir, strings.NewReader(csv))
	req.Header.Set("Content-Type", "text/csv")
	req.Header.Set("Authorization", "Bearer "+admin)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("text/csv import = %d %s", rec.Code, rec.Body.String())
	}
	json.Unmarshal(rec.Body.Bytes(), &res)
	if res.Updated != 1 {
		t.Errorf("re-import result = %+v, want 1 updated", res)
	}

	// Wrong content type and a missing header row are 400s.
	req = httptest.NewRequest(http.MethodPost, "/users/import", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+admin)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("JSON body import = %d, want 400", rec.Code)
	}
	req = httptest.NewRequest(http.MethodPost, "/users/import", strings.NewReader("first_name\nx\n"))
	req.Header.Set("Content-Type", "text/csv")
	req.Header.Set("Authorization", "Bearer "+admin)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("import without required columns = %d, want 400", rec.Code)
	}
}

// Routing for the new paths is asserted without a database.
func TestAuthRoutesRejectWrongMethod(t *testing.T) {
	h := newRouter(deps{})
	for _, p := range []string{"/auth/scan", "/auth/password", "/auth/set-password", "/auth/logout"} {
		if rec := do(h, http.MethodGet, p); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s = %d, want 405", p, rec.Code)
		}
	}
	if rec := do(h, http.MethodPost, "/me"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /me = %d, want 405", rec.Code)
	}
}
