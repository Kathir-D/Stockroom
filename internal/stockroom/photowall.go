package stockroom

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// The sign-in photo wall's reel: the prefetch buffer the sign-in screen draws
// from. docs/design/signin-photo-wall.html is the design; this file is §2 of
// it and deliberately nothing else. Where the photographs come from (rclone,
// §3) and how they are normalized (§4) sit behind PhotoSource; the HTTP
// endpoint (§5), the component (§6) and the admin screen (§7) are not built.
//
// The reel is a prefetch buffer with single-use contents: a fixed number of
// tiles are kept normalized and ready on disk, handed out in batches, and
// deleted once the batch that named them has expired. Nothing accumulates,
// which is what bounds both the disk footprint (~6 MB) and the privacy
// exposure in §0 -- a tile exists for about fifteen minutes and then is gone.
//
// Two properties are load-bearing and easy to lose later:
//
//   - A tile is NOT deleted when it is handed out. GET /signin/photos returns
//     a list of URLs and the browser has fetched nothing yet, so deleting on
//     hand-out yields a wall of 404s. Deletion is deferred behind a TTL.
//   - A tile's name is random, not a content hash. A stable name would make
//     the same photograph resolve to the same URL run after run, which
//     quietly undoes "delete after use".

// Defaults for the reel, matching the SIGNIN_PHOTOS_* table in §8. They are
// exported because config.go names them as the fallbacks for unset variables
// and .env.example documents the same numbers.
const (
	// DefaultPhotoWallDir is where normalized tiles are cached. It is wiped
	// on every boot, so it must be a directory nothing else owns; see
	// wipePhotoWallDir for the guard that enforces that.
	DefaultPhotoWallDir = "./.cache/signin-photos"
	// DefaultPhotoWallCount is how many ready tiles the reel keeps buffered.
	// 48 at ~120 KB is about 6 MB of disk and three batches of runway.
	DefaultPhotoWallCount = 48
	// DefaultPhotoWallBatch is how many tiles one request is handed.
	DefaultPhotoWallBatch = 16
	// DefaultPhotoWallTTL is how long a served tile survives before the
	// reaper deletes it. Comfortably longer than a page load and longer than
	// the session idle timeout (CLAUDE.md §7), because the number that
	// matters is how long the browser might still be rendering the batch.
	DefaultPhotoWallTTL = 15 * time.Minute
)

const (
	// photoWallTick is how often the one goroutine reaps while the reel is
	// full. While it is filling, the fetch delay paces it instead.
	photoWallTick = 10 * time.Second
	// photoWallFetchDelay spaces the fetches. There is no deadline here: the
	// reel refills over minutes and nobody is waiting on it. Hammering the
	// Drive API to fill a decorative buffer is how a 403 rate-limit gets
	// earned -- one that would also break the nightly backup, since both go
	// through the same rclone remote and the same Google quota (§2).
	photoWallFetchDelay = 2 * time.Second
	// photoWallFailureCap is how many consecutive fetch failures back the
	// filler off. A folder full of .mov files, or no internet, must not spin
	// the CPU (§4, §9).
	photoWallFailureCap = 25
	// photoWallBackoff is how long the filler waits once it has hit the cap.
	photoWallBackoff = 5 * time.Minute
	// photoWallMarker names a file written into the cache directory so the
	// boot wipe can tell a cache it owns from a directory somebody pointed
	// SIGNIN_PHOTOS_DIR at by mistake. See wipePhotoWallDir.
	photoWallMarker = ".stockroom-signin-photos"
	// photoWallExt is the extension every tile carries. The normalizer (§4)
	// re-encodes everything to JPEG, so there is only ever one.
	photoWallExt = ".jpg"
	// photoWallTiles is the subdirectory the tiles themselves live in, and it
	// exists to keep everything else out of the one directory §5 mounts on an
	// unauthenticated route.
	//
	// The cache directory holds three kinds of thing -- tiles, the ownership
	// marker, and §3's manifest -- and the manifest is a listing of Drive
	// paths, which §0's second barrier says no client may ever receive.
	// http.FileServer refuses to enumerate a directory but serves any file in
	// it by name, so a flat layout would put that listing one guessed URL
	// away from anybody standing at the machine. Separating them means the
	// served root contains only tiles by construction, rather than because
	// §5's handler remembered to exclude a filename.
	photoWallTiles = "tiles"
)

