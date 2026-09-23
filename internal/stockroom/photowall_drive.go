package stockroom

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Where the wall's photographs come from: a Google Drive folder, read through
// the rclone binary (docs/design/signin-photo-wall.html §3).
//
// rclone is already the decided Drive mechanism for the nightly backup
// (CLAUDE.md §11), so this reuses one token store and one consent flow rather
// than adding a second. There is no Google API client here and no OAuth
// handling in Go; the credential lives in rclone's own config file and is
// configured drive.readonly, so the machine can read this Drive and can never
// modify or delete anything in it.
//
// The one structural idea is that **listing is separated from fetching**. The
// source folder is assumed to hold tens of thousands of files, and listing it
// takes minutes; fetching one photograph takes a second. So a manifest of
// paths and sizes -- no image data -- is built once per folder and refreshed
// weekly in the background, and choosing a photograph is then a random index
// into a slice rather than an API call.

const (
	// photoWallManifest is the manifest's filename inside the reel's cache
	// directory. The boot wipe skips it by name: every tile is single-use and
	// re-derivable in a second, but the manifest costs minutes of Drive
	// listing to rebuild and §3 specifies it as a weekly artifact, so wiping
	// it on every start would make a closet PC that reboots nightly re-list
	// the whole folder nightly.
	photoWallManifest = "manifest.json"

	// photoManifestTimeout bounds one listing. §3 expects minutes for a large
	// folder, so this is generous; it exists to stop a wedged rclone from
	// holding a goroutine and a Drive quota slot forever.
	photoManifestTimeout = 30 * time.Minute
	// photoFetchTimeout bounds one `rclone cat`. A single photograph over a
	// school uplink is seconds; a minute is the point at which something is
	// wrong and the filler is better off picking a different file.
	photoFetchTimeout = 1 * time.Minute

	// DefaultPhotoWallManifestHours is how often the manifest is refreshed.
	// Weekly: the folder is departmental photography, which gains a batch
	// after an event and is otherwise static, and every rebuild is minutes of
	// Drive listing.
	DefaultPhotoWallManifestHours = 168

	// photoManifestRetry is how long a *failed* listing waits before trying
	// again. Distinct from the refresh interval on purpose: a folder that was
	// unreachable at boot because the uplink was not up yet should be retried
	// in minutes, not in a week.
	photoManifestRetry = 5 * time.Minute
	// photoManifestPoll is how often the refresher wakes to check whether
	// anything is due. It is also the ceiling on how long a folder switch
	// waits before its rebuild starts, for the case where the switch's nudge
	// is missed.
	photoManifestPoll = 1 * time.Minute

	// photoFetchAttempts is how many photographs one NextPhoto will try
	// before giving up. PhotoSource's contract is that rejecting unusable
	// files and retrying past them is the source's business, and a real
	// folder legitimately holds portraits and panoramas the ratio gate turns
	// away (§4). Bounded so a folder of nothing but video still returns.
	photoFetchAttempts = 4
)

// photoEntry is one photograph in the manifest: a path relative to the folder
// and the size rclone reported. No image data, no Drive file ID -- the path is
// all `rclone cat` needs, and the size is what lets an oversized original be
// skipped before it is downloaded rather than after.
type photoEntry struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// photoManifest is what is written to manifest.json.
type photoManifest struct {
	// FolderID records which folder this listing describes, so a manifest
	// left over from a folder an admin has since replaced (§7) is recognised
	// and discarded rather than quietly feeding the wall from the old folder.
	FolderID string       `json:"folder_id"`
	BuiltAt  time.Time    `json:"built_at"`
	Entries  []photoEntry `json:"entries"`
}

// photoExtensions is what the wall will attempt to decode, lowercased.
//
// This is deliberately the *only* place formats are filtered: §3 sketched an
// `--include` glob on the rclone side as well, and two filter lists that can
// disagree is one more thing to keep in step than this needs. Filtering here
// also keeps the list in the same package as the decoder that has to honour
// it -- adding a format is one edit, not two.
//
// HEIC is absent, and that is the one deliberate omission: Go has no HEIC
// decoder, in the standard library or in golang.org/x/image, so every .heic
// in the folder would be a multi-megabyte download that can only ever be
// thrown away. Rejecting it from the listing costs nothing and downloading it
// costs the filler a slot.
var photoExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true,
}

