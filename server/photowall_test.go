package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"stockroom/internal/stockroom"
)

// Tests for the sign-in photo wall's two routes
// (docs/design/signin-photo-wall.html §5).

// readPhotos decodes the batch endpoint's body.
func readPhotos(t *testing.T, rec *httptest.ResponseRecorder) signInPhotosResponse {
	t.Helper()
	var body signInPhotosResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return body
}

// TestSignInPhotosWithNoWall is the state the feature spends most of its life
// in: nobody has signed in to Google for it, so no set is running.
//
// It has to be a 200 with an empty list rather than a 404 or a 503. The caller
// is the sign-in screen, "draw no columns" is the only thing it can do with
// any of these answers, and an error status here would be a red herring in the
// log on the one screen that must never look broken (§9).
func TestSignInPhotosWithNoWall(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	rec := do(newRouter(deps{db: db}), http.MethodGet, "/signin/photos")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store: the set changes every few minutes", got)
	}

	// Literally "[]", not null: a nil slice marshals as null and the component
	// would have to guard against it.
	if !strings.Contains(rec.Body.String(), `"photos":[]`) {
		t.Errorf("body = %q, want an empty photos array", rec.Body.String())
	}
	body := readPhotos(t, rec)
	if len(body.Photos) != 0 {
		t.Errorf("photos = %v, want none", body.Photos)
	}
	if !strings.Contains(rec.Body.String(), `"size":0`) {
		t.Errorf("body = %q, want size 0 with no wall", rec.Body.String())
	}
}

// TestSignInPhotosNeedsNoSession. Every other route on this server answers 401
// without a token. This one cannot: it is what the screen you are looking at
// *before* you have a token renders.
func TestSignInPhotosNeedsNoSession(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	req := httptest.NewRequest(http.MethodGet, "/signin/photos", nil)
	rec := httptest.NewRecorder()
	newRouter(deps{db: db}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d with no Authorization header and no cookie, want 200", rec.Code)
	}
	// And the reverse, so this test fails if the route is ever moved into the
	// session block: a route that needs one answers 401 here.
	if code := do(newRouter(deps{db: db}), http.MethodGet, "/me").Code; code != http.StatusUnauthorized {
		t.Fatalf("/me without a session = %d, want 401; the comparison this test rests on is wrong", code)
	}
}

// fixedSource is a PhotoSource handing out the same bytes under a new key
// every time. The normalizer is not in the loop here; §5 is about serving
// whatever the set holds, and what a tile contains has its own tests in
// internal/stockroom.
type fixedSource struct {
	data []byte
	n    *atomic.Int64
}

func (s fixedSource) NextPhoto(context.Context, stockroom.PhotoPick) (string, string, []byte, error) {
	return fmt.Sprintf("p%d", s.n.Add(1)), "f", append([]byte(nil), s.data...), nil
}
func (s fixedSource) Listed(string) (bool, bool) { return true, true }