// PhotoWallPrefix is where the server mounts the reel's directory, and so the
// prefix of every tile URL handed to a frontend. It is deliberately not under
// FilesPrefix: tiles are re-derivable and live for minutes, and the Phase 7
// design mirrors uploads/ into a never-purged photo backup (CLAUDE.md §11).
// Mirroring tiles would inflate that backup for nothing.
const PhotoWallPrefix = "/signin-photos/"

// PhotoSource yields one normalized tile at a time. §3 and §4 implement it
// over rclone and the image pipeline; the reel knows nothing about either,
// which is also what lets the tests run with no network and no Google account.
//
// NextPhoto returns the bytes of one tile, already normalized to the wall's
// 900x600 JPEG. Choosing which photograph, rejecting the ones that fail the
// ratio gate and retrying past them are the source's business: an error out of
// NextPhoto means "nothing usable right now", and the reel answers by backing
// off rather than by trying to diagnose it.
type PhotoSource interface {
	NextPhoto(ctx context.Context) ([]byte, error)
}

// PhotoWallOptions is what NewPhotoWall needs. Zero values take the defaults
// above, so a test can ask for a two-tile reel and leave the rest alone.
type PhotoWallOptions struct {
	// Dir is the cache directory. Required, and wiped on construction.
	Dir string
	// Count is how many ready tiles to keep buffered.
	Count int
	// Batch is how many tiles TakePhotos hands out when asked for none in
	// particular.
	Batch int
	// TTL is how long a served tile survives before deletion.
	TTL time.Duration
	// Source is where tiles come from. Nil is legal and means the reel never
	// fills: the directory is still created and wiped, TakePhotos returns
	// nothing, and the sign-in screen is exactly what it is today. That is
	// the state whenever rclone is missing or no Drive folder has been
	// chosen, which is most installations.
	Source PhotoSource
	// FetchDelay spaces successive fetches. Zero means photoWallFetchDelay;
	// tests set a negative value to disable the pacing entirely.
	FetchDelay time.Duration
}

// photoTile is one normalized image on disk. The state machine is the whole
// of §2: ready -> served -> gone.
type photoTile struct {
	// id is crypto-random hex and is the whole filename stem. It has no
	// relationship to the Drive path or to the image bytes, so a tile URL
	// leaks nothing about the folder behind it (§0) and is never reproducible
	// after deletion.
	id string
	// gen is the folder generation this tile was fetched under. A tile whose
	// generation no longer matches the reel's belongs to a folder an admin
	// has already replaced, and is discarded rather than shown (§7).
	gen uint64
	// served is false while the tile is ready and nobody has been told about
	// it; true once it is in a batch a browser was handed.
	served bool
	// expiresAt is when the reaper may delete a served tile. Zero while ready.
	expiresAt time.Time
}

