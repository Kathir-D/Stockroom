package stockroom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Tests for the Drive source (docs/design/signin-photo-wall.html §3). rclone
// sits behind two function fields on the source, so none of this needs the
// binary, a network or a Google account -- which is the same reason the reel's
// tests could be written before this file existed.

// newTestSource builds a source with both rclone calls stubbed out. It does
// not go through NewDrivePhotoSource because that insists on finding the
// rclone binary on PATH, which a CI runner has no reason to have.
func newTestSource(t *testing.T, folderID string) *DrivePhotoSource {
	t.Helper()
	return &DrivePhotoSource{
		remote:   "gdrive",
		dir:      t.TempDir(),
		interval: DefaultPhotoWallManifestHours * time.Hour,
		rebuild:  make(chan struct{}, 1),
		folderID: folderID,
		now:      time.Now,
		list: func(context.Context, string) (io.ReadCloser, func() error, error) {
			return nil, nil, errors.New("no listing stubbed for this test")
		},
		fetch: func(context.Context, string, string) ([]byte, error) {
			return nil, errors.New("no download stubbed for this test")
		},
	}
}

// stubListing turns rclone lsjson output into the pair of values the real
// streamer returns.
func stubListing(body string) func(context.Context, string) (io.ReadCloser, func() error, error) {
	return func(context.Context, string) (io.ReadCloser, func() error, error) {
		return io.NopCloser(strings.NewReader(body)), func() error { return nil }, nil
	}
}

// TestDriveManifestFiltersTheListing is §3's manifest: paths and sizes only,
// no image data, and only the files the wall could actually show. The size
// ceiling is applied here rather than after the download, so a pathological
// original costs a line of JSON instead of forty megabytes.
func TestDriveManifestFiltersTheListing(t *testing.T) {
	s := newTestSource(t, "FOLDER-1")
	s.list = stubListing(`[
	  {"Path":"events/gala.jpg","Name":"gala.jpg","Size":2400000,"IsDir":false},
	  {"Path":"events/RAW.JPEG","Name":"RAW.JPEG","Size":1200000,"IsDir":false},
	  {"Path":"logo.png","Name":"logo.png","Size":40000,"IsDir":false},
	  {"Path":"clip.mov","Name":"clip.mov","Size":80000000,"IsDir":false},
	  {"Path":"phone.heic","Name":"phone.heic","Size":3000000,"IsDir":false},
	  {"Path":"events","Name":"events","Size":-1,"IsDir":true},
	  {"Path":"huge.jpg","Name":"huge.jpg","Size":419430400,"IsDir":false}
	]`)

	s.buildManifest(context.Background(), "FOLDER-1")

	got := map[string]bool{}
	for _, e := range s.manifest.Entries {
		got[e.Path] = true
	}
	want := []string{"events/gala.jpg", "events/RAW.JPEG", "logo.png"}
	if len(got) != len(want) {
		t.Fatalf("manifest holds %v, want exactly %v", got, want)
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("manifest is missing %s", w)
		}
	}

	// Written through, so a restart does not cost another listing.
	var onDisk photoManifest
	raw, err := os.ReadFile(filepath.Join(s.dir, photoWallManifest))
	if err != nil {
		t.Fatalf("manifest.json: %v", err)
	}
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatal(err)
	}
	if onDisk.FolderID != "FOLDER-1" || len(onDisk.Entries) != len(want) {
		t.Fatalf("manifest.json holds %+v", onDisk)
	}
}

// TestDriveManifestSurvivesAFailedListing is the staged-write discipline: a
// listing that dies half way through must never replace a working manifest.
// The alternative is a wall that goes dark because the uplink blinked during a
// weekly refresh.
func TestDriveManifestSurvivesAFailedListing(t *testing.T) {
	s := newTestSource(t, "FOLDER-1")
	s.list = stubListing(`[{"Path":"a.jpg","Name":"a.jpg","Size":1000,"IsDir":false}]`)
	s.buildManifest(context.Background(), "FOLDER-1")
	before := s.manifest

	s.list = func(context.Context, string) (io.ReadCloser, func() error, error) {
		return io.NopCloser(strings.NewReader(`[{"Path":"b.jpg",`)),
			func() error { return errors.New("connection reset") }, nil
	}
	s.buildManifest(context.Background(), "FOLDER-1")

	if s.manifest != before {
		t.Fatal("a failed listing replaced the working manifest")
	}
	if s.lastErr == nil {
		t.Error("a failed listing should be reportable on the admin screen")
	}
	// And it must not retry a minute later, or a dead uplink becomes a loop
	// against the same Drive quota the nightly backup depends on.
	if _, due := s.buildDue(); due {
		t.Error("a failed listing should back off before trying again")
	}
}

