package stockroom

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Tests for the set (docs/design/signin-photo-wall.html §2). None of them
// need a network, a Google account or Postgres: the source is an interface and
// the clock is injectable, which is the whole reason both are shaped that way.

// stubSource is a PhotoSource that hands out a new key per call ("p1",
// "p2", ...) in folder "f", with the bytes it is told to. err, when set, fails
// every call. gone lists keys Listed reports as removed.
type stubSource struct {
	mu    sync.Mutex
	calls int
	data  []byte
	err   error
	gone  map[string]bool
	picks []PhotoPick
}

func (s *stubSource) NextPhoto(_ context.Context, pick PhotoPick) (string, string, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.picks = append(s.picks, pick)
	if s.err != nil {
		return "", "", nil, s.err
	}
	return fmt.Sprintf("p%d", s.calls), "f", append([]byte(nil), s.data...), nil
}

func (s *stubSource) Listed(key string) (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.gone[key], true
}

// newTestWall builds a set with no fetch pacing and a clock the test
// controls.
func newTestWall(t *testing.T, opts PhotoWallOptions) (*PhotoWall, *time.Time) {
	t.Helper()
	opts.FetchDelay = -1
	w := NewPhotoWall(opts)
	clock := time.Date(2026, 9, 16, 9, 0, 0, 0, time.UTC)
	w.now = func() time.Time { return clock }
	w.lastSwap = clock
	return w, &clock
}

// fill runs the filler until the set needs nothing, without Run's pacing.
func fill(t *testing.T, w *PhotoWall) {
	t.Helper()
	for i := 0; w.needsFetch(); i++ {
		if i > 2000 {
			t.Fatal("filler never reached the target")
		}
		w.fillOne(context.Background())
	}
}

func keys(w *PhotoWall) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for _, t := range w.tiles {
		out = append(out, t.key)
	}
	return out
}

// TestPhotoWallFillsToSize: the set grows to its size and stops asking.
func TestPhotoWallFillsToSize(t *testing.T) {
	src := &stubSource{data: []byte("tile")}
	w, _ := newTestWall(t, PhotoWallOptions{Size: 12, Source: src})
	fill(t, w)
	if ready, size := w.Counts(); ready != 12 || size != 12 {
		t.Fatalf("Counts = %d of %d, want 12 of 12", ready, size)
	}
	if got := len(w.Photos()); got != 12 {
		t.Errorf("Photos has %d URLs, want 12", got)
	}
	if w.needsFetch() {
		t.Error("a full set with no swap due still wants a download")
	}
}

// TestPhotoWallSizeIsClamped keeps the memory bound true whatever is asked.
func TestPhotoWallSizeIsClamped(t *testing.T) {
	for in, want := range map[int]int{0: DefaultPhotoWallSize, 3: MinPhotoWallSize, 5000: MaxPhotoWallSize, 200: 200} {
		if got := clampPhotoWallSize(in); got != want {
			t.Errorf("clampPhotoWallSize(%d) = %d, want %d", in, got, want)
		}
	}
}

// TestPhotoWallAppendsAndSwapsSlowly: new tiles go at the end, so a strip
// that re-reads the set never jumps, and a full set swaps one tile per
// refresh interval rather than all at once.
func TestPhotoWallAppendsAndSwapsSlowly(t *testing.T) {
	src := &stubSource{data: []byte("tile")}
	w, clock := newTestWall(t, PhotoWallOptions{Size: 10, Source: src, RefreshEvery: time.Minute})
	fill(t, w)
	before := w.Photos()

	*clock = clock.Add(30 * time.Second)
	if w.needsFetch() {
		t.Fatal("swapped before the refresh interval was up")
	}
	*clock = clock.Add(31 * time.Second)
	fill(t, w)
	after := w.Photos()
	if len(after) != 10 {
		t.Fatalf("the swap changed the size to %d", len(after))
	}
	kept := 0
	old := map[string]bool{}
	for _, u := range before {
		old[u] = true
	}
	for _, u := range after[:9] {
		if old[u] {
			kept++
		}
	}
	if kept != 9 {
		t.Errorf("one swap replaced %d tiles, want 1", 10-kept)
	}
	if old[after[9]] {
		t.Error("the new tile is not at the end")
	}
}

