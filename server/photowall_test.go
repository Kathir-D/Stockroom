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

	// The reel fills on its own goroutine, so the batch is polled for rather
	// than assumed. Three tiles from a source that never fails is microseconds;
	// the bound is here so a broken filler fails this test instead of hanging.
	var body signInPhotosResponse
	for i := 0; i < 200; i++ {
		body = readPhotos(t, do(router, http.MethodGet, "/signin/photos"))
		if len(body.Photos) == 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
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

	// A second call must not be handed the same tiles: they were flipped to
	// served under the reel's mutex on the way out.
	if again := readPhotos(t, do(router, http.MethodGet, "/signin/photos")); len(again.Photos) > 0 {
		for _, u := range again.Photos {
			for _, first := range body.Photos {
				if u == first {
					t.Errorf("tile %s was handed out twice", u)
				}
			}
		}
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