// TestDriveManifestReloadsAfterARestart: the manifest is the one artifact of
// this feature worth keeping across a restart, because rebuilding it is
// minutes of Drive listing where a tile is a second.
func TestDriveManifestReloadsAfterARestart(t *testing.T) {
	first := newTestSource(t, "FOLDER-1")
	// The reel owns the directory and marks it before the source ever writes
	// there, so the test adopts it in the same order the server does.
	if err := wipePhotoWallDir(first.dir); err != nil {
		t.Fatalf("wipePhotoWallDir: %v", err)
	}
	first.list = stubListing(`[{"Path":"a.jpg","Name":"a.jpg","Size":1000,"IsDir":false}]`)
	first.buildManifest(context.Background(), "FOLDER-1")

	// The reel wipes its cache directory on every boot. The manifest has to
	// come through that, which is why wipePhotoWallDir skips it by name.
	if err := wipePhotoWallDir(first.dir); err != nil {
		t.Fatalf("the second boot's wipe: %v", err)
	}
	if _, err := os.Stat(filepath.Join(first.dir, photoWallManifest)); err != nil {
		t.Fatalf("the boot wipe deleted the manifest: %v", err)
	}

	same := newTestSource(t, "FOLDER-1")
	same.dir = first.dir
	same.loadManifest()
	if same.manifest == nil || len(same.manifest.Entries) != 1 {
		t.Fatal("the manifest did not survive a restart")
	}
	if _, due := same.buildDue(); due {
		t.Error("a fresh manifest should not trigger an immediate re-listing")
	}

	// A manifest describing a folder that is no longer live is worse than no
	// manifest: it feeds the wall from the folder somebody just replaced.
	other := newTestSource(t, "FOLDER-2")
	other.dir = first.dir
	other.loadManifest()
	if other.manifest != nil {
		t.Fatal("a manifest for the previous folder was adopted")
	}
	if _, due := other.buildDue(); !due {
		t.Error("a folder with no manifest should be listed at once")
	}
}

// TestDriveNextPhotoRetriesPastRubbish is PhotoSource's contract: rejecting
// unusable files and retrying past them is the source's business, because a
// real folder legitimately holds portraits, panoramas and video that the ratio
// gate turns away. An error out of NextPhoto means "nothing usable right now",
// and the reel answers that by backing off.
func TestDriveNextPhotoRetriesPastRubbish(t *testing.T) {
	s := newTestSource(t, "FOLDER-1")
	s.list = stubListing(`[
	  {"Path":"a.jpg","Name":"a.jpg","Size":1000,"IsDir":false},
	  {"Path":"b.jpg","Name":"b.jpg","Size":1000,"IsDir":false}
	]`)
	s.buildManifest(context.Background(), "FOLDER-1")

	calls := 0
	good := testJPEG(t, 1500, 1000)
	s.fetch = func(context.Context, string, string) ([]byte, error) {
		calls++
		switch calls {
		case 1:
			return []byte("a .mov that somebody renamed"), nil
		case 2:
			return nil, errors.New("connection reset mid-download")
		default:
			return good, nil
		}
	}

	tile, err := s.NextPhoto(context.Background())
	if err != nil {
		t.Fatalf("NextPhoto: %v", err)
	}
	if calls != 3 {
		t.Fatalf("gave up after %d attempts", calls)
	}
	if b := decodeTile(t, tile).Bounds(); b.Dx() != photoTileWidth || b.Dy() != photoTileHeight {
		t.Fatalf("NextPhoto returned a %dx%d tile", b.Dx(), b.Dy())
	}
}