// PhotoWall is the reel. It hangs off DB the way Sessions does, and like the
// session store it is pure memory plus, here, a directory it owns outright.
//
// Every exported method is safe on a nil receiver, because "the wall is off"
// is the common case -- SIGNIN_PHOTOS_REMOTE unset, rclone missing, the cache
// directory unusable -- and §9's invariant is that no failure in this
// subsystem may delay, block or visibly break sign-in. A handler that has to
// remember a nil check is a handler that will one day forget it.
type PhotoWall struct {
	dir   string
	count int
	batch int
	ttl   time.Duration
	delay time.Duration
	tick  time.Duration

	src PhotoSource

	// now is injectable so the TTL tests can advance the clock instead of
	// sleeping, the same trick SessionStore uses.
	now func() time.Time

	mu    sync.Mutex
	tiles map[string]*photoTile
	// gen advances every time an admin replaces the folder (§7). Fetches in
	// flight carry the generation they began under, so a slow rclone cat
	// cannot deposit a photograph from the replaced folder into the new reel
	// minutes later.
	gen uint64
	// fails counts consecutive fetch failures, and resets on any success.
	fails int
	// warming is whether the last attempt found the source not ready yet --
	// no folder chosen, or the first listing still running. Not a failure
	// (§9 calls both normal), so it neither counts toward the rest nor sets
	// lastErr; Run just asks again at the idle tick rather than every two
	// seconds.
	warming bool
	// lastErr is the most recent fetch failure. It is *not* logged per
	// failure: a decorative buffer failing every few seconds on a machine
	// with no internet would fill the log with noise nobody asked for. What
	// is logged is §9's single explanatory line when a streak reaches the
	// cap, and one more when a success ends it (recordFailure, fillOne).
	lastErr   error
	lastErrAt time.Time
}

// NewPhotoWall prepares the cache directory and returns an empty reel. It does
// not start the goroutine; Run does, so the caller owns the lifetime and a
// test can drive the reel a step at a time.
//
// The error is for the caller to log and carry on with, not to die on. server/
// treats it the way it treats a missing failsafe admin (CLAUDE.md §7): a
// warning, and the API starts without the feature.
func NewPhotoWall(opts PhotoWallOptions) (*PhotoWall, error) {
	if opts.Dir == "" {
		return nil, fmt.Errorf("%w: SIGNIN_PHOTOS_DIR is not set, so there is nowhere to cache tiles", ErrNotConfigured)
	}
	w := &PhotoWall{
		dir:   opts.Dir,
		count: opts.Count,
		batch: opts.Batch,
		ttl:   opts.TTL,
		delay: opts.FetchDelay,
		tick:  photoWallTick,
		src:   opts.Source,
		now:   time.Now,
		tiles: map[string]*photoTile{},
	}
	if w.count <= 0 {
		w.count = DefaultPhotoWallCount
	}
	if w.batch <= 0 {
		w.batch = DefaultPhotoWallBatch
	}
	if w.ttl <= 0 {
		w.ttl = DefaultPhotoWallTTL
	}
	if w.delay == 0 {
		w.delay = photoWallFetchDelay
	}
	if err := wipePhotoWallDir(w.dir); err != nil {
		return nil, err
	}
	return w, nil
}

// TTL is how long a served tile survives before the reaper deletes it, and so
// how long a tile URL stays fetchable. §5 reports it in the batch response and
// uses it as the tiles' Cache-Control lifetime, since caching a file past the
// point the server deletes it is the one thing "delete after use" forbids.
//
// The default on a nil reel, so a caller computing a cache header never has to
// branch on the feature being off.
func (w *PhotoWall) TTL() time.Duration {
	if w == nil {
		return DefaultPhotoWallTTL
	}
	return w.ttl
}

// TileDir is the directory §5 mounts at PhotoWallPrefix. It is a subdirectory
// of the cache directory and holds nothing but tiles; see photoWallTiles for
// why that separation is structural rather than a convention.
//
// An empty string on a nil reel, which fileServer already answers with a 404
// handler, so "the wall is off" needs no branch in the router.
func (w *PhotoWall) TileDir() string {
	if w == nil {
		return ""
	}
	return filepath.Join(w.dir, photoWallTiles)
}

