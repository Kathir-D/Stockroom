package stockroom

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Tests for the reel (docs/design/signin-photo-wall.html §2). None of them
// need a network, a Google account or Postgres: the source is an interface and
// the clock is injectable, which is the whole reason both are shaped that way.

// stubSource is a PhotoSource that hands out the bytes it is told to, and
// counts how often it was asked. err, when set, fails every call.
type stubSource struct {
	mu    sync.Mutex
	calls int
	data  []byte
	err   error
}

func (s *stubSource) NextPhoto(context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return append([]byte(nil), s.data...), nil
}

// newTestWall builds a reel in a temp directory with no fetch pacing and a
// clock the test controls.
func newTestWall(t *testing.T, opts PhotoWallOptions) (*PhotoWall, *time.Time) {
	t.Helper()
	if opts.Dir == "" {
		opts.Dir = t.TempDir()
	}
	opts.FetchDelay = -1
	w, err := NewPhotoWall(opts)
	if err != nil {
		t.Fatalf("NewPhotoWall: %v", err)
	}
	clock := time.Date(2026, 9, 16, 9, 0, 0, 0, time.UTC)
	w.now = func() time.Time { return clock }
	return w, &clock
}

// tileFiles lists the tiles on disk. They live in their own subdirectory, so
// there is nothing else in it to filter out -- which is the point of the
// separation: it is the only directory §5 serves.
func tileFiles(t *testing.T, w *PhotoWall) []string {
	t.Helper()
	entries, err := os.ReadDir(w.TileDir())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// fill runs the filler until the buffer is full, without Run's pacing.
func fill(t *testing.T, w *PhotoWall) {
	t.Helper()
	for i := 0; w.needsFill(); i++ {
		if i > 1000 {
			t.Fatal("filler never reached the target")
		}
		w.fillOne(context.Background())
	}
}

// TestPhotoWallBootWipe is §2's "on boot, wipe the directory": every tile is
// single-use and re-derivable, so a leftover from the last run is a photograph
// already shown, and a crash must not strand files forever.
func TestPhotoWallBootWipe(t *testing.T) {
	dir := t.TempDir()
	// A previous run's cache: the marker plus junk, including a subdirectory.
	if err := os.WriteFile(filepath.Join(dir, photoWallMarker), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, photoWallTiles, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		filepath.Join(photoWallTiles, "aa"+photoWallExt),
		filepath.Join(photoWallTiles, "bb"+photoWallExt),
		filepath.Join(photoWallTiles, ".staged-tile-123"),
		"stray" + photoWallExt,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("junk"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	w, err := NewPhotoWall(PhotoWallOptions{Dir: dir})
	if err != nil {
		t.Fatalf("NewPhotoWall: %v", err)
	}
	if got := tileFiles(t, w); len(got) != 0 {
		t.Errorf("cache still holds %v after boot; want it empty", got)
	}
	if _, err := os.Stat(filepath.Join(dir, photoWallMarker)); err != nil {
		t.Errorf("marker file missing after the wipe: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "stray"+photoWallExt)); !os.IsNotExist(err) {
		t.Error("the wipe left a file in the cache root")
	}
}

// TestPhotoWallAdoptsAnEmptyDirectory is the fresh-install path: no marker
// yet, nothing to lose, so the reel takes the directory over.
func TestPhotoWallAdoptsAnEmptyDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does", "not", "exist", "yet")
	if _, err := NewPhotoWall(PhotoWallOptions{Dir: dir}); err != nil {
		t.Fatalf("NewPhotoWall on a fresh path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, photoWallMarker)); err != nil {
		t.Errorf("expected the cache directory to be created and marked: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, photoWallTiles)); err != nil {
		t.Errorf("expected the tiles subdirectory to be created: %v", err)
	}
}

// TestPhotoWallRefusesAForeignDirectory is the guard that matters most in this
// file. SIGNIN_PHOTOS_DIR is operator-set and the boot wipe deletes everything
// in whatever it names; pointed at ./uploads by a slip it would destroy every
// profile and asset photo on the machine on the next restart.
func TestPhotoWallRefusesAForeignDirectory(t *testing.T) {
	dir := t.TempDir()
	photo := filepath.Join(dir, "234567.jpg")
	if err := os.WriteFile(photo, []byte("a student's photo"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := NewPhotoWall(PhotoWallOptions{Dir: dir})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("NewPhotoWall on a directory it does not own = %v, want ErrInvalid", err)
	}
	if !strings.Contains(err.Error(), "SIGNIN_PHOTOS_DIR") {
		t.Errorf("the error should name the variable to fix, got %q", err)
	}
	if _, err := os.Stat(photo); err != nil {
		t.Errorf("the refusal deleted the file anyway: %v", err)
	}
}

// TestPhotoWallLifecycle walks the whole state machine: fill to target, take a
// batch, and -- the trap §2 names -- assert the files are STILL THERE right
// after the hand-out, because the browser has a list of URLs and has not
// fetched a single image yet. Then advance past the TTL and watch them go.
func TestPhotoWallLifecycle(t *testing.T) {
	src := &stubSource{data: []byte("tile bytes")}
	w, clock := newTestWall(t, PhotoWallOptions{Count: 6, Batch: 4, TTL: 15 * time.Minute, Source: src})

	fill(t, w)
	if got := len(tileFiles(t, w)); got != 6 {
		t.Fatalf("reel filled to %d tiles, want 6", got)
	}

	urls, ttl := w.TakePhotos(0)
	if len(urls) != 4 {
		t.Fatalf("TakePhotos handed out %d urls, want the batch size 4", len(urls))
	}
	if ttl != 15*time.Minute {
		t.Errorf("ttl = %v, want 15m", ttl)
	}
	for _, u := range urls {
		if !strings.HasPrefix(u, PhotoWallPrefix) {
			t.Errorf("url %q is not under %s", u, PhotoWallPrefix)
		}
		if _, err := os.Stat(filepath.Join(w.TileDir(), strings.TrimPrefix(u, PhotoWallPrefix))); err != nil {
			t.Errorf("tile deleted on hand-out, which would 404 the whole wall: %v", err)
		}
	}

	// The reaper must not touch a served tile before its batch expires.
	w.reap()
	if got := len(tileFiles(t, w)); got != 6 {
		t.Fatalf("reaping before the TTL left %d tiles, want all 6", got)
	}

	*clock = clock.Add(16 * time.Minute)
	w.reap()
	if got := len(tileFiles(t, w)); got != 2 {
		t.Fatalf("after the TTL the reel holds %d tiles, want the 2 never served", got)
	}

	// The freed slots are refilled, which is what makes the reel a buffer
	// rather than a one-shot.
	fill(t, w)
	if got := len(tileFiles(t, w)); got != 6 {
		t.Errorf("reel refilled to %d, want 6", got)
	}
}

// TestPhotoWallTakeIsAtomic: two requests arriving together must never be
// handed the same tile, or two browsers race the same TTL over one file.
func TestPhotoWallTakeIsAtomic(t *testing.T) {
	src := &stubSource{data: []byte("tile bytes")}
	w, _ := newTestWall(t, PhotoWallOptions{Count: 20, Batch: 10, Source: src})
	fill(t, w)

	var wg sync.WaitGroup
	results := make([][]string, 8)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], _ = w.TakePhotos(5)
		}(i)
	}
	wg.Wait()

	seen := map[string]bool{}
	total := 0
	for _, batch := range results {
		for _, u := range batch {
			if seen[u] {
				t.Errorf("tile %s was handed out twice", u)
			}
			seen[u] = true
			total++
		}
	}
	if total != 20 {
		t.Errorf("handed out %d tiles in total, want the 20 that were ready", total)
	}
}

// TestPhotoWallTakeWhenEmpty: an unconfigured or drained reel is a normal
// answer, not an error. §9's invariant is that the wall is the first thing to
// go and the last thing to complain.
func TestPhotoWallTakeWhenEmpty(t *testing.T) {
	var off *PhotoWall
	if urls, ttl := off.TakePhotos(0); len(urls) != 0 || ttl <= 0 {
		t.Errorf("nil reel returned %v / %v, want no urls and a usable ttl", urls, ttl)
	}
	off.Invalidate() // must not panic

	w, _ := newTestWall(t, PhotoWallOptions{Count: 4})
	if urls, _ := w.TakePhotos(0); len(urls) != 0 {
		t.Errorf("a reel with no source returned %v, want nothing", urls)
	}
	if w.needsFill() {
		t.Error("a reel with no source should not ask to be filled")
	}
}

// TestPhotoWallFillBacksOff: a folder of unusable files, or no internet, must
// not spin. The filler counts consecutive failures and rests at the cap.
func TestPhotoWallFillBacksOff(t *testing.T) {
	src := &stubSource{err: errors.New("rclone: connection refused")}
	w, _ := newTestWall(t, PhotoWallOptions{Count: 5, Source: src})

	for i := 0; i < photoWallFailureCap; i++ {
		if w.backingOff() {
			t.Fatalf("backed off after %d failures, want %d", i, photoWallFailureCap)
		}
		w.fillOne(context.Background())
	}
	if !w.backingOff() {
		t.Fatalf("still not backing off after %d failures", photoWallFailureCap)
	}
	if got := len(tileFiles(t, w)); got != 0 {
		t.Errorf("a failing source wrote %d files", got)
	}
	if w.lastErr == nil {
		t.Error("lastErr is nil; §7's admin screen has nothing to report")
	}

	// One success clears the record, so a transient outage does not leave the
	// reel resting for five minutes after the network comes back.
	src.mu.Lock()
	src.err, src.data = nil, []byte("tile bytes")
	src.mu.Unlock()
	w.fillOne(context.Background())
	if w.backingOff() || w.lastErr != nil {
		t.Errorf("a successful fetch did not clear the failure state (fails=%d, lastErr=%v)", w.fails, w.lastErr)
	}
}

// captureLog points the standard logger at a buffer for one test, so a test
// can count the lines §9 promises instead of trusting a comment about them.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	flags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		log.SetFlags(flags)
	})
	return &buf
}