// DrivePhotoSourceOptions is what NewDrivePhotoSource needs.
type DrivePhotoSourceOptions struct {
	// Remote is the rclone remote name holding the photographs, from
	// SIGNIN_PHOTOS_REMOTE. Required.
	Remote string
	// FolderID is the Drive folder to read. It may be empty: §7 makes this an
	// admin-panel value, so "configured but no folder chosen yet" is a normal
	// state in which the source simply never lists anything.
	FolderID string
	// Dir is where manifest.json lives. This is the reel's cache directory,
	// which the reel has already created and taken ownership of.
	Dir string
	// RefreshInterval is how often the manifest is rebuilt. Zero takes the
	// weekly default.
	RefreshInterval time.Duration
}

// DrivePhotoSource implements PhotoSource over rclone. It is safe for
// concurrent use: the reel's filler calls NextPhoto while its own refresher
// goroutine rebuilds the manifest, and §7 will call SetFolder from an HTTP
// handler.
type DrivePhotoSource struct {
	remote   string
	dir      string
	interval time.Duration

	// list, fetch and probe are the rclone seam. They are fields rather than
	// direct calls so the tests can run the whole of this file with no
	// network, no rclone binary and no Google account, which is what §10 asks
	// for.
	list  func(ctx context.Context, remote string) (io.ReadCloser, func() error, error)
	fetch func(ctx context.Context, remote, file string) ([]byte, error)
	probe func(ctx context.Context, remote string) error

	// rebuild carries a nudge from SetFolder. Buffered to one, so a switch
	// never blocks on the refresher being mid-listing.
	rebuild chan struct{}

	mu       sync.Mutex
	folderID string
	manifest *photoManifest
	loaded   bool
	// nextAttempt gates the refresher: it is the refresh interval after a
	// success and a few minutes after a failure.
	nextAttempt time.Time
	// listing and listed are progress for §7's admin screen, which has to be
	// able to say "rebuilding -- 12,400 files listed so far". A large folder
	// takes minutes and an empty wall in the meantime is correct but
	// indistinguishable from a broken feature unless the screen says why.
	listing bool
	listed  int
	// cancelListing stops the listing in progress, and is what SetFolder
	// calls: a listing of the folder that was just replaced is minutes of
	// Drive traffic whose result will be thrown away, and the refresher is
	// one goroutine, so the new folder's listing would otherwise wait behind
	// it. Nil when nothing is listing.
	cancelListing context.CancelFunc
	// rebuildRequested is §7's Rebuild button, waiting to be acted on. It is
	// what lets that button keep the current manifest: without it, "rebuild
	// now" and "the manifest is stale" would have to be the same state, and
	// the only way to say the first would be to throw the second away.
	rebuildRequested bool
	// lastErr is kept rather than logged, for the same reason the reel keeps
	// its own: a decorative subsystem failing every few minutes on a machine
	// with no internet must not fill the log with noise nobody asked for.
	lastErr   error
	lastErrAt time.Time
	// authWarned is whether the one expired-sign-in warning (§9) has been
	// logged for the current outage. Cleared by any success, so a token that
	// is reconnected and later expires again is warned about again.
	authWarned bool
	now        func() time.Time
}

// PhotoWallSourceStatus is the read §7's admin screen is built on. It carries
// the folder's *name* and never its ID: the ID is the capability, and a folder
// shared "anyone with the link" is readable by whoever holds it (§7).
type PhotoWallSourceStatus struct {
	Configured  bool      `json:"configured"`
	Listing     bool      `json:"listing"`
	ListedSoFar int       `json:"listed_so_far"`
	PhotoCount  int       `json:"photo_count"`
	BuiltAt     time.Time `json:"built_at"`
	LastError   string    `json:"last_error"`
	LastErrorAt time.Time `json:"last_error_at"`
}

