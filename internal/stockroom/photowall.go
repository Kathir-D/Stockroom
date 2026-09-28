package stockroom

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	mrand "math/rand/v2"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The sign-in photo wall's set: the photographs the sign-in screen scrolls
// through as film strips. docs/design/signin-photo-wall.html is the design.
// Where the photographs come from (rclone) and how they are normalized sit
// behind PhotoSource, in photowall_drive.go and photowall_image.go; the HTTP
// routes are server/photowall.go and the admin screen's reads are
// photowall_admin.go.
//
// The set lives in memory and nowhere else. It holds up to Size tiles of
// about 100 KB each, fills in the background one download at a time, and
// then changes slowly: every refresh interval one tile is swapped for a new
// pick, so the strip is different by the end of the day without the whole
// set being downloaded again. A restart starts it from empty.
//
// Order matters to the screen. New tiles are appended, so a strip that
// re-reads the set only ever grows at its end and never jumps. A swapped
// tile leaves from wherever it was and its replacement goes at the end.

const (
	// DefaultPhotoWallSize is the set's size until an admin changes it.
	DefaultPhotoWallSize = 150
	// MinPhotoWallSize and MaxPhotoWallSize bound the setting, matching the
	// check constraint on app_settings.photo_wall_size. The maximum is what
	// bounds memory: 400 tiles at about 100 KB is 40 MB.
	MinPhotoWallSize = 10
	MaxPhotoWallSize = 400
)

const (
	// photoWallTick is how often the one goroutine wakes while the set is
	// full: to drop photographs that left the folder, and to see whether a
	// swap is due.
	photoWallTick = 10 * time.Second
	// photoWallFetchDelay spaces the downloads while the set fills. The set
	// fills over minutes and nobody is waiting on it. Hammering the Drive API
	// to fill a decorative wall is how a 403 rate limit gets earned, and one
	// that would also break the nightly backup, since both go through the
	// same rclone remote and the same Google quota.
	photoWallFetchDelay = 2 * time.Second
	// photoWallRefreshEvery is how often a full set swaps one tile. 150 tiles
	// at one every five minutes is the whole set over about half a day.
	photoWallRefreshEvery = 5 * time.Minute
	// photoWallFailureCap is how many consecutive fetch failures back the
	// filler off. A folder full of .mov files, or no internet, must not spin
	// the CPU.
	photoWallFailureCap = 25
	// photoWallBackoff is how long the filler waits once it has hit the cap.
	photoWallBackoff = 5 * time.Minute
	// photoWallExt is the extension every tile URL carries. The normalizer
	// re-encodes everything to JPEG, so there is only ever one.
	photoWallExt = ".jpg"
	// photoWallMaxTileBytes refuses a tile bigger than the normalizer should
	// ever make, so a bug there can't turn the memory bound into a guess.
	photoWallMaxTileBytes = 1 << 20
)

// PhotoWallPrefix is where the server serves tiles, and so the prefix of
// every tile URL handed to a frontend. Not under FilesPrefix: tiles are
// re-derivable, and the photo mirror copies uploads/ into a never-purged
// backup (CLAUDE.md §11).
const PhotoWallPrefix = "/signin-photos/"

// PhotoPick is what the set tells the source when it asks for a photograph:
// which ones it already holds, and how many from each folder, so the source
// can pick something new and keep one folder from filling the wall.
type PhotoPick struct {
	// Held is the keys of every tile in the set.
	Held map[string]bool
	// PerFolder counts the tiles in the set by the folder they came from.
	PerFolder map[string]int
	// Size is the set's target size. The source derives each folder's cap
	// from it.
	Size int
}

// PhotoSource yields one normalized tile at a time. photowall_drive.go
// implements it over rclone; the set knows nothing about rclone, which is
// what lets the tests run with no network and no Google account.
type PhotoSource interface {
	// NextPhoto picks a photograph the set doesn't hold, downloads it and
	// normalizes it to the wall's 900x600 JPEG. key identifies the
	// photograph within the source, and folder is the folder it came from.
	// Rejecting files the normalizer turns away and retrying past them is
	// the source's business: an error means "nothing usable right now", and
	// the set answers by backing off.
	NextPhoto(ctx context.Context, pick PhotoPick) (key, folder string, tile []byte, err error)
	// Listed reports whether key is still in the source's listing. known is
	// false while there is no listing to ask, and then nothing is dropped.
	Listed(key string) (present, known bool)
}