// TestPhotoWallWaitsOutTheManifest: until the first listing finishes the
// source has nothing to hand out, and §9 calls that normal, not a failure.
// Counted as failures, it rested the reel for five minutes before the listing
// had even finished, and the wall came up twelve minutes after boot on a
// folder that was ready in one -- seen on the first run against a real Drive.
func TestPhotoWallWaitsOutTheManifest(t *testing.T) {
	src := &stubSource{err: warmingUp(errors.New("the Drive folder is still being listed (500 files so far)"))}
	w, _ := newTestWall(t, PhotoWallOptions{Count: 2, Source: src})
	logged := captureLog(t)

	for i := 0; i < 2*photoWallFailureCap; i++ {
		w.fillOne(context.Background())
	}
	if w.backingOff() || w.fails != 0 || w.lastErr != nil {
		t.Fatalf("waiting for the manifest was counted as failing: fails=%d, lastErr=%v", w.fails, w.lastErr)
	}
	if !w.isWarming() {
		t.Error("the reel does not know it is waiting, so Run asks every two seconds rather than at the idle tick")
	}
	if logged.Len() != 0 {
		t.Errorf("waiting for the manifest logged %q; a normal state is not news", logged.String())
	}

	src.mu.Lock()
	src.err, src.data = nil, []byte("tile bytes")
	src.mu.Unlock()
	w.fillOne(context.Background())
	if ready, _ := w.Counts(); ready != 1 {
		t.Fatalf("ready = %d on the first try after the manifest arrived, want 1", ready)
	}
	if w.isWarming() {
		t.Error("still marked as waiting after a tile arrived")
	}
}