// TestPhotoWallPrunesWhatLeftTheFolder: a photograph deleted from Drive
// leaves the set on the next tick, and the set refills.
func TestPhotoWallPrunesWhatLeftTheFolder(t *testing.T) {
	src := &stubSource{data: []byte("tile"), gone: map[string]bool{}}
	w, _ := newTestWall(t, PhotoWallOptions{Size: 10, Source: src})
	fill(t, w)
	src.mu.Lock()
	src.gone["p3"], src.gone["p7"] = true, true
	src.mu.Unlock()

	w.prune()
	for _, k := range keys(w) {
		if k == "p3" || k == "p7" {
			t.Fatalf("%s is still in the set after it left the folder", k)
		}
	}
	if ready, _ := w.Counts(); ready != 8 {
		t.Fatalf("ready = %d after pruning two of ten, want 8", ready)
	}
	fill(t, w)
	if ready, _ := w.Counts(); ready != 10 {
		t.Errorf("ready = %d after refilling, want 10", ready)
	}
}

// TestPhotoWallTellsTheSourceWhatItHolds: the source can't pick fresh
// photographs or balance folders unless the set says what it has.
func TestPhotoWallTellsTheSourceWhatItHolds(t *testing.T) {
	src := &stubSource{data: []byte("tile")}
	w, _ := newTestWall(t, PhotoWallOptions{Size: 10, Source: src})
	for range 3 {
		w.fillOne(context.Background())
	}
	w.fillOne(context.Background())
	last := src.picks[len(src.picks)-1]
	if len(last.Held) != 3 || !last.Held["p1"] || last.PerFolder["f"] != 3 || last.Size != 10 {
		t.Errorf("the fourth pick was told %+v", last)
	}
}

// TestPhotoWallSetSize: a larger size fills in the background, a smaller one
// trims the newest tiles at once.
func TestPhotoWallSetSize(t *testing.T) {
	src := &stubSource{data: []byte("tile")}
	w, _ := newTestWall(t, PhotoWallOptions{Size: 20, Source: src})
	fill(t, w)
	first := w.Photos()[:10]

	w.SetSize(10)
	if ready, size := w.Counts(); ready != 10 || size != 10 {
		t.Fatalf("after shrinking, Counts = %d of %d, want 10 of 10", ready, size)
	}
	for i, u := range w.Photos() {
		if u != first[i] {
			t.Fatal("shrinking dropped older tiles; the strip would jump")
		}
	}
	w.SetSize(30)
	fill(t, w)
	if ready, _ := w.Counts(); ready != 30 {
		t.Errorf("after growing, ready = %d, want 30", ready)
	}
}

// TestPhotoWallReshuffleNeverEmpties: every tile is replaced, one at a time,
// and the set holds its size throughout.
func TestPhotoWallReshuffleNeverEmpties(t *testing.T) {
	src := &stubSource{data: []byte("tile")}
	w, _ := newTestWall(t, PhotoWallOptions{Size: 10, Source: src})
	fill(t, w)
	old := map[string]bool{}
	for _, k := range keys(w) {
		old[k] = true
	}

	w.Reshuffle()
	for w.needsFetch() {
		w.fillOne(context.Background())
		if ready, _ := w.Counts(); ready != 10 {
			t.Fatalf("the set dropped to %d during a reshuffle", ready)
		}
	}
	for _, k := range keys(w) {
		if old[k] {
			t.Errorf("%s survived the reshuffle", k)
		}
	}
}

