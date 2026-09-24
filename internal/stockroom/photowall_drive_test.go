package stockroom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// TestDriveListingReadsWhatRcloneActuallyPrints is the listing as rclone
// 1.75 really emits it under --no-modtime: every field present, and ModTime
// an empty string rather than absent. Every other fixture in this file leaves
// ModTime out, which is the one shape rclone never produces, and so the suite
// passed while the first listing against a real Drive failed on its first
// line -- the listing decoded into a struct whose time.Time field refuses "".
// Found by §10's manual pass, not by a test, which is why this one exists.
func TestDriveListingReadsWhatRcloneActuallyPrints(t *testing.T) {
	s := newTestSource(t, "FOLDER-1")
	s.list = stubListing(`[
{"Path":"events/gala 2026/DSC_0142.JPG","Name":"DSC_0142.JPG","Size":2400000,"MimeType":"image/jpeg","ModTime":"","IsDir":false,"ID":"1aBcDeFgHiJkLmNoPqRsTuVwXyZ012345"},
{"Path":"notes.docx","Name":"notes.docx","Size":12966,"MimeType":"application/vnd.openxmlformats-officedocument.wordprocessingml.document","ModTime":"","IsDir":false,"ID":"1zYxWvUtSrQpOnMlKjIhGfEdCbA543210"}
]`)

	s.buildManifest(context.Background(), "FOLDER-1")

	if s.lastErr != nil {
		t.Fatalf("the listing was refused: %v", s.lastErr)
	}
	if len(s.manifest.Entries) != 1 || s.manifest.Entries[0].Path != "events/gala 2026/DSC_0142.JPG" {
		t.Fatalf("manifest holds %+v, want the one photograph", s.manifest.Entries)
	}
}

// TestDriveNotReadyIsWarmingUp: the source's two normal not-yet states are
// tagged so the reel does not count them, and its real failures are not.
func TestDriveNotReadyIsWarmingUp(t *testing.T) {
	ctx := context.Background()

	_, err := newTestSource(t, "").NextPhoto(ctx)
	if !errors.Is(err, errPhotoWallWarmingUp) || !errors.Is(err, ErrNotConfigured) {
		t.Errorf("no folder chosen: %v, want warming up and still ErrNotConfigured", err)
	}
	s := newTestSource(t, "FOLDER-1")
	if _, err := s.NextPhoto(ctx); !errors.Is(err, errPhotoWallWarmingUp) {
		t.Errorf("manifest not built yet: %v, want warming up", err)
	}
	s.listing = true
	if _, err := s.NextPhoto(ctx); !errors.Is(err, errPhotoWallWarmingUp) {
		t.Errorf("first listing running: %v, want warming up", err)
	}

	// A listing that failed is a failure: counted, it is what makes a machine
	// with no internet rest rather than ask every ten seconds forever.
	s.listing = false
	s.lastErr = errors.New("list the Drive folder: dial tcp: no such host")
	if _, err := s.NextPhoto(ctx); err == nil || errors.Is(err, errPhotoWallWarmingUp) {
		t.Errorf("failed listing: %v, want a failure the reel counts", err)
	}
	// So is a folder that listed fine and holds nothing usable (§9).
	empty := newTestSource(t, "FOLDER-1")
	empty.list = stubListing(`[]`)
	empty.buildManifest(ctx, "FOLDER-1")
	if _, err := empty.NextPhoto(ctx); err == nil || errors.Is(err, errPhotoWallWarmingUp) {
		t.Errorf("empty folder: %v, want a failure the reel counts", err)
	}
}

