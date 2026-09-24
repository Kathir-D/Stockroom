package stockroom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// Tests for admin control of the photo wall
// (docs/design/signin-photo-wall.html §7 and §10's last five bullets). The
// rclone seam keeps all of the link and switch behaviour off the network; the
// ones that touch app_settings need the local Postgres and skip without it,
// like every other database test in this package.

/* ---------------------------------------------------------- link parse ---- */

// Every form Drive's Share button can put on the clipboard yields the same
// id, and everything else is refused with a message naming those forms.
// Requiring a hand-extracted id would be the kind of demand that gets a
// folder misconfigured once and then avoided forever.
func TestParseDriveFolderLink(t *testing.T) {
	const id = "1N5pUYCMabcdefgHIJklmn0w7qm"

	accepted := []string{
		"https://drive.google.com/drive/folders/" + id + "?usp=sharing",
		"https://drive.google.com/drive/folders/" + id,
		"https://drive.google.com/drive/u/0/folders/" + id,
		"https://drive.google.com/drive/u/3/folders/" + id + "?usp=drive_link",
		"https://drive.google.com/open?id=" + id,
		id,
		"  " + id + "  ",
	}
	for _, in := range accepted {
		got, err := ParseDriveFolderLink(in)
		if err != nil {
			t.Errorf("ParseDriveFolderLink(%q) = error %v, want %q", in, err, id)
			continue
		}
		if got != id {
			t.Errorf("ParseDriveFolderLink(%q) = %q, want %q", in, got, id)
		}
	}

	refused := []string{
		"",
		"   ",
		"the shared folder Jamie made",
		"https://docs.google.com/document/d/" + id + "/edit",
		"https://drive.google.com/file/d/" + id + "/view",
		"https://example.com/drive/folders/" + id,
		"https://drive.google.com/drive/folders/",
		"https://drive.google.com/drive/my-drive",
		"short",
	}
	for _, in := range refused {
		if got, err := ParseDriveFolderLink(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseDriveFolderLink(%q) = %q, %v; want ErrInvalid", in, got, err)
		}
	}
}

/* ---------------------------------------------------------- the switch ---- */

// probeSource is a source whose rclone calls are all stubbed. The probe is
// what a paste is validated against, so its outcome is the thing these tests
// drive.
func probeSource(t *testing.T, folderID string, probeErr error) *DrivePhotoSource {
	t.Helper()
	s := newTestSource(t, folderID)
	s.probe = func(context.Context, string) error { return probeErr }
	return s
}

// A folder Drive will not answer for leaves everything exactly as it was:
// nothing written, the previous folder still live, the reel untouched. This
// is the whole reason the write is not a plain settings field -- without it a
// typo returns a success message and the wall silently empties ten minutes
// later, with nothing on any screen connecting the two events.
func TestPhotoWallProbeGatesTheWrite(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	defer restorePhotoWallFolder(t, db)()

	const oldID = "1OLDfolderIDaaaaaaaaaaaaaaa"
	source := probeSource(t, oldID, nil)
	wall, _ := newTestWall(t, PhotoWallOptions{Count: 2, Source: &stubSource{data: []byte("tile")}})
	db.SetPhotoWall(wall, source)
	t.Cleanup(func() { db.SetPhotoWall(nil, nil) })

	// A folder that does work, so there is a previous setting to protect.
	if _, err := db.SetPhotoWallFolder(ctx, admin, "https://drive.google.com/drive/folders/"+oldID, "Last season"); err != nil {
		t.Fatalf("SetPhotoWallFolder: %v", err)
	}
	fill(t, wall)
	before := len(tileFiles(t, wall))
	if before == 0 {
		t.Fatal("the reel did not fill, so this test cannot tell a preserved reel from an empty one")
	}

	// Now one that does not.
	source.probe = func(context.Context, string) error {
		return errors.New("directory not found")
	}
	const badID = "1BADfolderIDbbbbbbbbbbbbbbb"
	_, err := db.SetPhotoWallFolder(ctx, admin, "https://drive.google.com/drive/folders/"+badID, "Typo")
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("SetPhotoWallFolder with an unreachable folder = %v, want ErrInvalid", err)
	}
	if !strings.Contains(err.Error(), "shared") {
		t.Errorf("the refusal does not name the likely cause: %v", err)
	}

	gotID, gotLabel, err := db.PhotoWallFolder(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if gotID != oldID || gotLabel != "Last season" {
		t.Errorf("app_settings now holds %q/%q; a failed probe must write nothing", gotID, gotLabel)
	}
	if got := len(tileFiles(t, wall)); got != before {
		t.Errorf("the reel went from %d tiles to %d; a failed probe must not tear it down", before, got)
	}
}

