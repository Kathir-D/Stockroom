package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
// in: SIGNIN_PHOTOS_REMOTE unset, so DB.PhotoWall is nil.
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
		t.Errorf("Cache-Control = %q, want no-store: a cached list names files that are already gone", got)
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
	if body.TTLSeconds <= 0 {
		t.Errorf("ttl_seconds = %d; the response must stay self-describing even with nothing to hand out", body.TTLSeconds)
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

// fixedSource is a PhotoSource handing out the same bytes every time. The
// normalizer is not in the loop here; §5 is about serving whatever the reel
// holds, and what a tile contains has its own tests in internal/stockroom.
type fixedSource struct{ data []byte }

func (s fixedSource) NextPhoto(context.Context) ([]byte, error) {
	return append([]byte(nil), s.data...), nil
}

// TestSignInPhotosServesTiles is the whole round trip the component makes: ask
// for a batch, then fetch the URLs it named and get the bytes back. It is the
// one test that proves the endpoint's URLs and the static mount's paths agree,
// which is the single thing §5 can get wrong and still look fine in isolation.
func TestSignInPhotosServesTiles(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	want := []byte("tile bytes")
	wall, err := stockroom.NewPhotoWall(stockroom.PhotoWallOptions{
		Dir:    t.TempDir(),
		Count:  3,
		Batch:  3,
		Source: fixedSource{data: want},
		// Negative disables the filler's pacing, which is two seconds a tile
		// in production and nobody's idea of a test.
		FetchDelay: -1,
	})
	if err != nil {
		t.Fatalf("NewPhotoWall: %v", err)
	}
	db.PhotoWall = wall

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go wall.Run(ctx)

	router := newRouter(deps{db: db})

	// The reel fills on its own goroutine, so a full reel is waited for rather
	// than assumed. Three tiles from a source that never fails is microseconds;
	// the bound is here so a broken filler fails this test instead of hanging.
	// Waited for on the reel's counts, not by polling the endpoint: every call
	// hands tiles out, and enough early calls would reach the ceiling of twice
	// the buffer and hold the refill the second half of this test waits for.
	for i := 0; i < 200; i++ {
		if ready, _ := wall.Counts(); ready == 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	body := readPhotos(t, do(router, http.MethodGet, "/signin/photos"))
	if len(body.Photos) != 3 {
		t.Fatalf("the endpoint handed out %d tiles after 2s, want 3", len(body.Photos))
	}

	for _, url := range body.Photos {
		if !strings.HasPrefix(url, stockroom.PhotoWallPrefix) {
			t.Fatalf("tile URL %q does not start with the mounted prefix", url)
		}
		rec := do(router, http.MethodGet, url)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200: the endpoint named a URL the mount does not serve", url, rec.Code)
		}
		if rec.Body.String() != string(want) {
			t.Errorf("GET %s returned %q, want %q", url, rec.Body.String(), want)
		}
		// Immutable for as long as the file exists, and not a second longer:
		// the reaper deletes it at the TTL, and a cache entry outliving the
		// file is the opposite of what "delete after use" is for (§2).
		cc := rec.Header().Get("Cache-Control")
		if !strings.Contains(cc, "immutable") || !strings.Contains(cc, "max-age=900") {
			t.Errorf("Cache-Control = %q, want immutable with the reel's 15-minute TTL", cc)
		}
	}

	// A second call with nothing fresh ready is the drained reel (§2,
	// "Draining"): it gets the first batch again rather than an empty wall,
	// each tile still fetchable, and ttl_seconds no longer than the first
	// hand-out allowed. The filler is stopped first so a refill cannot race
	// the call; that fresh tiles are never handed out twice is the reel's own
	// TestPhotoWallTakeIsAtomic.
	cancel()
	again := readPhotos(t, do(router, http.MethodGet, "/signin/photos"))
	if len(again.Photos) != len(body.Photos) {
		t.Fatalf("a drained reel handed out %d tiles, want the %d already out", len(again.Photos), len(body.Photos))
	}
	first := map[string]bool{}
	for _, u := range body.Photos {
		first[u] = true
	}
	for _, u := range again.Photos {
		if !first[u] {
			t.Errorf("repeat %s was never handed out", u)
		}
		if rec := do(router, http.MethodGet, u); rec.Code != http.StatusOK {
			t.Errorf("GET repeat %s = %d, want 200", u, rec.Code)
		}
	}
	if again.TTLSeconds <= 0 || again.TTLSeconds > 900 {
		t.Errorf("ttl_seconds = %d, want the time the repeats have left", again.TTLSeconds)
	}
}

// TestSignInPhotosNeverServesTheManifest is §0's second barrier -- the
// frontend never receives a Drive file ID or a folder listing -- asserted at
// the HTTP boundary rather than at the filesystem one.
//
// The manifest is a listing of every photograph's path inside the Drive
// folder. http.FileServer will not enumerate a directory, which is what the
// barrier leans on, but it serves any file in one quite happily by name. This
// is the test that fails if somebody ever mounts the cache root.
func TestSignInPhotosNeverServesTheManifest(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	dir := t.TempDir()
	wall, err := stockroom.NewPhotoWall(stockroom.PhotoWallOptions{Dir: dir})
	if err != nil {
		t.Fatalf("NewPhotoWall: %v", err)
	}
	db.PhotoWall = wall

	// A manifest where §3 puts it, in the cache root.
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"),
		[]byte(`{"entries":[{"path":"events/gala 2026/DSC_0142.jpg"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	router := newRouter(deps{db: db})
	for _, path := range []string{
		stockroom.PhotoWallPrefix + "manifest.json",
		stockroom.PhotoWallPrefix + "../manifest.json",
		stockroom.PhotoWallPrefix + "..%2Fmanifest.json",
		stockroom.PhotoWallPrefix + ".stockroom-signin-photos",
		stockroom.PhotoWallPrefix, // the directory itself
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

// TestFilesRouteStillWorks. fileServer grew a prefix parameter for the tile
// route; /files/ is the caller that was already there, and its photos are what
// every browse row and profile renders.
func TestFilesRouteStillWorks(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "a.jpg"), []byte("photo"), 0o644); err != nil {
		t.Fatal(err)
	}
	db.UploadsDir = dir

	router := newRouter(deps{db: db})
	rec := do(router, http.MethodGet, "/files/assets/a.jpg")
	if rec.Code != http.StatusOK || rec.Body.String() != "photo" {
		t.Fatalf("GET /files/assets/a.jpg = %d %q", rec.Code, rec.Body.String())
	}
	// Still not enumerable: that property is why the tiles could reuse it.
	if code := do(router, http.MethodGet, "/files/assets/").Code; code == http.StatusOK {
		t.Error("the uploads directory is enumerable")
	}
}

/* --------------------------------------------------------- §7's routes ---- */

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

// TestPhotoWallPreviewDoesNotConsumeOverHTTP is §10's "preview does not
// consume", asserted where it can actually go wrong: a handler that reached
// for TakePhotos because it was the method already there would empty the reel
// every time an admin opened the screen.
func TestPhotoWallPreviewDoesNotConsumeOverHTTP(t *testing.T) {
	h, d := testDeps(t)
	admin := adminToken(t, h, d)

	wall, err := stockroom.NewPhotoWall(stockroom.PhotoWallOptions{
		Dir:        t.TempDir(),
		Count:      4,
		Batch:      4,
		Source:     fixedSource{data: []byte("tile bytes")},
		FetchDelay: -1,
	})
	if err != nil {
		t.Fatalf("NewPhotoWall: %v", err)
	}
	d.db.PhotoWall = wall
	t.Cleanup(func() { d.db.PhotoWall = nil })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go wall.Run(ctx)

	var ready int
	for i := 0; i < 200 && ready < 4; i++ {
		ready, _ = wall.Counts()
		time.Sleep(10 * time.Millisecond)
	}
	if ready != 4 {
		t.Fatalf("the reel holds %d tiles after 2s, want 4", ready)
	}

	for i := 0; i < 3; i++ {
		code, body := call(t, h, http.MethodGet, "/admin/photo-wall/preview", admin, nil)
		if code != http.StatusOK {
			t.Fatalf("preview %d = %d %v", i, code, body)
		}
		if photos, _ := body["photos"].([]any); len(photos) != 4 {
			t.Fatalf("preview %d returned %d tiles, want 4", i, len(photos))
		}
	}
	if ready, served := wall.Counts(); ready != 4 || served != 0 {
		t.Errorf("after three previews the reel is %d ready / %d served, want 4/0", ready, served)
	}

	// And the sign-in screen still gets the full batch.
	if got := readPhotos(t, do(newRouter(d), http.MethodGet, "/signin/photos")); len(got.Photos) != 4 {
		t.Errorf("the sign-in batch got %d tiles after three previews, want 4", len(got.Photos))
	}
}