// NewDrivePhotoSource returns a source reading photographs from a Drive folder
// through rclone.
//
// A missing rclone binary is an error the caller logs and carries on without,
// exactly like a missing failsafe admin (CLAUDE.md §7). §9's invariant is that
// nothing in this subsystem may delay, block or visibly break sign-in, and a
// server that refuses to start because a decorative wall has no binary breaks
// it in the most complete way available.
func NewDrivePhotoSource(opts DrivePhotoSourceOptions) (*DrivePhotoSource, error) {
	if strings.TrimSpace(opts.Remote) == "" {
		return nil, fmt.Errorf("%w: SIGNIN_PHOTOS_REMOTE is not set", ErrNotConfigured)
	}
	if opts.Dir == "" {
		return nil, fmt.Errorf("%w: SIGNIN_PHOTOS_DIR is not set, so there is nowhere to keep the manifest", ErrNotConfigured)
	}
	if _, err := exec.LookPath(rcloneBinary); err != nil {
		return nil, fmt.Errorf("%w: rclone is not installed. On this machine run `brew install rclone` (macOS) or `winget install Rclone.Rclone` (Windows), then restart the server", ErrNotConfigured)
	}
	s := &DrivePhotoSource{
		// A trailing colon is how rclone *prints* a remote ("gdrive:"), so it
		// is a natural thing to type into .env -- and left on, driveRoot would
		// build "gdrive:,root_folder_id=...:", which rclone reads as a path
		// inside the remote rather than as a connection string.
		remote:   strings.TrimSuffix(strings.TrimSpace(opts.Remote), ":"),
		dir:      opts.Dir,
		interval: opts.RefreshInterval,
		list:     streamRcloneList,
		fetch:    catRclone,
		probe:    probeRcloneFolder,
		rebuild:  make(chan struct{}, 1),
		folderID: strings.TrimSpace(opts.FolderID),
		now:      time.Now,
	}
	if s.interval <= 0 {
		s.interval = DefaultPhotoWallManifestHours * time.Hour
	}
	return s, nil
}

// driveRoot is the rclone connection string naming one folder for one command.
//
// The remote itself is deliberately configured with root_folder_id left blank
// (§3): a remote pinned to one folder cannot read a different pasted one,
// which is the whole of §7. Supplying the folder per invocation instead means
// no command can act on a folder its caller did not name.
func (s *DrivePhotoSource) driveRoot(folderID string, file ...string) string {
	root := fmt.Sprintf("%s,root_folder_id=%s:", s.remote, folderID)
	if len(file) > 0 && file[0] != "" {
		return root + file[0]
	}
	return root
}

// SetFolder points the source at a different Drive folder, discards the
// manifest for the old one, cancels any listing of it still running, and asks
// the refresher to list the new one now.
//
// It reports whether anything changed, so §7 can skip the reel teardown when
// an admin pastes the link that is already live. The reel's half of a switch
// -- dropping every ready tile and advancing the generation -- is
// PhotoWall.Invalidate, and the two are called together.
func (s *DrivePhotoSource) SetFolder(folderID string) bool {
	if s == nil {
		return false
	}
	folderID = strings.TrimSpace(folderID)

	s.mu.Lock()
	if folderID == s.folderID {
		s.mu.Unlock()
		return false
	}
	s.folderID = folderID
	s.manifest = nil
	s.listed = 0
	s.lastErr, s.lastErrAt = nil, time.Time{}
	s.nextAttempt = time.Time{}
	if s.cancelListing != nil {
		s.cancelListing()
	}
	s.mu.Unlock()

	// Buffered and non-blocking: a nudge that finds the channel full has
	// already been delivered.
	select {
	case s.rebuild <- struct{}{}:
	default:
	}
	return true
}