// A switch is a config write *plus* a teardown. Doing only the first leaves
// the wall showing photographs from the folder that was just replaced, which
// is the exact outcome this screen exists to prevent.
func TestPhotoWallSwitchInvalidatesTheReel(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	defer restorePhotoWallFolder(t, db)()

	source := probeSource(t, "1OLDfolderIDaaaaaaaaaaaaaaa", nil)
	source.manifest = &photoManifest{
		FolderID: "1OLDfolderIDaaaaaaaaaaaaaaa",
		BuiltAt:  time.Now(),
		Entries:  []photoEntry{{Path: "a.jpg", Size: 10}},
	}
	wall, clock := newTestWall(t, PhotoWallOptions{Count: 4, Batch: 2, Source: &stubSource{data: []byte("tile")}})
	db.SetPhotoWall(wall, source)
	t.Cleanup(func() { db.SetPhotoWall(nil, nil) })

	fill(t, wall)
	served, _ := wall.TakePhotos(2)
	if len(served) != 2 {
		t.Fatalf("took %d tiles, want 2", len(served))
	}
	genBefore := wall.gen

	if _, err := db.SetPhotoWallFolder(ctx, admin,
		"https://drive.google.com/drive/folders/1NEWfolderIDcccccccccccccc", "This season"); err != nil {
		t.Fatalf("SetPhotoWallFolder: %v", err)
	}

	ready, stillServed := wall.Counts()
	if ready != 0 {
		t.Errorf("%d ready tiles survived the switch; every one belongs to the replaced folder", ready)
	}
	// Served tiles are deliberately left to expire on their own TTL: their
	// URLs sit in a browser that has already rendered them, and 404ing a live
	// page to save fifteen minutes is the worse trade.
	if stillServed != 2 {
		t.Errorf("%d served tiles survived, want 2", stillServed)
	}
	if wall.gen == genBefore {
		t.Error("the generation did not advance, so a fetch in flight would file a photograph from the old folder into the new reel")
	}
	if source.manifest != nil {
		t.Error("the manifest for the replaced folder was kept")
	}
	if len(source.rebuild) != 1 {
		t.Error("no rebuild was queued, so the wall would stay empty until the weekly refresh")
	}

	// And the served tiles still expire on the old schedule rather than being
	// stranded on disk by the switch.
	*clock = clock.Add(DefaultPhotoWallTTL + time.Minute)
	wall.reap()
	if _, servedAfter := wall.Counts(); servedAfter != 0 {
		t.Errorf("%d served tiles outlived their TTL", servedAfter)
	}
}

// Re-pasting the live link -- to rename it, or to be sure -- is a label edit,
// not a switch. Tearing the reel down for it would empty the wall for the
// minutes a refill takes, and every tile it threw away was from the right
// folder. Found in review: SetFolder already reported "nothing changed" for
// exactly this case, and nothing read the answer.
func TestPhotoWallRepastingTheLiveFolderKeepsTheReel(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	defer restorePhotoWallFolder(t, db)()

	const id = "1SAMEfolderIDbbbbbbbbbbbbbb"
	source := probeSource(t, id, nil)
	wall, _ := newTestWall(t, PhotoWallOptions{Count: 4, Batch: 2, Source: &stubSource{data: []byte("tile")}})
	db.SetPhotoWall(wall, source)
	t.Cleanup(func() { db.SetPhotoWall(nil, nil) })

	fill(t, wall)
	genBefore := wall.gen

	status, err := db.SetPhotoWallFolder(ctx, admin, "https://drive.google.com/drive/folders/"+id, "Renamed")
	if err != nil {
		t.Fatalf("SetPhotoWallFolder: %v", err)
	}
	if status.FolderLabel != "Renamed" {
		t.Errorf("label = %q, want the new one", status.FolderLabel)
	}
	if ready, _ := wall.Counts(); ready != 4 {
		t.Errorf("%d ready tiles after re-pasting the live folder, want all 4", ready)
	}
	if wall.gen != genBefore {
		t.Error("the generation advanced for a folder that did not change")
	}
}

