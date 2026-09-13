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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestAssetAdminRouteStatuses(t *testing.T) {
	h, d := testDeps(t)
	token := adminToken(t, h, d)
	tag := newAssetTag()

	_, body := call(t, h, http.MethodPost, "/assets", token, map[string]any{"asset_tag": tag, "name": "Status cases"})
	id, _ := body["id"].(string)
	dropAsset(t, d, id)

	cases := []struct {
		name   string
		method string
		path   string
		body   any
		want   int
	}{
		{"duplicate tag", http.MethodPost, "/assets", map[string]any{"asset_tag": tag, "name": "Twin"}, http.StatusConflict},
		{"no name", http.MethodPost, "/assets", map[string]any{"asset_tag": newAssetTag()}, http.StatusBadRequest},
		{"unknown field", http.MethodPost, "/assets", map[string]any{"asset_tag": newAssetTag(), "name": "X", "colour": "red"}, http.StatusBadRequest},
		{"unknown category", http.MethodPost, "/assets", map[string]any{"asset_tag": newAssetTag(), "name": "X", "category_id": "0f3d3a9e-0000-4000-8000-000000000000"}, http.StatusNotFound},
		{"malformed id", http.MethodPut, "/assets/not-a-uuid", map[string]any{"asset_tag": tag, "name": "X"}, http.StatusBadRequest},
		{"unknown id", http.MethodPut, "/assets/0f3d3a9e-0000-4000-8000-000000000000", map[string]any{"asset_tag": newAssetTag(), "name": "X"}, http.StatusNotFound},
		{"checked_out is not a toggle value", http.MethodPost, "/assets/" + id + "/status", map[string]any{"status": "checked_out"}, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if code, body := call(t, h, c.method, c.path, token, c.body); code != c.want {
				t.Errorf("%s %s = %d %v, want %d", c.method, c.path, code, body, c.want)
			}
		})
	}

	// An item in someone's hands: delete and the status toggle both answer
	// 409, and the message has to say which item and why.
	t.Run("held by someone", func(t *testing.T) {
		heldID, _ := seedAsset(t, d, stockroom.StatusAvailable)
		holder, _ := seedUser(t, d, false, "holder-password")
		checkOut(t, d, heldID, holder, time.Now().Add(24*time.Hour))

		if code, body := call(t, h, http.MethodDelete, "/assets/"+heldID, token, nil); code != http.StatusConflict {
			t.Errorf("DELETE a checked-out asset = %d %v, want 409", code, body)
		}
		if code, body := call(t, h, http.MethodPost, "/assets/"+heldID+"/status", token,
			map[string]any{"status": "unavailable"}); code != http.StatusConflict {
			t.Errorf("toggling a checked-out asset = %d %v, want 409", code, body)
		}
	})
}

func TestCategoryAdminRoutes(t *testing.T) {
	h, d := testDeps(t)
	token := adminToken(t, h, d)
	ctx := context.Background()
	name := fmt.Sprintf("ZZ HTTP Type %09d", rand.IntN(1_000_000_000))

	code, body := call(t, h, http.MethodPost, "/categories", token, map[string]any{"name": name})
	if code != http.StatusCreated {
		t.Fatalf("POST /categories = %d %v, want 201", code, body)
	}
	id, _ := body["id"].(string)
	t.Cleanup(func() { _, _ = d.db.Pool.Exec(ctx, `delete from categories where id = $1`, id) })
	if body["sort_order"] == nil {
		t.Errorf("create response is missing sort_order: %v", body)
	}

	code, body = call(t, h, http.MethodPut, "/categories/"+id, token,
		map[string]any{"name": name + " renamed", "sort_order": 3})
	if code != http.StatusOK || body["name"] != name+" renamed" || body["sort_order"] != float64(3) {
		t.Fatalf("PUT /categories/{id} = %d %v", code, body)
	}

	// A node with something under it cannot be deleted, and the message says
	// which of the two reasons it is.
	code, child := call(t, h, http.MethodPost, "/categories", token,
		map[string]any{"name": name + " child", "parent_id": id})
	if code != http.StatusCreated {
		t.Fatalf("POST /categories child = %d %v", code, child)
	}
	childID, _ := child["id"].(string)
	t.Cleanup(func() { _, _ = d.db.Pool.Exec(ctx, `delete from categories where id = $1`, childID) })

	if code, body = call(t, h, http.MethodDelete, "/categories/"+id, token, nil); code != http.StatusConflict {
		t.Errorf("DELETE a category with children = %d %v, want 409", code, body)
	}
	if code, body = call(t, h, http.MethodDelete, "/categories/"+childID, token, nil); code != http.StatusOK {
		t.Errorf("DELETE an empty category = %d %v", code, body)
	}
	if code, body = call(t, h, http.MethodDelete, "/categories/"+childID, token, nil); code != http.StatusNotFound {
		t.Errorf("DELETE it twice = %d %v, want 404", code, body)
	}

	// The tree read is the same route the browse filter uses, and it has to
	// show the edit immediately.
	code, tree := call(t, h, http.MethodGet, "/categories/tree", token, nil)
	if code != http.StatusOK {
		t.Fatalf("GET /categories/tree = %d %v", code, tree)
	}
	nodes, _ := tree["_list"].([]any)
	found := false
	for _, n := range nodes {
		if node, ok := n.(map[string]any); ok && node["id"] == id {
			found = true
		}
	}
	if !found {
		t.Errorf("the new Type is not in the tree")
	}
}