// Rebuild asks the refresher to re-list the folder now rather than waiting
// out the weekly interval (§7's Rebuild button).
//
// The nudge is the same one SetFolder sends, but the manifest is *kept*, which
// is the one difference between the two. SetFolder discards it because it
// describes a folder that is no longer live; here the folder has not changed,
// so the existing listing is still correct -- and a listing takes minutes
// (§3), so dropping it would black the wall out for the whole rebuild, exactly
// when an admin is standing at the screen watching the feature they just
// pressed a button on appear to break. buildManifest replaces the manifest in
// one assignment when it finishes, so the wall crosses over with no gap.
//
// The request is a flag rather than a cleared nextAttempt: with the manifest
// kept, a fresh one would make buildDue answer "not due for a week" and the
// press would do nothing at all. The flag also carries what clearing
// nextAttempt used to -- a rebuild requested minutes after a *failed* listing
// is not swallowed by the five-minute retry gate, because an admin pressing
// the button has usually just fixed the thing that made it fail.
func (s *DrivePhotoSource) Rebuild() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.rebuildRequested = true
	s.listed = 0
	s.mu.Unlock()

	select {
	case s.rebuild <- struct{}{}:
	default:
	}
}

// Probe checks that a folder id is reachable with the configured credential,
// and is what stands between a mistyped link and a wall that silently empties
// ten minutes later (§7).
//
// It is a *bounded* listing -- one level, not the recursive walk a manifest
// build does -- because an admin is standing at the screen waiting for the
// answer and §3's full listing of a large folder takes minutes.
//
// An empty folder passes. §9 is explicit that a folder holding no usable
// photographs is permitted: it may be mid-upload, and refusing it would make
// the panel unusable in exactly the moment somebody is setting the feature up.
// What Probe rejects is Drive saying no -- a folder that does not exist, or
// one the machine's account cannot see.
func (s *DrivePhotoSource) Probe(ctx context.Context, folderID string) error {
	if s == nil {
		return fmt.Errorf("%w: the sign-in photo wall has no Drive source", ErrNotConfigured)
	}
	folderID = strings.TrimSpace(folderID)
	if folderID == "" {
		return fmt.Errorf("%w: no folder id to check", ErrInvalid)
	}
	if err := s.probe(ctx, s.driveRoot(folderID)); err != nil {
		err = s.explain(folderID, err)
		// An expired sign-in is not the link's fault, and blaming the folder's
		// sharing would send the admin to fix the one thing that is fine. It
		// is the server's credential, so it is a 503 carrying the fix.
		if errors.Is(err, errPhotoDriveAuth) {
			return fmt.Errorf("%w: %v", ErrNotConfigured, err)
		}
		// Named causes rather than a generic failure: these two are what it
		// almost always is, and an admin who is told which one can fix it in
		// Drive without a support conversation.
		return fmt.Errorf("%w: that folder isn't reachable with the configured Drive account — check that it is shared with that account, or set to anyone-with-the-link. rclone said: %s",
			ErrInvalid, err.Error())
	}
	s.clearAuthWarning()
	return nil
}

// Status is §7's read.
func (s *DrivePhotoSource) Status() PhotoWallSourceStatus {
	if s == nil {
		return PhotoWallSourceStatus{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	st := PhotoWallSourceStatus{
		Configured:  s.folderID != "",
		Listing:     s.listing,
		ListedSoFar: s.listed,
		LastErrorAt: s.lastErrAt,
	}
	if s.lastErr != nil {
		st.LastError = s.lastErr.Error()
	}
	if s.manifest != nil {
		st.PhotoCount = len(s.manifest.Entries)
		st.BuiltAt = s.manifest.BuiltAt
	}
	return st
}

// Run keeps the manifest current. It is a second goroutine beside the reel's,
// and deliberately so: the reel's one-goroutine rule is about the *tiles*, so
// that the filler and the reaper never argue over a file being written as it
// is deleted. The manifest is a different file with a single writer, on a
// timescale of weeks rather than seconds.
//
// It returns when ctx is cancelled, which in server/ is Ctrl+C.
func (s *DrivePhotoSource) Run(ctx context.Context) {
	if s == nil {
		return
	}
	s.loadManifest()

	for {
		if folderID, due := s.buildDue(); due {
			s.buildManifest(ctx, folderID)
		}
		if ctx.Err() != nil {
			return
		}
		t := time.NewTimer(photoManifestPoll)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-s.rebuild:
			t.Stop()
		case <-t.C:
		}
	}
}

// buildDue reports whether a listing should start now, and for which folder.
func (s *DrivePhotoSource) buildDue() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Consumed whether or not it is acted on, so a press that arrives with no
	// folder chosen cannot queue a redundant second listing behind the build
	// the eventual SetFolder starts.
	requested := s.rebuildRequested
	s.rebuildRequested = false

	if s.folderID == "" {
		return "", false // configured but no folder chosen: nothing to list
	}
	if requested {
		return s.folderID, true // §7's button: now, not after the retry gate
	}
	if s.now().Before(s.nextAttempt) {
		return "", false
	}
	if s.manifest == nil {
		return s.folderID, true
	}
	return s.folderID, s.now().Sub(s.manifest.BuiltAt) >= s.interval
}