// The test that keeps the privacy property from quietly regressing when
// somebody later adds a debug field: neither the read nor the write ever says
// the folder id or a Drive URL out loud.
// TestPhotoWallStatusExplainsARestingReel: the admin screen says why a wall
// with a full manifest is empty. Before this, the status read only the
// source's listing errors, so a reel resting after 25 failed downloads showed
// two thousand photographs, zero tiles and no error at all. A stray failure
// among successes is *not* shown, because that is a healthy wall.
func TestPhotoWallStatusExplainsARestingReel(t *testing.T) {
	portrait := fmt.Errorf("%w: 3024x4032 is 0.75:1, outside 1.33-1.60", errPhotoUnusable)
	src := &stubSource{err: portrait}
	w, _ := newTestWall(t, PhotoWallOptions{Count: 2, Source: src})
	captureLog(t)
	db := &DB{}
	db.SetPhotoWall(w, nil)

	for i := 0; i < photoWallFailureCap-1; i++ {
		w.fillOne(context.Background())
	}
	if st := db.photoWallStatus(context.Background(), photoWallFolder{}); st.LastError != "" {
		t.Errorf("failures short of the rest already show as %q", st.LastError)
	}

	w.fillOne(context.Background())
	st := db.photoWallStatus(context.Background(), photoWallFolder{})
	if !strings.Contains(st.LastError, "3:2") || !strings.Contains(st.LastError, "portrait") || st.LastErrorAt == nil {
		t.Errorf("a reel resting on the ratio gate reads %q, want §9's no-usable-photos sentence naming 3:2", st.LastError)
	}

	src.mu.Lock()
	src.err = errors.New("download a.jpg: dial tcp: no such host")
	src.mu.Unlock()
	w.fillOne(context.Background())
	if st := db.photoWallStatus(context.Background(), photoWallFolder{}); !strings.Contains(st.LastError, "paused") || !strings.Contains(st.LastError, "no such host") {
		t.Errorf("a reel resting on the network reads %q, want paused and the cause", st.LastError)
	}
}

func TestPhotoWallStatusNeverCarriesTheFolderID(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	defer restorePhotoWallFolder(t, db)()

	const id = "1SECRETfolderIDdddddddddddd"
	db.SetPhotoWall(nil, probeSource(t, "", nil))
	t.Cleanup(func() { db.SetPhotoWall(nil, nil) })

	written, err := db.SetPhotoWallFolder(ctx, admin, "https://drive.google.com/drive/folders/"+id, "Department photos")
	if err != nil {
		t.Fatalf("SetPhotoWallFolder: %v", err)
	}
	read, err := db.GetPhotoWallStatus(ctx, admin)
	if err != nil {
		t.Fatalf("GetPhotoWallStatus: %v", err)
	}

	for name, status := range map[string]PhotoWallStatus{"PUT": written, "GET": read} {
		raw, err := json.Marshal(status)
		if err != nil {
			t.Fatal(err)
		}
		body := string(raw)
		if strings.Contains(body, id) {
			t.Errorf("the %s response carries the folder id: %s", name, body)
		}
		if strings.Contains(body, "drive.google.com") {
			t.Errorf("the %s response carries a Drive URL: %s", name, body)
		}
		if !strings.Contains(body, "Department photos") {
			t.Errorf("the %s response does not carry the label, which is all the screen has to identify the folder by: %s", name, body)
		}
	}

	// The label, and only the label, reaches the audit log. The log is
	// exported in backups, so not echoing the link to the screen would buy
	// nothing if the id were sitting in activity_log.csv.
	var details []byte
	if err := db.Pool.QueryRow(ctx, `
		select details from activity_log
		where action = 'signin_photo_wall_folder'
		order by created_at desc limit 1`).Scan(&details); err != nil {
		t.Fatalf("read the audit row: %v", err)
	}
	if strings.Contains(string(details), id) {
		t.Errorf("activity_log holds the folder id: %s", details)
	}
	if !strings.Contains(string(details), "Department photos") {
		t.Errorf("activity_log does not name the folder: %s", details)
	}
}

// The folder id is redacted from the CSV export for the same reason
// github_token is: app_settings is in the public schema, so an unredacted
// export pushes the key to the department's photographs into the backup
// repository.
func TestPhotoWallFolderIsRedactedFromTheExport(t *testing.T) {
	cols := exportRedactions["app_settings"]
	for _, c := range cols {
		if c == "signin_photos_folder_id" {
			return
		}
	}
	t.Errorf("signin_photos_folder_id is not in exportRedactions[app_settings] (%v); the first nightly backup would push it off the machine", cols)
}