// TestDriveNestedPathsSurviveToTheFetch. The source folder is a real Drive
// folder, which means subfolders -- events, a year, a shoot -- and `lsjson -R`
// reports a path relative to the root rather than a bare filename.
//
// Every link in the chain already had its own test and none of them covered
// the chain: the manifest keeps `events/gala.jpg`, and driveRoot composes a
// path onto the connection string, but nothing asserted that the path the
// manifest stored is the path the download is given. A `path.Base` added
// anywhere between them would pass both of those tests and fetch nothing, and
// the symptom would be an empty wall with a download error nobody reads.
func TestDriveNestedPathsSurviveToTheFetch(t *testing.T) {
	s := newTestSource(t, "FOLDER-1")
	// A space, a nested directory and an uppercase extension: all three are
	// ordinary in a folder a person filled, and all three have been a bug
	// somewhere. The extension filter lowercases; the path never touches the
	// local filesystem, so its separators stay rclone's.
	const nested = "events/gala 2026/sub/DSC_0142.JPG"
	s.list = stubListing(`[
	  {"Path":"` + nested + `","Name":"DSC_0142.JPG","Size":1000,"IsDir":false}
	]`)
	s.buildManifest(context.Background(), "FOLDER-1")
	if len(s.manifest.Entries) != 1 || s.manifest.Entries[0].Path != nested {
		t.Fatalf("manifest = %+v, want the nested path kept whole", s.manifest.Entries)
	}

	var sawRemote string
	good := testJPEG(t, 1500, 1000)
	s.fetch = func(_ context.Context, remote, _ string) ([]byte, error) {
		sawRemote = remote
		return good, nil
	}
	if _, err := s.NextPhoto(context.Background()); err != nil {
		t.Fatalf("NextPhoto: %v", err)
	}
	if want := "gdrive,root_folder_id=FOLDER-1:" + nested; sawRemote != want {
		t.Errorf("rclone cat was given %q, want %q", sawRemote, want)
	}
}

// TestDriveNextPhotoGivesUp bounds the retry, so a folder holding nothing but
// video returns instead of downloading it all.
func TestDriveNextPhotoGivesUp(t *testing.T) {
	s := newTestSource(t, "FOLDER-1")
	s.list = stubListing(`[{"Path":"a.jpg","Name":"a.jpg","Size":1000,"IsDir":false}]`)
	s.buildManifest(context.Background(), "FOLDER-1")

	calls := 0
	s.fetch = func(context.Context, string, string) ([]byte, error) {
		calls++
		return []byte("not a photograph"), nil
	}
	if _, err := s.NextPhoto(context.Background()); err == nil {
		t.Fatal("want an error when nothing in the folder is usable")
	}
	if calls != photoFetchAttempts {
		t.Fatalf("made %d attempts, want %d", calls, photoFetchAttempts)
	}
}

// TestDriveNextPhotoSaysWhyItHasNothing. Every one of these resolves to an
// empty wall and a normal sign-in screen; the distinction exists for §7's
// screen, which is where an admin finds out whether to wait or to fix
// something.
func TestDriveNextPhotoSaysWhyItHasNothing(t *testing.T) {
	unset := newTestSource(t, "")
	if _, err := unset.NextPhoto(context.Background()); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("no folder chosen should be ErrNotConfigured, got %v", err)
	}

	waiting := newTestSource(t, "FOLDER-1")
	_, err := waiting.NextPhoto(context.Background())
	if err == nil || !strings.Contains(err.Error(), "manifest") {
		t.Fatalf("no manifest yet should say so, got %v", err)
	}
}

// TestDriveSetFolderDiscardsTheManifest is the source's half of §7's switch.
// Leaving the old manifest in place would keep the wall drawing from the
// folder an admin has just replaced, which is the exact outcome that screen
// exists to prevent.
func TestDriveSetFolderDiscardsTheManifest(t *testing.T) {
	s := newTestSource(t, "FOLDER-1")
	s.list = stubListing(`[{"Path":"a.jpg","Name":"a.jpg","Size":1000,"IsDir":false}]`)
	s.buildManifest(context.Background(), "FOLDER-1")

	if s.SetFolder("FOLDER-1") {
		t.Error("pasting the folder that is already live is not a change")
	}
	if s.manifest == nil {
		t.Fatal("a no-op switch threw the manifest away")
	}

	if !s.SetFolder("FOLDER-2") {
		t.Error("a new folder is a change")
	}
	if s.manifest != nil {
		t.Fatal("the manifest for the replaced folder survived the switch")
	}
	select {
	case <-s.rebuild:
	default:
		t.Error("a switch should ask for a rebuild rather than wait a week")
	}
	if st := s.Status(); st.PhotoCount != 0 || !st.Configured {
		t.Errorf("status after a switch = %+v", st)
	}
}

// TestDriveSwitchMidListingIsDiscarded: a listing takes minutes, and an admin
// can replace the folder while one is running. Filing that result under the
// new folder would put photographs from the old one on the wall, with nothing
// on any screen connecting the two events.
func TestDriveSwitchMidListingIsDiscarded(t *testing.T) {
	s := newTestSource(t, "FOLDER-2")
	s.list = stubListing(`[{"Path":"old.jpg","Name":"old.jpg","Size":1000,"IsDir":false}]`)

	// A listing that began under FOLDER-1, landing after the switch.
	s.buildManifest(context.Background(), "FOLDER-1")

	if s.manifest != nil {
		t.Fatal("a listing of the replaced folder was filed under the new one")
	}
	if _, err := os.Stat(filepath.Join(s.dir, photoWallManifest)); !os.IsNotExist(err) {
		t.Fatal("a listing of the replaced folder was written to disk")
	}
}