// TestPhotoWallTile serves the bytes behind a URL and nothing else.
func TestPhotoWallTile(t *testing.T) {
	src := &stubSource{data: []byte("jpeg bytes")}
	w, _ := newTestWall(t, PhotoWallOptions{Size: 10, Source: src})
	w.fillOne(context.Background())
	u := w.Photos()[0]
	if data, ok := w.Tile(u); !ok || string(data) != "jpeg bytes" {
		t.Errorf("Tile(%q) = %q, %v", u, data, ok)
	}
	for _, bad := range []string{PhotoWallPrefix + "manifest.json", PhotoWallPrefix + "nope.jpg", strings.TrimSuffix(u, ".jpg") + ".png"} {
		if _, ok := w.Tile(bad); ok {
			t.Errorf("Tile(%q) served something", bad)
		}
	}
	var nilWall *PhotoWall
	if _, ok := nilWall.Tile(u); ok {
		t.Error("a nil set served a tile")
	}
}

// TestPhotoWallRefusesAnOversizedTile keeps the memory bound a fact.
func TestPhotoWallRefusesAnOversizedTile(t *testing.T) {
	src := &stubSource{data: make([]byte, photoWallMaxTileBytes+1)}
	w, _ := newTestWall(t, PhotoWallOptions{Size: 10, Source: src})
	w.fillOne(context.Background())
	if ready, _ := w.Counts(); ready != 0 {
		t.Errorf("kept a tile over %d bytes", photoWallMaxTileBytes)
	}
}

// TestPhotoWallEmptyAndNil: an unconfigured wall is a normal, empty answer.
func TestPhotoWallEmptyAndNil(t *testing.T) {
	var nilWall *PhotoWall
	if got := nilWall.Photos(); got == nil || len(got) != 0 {
		t.Errorf("nil Photos = %#v, want an empty, non-nil slice", got)
	}
	nilWall.SetSize(40)
	nilWall.Reshuffle()
	nilWall.Invalidate()

	w, _ := newTestWall(t, PhotoWallOptions{})
	if w.needsFetch() {
		t.Error("a set with no source wants a download")
	}
	if got := w.Photos(); got == nil || len(got) != 0 {
		t.Errorf("empty Photos = %#v", got)
	}
}