// PhotoWallOptions is what NewPhotoWall needs. Zero values take the defaults.
type PhotoWallOptions struct {
	// Size is how many tiles the set holds. Zero means DefaultPhotoWallSize.
	Size int
	// Source is where tiles come from. Nil is legal and means the set never
	// fills, which is the sign-in screen with no wall.
	Source PhotoSource
	// FetchDelay spaces successive downloads while filling. Zero means
	// photoWallFetchDelay; tests set a negative value to disable pacing.
	FetchDelay time.Duration
	// RefreshEvery is how often a full set swaps one tile. Zero means
	// photoWallRefreshEvery.
	RefreshEvery time.Duration
}

// photoTile is one normalized image in memory.
type photoTile struct {
	// id is crypto-random hex and is the whole URL stem. It has no relation
	// to the Drive path or to the bytes, so a tile URL tells nobody anything
	// about the folder behind it.
	id     string
	key    string
	folder string
	data   []byte
	// stale marks a tile Reshuffle asked to replace. The filler replaces
	// stale tiles at filling pace, so the wall never goes empty.
	stale bool
}

// PhotoWall is the set. It hangs off DB the way Sessions does.
//
// Every exported method is safe on a nil receiver, because "the wall is off"
// is the common case: nobody signed in to Google for it, or rclone is
// missing. The rule the whole subsystem follows is that no failure in it may
// delay, block or visibly break sign-in.
type PhotoWall struct {
	delay   time.Duration
	refresh time.Duration
	tick    time.Duration
	src     PhotoSource

	now func() time.Time

	mu    sync.Mutex
	size  int
	tiles []*photoTile
	byID  map[string]*photoTile
	// gen advances every time an admin replaces the folder. A download in
	// flight carries the generation it began under, so a slow rclone cat
	// can't put a photograph from the replaced folder into the new set.
	gen uint64
	// lastSwap is when the full set last swapped a tile.
	lastSwap time.Time
	// fails counts consecutive fetch failures and resets on any success.
	fails int
	// warming is whether the last attempt found the source not ready yet: no
	// folder chosen, or the first listing still running. Not a failure, so
	// it neither counts toward the rest nor sets lastErr.
	warming bool
	// lastErr is the most recent fetch failure. It is not logged per
	// failure: a decorative wall failing every few seconds on a machine with
	// no internet would fill the log with noise. One line is logged when a
	// streak reaches the cap, and one when a success ends it.
	lastErr   error
	lastErrAt time.Time
}

// NewPhotoWall returns an empty set. It doesn't start the goroutine; Run
// does, so the caller owns the lifetime and a test can drive the set a step
// at a time.
func NewPhotoWall(opts PhotoWallOptions) *PhotoWall {
	w := &PhotoWall{
		delay:   opts.FetchDelay,
		refresh: opts.RefreshEvery,
		tick:    photoWallTick,
		src:     opts.Source,
		now:     time.Now,
		size:    clampPhotoWallSize(opts.Size),
		byID:    map[string]*photoTile{},
	}
	if w.delay == 0 {
		w.delay = photoWallFetchDelay
	}
	if w.refresh <= 0 {
		w.refresh = photoWallRefreshEvery
	}
	w.lastSwap = w.now()
	return w
}

func clampPhotoWallSize(n int) int {
	switch {
	case n <= 0:
		return DefaultPhotoWallSize
	case n < MinPhotoWallSize:
		return MinPhotoWallSize
	case n > MaxPhotoWallSize:
		return MaxPhotoWallSize
	}
	return n
}

// Run is the set's one goroutine. It fills the set, drops photographs that
// left the folder, and swaps one tile per refresh interval once full. It
// returns when ctx is cancelled.
func (w *PhotoWall) Run(ctx context.Context) {
	if w == nil {
		return
	}
	for {
		w.prune()
		wait := w.tick
		if w.needsFetch() {
			w.fillOne(ctx)
			wait = w.delay
			switch {
			case w.backingOff():
				wait = photoWallBackoff
			case w.isWarming():
				wait = w.tick
			}
		}
		if !sleepCtx(ctx, wait) {
			return
		}
	}
}

// needsFetch reports whether the next step is a download: the set is short,
// holds stale tiles, or is due a swap. A nil source never fetches.
func (w *PhotoWall) needsFetch() bool {
	if w.src == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.trimLocked()
	if len(w.tiles) < w.size || w.staleLocked() > 0 {
		return true
	}
	return w.now().Sub(w.lastSwap) >= w.refresh
}