// loadManifest reads manifest.json back after a restart, so a reboot does not
// cost another full listing of the folder.
//
// A manifest describing a folder that is no longer the live one is dropped
// rather than used. Without that check, changing the folder while the server
// was stopped -- editing .env, or a future admin-panel write -- would feed the
// wall from the folder that was replaced, which is precisely the outcome §7's
// generation counter exists to prevent at runtime.
func (s *DrivePhotoSource) loadManifest() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded {
		return
	}
	s.loaded = true

	raw, err := os.ReadFile(filepath.Join(s.dir, photoWallManifest))
	if err != nil {
		return // absent is the normal first-boot state, not a fault
	}
	var m photoManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return // a truncated manifest is simply rebuilt
	}
	if m.FolderID != s.folderID || len(m.Entries) == 0 {
		return
	}
	s.manifest = &m
}

// buildManifest lists the folder and writes the result to manifest.json.
//
// The listing is *streamed* rather than buffered, for two reasons that both
// matter on a big folder: tens of thousands of entries is a few MB of JSON
// that never has to be held twice, and the running count is what lets §7's
// screen say "rebuilding -- 12,400 files listed so far" instead of showing an
// empty wall with no explanation for several minutes.
func (s *DrivePhotoSource) buildManifest(ctx context.Context, folderID string) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	s.mu.Lock()
	s.listing, s.listed = true, 0
	s.cancelListing = cancel
	s.mu.Unlock()

	entries, err := s.listFolder(ctx, folderID)

	s.mu.Lock()
	s.listing = false
	s.cancelListing = nil
	// The folder may have been replaced while this listing ran -- usually
	// because SetFolder cancelled it. Checked before the error, not after: a
	// listing of the old folder that failed (or was cancelled) is not news
	// about the new one, and recording it would put a stale error on the admin
	// screen and push the new folder's first listing behind the five-minute
	// retry gate. Filing a *successful* result would be worse: photographs
	// from the folder that was just replaced.
	if folderID != s.folderID {
		s.mu.Unlock()
		return
	}
	if err != nil {
		s.lastErr, s.lastErrAt = err, s.now()
		s.nextAttempt = s.now().Add(photoManifestRetry)
		s.mu.Unlock()
		return
	}
	m := &photoManifest{FolderID: folderID, BuiltAt: s.now(), Entries: entries}
	s.manifest = m
	s.lastErr, s.lastErrAt = nil, time.Time{}
	s.authWarned = false
	s.nextAttempt = time.Time{}
	if len(entries) == 0 {
		// Permitted -- the folder may be mid-upload -- but worth reporting,
		// and worth not re-listing a minute later.
		s.lastErr, s.lastErrAt = errors.New("no usable photographs in that folder: the wall accepts JPEG, PNG and WebP, roughly landscape (§4)"), s.now()
	}
	s.mu.Unlock()

	// Staged write and rename, so a crash mid-write never replaces a working
	// manifest with half of one -- the same discipline photos.go and
	// backup.go already use.
	raw, err := json.Marshal(m)
	if err == nil {
		err = writeFileAtomic(filepath.Join(s.dir, photoWallManifest), raw)
	}
	if err != nil {
		s.mu.Lock()
		s.lastErr, s.lastErrAt = fmt.Errorf("save the photo manifest: %w", err), s.now()
		s.mu.Unlock()
	}
}