// TestPhotoWallLogsOncePerRest is §9's "logs a single explanatory line": one
// when a streak reaches the cap, none for the failures after it, and one when
// a success ends it -- so the last word in the log is never "pausing" for a
// wall that has long since recovered.
func TestPhotoWallLogsOncePerRest(t *testing.T) {
	src := &stubSource{err: errors.New("download a.jpg: dial tcp: lookup www.googleapis.com: no such host")}
	w, _ := newTestWall(t, PhotoWallOptions{Count: 2, Source: src})
	logged := captureLog(t)

	for i := 0; i < 3*photoWallFailureCap; i++ {
		w.fillOne(context.Background())
	}
	lines := strings.Split(strings.TrimSpace(logged.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], "pausing") || !strings.Contains(lines[0], "no such host") {
		t.Fatalf("after %d failures the log holds %q, want one line saying it is pausing and why", 3*photoWallFailureCap, logged.String())
	}

	logged.Reset()
	src.mu.Lock()
	src.err, src.data = nil, []byte("tile bytes")
	src.mu.Unlock()
	w.fillOne(context.Background())
	w.fillOne(context.Background())
	lines = strings.Split(strings.TrimSpace(logged.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], "fetching again") {
		t.Fatalf("recovering logged %q, want exactly one line saying it is fetching again", logged.String())
	}
}