func (w *PhotoWall) isWarming() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.warming
}

func (w *PhotoWall) backingOff() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.fails >= photoWallFailureCap
}

// fillOne downloads one tile and files it: appended while the set is short,
// or in place of an old tile once it is full. One per call is what paces the
// filler, so there is never more than one download in flight.
func (w *PhotoWall) fillOne(ctx context.Context) {
	w.mu.Lock()
	gen := w.gen
	pick := w.pickLocked()
	w.mu.Unlock()

	// Outside the mutex: a network round trip and an image decode. Holding
	// the lock across it would stall the sign-in screen's reads.
	key, folder, data, err := w.src.NextPhoto(ctx, pick)
	if errors.Is(err, errPhotoWallWarmingUp) {
		w.mu.Lock()
		w.warming = true
		w.mu.Unlock()
		return
	}
	w.mu.Lock()
	w.warming = false
	w.mu.Unlock()
	switch {
	case err != nil:
		w.recordFailure(err)
		return
	case len(data) == 0:
		w.recordFailure(errors.New("the photo source returned an empty tile"))
		return
	case len(data) > photoWallMaxTileBytes:
		w.recordFailure(fmt.Errorf("the photo source returned a %d-byte tile, more than the wall keeps", len(data)))
		return
	}
	id, err := newPhotoTileID()
	if err != nil {
		w.recordFailure(err)
		return
	}

	w.mu.Lock()
	rested := 0
	// A folder replaced while this was downloading, or a photograph that
	// arrived twice, is thrown away rather than shown.
	if gen == w.gen && !w.holdsLocked(key) {
		w.evictForLocked()
		t := &photoTile{id: id, key: key, folder: folder, data: data}
		w.tiles = append(w.tiles, t)
		w.byID[id] = t
		if w.fails >= photoWallFailureCap {
			rested = w.fails
		}
		w.fails = 0
		w.lastErr = nil
	}
	w.mu.Unlock()

	if rested > 0 {
		log.Printf("sign-in photo wall: fetching again after %d failed tries in a row", rested)
	}
}

// evictForLocked makes room for one more tile. A short set needs none. A
// full one drops a stale tile if it has any, otherwise a random one, and
// counts the swap. The caller holds w.mu.
func (w *PhotoWall) evictForLocked() {
	if len(w.tiles) < w.size && w.staleLocked() == 0 {
		return
	}
	victim := -1
	for i, t := range w.tiles {
		if t.stale {
			victim = i
			break
		}
	}
	if victim < 0 {
		if len(w.tiles) < w.size {
			return
		}
		victim = mrand.IntN(len(w.tiles))
		w.lastSwap = w.now()
	}
	w.removeLocked(victim)
}

// removeLocked drops the tile at index i. The caller holds w.mu.
func (w *PhotoWall) removeLocked(i int) {
	delete(w.byID, w.tiles[i].id)
	w.tiles = append(w.tiles[:i], w.tiles[i+1:]...)
}

// trimLocked drops tiles past the target size, newest first, after an admin
// lowers it. The caller holds w.mu.
func (w *PhotoWall) trimLocked() {
	for len(w.tiles) > w.size {
		w.removeLocked(len(w.tiles) - 1)
	}
}

func (w *PhotoWall) staleLocked() int {
	n := 0
	for _, t := range w.tiles {
		if t.stale {
			n++
		}
	}
	return n
}

func (w *PhotoWall) holdsLocked(key string) bool {
	for _, t := range w.tiles {
		if t.key == key {
			return true
		}
	}
	return false
}

// pickLocked describes the set for the source. The caller holds w.mu.
func (w *PhotoWall) pickLocked() PhotoPick {
	p := PhotoPick{Held: map[string]bool{}, PerFolder: map[string]int{}, Size: w.size}
	for _, t := range w.tiles {
		p.Held[t.key] = true
		if !t.stale {
			p.PerFolder[t.folder]++
		}
	}
	return p
}