// TestDriveEmptyFolderIsReported. Permitted -- the folder may be mid-upload --
// but an empty wall with no explanation is indistinguishable from a broken
// feature, so it has to reach §7's screen as a sentence.
func TestDriveEmptyFolderIsReported(t *testing.T) {
	s := newTestSource(t, "FOLDER-1")
	s.list = stubListing(`[{"Path":"clip.mov","Name":"clip.mov","Size":1000,"IsDir":false}]`)
	s.buildManifest(context.Background(), "FOLDER-1")

	st := s.Status()
	if st.PhotoCount != 0 {
		t.Fatalf("photo count = %d", st.PhotoCount)
	}
	if !strings.Contains(st.LastError, "no usable photographs") {
		t.Errorf("last error = %q", st.LastError)
	}
}

// TestDriveRootNamesTheFolderPerCommand is §3's containment: the remote is
// configured with root_folder_id left blank, so a remote pinned to one folder
// can never be asked for a different one, and every command names the folder
// it is allowed to touch.
func TestDriveRootNamesTheFolderPerCommand(t *testing.T) {
	s := newTestSource(t, "FOLDER-1")
	if got, want := s.driveRoot("FOLDER-1"), "gdrive,root_folder_id=FOLDER-1:"; got != want {
		t.Fatalf("driveRoot = %q, want %q", got, want)
	}
	if got, want := s.driveRoot("FOLDER-1", "events/gala.jpg"),
		"gdrive,root_folder_id=FOLDER-1:events/gala.jpg"; got != want {
		t.Fatalf("driveRoot with a file = %q, want %q", got, want)
	}
}

// TestDriveRunStopsWithContext: the refresher's lifetime is the signal
// context, so Ctrl+C ends it with everything else.
func TestDriveRunStopsWithContext(t *testing.T) {
	s := newTestSource(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return when its context was cancelled")
	}
}

// TestDriveListingCountsProgress. A large folder takes minutes to list, and
// §7's screen has to be able to say "rebuilding -- 12,400 files listed so far"
// rather than showing an empty wall with no explanation.
func TestDriveListingCountsProgress(t *testing.T) {
	var body bytes.Buffer
	body.WriteString("[")
	for i := 0; i < 1200; i++ {
		if i > 0 {
			body.WriteString(",")
		}
		body.WriteString(`{"Path":"clip.mov","Name":"clip.mov","Size":10,"IsDir":false}`)
	}
	body.WriteString("]")

	s := newTestSource(t, "FOLDER-1")
	s.list = stubListing(body.String())
	s.buildManifest(context.Background(), "FOLDER-1")

	if got := s.Status().ListedSoFar; got != 1200 {
		t.Fatalf("listed %d files, want 1200", got)
	}
}

// TestDriveManifestIsNotInTheServedDirectory is §0's second barrier -- the
// frontend never receives a Drive file ID or a folder listing -- held against
// §5's static mount.
//
// The manifest is exactly such a listing, and http.FileServer refuses to
// enumerate a directory but serves any file in one by name. A flat cache
// directory would therefore put the whole of the Drive folder's structure one
// guessed URL away, on the only unauthenticated screen in the application. The
// tiles live in their own subdirectory so that the directory §5 mounts holds
// nothing else by construction, rather than because a handler remembered to
// exclude a filename.
func TestDriveManifestIsNotInTheServedDirectory(t *testing.T) {
	dir := t.TempDir()
	w, err := NewPhotoWall(PhotoWallOptions{Dir: dir})
	if err != nil {
		t.Fatalf("NewPhotoWall: %v", err)
	}

	s := newTestSource(t, "FOLDER-1")
	s.dir = dir
	s.list = stubListing(`[{"Path":"events/gala.jpg","Name":"gala.jpg","Size":1000,"IsDir":false}]`)
	s.buildManifest(context.Background(), "FOLDER-1")

	if _, err := os.Stat(filepath.Join(dir, photoWallManifest)); err != nil {
		t.Fatalf("the manifest should be in the cache root: %v", err)
	}
	served, err := os.ReadDir(w.TileDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range served {
		t.Errorf("the served directory holds %q, which is not a tile", e.Name())
	}
}