// TestPhotoWallInvalidate is §7's half of replacing the folder, tested here
// because the generation counter lives in the reel. Ready tiles go, served
// tiles are left to expire on their own TTL, and a fetch that was in flight
// when the switch landed is written nowhere.
func TestPhotoWallInvalidate(t *testing.T) {
	src := &stubSource{data: []byte("tile bytes")}
	w, _ := newTestWall(t, PhotoWallOptions{Count: 5, Batch: 2, Source: src})
	fill(t, w)

	served, _ := w.TakePhotos(2)
	if len(served) != 2 {
		t.Fatalf("took %d tiles, want 2", len(served))
	}

	w.Invalidate()

	if got := tileFiles(t, w); len(got) != 2 {
		t.Errorf("after the switch the cache holds %v, want only the 2 served tiles", got)
	}
	for _, u := range served {
		if _, err := os.Stat(filepath.Join(w.TileDir(), strings.TrimPrefix(u, PhotoWallPrefix))); err != nil {
			t.Errorf("a served tile was deleted under a live page: %v", err)
		}
	}
	if urls, _ := w.TakePhotos(0); len(urls) != 0 {
		t.Errorf("the reel still handed out %v from the replaced folder", urls)
	}
	if w.gen == 0 {
		t.Error("the generation did not advance")
	}
}

// TestPhotoWallDiscardsAStaleGeneration: a slow fetch that began under the old
// folder must not deposit a photograph into the new reel minutes later. This
// is the bug the generation counter exists for, and it is unreproducible once
// it ships, so it is pinned here.
func TestPhotoWallDiscardsAStaleGeneration(t *testing.T) {
	switched := make(chan struct{})
	var w *PhotoWall
	src := &stubSource{data: []byte("tile bytes")}

	w, _ = newTestWall(t, PhotoWallOptions{Count: 5, Source: photoSourceFunc(func(ctx context.Context) ([]byte, error) {
		// The switch lands while this fetch is in flight.
		w.Invalidate()
		close(switched)
		return src.NextPhoto(ctx)
	})})

	w.fillOne(context.Background())
	<-switched

	if got := tileFiles(t, w); len(got) != 0 {
		t.Errorf("a tile from the replaced folder reached the cache: %v", got)
	}
	if urls, _ := w.TakePhotos(0); len(urls) != 0 {
		t.Errorf("the reel handed out %v from the replaced folder", urls)
	}
}

// photoSourceFunc adapts a function to PhotoSource.
type photoSourceFunc func(context.Context) ([]byte, error)

func (f photoSourceFunc) NextPhoto(ctx context.Context) ([]byte, error) { return f(ctx) }

// TestPhotoWallTileNamesAreOpaque covers §0's second barrier: a tile URL must
// carry nothing about the Drive folder behind it, and must not be stable
// across runs, or "delete after use" is undone by a cached page.
func TestPhotoWallTileNamesAreOpaque(t *testing.T) {
	src := &stubSource{data: []byte("identical bytes every time")}
	w, _ := newTestWall(t, PhotoWallOptions{Count: 8, Batch: 8, Source: src})
	fill(t, w)

	urls, _ := w.TakePhotos(0)
	if len(urls) != 8 {
		t.Fatalf("took %d tiles, want 8", len(urls))
	}
	seen := map[string]bool{}
	for _, u := range urls {
		if seen[u] {
			t.Errorf("identical bytes produced a repeated name %q; the name must not be a content hash", u)
		}
		seen[u] = true
		id := strings.TrimSuffix(strings.TrimPrefix(u, PhotoWallPrefix), photoWallExt)
		if len(id) != 32 {
			t.Errorf("tile id %q is %d chars, want 32 hex", id, len(id))
		}
		if strings.ContainsAny(id, "/\\.") {
			t.Errorf("tile id %q is not opaque", id)
		}
	}
}

// TestPhotoWallRunStopsWithContext: the goroutine's lifetime is the server's.
func TestPhotoWallRunStopsWithContext(t *testing.T) {
	w, _ := newTestWall(t, PhotoWallOptions{Count: 2, Source: &stubSource{data: []byte("tile bytes")}})
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()

	deadline := time.After(2 * time.Second)
	for {
		w.mu.Lock()
		full := w.readyLocked() == 2
		w.mu.Unlock()
		if full {
			break
		}
		select {
		case <-deadline:
			t.Fatal("the reel never filled")
		case <-time.After(time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return when the context was cancelled")
	}
}