// TestDriveErrorsNeverCarryTheFolderID is §7's promise -- no response carries
// the folder id -- held against rclone's real failure text, which breaks it.
// A failed call quotes the Drive API request it made, and that request's query
// string is `'<id>' in parents`; everything the source keeps reaches GET
// /admin/photo-wall as last_error. The strings below are rclone 1.75.1's own,
// captured with a revoked token and with the network down, with the id and the
// remote swapped for the test's.
func TestDriveErrorsNeverCarryTheFolderID(t *testing.T) {
	const id = "1AbCd-EfGh_IjKlMnOpQrStUvWxYz0123"
	const notice = `2026/09/22 17:33:43 NOTICE: gdrive{PDhs9}: This remote uses rclone's shared Google Drive client_id, which is being retired and will stop working during 2026. Create your own client_id to avoid interruption: https://rclone.org/drive/#making-your-own-client-id `
	request := `Get "https://www.googleapis.com/drive/v3/files?alt=json&q=trashed%3Dfalse+and+%28%27` + id + `%27+in+parents%29&supportsAllDrives=true"`
	expired := notice + `2026/09/22 17:33:56 NOTICE: Failed to cat: couldn't list directory: ` + request +
		`: couldn't fetch token: invalid_grant: maybe token expired? - try refreshing with "rclone config reconnect gdrive{PDhs9}:"`
	offline := notice + `2026/09/22 17:40:02 NOTICE: Failed to lsjson: error in ListJSON: couldn't list directory: ` + request +
		`: dial tcp: lookup www.googleapis.com: no such host`

	clean := func(where, msg string) {
		t.Helper()
		for _, bad := range []string{id, "googleapis.com/drive", "client_id", "{PDhs9}"} {
			if strings.Contains(msg, bad) {
				t.Errorf("%s carries %q: %s", where, bad, msg)
			}
		}
	}
	ctx := context.Background()
	logged := captureLog(t)

	// A download with an expired token: the fix named with a remote a person
	// can type, and warned about once however many downloads fail.
	s := newTestSource(t, id)
	s.manifest = &photoManifest{FolderID: id, Entries: []photoEntry{{Path: "a.jpg", Size: 1000}}}
	s.fetch = func(context.Context, string, string) ([]byte, error) {
		return nil, fmt.Errorf("download a.jpg: %s", expired)
	}
	var err error
	for i := 0; i < 3; i++ {
		_, err = s.NextPhoto(ctx)
	}
	clean("NextPhoto's error", err.Error())
	if !errors.Is(err, errPhotoDriveAuth) || !strings.Contains(err.Error(), "rclone config reconnect gdrive:") ||
		!strings.Contains(err.Error(), "Admin → Photo wall") {
		t.Errorf("an expired sign-in reads %q, want the Photo wall button and the reconnect command for the configured remote", err)
	}
	if n := strings.Count(logged.String(), "warning: sign-in photo wall"); n != 1 {
		t.Errorf("%d expired-sign-in warnings after three failed downloads, want exactly 1:\n%s", n, logged.String())
	}
	clean("the log", logged.String())

	// A success re-arms it: a token reconnected and later expired again is
	// warned about again rather than silently.
	jpg := testJPEG(t, 1500, 1000)
	s.fetch = func(context.Context, string, string) ([]byte, error) { return jpg, nil }
	if _, err := s.NextPhoto(ctx); err != nil {
		t.Fatalf("NextPhoto with a good download: %v", err)
	}
	s.fetch = func(context.Context, string, string) ([]byte, error) {
		return nil, fmt.Errorf("download a.jpg: %s", expired)
	}
	_, _ = s.NextPhoto(ctx)
	if n := strings.Count(logged.String(), "warning: sign-in photo wall"); n != 2 {
		t.Errorf("%d warnings after the sign-in expired a second time, want 2", n)
	}

	// A listing with the network down: the cause survives, the id does not.
	s.list = func(context.Context, string) (io.ReadCloser, func() error, error) {
		return io.NopCloser(strings.NewReader("[")), func() error { return errors.New(offline) }, nil
	}
	s.buildManifest(ctx, id)
	st := s.Status()
	clean("the listing's last_error", st.LastError)
	if !strings.Contains(st.LastError, "no such host") {
		t.Errorf("the listing's last_error lost its cause: %q", st.LastError)
	}

	// The probe behind PUT /admin/photo-wall: an expired sign-in is the
	// server's credential and says so, rather than blaming the folder's
	// sharing; anything else is still the link's 400.
	s.probe = func(context.Context, string) error { return errors.New(expired) }
	err = s.Probe(ctx, id)
	clean("the probe's error", err.Error())
	if !errors.Is(err, ErrNotConfigured) || !strings.Contains(err.Error(), "rclone config reconnect gdrive:") {
		t.Errorf("probe with an expired sign-in: %v, want ErrNotConfigured naming the reconnect", err)
	}
	s.probe = func(context.Context, string) error { return errors.New(offline) }
	err = s.Probe(ctx, id)
	clean("the probe's error", err.Error())
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("probe offline: %v, want ErrInvalid", err)
	}
	// Drive also names an id bare, outside any request URL, when the thing it
	// could not find is the folder itself.
	s.probe = func(context.Context, string) error {
		return errors.New(notice + `2026/09/22 17:41:10 NOTICE: Failed to lsjson: googleapi: Error 404: File not found: ` + id + `., notFound`)
	}
	err = s.Probe(ctx, id)
	clean("the probe's error", err.Error())
	if !strings.Contains(err.Error(), "File not found") {
		t.Errorf("probe of a missing folder lost its cause: %v", err)
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

// TestDriveSetFolderCancelsTheListingItReplaces: the refresher is one
// goroutine, so a switch made while the old folder is still being listed used
// to wait behind minutes of Drive traffic whose result was then thrown away --
// and if that listing failed, its error was filed against the *new* folder and
// pushed the new folder's first listing behind the five-minute retry gate.
func TestDriveSetFolderCancelsTheListingItReplaces(t *testing.T) {
	s := newTestSource(t, "FOLDER-1")
	started := make(chan struct{})
	s.list = func(ctx context.Context, _ string) (io.ReadCloser, func() error, error) {
		close(started)
		<-ctx.Done()
		return nil, nil, ctx.Err()
	}

	done := make(chan struct{})
	go func() {
		s.buildManifest(context.Background(), "FOLDER-1")
		close(done)
	}()
	<-started
	s.SetFolder("FOLDER-2")

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the listing of the replaced folder kept running after the switch")
	}
	if st := s.Status(); st.LastError != "" || st.Listing {
		t.Errorf("status after the switch = %+v; the old folder's listing is not news about the new one", st)
	}
	if folderID, due := s.buildDue(); !due || folderID != "FOLDER-2" {
		t.Errorf("buildDue() = %q, %v; the new folder must be listed now, not after the retry gate", folderID, due)
	}
}