// listFolder runs one recursive listing and returns the usable photographs.
func (s *DrivePhotoSource) listFolder(ctx context.Context, folderID string) ([]photoEntry, error) {
	body, wait, err := s.list(ctx, s.driveRoot(folderID))
	if err != nil {
		return nil, s.explain(folderID, err)
	}
	defer body.Close()

	entries, decodeErr := s.decodeListing(body)
	// Drain whatever is left before waiting, or rclone blocks writing into a
	// pipe nobody is reading and the wait never returns.
	_, _ = io.Copy(io.Discard, body)
	if err := wait(); err != nil {
		return nil, s.explain(folderID, fmt.Errorf("list the Drive folder: %w", err))
	}
	if decodeErr != nil {
		return nil, s.explain(folderID, fmt.Errorf("read the Drive listing: %w", decodeErr))
	}
	return entries, nil
}

// photoListingEntry is the subset of one `rclone lsjson` line the wall reads.
//
// Its own type rather than target_drive.go's rcloneEntry, and deliberately
// without ModTime: this listing passes --no-modtime, under which rclone still
// prints the key but as "", and a time.Time field refuses to decode "" -- so
// sharing the backup's struct failed every real listing on its first line.
// The two read the same command with different flags; declaring only what is
// read keeps a field one of them needs from breaking the other.
type photoListingEntry struct {
	Path  string `json:"Path"`
	Size  int64  `json:"Size"`
	IsDir bool   `json:"IsDir"`
}

// decodeListing walks the JSON array rclone streams, counting as it goes.
func (s *DrivePhotoSource) decodeListing(r io.Reader) ([]photoEntry, error) {
	dec := json.NewDecoder(bufio.NewReaderSize(r, 64<<10))
	if _, err := dec.Token(); err != nil { // the opening '['
		return nil, err
	}

	entries := []photoEntry{}
	seen := 0
	for dec.More() {
		var e photoListingEntry
		if err := dec.Decode(&e); err != nil {
			return nil, err
		}
		seen++
		if seen%500 == 0 {
			s.mu.Lock()
			s.listed = seen
			s.mu.Unlock()
		}
		if e.IsDir || e.Path == "" {
			continue
		}
		if !photoExtensions[strings.ToLower(path.Ext(e.Path))] {
			continue
		}
		// The size ceiling is applied here, against the size the listing
		// already reported, so a pathological original is skipped for free
		// rather than downloaded and then thrown away (§3).
		if e.Size > photoMaxSourceBytes {
			continue
		}
		entries = append(entries, photoEntry{Path: e.Path, Size: e.Size})
	}

	s.mu.Lock()
	s.listed = seen
	s.mu.Unlock()
	return entries, nil
}

// NextPhoto picks a photograph at random, downloads it and normalizes it.
//
// Selection is a random index into the manifest: no API call to choose, and
// uniform across the whole folder. Files the normalizer turns away -- portraits,
// panoramas, anything Go cannot decode -- are retried past here rather than
// reported, because PhotoSource's contract is that an error out of this method
// means "nothing usable right now" and the reel answers it by backing off.
func (s *DrivePhotoSource) NextPhoto(ctx context.Context) ([]byte, error) {
	if s == nil {
		return nil, errors.New("the photo wall has no source")
	}

	var lastErr error
	for attempt := 0; attempt < photoFetchAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		folderID, entry, ok := s.pick()
		if !ok {
			return nil, s.notReadyErr()
		}

		// The download streams to stdout and into the decoder; the
		// full-resolution original is never written to disk (§3).
		raw, err := s.fetch(ctx, s.driveRoot(folderID, entry.Path), entry.Path)
		if err != nil {
			lastErr = s.explain(folderID, err)
			continue
		}
		tile, err := normalizePhoto(bytes.NewReader(raw))
		if err != nil {
			lastErr = err
			continue
		}
		s.clearAuthWarning()
		return tile, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no usable photograph found")
	}
	return nil, lastErr
}

