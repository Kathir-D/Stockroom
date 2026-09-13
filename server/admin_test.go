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
	"testing"

	"stockroom/internal/stockroom"
)

// The Phase 5 routes over HTTP. The rules are pinned one layer down in
// internal/stockroom; these check the wire: that the routes exist and don't
// shadow the Phase 3 and 4 ones on the same paths, which sentinel becomes
// which status, and that a photo upload survives a multipart round trip.

// adminToken seeds an admin and signs in as them.
func adminToken(t *testing.T, h http.Handler, d deps) string {
	t.Helper()
	_, sn := seedUser(t, d, true, "admin-route-password")
	return login(t, h, sn, "admin-route-password")
}

// newAssetTag is a tag no other row is using.
func newAssetTag() string {
	return fmt.Sprintf("HTTP-ADM-%09d", rand.IntN(1_000_000_000))
}

// dropAsset removes an asset (and, by cascade, its custody rows) at the end
// of the test, for rows created through the API rather than through
// seedAsset.
func dropAsset(t *testing.T, d deps, id string) {
	t.Helper()
	t.Cleanup(func() { _, _ = d.db.Pool.Exec(context.Background(), `delete from assets where id = $1`, id) })
}

func TestAssetAdminRoutes(t *testing.T) {
	h, d := testDeps(t)
	token := adminToken(t, h, d)
	tag := newAssetTag()

	code, body := call(t, h, http.MethodPost, "/assets", token, map[string]any{
		"asset_tag": tag, "name": "Route camera", "serial_number": tag + "-S",
	})
	if code != http.StatusCreated {
		t.Fatalf("POST /assets = %d %v, want 201", code, body)
	}
	id, _ := body["id"].(string)
	if id == "" {
		t.Fatalf("POST /assets returned no id: %v", body)
	}
	dropAsset(t, d, id)
	// The create response is the detail payload, so the admin table can use
	// it without a second fetch.
	if _, ok := body["category_path"]; !ok {
		t.Errorf("create response is missing category_path: %v", body)
	}
	if body["status"] != string(stockroom.StatusAvailable) {
		t.Errorf("status = %v, want available", body["status"])
	}

	code, body = call(t, h, http.MethodPut, "/assets/"+id, token, map[string]any{
		"asset_tag": tag, "name": "Renamed over HTTP",
	})
	if code != http.StatusOK || body["name"] != "Renamed over HTTP" {
		t.Fatalf("PUT /assets/{id} = %d %v", code, body)
	}

	code, body = call(t, h, http.MethodPost, "/assets/"+id+"/status", token, map[string]any{"status": "unavailable"})
	if code != http.StatusOK || body["status"] != "unavailable" {
		t.Fatalf("POST /assets/{id}/status = %d %v", code, body)
	}

	if code, body = call(t, h, http.MethodDelete, "/assets/"+id, token, nil); code != http.StatusOK {
		t.Fatalf("DELETE /assets/{id} = %d %v", code, body)
	}
	if code, _ = call(t, h, http.MethodGet, "/assets/"+id, token, nil); code != http.StatusNotFound {
		t.Errorf("GET after delete = %d, want 404", code)
	}
}

// Every Phase 5 route is admin-only, and the refusal comes from the package,
// not the router. A student with a full session gets 403 from all of them.
func TestAdminRoutesRefuseAStudent(t *testing.T) {
	h, d := testDeps(t)
	_, sn := seedUser(t, d, false, "student-route-password")
	token := login(t, h, sn, "student-route-password")
	id, _ := seedAsset(t, d, stockroom.StatusAvailable)
	var categoryID string
	if err := d.db.Pool.QueryRow(context.Background(), `select id from categories limit 1`).Scan(&categoryID); err != nil {
		t.Fatalf("select category for guarded routes: %v", err)
	}

	routes := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodPost, "/assets", map[string]any{"asset_tag": newAssetTag(), "name": "No"}},
		{http.MethodPut, "/assets/" + id, map[string]any{"asset_tag": newAssetTag(), "name": "No"}},
		{http.MethodDelete, "/assets/" + id, nil},
		{http.MethodPost, "/assets/" + id + "/status", map[string]any{"status": "unavailable"}},
		{http.MethodPost, "/categories", map[string]any{"name": fmt.Sprintf("ZZ HTTP Denied %09d", rand.IntN(1_000_000_000))}},
		{http.MethodPut, "/categories/" + categoryID, map[string]any{"name": "Denied"}},
		{http.MethodDelete, "/categories/" + categoryID, nil},
		{http.MethodPost, "/admin/backup", nil},
	}
	for _, r := range routes {
		if code, body := call(t, h, r.method, r.path, token, r.body); code != http.StatusForbidden {
			t.Errorf("%s %s as a student = %d %v, want 403", r.method, r.path, code, body)
		}
	}
	if code, body := uploadPhoto(t, h, token, "/assets/"+id+"/photo", "x.png", []byte("x")); code != http.StatusForbidden {
		t.Errorf("photo upload as a student = %d %v, want 403", code, body)
	}
}

// uploadPhoto posts a multipart form with the bytes in a "photo" part.
func uploadPhoto(t *testing.T, h http.Handler, token, path, filename string, content []byte) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	part, err := form.CreateFormFile("photo", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	out := map[string]any{}
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("POST %s: body is not a JSON object: %q", path, rec.Body.String())
		}
	}
	return rec.Code, out
}