// TestDriveRebuildKeepsTheManifestServing is §7's Rebuild button: it re-lists
// the folder that is already live, so the listing it is replacing is still
// correct and must keep feeding the wall for the minutes the new one takes
// (§3). Dropping it would empty the wall the moment an admin pressed the
// button -- the feature appearing to break as a direct result of somebody
// asking for more of it.
func TestDriveRebuildKeepsTheManifestServing(t *testing.T) {
	s := newTestSource(t, "FOLDER-1")
	s.list = stubListing(`[{"Path":"a.jpg","Name":"a.jpg","Size":1000,"IsDir":false}]`)
	s.buildManifest(context.Background(), "FOLDER-1")
	before := s.manifest

	s.Rebuild()

	if s.manifest != before {
		t.Fatal("Rebuild discarded the manifest the wall is still serving from")
	}
	if _, _, ok := s.pick(); !ok {
		t.Error("the wall went dark while the rebuild was pending")
	}
	// And the press has to actually list: with the manifest kept, a fresh one
	// would otherwise read as "not due for another week".
	folderID, due := s.buildDue()
	if !due || folderID != "FOLDER-1" {
		t.Fatalf("buildDue() = %q, %v; want the pressed folder", folderID, due)
	}
	if _, due := s.buildDue(); due {
		t.Error("one press listed twice")
	}

	// A folder switch is the other case, and still discards: that manifest
	// describes a folder that is no longer live.
	if !s.SetFolder("FOLDER-2") {
		t.Fatal("SetFolder reported no change")
	}
	if s.manifest != nil {
		t.Error("a folder switch kept the previous folder's manifest")
	}
}

// TestDriveRebuildBeatsTheRetryGate: a failed listing backs off for five
// minutes, and an admin pressing Rebuild has usually just fixed whatever made
// it fail. The press is not swallowed by that gate.
func TestDriveRebuildBeatsTheRetryGate(t *testing.T) {
	s := newTestSource(t, "FOLDER-1")
	s.list = func(context.Context, string) (io.ReadCloser, func() error, error) {
		return nil, nil, errors.New("connection reset")
	}
	s.buildManifest(context.Background(), "FOLDER-1")
	if _, due := s.buildDue(); due {
		t.Fatal("a failed listing should back off before trying again")
	}

	s.Rebuild()
	if _, due := s.buildDue(); !due {
		t.Error("the retry gate swallowed an admin's Rebuild press")
	}
}

// TestDriveRebuildWithNoFolderQueuesNothing: pressing Rebuild before a folder
// has been chosen must not leave a request sitting in wait, or the eventual
// SetFolder's listing would be followed straight away by a redundant second
// one -- minutes of Drive listing for nothing.
func TestDriveRebuildWithNoFolderQueuesNothing(t *testing.T) {
	s := newTestSource(t, "")
	s.Rebuild()
	if _, due := s.buildDue(); due {
		t.Fatal("listed a folder that has not been chosen")
	}

	s.list = stubListing(`[{"Path":"a.jpg","Name":"a.jpg","Size":1000,"IsDir":false}]`)
	s.SetFolder("FOLDER-1")
	folderID, due := s.buildDue()
	if !due || folderID != "FOLDER-1" {
		t.Fatalf("buildDue() = %q, %v; want the newly chosen folder", folderID, due)
	}
	s.buildManifest(context.Background(), folderID)
	if _, due := s.buildDue(); due {
		t.Error("a stale Rebuild request re-listed the folder immediately after")
	}
}