// pick chooses one manifest entry at random, along with the folder it belongs
// to so a switch mid-download can be detected.
func (s *DrivePhotoSource) pick() (string, photoEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.manifest == nil || len(s.manifest.Entries) == 0 {
		return "", photoEntry{}, false
	}
	return s.folderID, s.manifest.Entries[rand.IntN(len(s.manifest.Entries))], true
}

// notReadyErr says *why* there is nothing to hand out. All of these resolve to
// an empty wall and a normal sign-in screen; the distinction is for §7's
// screen, which is where an admin finds out whether to wait or to fix
// something.
func (s *DrivePhotoSource) notReadyErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.folderID == "":
		return warmingUp(fmt.Errorf("%w: no Drive folder has been chosen for the sign-in photo wall", ErrNotConfigured))
	case s.listing:
		return warmingUp(fmt.Errorf("the Drive folder is still being listed (%d files so far)", s.listed))
	case s.lastErr != nil:
		// Not warming up: the listing failed, or the folder holds nothing
		// usable. Those are real, and the reel counting them toward its rest
		// is what stops a machine with no internet from asking every ten
		// seconds forever.
		return s.lastErr
	default:
		return warmingUp(errors.New("the photo manifest has not been built yet"))
	}
}

// errPhotoWallWarmingUp marks the not-ready answers that are a *normal state*
// rather than a failure: no folder chosen yet, or the first listing still
// running. §9 lists both as normal, and the reel must not count them.
//
// It did, once. At one attempt every two seconds, the minute a first listing
// takes was 25 "failures" and a five-minute rest before the listing had even
// finished -- and since any failure past the cap buys another rest, the wall
// came up about twelve minutes after boot on a folder that was ready in one.
// Found on the first run against a real Drive folder (§10's manual pass).
var errPhotoWallWarmingUp = errors.New("the sign-in photo wall is warming up")

type warmingUpError struct{ err error }

func (e warmingUpError) Error() string        { return e.err.Error() }
func (e warmingUpError) Unwrap() error        { return e.err }
func (e warmingUpError) Is(target error) bool { return target == errPhotoWallWarmingUp }

// warmingUp tags err as a normal not-yet state, keeping its message and
// anything it already wraps (ErrNotConfigured, for the no-folder case).
func warmingUp(err error) error { return warmingUpError{err} }

/* ---------------------------------------------------- rclone's words ----- */

// errPhotoDriveAuth marks an rclone failure that is the machine's Google
// sign-in rather than the folder or the network (§9's "OAuth token expired"
// row). It is the one failure here an admin fixes at a terminal rather than
// in Drive, so it gets its own sentence and its own log line.
var errPhotoDriveAuth = errors.New("the Drive sign-in on this machine has expired")

var (
	// rcloneTimestamp is the date and time rclone puts before every line of
	// its log output. Noise once the lines are joined into one message.
	rcloneTimestamp = regexp.MustCompile(`\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} `)
	// rcloneClientIDNotice is the line rclone 1.7x prints before *every*
	// command on a remote using its shared client id. Left in, it is the first
	// thing in every error message and the actual cause is buried behind it.
	// The retirement it announces is tracked in CLAUDE.md §13, not here.
	rcloneClientIDNotice = regexp.MustCompile(`NOTICE: \S+: This remote uses rclone's shared Google Drive client_id.*?making-your-own-client-id\s*`)
	// rcloneRequestURL is a Drive API request rclone quotes when a call fails.
	// It carries the folder id in its query string, and is reduced to a word.
	rcloneRequestURL = regexp.MustCompile(`"https?://[^"]*"`)
)