func TestAssetPhotoRoute(t *testing.T) {
	h, d := testDeps(t)
	token := adminToken(t, h, d)
	id, _ := seedAsset(t, d, stockroom.StatusAvailable)

	code, body := uploadPhoto(t, h, token, "/assets/"+id+"/photo", "camera.png", []byte("png bytes"))
	if code != http.StatusOK {
		t.Fatalf("POST /assets/{id}/photo = %d %v", code, body)
	}
	if body["photo_url"] != "/files/assets/"+id+".png" {
		t.Fatalf("photo_url = %v, want /files/assets/%s.png", body["photo_url"], id)
	}
	if b, err := os.ReadFile(filepath.Join(d.uploadsDir, "assets", id+".png")); err != nil || string(b) != "png bytes" {
		t.Errorf("stored file = %q, %v", b, err)
	}

	// The photo is fetched by an <img> tag with no bearer token, so the same
	// URL has to work unauthenticated. That is the whole reason /files/ has no
	// session in front of it.
	req := httptest.NewRequest(http.MethodGet, body["photo_url"].(string), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "png bytes" {
		t.Errorf("GET %v = %d %q", body["photo_url"], rec.Code, rec.Body.String())
	}

	if code, body = uploadPhoto(t, h, token, "/assets/"+id+"/photo", "payload.html", []byte("<script>")); code != http.StatusBadRequest {
		t.Errorf("uploading an .html file = %d %v, want 400", code, body)
	}
	if code, body = uploadPhoto(t, h, token, "/assets/0f3d3a9e-0000-4000-8000-000000000000/photo", "x.png", []byte("x")); code != http.StatusNotFound {
		t.Errorf("uploading to an unknown asset = %d %v, want 404", code, body)
	}
	// A JSON body on the photo route is a mistake worth naming, not a 500.
	if code, body = call(t, h, http.MethodPost, "/assets/"+id+"/photo", token, map[string]any{"photo": "x"}); code != http.StatusBadRequest {
		t.Errorf("POST photo as JSON = %d %v, want 400", code, body)
	}
}

func TestBackupRoute(t *testing.T) {
	h, d := testDeps(t)
	token := adminToken(t, h, d)
	d.backupDir = t.TempDir()
	h = newRouter(d)

	code, body := call(t, h, http.MethodPost, "/admin/backup", token, nil)
	if code != http.StatusOK {
		t.Fatalf("POST /admin/backup = %d %v", code, body)
	}
	dir, _ := body["dir"].(string)
	if dir != filepath.Join(d.backupDir, time.Now().Format("2006-01-02")) {
		t.Errorf("dir = %q, want the dated folder under BACKUP_DIR", dir)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) == 0 {
		t.Errorf("the folder the response names is empty: %v %v", entries, err)
	}
	if tables, ok := body["tables"].([]any); !ok || len(tables) == 0 {
		t.Errorf("tables = %v, want one entry per table", body["tables"])
	}
}

// With BACKUP_DIR unset the answer is 503 naming the variable, not a 500 that
// hides it: the admin reading the message is the one who edits .env.
func TestBackupRouteWithoutABackupDir(t *testing.T) {
	h, d := testDeps(t)
	token := adminToken(t, h, d)

	code, body := call(t, h, http.MethodPost, "/admin/backup", token, nil)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("POST /admin/backup with no BACKUP_DIR = %d %v, want 503", code, body)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "BACKUP_DIR") {
		t.Errorf("error = %q, want it to name BACKUP_DIR", msg)
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
