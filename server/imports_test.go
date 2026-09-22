package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The Phase B routes over the wire: that they exist, accept both body shapes,
// and refuse a student. The rules are pinned in internal/stockroom.

func postFile(t *testing.T, h http.Handler, path, token, filename, content string) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", filename)
	_, _ = fw.Write([]byte(content))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestImportRoutes(t *testing.T) {
	h, d := testDeps(t)
	token := adminToken(t, h, d)
	ctx := context.Background()
	n := rand.IntN(1_000_000_000)
	typ := fmt.Sprintf("ZZ HTTP Type %09d", n)
	serial := fmt.Sprintf("HTTP-IMP-%09d", n)
	t.Cleanup(func() {
		_, _ = d.db.Pool.Exec(ctx, `delete from assets where serial_number = $1`, serial)
		_, _ = d.db.Pool.Exec(ctx, `delete from categories where name = $1`, typ)
	})

	// Multipart, the admin panel's shape, with an outline in a .md file.
	code, body := postFile(t, h, "/categories/import", token, "tree.md", typ+"\n")
	if code != http.StatusOK || body["created"] != float64(1) {
		t.Fatalf("POST /categories/import = %d %v", code, body)
	}

	// A raw text/csv body, curl's shape, filing under the category just made.
	req := httptest.NewRequest(http.MethodPost, "/assets/import",
		strings.NewReader("serial_number,name,category\n"+serial+",Imported,"+typ+"\n"))
	req.Header.Set("Content-Type", "text/csv")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"created":1`) {
		t.Fatalf("POST /assets/import = %d %s", rec.Code, rec.Body.String())
	}

	// Anything else is a 400 that says what to send.
	if code, body := call(t, h, http.MethodPost, "/assets/import", token, map[string]any{"x": 1}); code != http.StatusBadRequest {
		t.Errorf("JSON body to /assets/import = %d %v, want 400", code, body)
	}
}

func TestBulkRoutes(t *testing.T) {
	h, d := testDeps(t)
	token := adminToken(t, h, d)
	prefix := fmt.Sprintf("HTB%06d", rand.IntN(1_000_000))
	t.Cleanup(func() {
		_, _ = d.db.Pool.Exec(context.Background(), `delete from assets where serial_number like $1`, prefix+"-%")
	})
	in := map[string]any{"name": "HTTP battery", "prefix": prefix, "count": 2}

	code, body := call(t, h, http.MethodPost, "/assets/bulk-preview", token, in)
	if code != http.StatusOK || fmt.Sprint(body["serials"]) != fmt.Sprintf("[%s-001 %s-002]", prefix, prefix) {
		t.Fatalf("POST /assets/bulk-preview = %d %v", code, body)
	}
	code, body = call(t, h, http.MethodPost, "/assets/bulk", token, in)
	if list, _ := body["_list"].([]any); code != http.StatusCreated || len(list) != 2 {
		t.Fatalf("POST /assets/bulk = %d %v, want 201 with two assets", code, body)
	}
}

func TestImportRoutesRefuseAStudent(t *testing.T) {
	h, d := testDeps(t)
	_, sn := seedUser(t, d, false, "student-route-password")
	token := login(t, h, sn, "student-route-password")
	for _, path := range []string{"/categories/import", "/assets/import"} {
		if code, body := postFile(t, h, path, token, "x.csv", "serial_number,name\nA,B\n"); code != http.StatusForbidden {
			t.Errorf("%s as a student = %d %v, want 403", path, code, body)
		}
	}
	for _, path := range []string{"/assets/bulk-preview", "/assets/bulk"} {
		if code, body := call(t, h, http.MethodPost, path, token, map[string]any{"name": "x", "prefix": "X", "count": 1}); code != http.StatusForbidden {
			t.Errorf("%s as a student = %d %v, want 403", path, code, body)
		}
	}
}

// The sign-in screen reads this with no session at all.
func TestSignInConfigIsPublic(t *testing.T) {
	h, _ := testDeps(t)
	code, body := call(t, h, http.MethodGet, "/signin/config", "", nil)
	if code != http.StatusOK || body["student_number_format"] == nil {
		t.Fatalf("GET /signin/config = %d %v", code, body)
	}
}