// runWall starts a set of size photographs and waits for it to fill.
func runWall(t *testing.T, size int, data []byte) *stockroom.PhotoWall {
	t.Helper()
	wall := stockroom.NewPhotoWall(stockroom.PhotoWallOptions{
		Size:   size,
		Source: fixedSource{data: data, n: new(atomic.Int64)},
		// Negative disables the filler's pacing, which is two seconds a tile
		// in production and nobody's idea of a test.
		FetchDelay: -1,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); wall.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	for i := 0; i < 200; i++ {
		if ready, _ := wall.Counts(); ready == size {
			return wall
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the set never reached %d photographs", size)
	return nil
}

// TestSignInPhotosServesTiles is the whole round trip the component makes: ask
// for the set, then fetch the URLs it named and get the bytes back. It is the
// one test that proves the endpoint's URLs and the tile route agree.
func TestSignInPhotosServesTiles(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	want := []byte("tile bytes")
	wall := runWall(t, 10, want)
	db.SetPhotoWall(wall, nil)
	router := newRouter(deps{db: db})

	body := readPhotos(t, do(router, http.MethodGet, "/signin/photos"))
	if len(body.Photos) != 10 || body.Size != 10 {
		t.Fatalf("the endpoint returned %d photos of %d, want 10 of 10", len(body.Photos), body.Size)
	}
	for _, url := range body.Photos {
		if !strings.HasPrefix(url, stockroom.PhotoWallPrefix) {
			t.Fatalf("tile URL %q does not start with the tile route", url)
		}
		rec := do(router, http.MethodGet, url)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200: the endpoint named a URL the route does not serve", url, rec.Code)
		}
		if rec.Body.String() != string(want) {
			t.Errorf("GET %s returned %q, want %q", url, rec.Body.String(), want)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
			t.Errorf("Content-Type = %q, want image/jpeg", ct)
		}
		if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") || !strings.Contains(cc, "private") {
			t.Errorf("Cache-Control = %q, want private and immutable", cc)
		}
	}

	// Reading the set takes nothing from it: a second read is the same set.
	again := readPhotos(t, do(router, http.MethodGet, "/signin/photos"))
	if strings.Join(again.Photos, ",") != strings.Join(body.Photos, ",") {
		t.Error("a second read of an unchanged set returned something else")
	}
}

// TestSignInPhotosNeverServesTheManifest is §0's second barrier -- the
// frontend never receives a Drive file ID or a folder listing -- asserted at
// the HTTP boundary. Tiles are served from memory by id, so no name reaches
// the disk.
func TestSignInPhotosNeverServesTheManifest(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(filepath.Join(dir, ".cache", "signin-photos"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".cache", "signin-photos", "manifest.json"),
		[]byte(`{"entries":[{"path":"events/gala 2026/DSC_0142.jpg"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	db.SetPhotoWall(runWall(t, 10, []byte("tile")), nil)

	router := newRouter(deps{db: db})
	for _, path := range []string{
		stockroom.PhotoWallPrefix + "manifest.json",
		stockroom.PhotoWallPrefix + "../manifest.json",
		stockroom.PhotoWallPrefix + "..%2Fmanifest.json",
		stockroom.PhotoWallPrefix + "../.cache/signin-photos/manifest.json",
		stockroom.PhotoWallPrefix, // the route itself
	} {
		rec := do(router, http.MethodGet, path)
		if rec.Code == http.StatusOK {
			t.Errorf("GET %s = 200 with body %q; it must not be reachable", path, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "DSC_0142") {
			t.Errorf("GET %s leaked the Drive listing", path)
		}
	}
}

// TestPhotoWallAdminRoutes is the wire for §7: the four routes exist, they
// need a session, and the two that answer with a status never say the folder
// id or a Drive URL out loud.
//
// The switch itself has its rules pinned one layer down in
// internal/stockroom; what is checked here is that the routes are reachable,
// admin-gated, and that a *student's* token does not reach them, which is the
// thing a router edit can break without any package test noticing.
func TestPhotoWallAdminRoutes(t *testing.T) {
	h, d := testDeps(t)
	admin := adminToken(t, h, d)
	_, studentSN := seedUser(t, d, false, "student-route-password")
	student := login(t, h, studentSN, "student-route-password")

	for _, route := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/admin/photo-wall", nil},
		{http.MethodPut, "/admin/photo-wall", map[string]any{"link": "1abcdefghijklmnop", "label": "x"}},
		{http.MethodPost, "/admin/photo-wall/rebuild", nil},
		{http.MethodGet, "/admin/photo-wall/preview", nil},
		{http.MethodPut, "/admin/photo-wall/size", map[string]any{"size": 100}},
		{http.MethodPost, "/admin/photo-wall/reshuffle", nil},
		{http.MethodPut, "/admin/photo-wall", map[string]any{"folder": "x"}},
		{http.MethodPost, "/admin/google/folders", map[string]any{"in": "my-drive", "name": "x"}},
		{http.MethodPost, "/admin/local-folders", map[string]any{"parent": "/tmp", "name": "x"}},
	} {
		if code, _ := call(t, h, route.method, route.path, "", route.body); code != http.StatusUnauthorized {
			t.Errorf("%s %s with no token = %d, want 401", route.method, route.path, code)
		}
		if code, _ := call(t, h, route.method, route.path, student, route.body); code != http.StatusForbidden {
			t.Errorf("%s %s as a student = %d, want 403", route.method, route.path, code)
		}
	}

	// The status read works with no wall configured, which is the state every
	// machine is in before somebody sets one up.
	code, body := call(t, h, http.MethodGet, "/admin/photo-wall", admin, nil)
	if code != http.StatusOK {
		t.Fatalf("GET /admin/photo-wall = %d %v, want 200", code, body)
	}
	if body["enabled"] != false {
		t.Errorf("enabled = %v with no reel built, want false", body["enabled"])
	}
	// The screen disables the paste form on this one field, so it has to be
	// the same condition the write guards on. False here and a 503 from the
	// PUT below are the two halves of that, and this test holds them together.
	if body["can_set_folder"] != false {
		t.Errorf("can_set_folder = %v with no Drive source; the screen would offer a form that can only 503", body["can_set_folder"])
	}
	if _, ok := body["folder_label"]; !ok {
		t.Errorf("the status has no folder_label, which is all the screen has to name the folder by: %v", body)
	}
	for key := range body {
		if strings.Contains(key, "folder_id") {
			t.Errorf("the status carries %q; the folder id must never leave the server", key)
		}
	}

	// A pasted link that is not a Drive folder link is a 400 naming the forms
	// that do work, not a generic failure -- and nothing is written.
	code, body = call(t, h, http.MethodPut, "/admin/photo-wall", admin,
		map[string]any{"link": "the folder Jamie shared", "label": "Photos"})
	if code != http.StatusBadRequest {
		t.Fatalf("PUT with junk = %d %v, want 400", code, body)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "drive.google.com/drive/folders") {
		t.Errorf("the refusal does not name an accepted form: %v", body["error"])
	}

	// With no source on this server a good link is a 503 naming the setting,
	// not a 500: rclone being absent is a machine that has not been set up,
	// which is the same posture UPLOADS_DIR and BACKUP_DIR already have.
	code, body = call(t, h, http.MethodPut, "/admin/photo-wall", admin,
		map[string]any{"link": "https://drive.google.com/drive/folders/1abcdefghijklmnopqrst", "label": "Photos"})
	if code != http.StatusServiceUnavailable {
		t.Fatalf("PUT with no Drive source = %d %v, want 503", code, body)
	}

	// The preview is an empty list rather than an error when there is no reel,
	// for the same reason the sign-in batch is.
	code, body = call(t, h, http.MethodGet, "/admin/photo-wall/preview", admin, nil)
	if code != http.StatusOK {
		t.Fatalf("GET /admin/photo-wall/preview = %d %v, want 200", code, body)
	}
	if photos, ok := body["photos"].([]any); !ok || len(photos) != 0 {
		t.Errorf("preview = %v, want an empty photos array", body["photos"])
	}
}

// TestPhotoWallSizeAndReshuffleOverHTTP: the size is stored, bounded, and
// applied to a running set; Reshuffle needs a running set.
func TestPhotoWallSizeAndReshuffleOverHTTP(t *testing.T) {
	h, d := testDeps(t)
	admin := adminToken(t, h, d)
	ctx := context.Background()
	var before int
	if err := d.db.Pool.QueryRow(ctx, `select photo_wall_size from app_settings`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.db.Pool.Exec(ctx, `update app_settings set photo_wall_size = $1`, before) })

	if code, body := call(t, h, http.MethodPost, "/admin/photo-wall/reshuffle", admin, nil); code != http.StatusServiceUnavailable {
		t.Errorf("reshuffle with no wall = %d %v, want 503", code, body)
	}
	for _, bad := range []int{0, 9, 401} {
		if code, _ := call(t, h, http.MethodPut, "/admin/photo-wall/size", admin, map[string]any{"size": bad}); code != http.StatusBadRequest {
			t.Errorf("size %d = %d, want 400", bad, code)
		}
	}

	wall := runWall(t, 20, []byte("tile"))
	d.db.SetPhotoWall(wall, nil)
	t.Cleanup(func() { d.db.SetPhotoWall(nil, nil) })

	code, body := call(t, h, http.MethodPut, "/admin/photo-wall/size", admin, map[string]any{"size": 12})
	if code != http.StatusOK || body["size"] != float64(12) || body["max_size"] != float64(stockroom.MaxPhotoWallSize) {
		t.Fatalf("PUT size 12 = %d %v", code, body)
	}
	if ready, size := wall.Counts(); ready != 12 || size != 12 {
		t.Errorf("the running set is %d of %d after resizing to 12", ready, size)
	}
	if code, body := call(t, h, http.MethodPost, "/admin/photo-wall/reshuffle", admin, nil); code != http.StatusOK {
		t.Errorf("reshuffle = %d %v", code, body)
	}
}

// TestPhotoWallPreviewDoesNotConsumeOverHTTP: the preview reads the first
// six photographs and leaves the set as it was.
func TestPhotoWallPreviewDoesNotConsumeOverHTTP(t *testing.T) {
	h, d := testDeps(t)
	admin := adminToken(t, h, d)

	wall := runWall(t, 10, []byte("tile bytes"))
	d.db.SetPhotoWall(wall, nil)
	t.Cleanup(func() { d.db.SetPhotoWall(nil, nil) })

	for i := 0; i < 3; i++ {
		code, body := call(t, h, http.MethodGet, "/admin/photo-wall/preview", admin, nil)
		if code != http.StatusOK {
			t.Fatalf("preview %d = %d %v", i, code, body)
		}
		if photos, _ := body["photos"].([]any); len(photos) != 6 {
			t.Fatalf("preview %d returned %d tiles, want 6", i, len(photos))
		}
	}
	if got := readPhotos(t, do(newRouter(d), http.MethodGet, "/signin/photos")); len(got.Photos) != 10 {
		t.Errorf("the sign-in set has %d photographs after three previews, want 10", len(got.Photos))
	}
}