// TestPhotoWallFillBacksOff: a folder of unusable files, or no internet, must
// not spin.
func TestPhotoWallFillBacksOff(t *testing.T) {
	src := &stubSource{err: errors.New("no usable photograph found")}
	w, _ := newTestWall(t, PhotoWallOptions{Size: 10, Source: src})
	captureLog(t)

	for i := 0; i < photoWallFailureCap-1; i++ {
		w.fillOne(context.Background())
	}
	if w.backingOff() {
		t.Fatal("backing off before the cap")
	}
	if err, _ := w.fillFailure(); err != nil {
		t.Error("the admin screen hears of failures before the set rests")
	}
	w.fillOne(context.Background())
	if !w.backingOff() {
		t.Fatalf("not backing off after %d failures", photoWallFailureCap)
	}
	if err, _ := w.fillFailure(); err == nil {
		t.Error("a resting set tells the admin screen nothing")
	}

	src.mu.Lock()
	src.err, src.data = nil, []byte("tile")
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
func TestPhotoWallWaitsOutTheManifest(t *testing.T) {
	src := &stubSource{err: warmingUp(errors.New("the Drive folder is still being listed (500 files so far)"))}
	w, _ := newTestWall(t, PhotoWallOptions{Size: 10, Source: src})
	logged := captureLog(t)

	for i := 0; i < 2*photoWallFailureCap; i++ {
		w.fillOne(context.Background())
	}
	if w.backingOff() || w.fails != 0 || w.lastErr != nil {
		t.Fatalf("waiting for the manifest was counted as failing: fails=%d, lastErr=%v", w.fails, w.lastErr)
	}
	if !w.isWarming() {
		t.Error("the set does not know it is waiting, so Run asks every two seconds rather than at the idle tick")
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
// a success ends it.
func TestPhotoWallLogsOncePerRest(t *testing.T) {
	src := &stubSource{err: errors.New("download a.jpg: dial tcp: lookup www.googleapis.com: no such host")}
	w, _ := newTestWall(t, PhotoWallOptions{Size: 10, Source: src})
	logged := captureLog(t)

	for i := 0; i < 3*photoWallFailureCap; i++ {
		w.fillOne(context.Background())
	}
	if n := strings.Count(logged.String(), "\n"); n != 1 {
		t.Fatalf("a failure streak logged %d lines, want 1:\n%s", n, logged)
	}
	src.mu.Lock()
	src.err, src.data = nil, []byte("tile bytes")
	src.mu.Unlock()
	w.fillOne(context.Background())
	if n := strings.Count(logged.String(), "\n"); n != 2 || !strings.Contains(logged.String(), "fetching again") {
		t.Errorf("recovery should log one line saying so:\n%s", logged)
	}
}

// TestPhotoWallInvalidate is §7's half of replacing the folder: every tile
// goes at once.
func TestPhotoWallInvalidate(t *testing.T) {
	src := &stubSource{data: []byte("tile")}
	w, _ := newTestWall(t, PhotoWallOptions{Size: 10, Source: src})
	fill(t, w)
	u := w.Photos()[0]

	w.Invalidate()
	if ready, _ := w.Counts(); ready != 0 {
		t.Fatalf("ready = %d after Invalidate", ready)
	}
	if _, ok := w.Tile(u); ok {
		t.Error("a tile from the replaced folder is still served")
	}
}

// TestPhotoWallDiscardsAStaleGeneration: a slow fetch that began under the
// old folder must not land in the new set.
func TestPhotoWallDiscardsAStaleGeneration(t *testing.T) {
	w, _ := newTestWall(t, PhotoWallOptions{Size: 10})
	w.src = photoSourceFunc(func(context.Context, PhotoPick) (string, string, []byte, error) {
		w.Invalidate() // the admin switches folders mid-download
		return "old", "f", []byte("from the old folder"), nil
	})
	w.fillOne(context.Background())
	if ready, _ := w.Counts(); ready != 0 {
		t.Errorf("a download from the replaced folder landed in the new set")
	}
}

type photoSourceFunc func(context.Context, PhotoPick) (string, string, []byte, error)

func (f photoSourceFunc) NextPhoto(ctx context.Context, p PhotoPick) (string, string, []byte, error) {
	return f(ctx, p)
}
func (f photoSourceFunc) Listed(string) (bool, bool) { return false, false }

// TestPhotoWallTileNamesAreOpaque covers §0's second barrier: a tile URL must
// carry nothing about the Drive folder behind it.
func TestPhotoWallTileNamesAreOpaque(t *testing.T) {
	src := &stubSource{data: []byte("identical bytes every time")}
	w, _ := newTestWall(t, PhotoWallOptions{Size: 10, Source: src})
	fill(t, w)

	seen := map[string]bool{}
	for _, u := range w.Photos() {
		if seen[u] {
			t.Errorf("identical bytes produced a repeated name %q; the name must not be a content hash", u)
		}
		seen[u] = true
		id := strings.TrimSuffix(strings.TrimPrefix(u, PhotoWallPrefix), photoWallExt)
		if len(id) != 32 || strings.ContainsAny(id, "/\\.") {
			t.Errorf("tile id %q is not 32 opaque hex characters", id)
		}
	}
}

// TestPhotoWallRunStopsWithContext: the goroutine's lifetime is the server's.
func TestPhotoWallRunStopsWithContext(t *testing.T) {
	w, _ := newTestWall(t, PhotoWallOptions{Size: 10, Source: &stubSource{data: []byte("tile bytes")}})
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()

	deadline := time.After(2 * time.Second)
	for {
		if ready, _ := w.Counts(); ready == 10 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("the set never filled")
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