// wipePhotoWallDir empties the cache directory, creating it if it is absent.
//
// Every tile is single-use and re-derivable, so a leftover from the last run
// is just a photograph we have already shown -- and wiping is also what stops
// a crash from stranding files on disk forever (§2). Two files survive it: the
// marker below, and §3's manifest, which is neither single-use nor cheap to
// rebuild.
//
// The tiles sit in a subdirectory, which is emptied wholesale and recreated.
//
// The guard matters more than the wipe. SIGNIN_PHOTOS_DIR is operator-set, and
// this function deletes everything in whatever it names; pointed at ./uploads
// by a slip of the finger it would destroy every profile and asset photo on
// the machine on the next restart. So a directory that already has contents is
// only wiped when it carries the marker file this function writes, and
// anything else is refused with an error naming the variable to fix. An empty
// directory is adopted, because that is what a fresh install looks like.
func wipePhotoWallDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create the sign-in photo cache %s: %w", dir, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read the sign-in photo cache %s: %w", dir, err)
	}

	marker := filepath.Join(dir, photoWallMarker)
	if len(entries) > 0 {
		if _, err := os.Stat(marker); err != nil {
			return fmt.Errorf("%w: refusing to wipe %s, which holds files this server did not put there; "+
				"point SIGNIN_PHOTOS_DIR at a directory of its own", ErrInvalid, dir)
		}
	}
	for _, e := range entries {
		// The marker is how this directory is recognised next boot, and the
		// manifest (§3) is a weekly artifact that costs minutes of Drive
		// listing to rebuild -- neither is a tile, and wiping either would
		// undo something the wipe was never about.
		if e.Name() == photoWallMarker || e.Name() == photoWallManifest {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return fmt.Errorf("clear the sign-in photo cache %s: %w", dir, err)
		}
	}
	// Recreated after the sweep above has removed it, so the served directory
	// starts every run genuinely empty.
	if err := os.MkdirAll(filepath.Join(dir, photoWallTiles), 0o755); err != nil {
		return fmt.Errorf("create the sign-in photo cache %s: %w", dir, err)
	}
	if err := os.WriteFile(marker, []byte(
		"This directory is the Stockroom sign-in photo wall cache.\n"+
			"Everything in it is deleted on every server start. Do not put anything here.\n"), 0o644); err != nil {
		return fmt.Errorf("mark the sign-in photo cache %s: %w", dir, err)
	}
	return nil
}