// prune drops tiles whose photograph is no longer in the source's listing:
// deleted from Drive, or moved out of the folder. Checked every tick, which
// is a map lookup per tile.
func (w *PhotoWall) prune() {
	if w.src == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	kept := w.tiles[:0]
	for _, t := range w.tiles {
		if present, known := w.src.Listed(t.key); known && !present {
			delete(w.byID, t.id)
			continue
		}
		kept = append(kept, t)
	}
	// Clear the tail so dropped tiles' bytes can be collected.
	for i := len(kept); i < len(w.tiles); i++ {
		w.tiles[i] = nil
	}
	w.tiles = kept
}

// recordFailure counts a failed fetch and keeps the reason for the admin
// screen. It logs once per streak, when the streak reaches the cap.
func (w *PhotoWall) recordFailure(err error) {
	w.mu.Lock()
	w.fails++
	w.lastErr = err
	w.lastErrAt = w.now()
	reached := w.fails == photoWallFailureCap
	w.mu.Unlock()

	if reached {
		log.Printf("sign-in photo wall: %d tries in a row gave no usable photograph, so it is pausing for %s between tries until one works. The last: %v",
			photoWallFailureCap, photoWallBackoff, err)
	}
}

// fillFailure is the set's half of the admin screen's last_error: the most
// recent failure, but only while the filler is resting. A single failed
// download among successes is normal, and NextPhoto has already retried
// past it.
func (w *PhotoWall) fillFailure() (err error, at time.Time) {
	if w == nil {
		return nil, time.Time{}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fails < photoWallFailureCap {
		return nil, time.Time{}
	}
	return w.lastErr, w.lastErrAt
}

// Photos is the set's tile URLs in strip order, oldest first. New tiles
// arrive at the end.
func (w *PhotoWall) Photos() []string {
	if w == nil {
		return []string{}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	urls := make([]string, 0, len(w.tiles))
	for _, t := range w.tiles {
		urls = append(urls, PhotoWallPrefix+t.id+photoWallExt)
	}
	return urls
}

// Tile returns the bytes at a tile URL path, or false for a name the set
// doesn't hold. The bytes at one id never change.
func (w *PhotoWall) Tile(urlPath string) ([]byte, bool) {
	if w == nil {
		return nil, false
	}
	name := path.Base(urlPath)
	if !strings.HasSuffix(name, photoWallExt) {
		return nil, false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	t, ok := w.byID[strings.TrimSuffix(name, photoWallExt)]
	if !ok {
		return nil, false
	}
	return t.data, true
}

// PreviewPhotos is the first n tile URLs, for the admin screen's strip.
// n <= 0 asks for six.
func (w *PhotoWall) PreviewPhotos(n int) []string {
	urls := w.Photos()
	if n <= 0 {
		n = 6
	}
	if len(urls) > n {
		urls = urls[:n]
	}
	return urls
}

// Counts is how many tiles the set holds and how many it is aiming for.
func (w *PhotoWall) Counts() (ready, size int) {
	if w == nil {
		return 0, 0
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.tiles), w.size
}

// SetSize changes the target size. A larger set fills in the background; a
// smaller one drops its newest tiles on the next tick.
func (w *PhotoWall) SetSize(n int) {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.size = clampPhotoWallSize(n)
	w.trimLocked()
	w.mu.Unlock()
}

// Reshuffle marks every tile for replacement. The filler replaces them one
// at a time at filling pace, so the wall changes over the next minutes
// without going empty.
func (w *PhotoWall) Reshuffle() {
	if w == nil {
		return
	}
	w.mu.Lock()
	for _, t := range w.tiles {
		t.stale = true
	}
	w.fails, w.lastErr = 0, nil
	w.mu.Unlock()
}

// Invalidate drops every tile and advances the generation. It is the set's
// half of an admin replacing the folder: photographs from the old folder
// must not appear once it has been replaced.
func (w *PhotoWall) Invalidate() {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.gen++
	w.fails, w.lastErr, w.warming = 0, nil, false
	w.tiles = nil
	w.byID = map[string]*photoTile{}
	w.mu.Unlock()
}

// newPhotoTileID returns the random stem of one tile's URL.
func newPhotoTileID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate a tile name: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// writeFileAtomic writes data to a temporary file in the same directory and
// renames it into place, so a crash mid-write never leaves half a file. The
// manifest uses it.
func writeFileAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".staged-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}

// sleepCtx waits for d, or returns false as soon as ctx is cancelled. A
// non-positive d is a plain cancellation check, which lets a test run the
// loop with no pacing at all.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		select {
		case <-ctx.Done():
			return false
		default:
			return true
		}
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