// Every one of the four is admin-only, enforced in the package rather than by
// the router.
func TestPhotoWallAdminOnly(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	student := actorFor(insertTestProfile(t, db, false, "student-pw"))

	if _, err := db.GetPhotoWallStatus(ctx, student); !errors.Is(err, ErrForbidden) {
		t.Errorf("GetPhotoWallStatus as a student = %v, want ErrForbidden", err)
	}
	if _, err := db.SetPhotoWallFolder(ctx, student, "1abcdefghijklmnop", "x"); !errors.Is(err, ErrForbidden) {
		t.Errorf("SetPhotoWallFolder as a student = %v, want ErrForbidden", err)
	}
	if _, err := db.RebuildPhotoWallManifest(ctx, student); !errors.Is(err, ErrForbidden) {
		t.Errorf("RebuildPhotoWallManifest as a student = %v, want ErrForbidden", err)
	}
	if _, err := db.PhotoWallPreview(ctx, student, 6); !errors.Is(err, ErrForbidden) {
		t.Errorf("PhotoWallPreview as a student = %v, want ErrForbidden", err)
	}
}

/* --------------------------------------------------------- the preview ---- */

// A preview must not consume the buffer the sign-in screen is about to draw
// from: an admin reloading the screen a few times would otherwise empty the
// wall for the next person to walk up.
func TestPhotoWallPreviewDoesNotConsume(t *testing.T) {
	wall, _ := newTestWall(t, PhotoWallOptions{Count: 8, Batch: 8, Source: &stubSource{data: []byte("tile")}})
	fill(t, wall)

	first := wall.PreviewPhotos(6)
	second := wall.PreviewPhotos(6)
	if len(first) != 6 || len(second) != 6 {
		t.Fatalf("previews returned %d and %d tiles, want 6 each", len(first), len(second))
	}
	if ready, served := wall.Counts(); ready != 8 || served != 0 {
		t.Errorf("after two previews the reel is %d ready / %d served, want 8/0", ready, served)
	}

	// And the tiles a preview named are still there for the sign-in screen.
	batch, _ := wall.TakePhotos(8)
	if len(batch) != 8 {
		t.Errorf("the sign-in batch got %d tiles after two previews, want 8", len(batch))
	}
}

// Nil is the state most of this code runs in -- no remote configured -- so
// the admin surface has to answer it rather than crash on it.
func TestPhotoWallAdminSurvivesTheOffState(t *testing.T) {
	var wall *PhotoWall
	if got := wall.PreviewPhotos(6); len(got) != 0 {
		t.Errorf("PreviewPhotos on a nil reel = %v", got)
	}
	if ready, served := wall.Counts(); ready != 0 || served != 0 {
		t.Errorf("Counts on a nil reel = %d/%d", ready, served)
	}
	var src *DrivePhotoSource
	src.Rebuild() // must not panic
	if err := src.Probe(context.Background(), "1abcdefghijklmnop"); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("Probe on a nil source = %v, want ErrNotConfigured", err)
	}
}

// The probe is bounded on purpose: an admin is standing at the screen waiting
// for it, and §3's recursive listing of a large folder takes minutes. This
// asserts it does not reach for the full listing path at all.
func TestPhotoWallProbeDoesNotListRecursively(t *testing.T) {
	s := newTestSource(t, "")
	s.list = func(context.Context, string) (io.ReadCloser, func() error, error) {
		t.Error("the probe ran a full recursive listing")
		return nil, nil, errors.New("unexpected")
	}
	var sawRemote string
	s.probe = func(_ context.Context, remote string) error {
		sawRemote = remote
		return nil
	}
	if err := s.Probe(context.Background(), "1abcdefghijklmnop"); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !strings.Contains(sawRemote, "root_folder_id=1abcdefghijklmnop") {
		t.Errorf("the probe ran against %q, which does not name the pasted folder", sawRemote)
	}
}

/* -------------------------------------------------------------- helper ---- */

// restorePhotoWallFolder snapshots the four columns and returns the function
// that puts them back, so a test run against a developer's own database does
// not leave it pointing somewhere else.
func restorePhotoWallFolder(t *testing.T, db *DB) func() {
	t.Helper()
	ctx := context.Background()
	var id, label, by *string
	var at *time.Time
	if err := db.Pool.QueryRow(ctx, `
		select signin_photos_folder_id, signin_photos_label, signin_photos_changed_at, signin_photos_changed_by::text
		from app_settings where id = true`).Scan(&id, &label, &at, &by); err != nil {
		t.Fatalf("snapshot the photo wall folder: %v", err)
	}
	return func() {
		_, _ = db.Pool.Exec(context.Background(), `
			update app_settings set
				signin_photos_folder_id = $1, signin_photos_label = $2,
				signin_photos_changed_at = $3, signin_photos_changed_by = $4::uuid
			where id = true`, id, label, at, by)
		_, _ = db.Pool.Exec(context.Background(),
			`delete from activity_log where action = 'signin_photo_wall_folder'`)
	}
}