// Run is the reel's one goroutine: it reaps expired tiles and tops the buffer
// up, and it is the only thing that writes to or deletes from the cache
// directory. One goroutine rather than two means the filler and the reaper
// never have to agree about a tile that is being deleted as it is written.
//
// It returns when ctx is cancelled, which in server/ is Ctrl+C.
func (w *PhotoWall) Run(ctx context.Context) {
	if w == nil {
		return
	}
	for {
		w.reap()

		wait := w.tick
		if w.needsFill() {
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

// needsFill reports whether there is both room in the buffer and somewhere to
// fetch from. A nil source is the pre-§3 state and the SIGNIN_PHOTOS_REMOTE
// unset state: the reel simply idles, reaping nothing, forever.
func (w *PhotoWall) needsFill() bool {
	if w.src == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.readyLocked() < w.count
}

// isWarming reports whether the last attempt found the source not ready yet.
func (w *PhotoWall) isWarming() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.warming
}

// backingOff reports whether the filler has failed enough times in a row to
// deserve a rest.
func (w *PhotoWall) backingOff() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.fails >= photoWallFailureCap
}

// fillOne fetches exactly one tile and files it. One per call is what paces
// the filler: Run spaces the calls, so there is never more than one fetch in
// flight and never a burst at the Drive API.
func (w *PhotoWall) fillOne(ctx context.Context) {
	w.mu.Lock()
	gen := w.gen
	w.mu.Unlock()

	// The fetch happens outside the mutex. It is a network round trip and an
	// image decode; holding the lock across it would stall every TakePhotos
	// for the duration, on the one screen that must stay responsive.
	data, err := w.src.NextPhoto(ctx)
	if errors.Is(err, errPhotoWallWarmingUp) {
		w.mu.Lock()
		w.warming = true
		w.mu.Unlock()
		return
	}
	w.mu.Lock()
	w.warming = false
	w.mu.Unlock()
	if err != nil {
		w.recordFailure(err)
		return
	}
	if len(data) == 0 {
		w.recordFailure(errors.New("the photo source returned an empty tile"))
		return
	}

	id, err := newPhotoTileID()
	if err != nil {
		w.recordFailure(err)
		return
	}
	if err := writeFileAtomic(w.tilePath(id), data); err != nil {
		w.recordFailure(err)
		return
	}

	w.mu.Lock()
	// Two reasons to throw away a tile that just cost a download: the folder
	// was replaced while it was in flight (§7), or the buffer filled from
	// under it. Neither can happen today with one filler, but the generation
	// check has to be here rather than retrofitted, because the bug it
	// prevents -- a photograph from a replaced folder appearing minutes later
	// -- is unreproducible once it ships.
	stale := gen != w.gen || w.readyLocked() >= w.count
	rested := 0
	if !stale {
		w.tiles[id] = &photoTile{id: id, gen: gen}
		if w.fails >= photoWallFailureCap {
			rested = w.fails
		}
		w.fails = 0
		w.lastErr = nil
	}
	w.mu.Unlock()

	if stale {
		_ = os.Remove(w.tilePath(id))
	}
	if rested > 0 {
		// The other half of the rest's log line: without it the last word in
		// the log is "pausing", and whoever reads it next week cannot tell
		// whether the wall ever came back.
		log.Printf("sign-in photo wall: fetching again after %d failed tries in a row", rested)
	}
}

// recordFailure counts a failed fetch and keeps the reason for §7's admin
// screen. It logs once per streak, at the moment the streak reaches the cap
// -- §9's "single explanatory line" -- and never per failure: see lastErr.
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

// fillFailure is the reel's half of the admin screen's last_error: the most
// recent failure, but only while the filler is resting.
//
// Only while resting, because that is the only time the reel's errors explain
// what the screen shows. A single failed download among successes is normal --
// Drive answers "directory not found" for a file it listed a minute earlier
// often enough to have been seen on the first real run -- and NextPhoto has
// already retried past it. Reporting it would put a red line on a screen
// whose wall is full.
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

// reap deletes served tiles whose batch has expired, freeing the slot for the
// filler. Ready tiles are never reaped: they have no expiry because nobody has
// been told they exist.
func (w *PhotoWall) reap() {
	now := w.now()

	w.mu.Lock()
	var expired []string
	for id, t := range w.tiles {
		if t.served && !t.expiresAt.After(now) {
			expired = append(expired, id)
			delete(w.tiles, id)
		}
	}
	w.mu.Unlock()

	for _, id := range expired {
		_ = os.Remove(w.tilePath(id))
	}
}

// TakePhotos hands out up to n ready tiles and returns their URLs along with
// the TTL the caller should report to the browser. n <= 0 asks for the
// configured batch size.
//
// The hand-out is atomic under the reel's mutex, so two simultaneous requests
// can never be given the same tile. Tiles flip to served with an expiry rather
// than being deleted -- the caller has a list of URLs and the browser has not
// fetched a single image yet.
//
// Fewer than n, including zero, is a normal answer: not configured, the source
// still warming up, Drive unreachable, or a burst of sign-ins draining the
// buffer faster than it refills. Reel exhaustion degrades the wall's density
// and never its correctness, so this returns no error and the endpoint above
// it answers 200 either way (§5).
func (w *PhotoWall) TakePhotos(n int) ([]string, time.Duration) {
	if w == nil {
		return nil, DefaultPhotoWallTTL
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	if n <= 0 {
		n = w.batch
	}
	expires := w.now().Add(w.ttl)

	// Sorted so the choice is deterministic rather than map-iteration order.
	// Which tiles go out does not matter -- they are interchangeable
	// photographs -- but a reproducible order is worth having in a test.
	ids := make([]string, 0, len(w.tiles))
	for id, t := range w.tiles {
		if !t.served {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) > n {
		ids = ids[:n]
	}

	urls := make([]string, 0, len(ids))
	for _, id := range ids {
		t := w.tiles[id]
		t.served = true
		t.expiresAt = expires
		urls = append(urls, PhotoWallPrefix+id+photoWallExt)
	}
	return urls, w.ttl
}

// PreviewPhotos returns up to n ready tile URLs **without marking them
// served**, for §7's admin strip. n <= 0 asks for six, which is what that
// strip shows.
//
// This is the one read that does not consume. An admin reloading the Drive
// screen a few times must not empty the buffer the sign-in screen is about to
// draw from, and a preview that did would make the screen's own confirmation
// the thing that breaks the wall.
//
// Nothing is reserved either: a tile named here stays ready, so a real
// sign-in may be handed it a moment later and the reaper may delete it a TTL
// after that. The admin screen hides a tile that 404s, exactly as the wall
// does (§9).
func (w *PhotoWall) PreviewPhotos(n int) []string {
	if w == nil {
		return []string{}
	}
	if n <= 0 {
		n = 6
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	ids := make([]string, 0, len(w.tiles))
	for id, t := range w.tiles {
		if !t.served {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) > n {
		ids = ids[:n]
	}
	urls := make([]string, 0, len(ids))
	for _, id := range ids {
		urls = append(urls, PhotoWallPrefix+id+photoWallExt)
	}
	return urls
}

// Counts is how many tiles are waiting and how many are out in a browser
// somewhere, for §7's status block. Zero and zero on a nil reel, which is
// what "the wall is off" should read as.
func (w *PhotoWall) Counts() (ready, served int) {
	if w == nil {
		return 0, 0
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, t := range w.tiles {
		if t.served {
			served++
		} else {
			ready++
		}
	}
	return ready, served
}

// Invalidate discards every ready tile and advances the generation, which is
// the reel's half of an admin replacing the folder (§7). The other half --
// writing the new folder, dropping the manifest and kicking a rebuild -- is
// DB.SetPhotoWallFolder, which calls the two together.
//
// Served tiles are deliberately left to expire on their own TTL. Their URLs
// sit in a browser that has already rendered them, and 404ing a live page to
// save fifteen minutes is the worse trade.
func (w *PhotoWall) Invalidate() {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.gen++
	w.fails, w.lastErr, w.warming = 0, nil, false
	var dropped []string
	for id, t := range w.tiles {
		if !t.served {
			dropped = append(dropped, id)
			delete(w.tiles, id)
		}
	}
	w.mu.Unlock()

	for _, id := range dropped {
		_ = os.Remove(w.tilePath(id))
	}
}

// readyLocked counts unserved tiles. The caller holds w.mu.
func (w *PhotoWall) readyLocked() int {
	n := 0
	for _, t := range w.tiles {
		if !t.served {
			n++
		}
	}
	return n
}

// tilePath is where a tile id lives on disk. The id is hex from crypto/rand,
// so it can never be a path.
func (w *PhotoWall) tilePath(id string) string {
	return filepath.Join(w.dir, photoWallTiles, id+photoWallExt)
}

// newPhotoTileID returns the random stem of one tile's filename. Random, not
// a content hash: a hash would be stable across runs, so the same photograph
// would resolve to the same URL and could be re-requested later from a cached
// page, which is exactly what "delete after use" is meant to prevent (§2).
func newPhotoTileID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate a tile name: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// writeFileAtomic writes data to a temporary file in the same directory and
// renames it into place, so nothing serving the directory can ever see a
// half-written tile. The rename is the same staged-write discipline photos.go
// and backup.go already use.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".staged-tile-*")
	if err != nil {
		return fmt.Errorf("write a tile: %w", err)
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write a tile: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write a tile: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write a tile: %w", err)
	}
	return nil
}

// sleepCtx waits for d, or returns false as soon as ctx is cancelled. A
// non-positive d is a plain cancellation check, which is what lets a test run
// the loop with no pacing at all.
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