// explain turns an rclone failure into something fit to store, show and log.
//
// Stored is the important one. Every error kept here reaches GET
// /admin/photo-wall as last_error, and rclone's own messages carry the folder
// id: a failed call quotes the Drive API request it made, whose query string
// is `'<id>' in parents`, URL-encoded. An expired token or a dropped network
// connection would have put the id on the admin screen -- the one thing §7
// promises no response ever carries. Verified against rclone 1.75.1 with a
// revoked token before this was written.
//
// It must be called without s.mu held: an expired sign-in takes the lock to
// decide whether this is the first time it has been seen.
func (s *DrivePhotoSource) explain(folderID string, err error) error {
	if err == nil {
		return nil
	}
	msg := rcloneClientIDNotice.ReplaceAllString(err.Error(), "")
	msg = rcloneTimestamp.ReplaceAllString(msg, "")
	msg = rcloneRequestURL.ReplaceAllString(msg, "Drive")
	if folderID != "" {
		for _, form := range []string{folderID, url.QueryEscape(folderID), url.PathEscape(folderID)} {
			msg = strings.ReplaceAll(msg, form, "<folder>")
		}
	}
	msg = oneLine(msg)

	lower := strings.ToLower(msg)
	if strings.Contains(lower, "invalid_grant") || strings.Contains(lower, "couldn't fetch token") ||
		strings.Contains(lower, "cannot fetch token") || strings.Contains(lower, "token expired") {
		// rclone's own advice names the remote as `gdrive{AbCdE}:`, its
		// internal name for a connection string -- not something anybody can
		// type. The remote as configured is.
		authErr := fmt.Errorf("%w: Google refused its saved sign-in, so it has expired or been revoked. On this machine run `rclone config reconnect %s:` and sign in again; nothing needs restarting",
			errPhotoDriveAuth, s.remote)
		s.mu.Lock()
		first := !s.authWarned
		s.authWarned = true
		s.mu.Unlock()
		if first {
			log.Printf("warning: sign-in photo wall: %v", authErr)
		}
		return authErr
	}
	return errors.New(msg)
}

// clearAuthWarning re-arms the expired-sign-in warning after something worked.
func (s *DrivePhotoSource) clearAuthWarning() {
	s.mu.Lock()
	s.authWarned = false
	s.mu.Unlock()
}

/* ------------------------------------------------------------- rclone ----- */

// streamRcloneList runs `rclone lsjson` and hands back its stdout as it
// arrives, plus the function that waits for the process.
//
// --files-only drops the directory entries; --fast-list trades memory for far
// fewer Drive API calls on a deep tree, which is the difference between a
// listing that finishes in minutes and one that earns a rate limit;
// --no-modtime skips a per-file metadata read this has no use for.
func streamRcloneList(ctx context.Context, remote string) (io.ReadCloser, func() error, error) {
	ctx, cancel := context.WithTimeout(ctx, photoManifestTimeout)

	cmd := exec.CommandContext(ctx, rcloneBinary, "lsjson", remote,
		"-R", "--files-only", "--fast-list", "--no-modtime")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Env = os.Environ()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, nil, err
	}
	wait := func() error {
		err := cmd.Wait()
		defer cancel()
		if err == nil {
			return nil
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		if ctx.Err() != nil {
			return fmt.Errorf("rclone timed out after %s: %s", photoManifestTimeout, oneLine(msg))
		}
		return errors.New(oneLine(msg))
	}
	return stdout, wait, nil
}

// probeRcloneFolder runs the bounded listing Probe is built on.
//
// --max-depth 1 keeps it to the folder's own contents: what is being tested is
// whether Drive will answer for this id at all, and walking the tree to find
// that out would take the minutes a manifest build takes. --files-only is
// deliberately absent, so a folder whose photographs all live in subfolders
// still proves reachable rather than looking empty.
func probeRcloneFolder(ctx context.Context, remote string) error {
	_, err := runRclone(ctx, photoProbeTimeout, "lsjson", remote, "--max-depth", "1", "--no-modtime")
	return err
}

// catRclone downloads one file to memory. The path argument is only for the
// error message; remote already carries it.
//
// --head stops the download one byte past the ceiling. The manifest already
// skips anything listed as oversized, but runRclone buffers everything rclone
// prints, so a file that grew after the listing would otherwise be held in
// memory whole before normalizePhoto's own limit ever saw it. One byte over is
// all normalizePhoto needs to refuse it.
func catRclone(ctx context.Context, remote, file string) ([]byte, error) {
	out, err := runRclone(ctx, photoFetchTimeout, "cat", remote,
		"--head", strconv.Itoa(photoMaxSourceBytes+1))
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", path.Base(file), err)
	}
	return out, nil
}
