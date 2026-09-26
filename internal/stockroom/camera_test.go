package stockroom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDetector is the camera as the watcher sees it: a health answer, a list
// of person events a test edits between polls, and a clip and snapshot per
// event.
type fakeDetector struct {
	mu        sync.Mutex
	down      bool
	online    bool
	events    map[string]DetectorEvent
	forgotten map[string]bool
	noClip    map[string]bool // events whose clip the detector never made
}

func newFakeDetector() *fakeDetector {
	return &fakeDetector{online: true, events: map[string]DetectorEvent{}, forgotten: map[string]bool{}, noClip: map[string]bool{}}
}

func (f *fakeDetector) Health(ctx context.Context, camera string) (DetectorHealth, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return DetectorHealth{}, errors.New("the detector at fake is not answering")
	}
	h := DetectorHealth{Detector: "fake", Version: "1", CameraFound: true, CameraOnline: f.online}
	if f.online {
		h.CameraFPS = 5
		h.Message = "ok"
	} else {
		h.Message = "no frames"
	}
	return h, nil
}

func (f *fakeDetector) PersonEvents(ctx context.Context, camera string, since time.Time) ([]DetectorEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []DetectorEvent{}
	for _, e := range f.events {
		if e.Start.After(since) && !f.forgotten[e.ID] {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *fakeDetector) Event(ctx context.Context, id string) (DetectorEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.events[id]
	if !ok || f.forgotten[id] {
		return DetectorEvent{}, ErrNotFound
	}
	return e, nil
}

func (f *fakeDetector) Snapshot(ctx context.Context, id string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("jpeg of " + id)), nil
}

func (f *fakeDetector) Clip(ctx context.Context, id string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.noClip[id] {
		return nil, errors.New("no clip")
	}
	return io.NopCloser(strings.NewReader("mp4 of " + id)), nil
}

func (f *fakeDetector) set(e DetectorEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events[e.ID] = e
}

// cameraFixture turns the camera on against a fake detector under a camera
// name nobody else uses, and puts every setting back afterwards.
func cameraFixture(t *testing.T) (*DB, *fakeDetector, CameraSettings, Actor) {
	t.Helper()
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	before, err := db.loadCameraSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `
			update camera_settings set enabled = $1, detector_url = $2, camera_name = $3, recordings_dir = $4,
				retention_days = $5, min_free_gb = $6, min_visit_seconds = $7 where id = true`,
			before.Enabled, before.DetectorURL, before.CameraName, nullable(before.RecordingsDir),
			before.RetentionDays, before.MinFreeGB, before.MinVisitSeconds)
	})
	det := newFakeDetector()
	db.detectorFactory = func(string) Detector { return det }
	name := fmt.Sprintf("test%08d", rand.IntN(100_000_000))
	on, dir, zero, ret := true, t.TempDir(), 2, 30
	out, err := db.SaveCamera(ctx, admin, CameraSettingsInput{
		Enabled: &on, CameraName: &name, RecordingsDir: &dir, MinVisitSeconds: &zero, RetentionDays: &ret,
	})
	if err != nil {
		t.Fatalf("SaveCamera: %v", err)
	}
	// Media is fetched as soon as a visit has ended, not 20 s later.
	oldDelay := cameraMediaDelay
	cameraMediaDelay = 0
	t.Cleanup(func() { cameraMediaDelay = oldDelay })
	return db, det, out.Settings, admin
}

type visitRow struct {
	id         string
	ended      *time.Time
	snap, clip *string
	keep       bool
	deleted    *time.Time
}

func readVisit(t *testing.T, db *DB, eventID string) visitRow {
	t.Helper()
	var v visitRow
	err := db.Pool.QueryRow(context.Background(), `
		select id, ended_at, snapshot_path, clip_path, keep, recording_deleted_at
		from closet_visits where detector_event_id = $1`, eventID).
		Scan(&v.id, &v.ended, &v.snap, &v.clip, &v.keep, &v.deleted)
	if err != nil {
		t.Fatalf("read visit %s: %v", eventID, err)
	}
	return v
}

func logCount(t *testing.T, db *DB, where string, args ...any) int {
	t.Helper()
	var n int
	if err := db.Pool.QueryRow(context.Background(), `select count(*) from activity_log where `+where, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A person walking in and out is one visit, one walked-in row at the
// detector's start time, one walked-out row at its end, and a clip and
// snapshot in the recordings folder -- however many times the watcher polls.
func TestCameraWalkInWalkOutMakesOneVisit(t *testing.T) {
	db, det, s, _ := cameraFixture(t)
	ctx := context.Background()
	w := db.camera()

	start := time.Now().Add(-5 * time.Minute).Truncate(time.Millisecond)
	id := "evt-" + s.CameraName
	det.set(DetectorEvent{ID: id, Camera: s.CameraName, Start: start, Score: 0.83})
	w.poll(ctx)
	w.poll(ctx)

	v := readVisit(t, db, id)
	if v.ended != nil {
		t.Fatal("the visit ended before the detector said so")
	}
	if n := logCount(t, db, `visit_id = $1 and action = 'walked_in'`, v.id); n != 1 {
		t.Errorf("walked_in rows = %d after two polls, want 1", n)
	}
	var at time.Time
	if err := db.Pool.QueryRow(ctx, `select created_at from activity_log where visit_id = $1 and action = 'walked_in'`, v.id).Scan(&at); err != nil {
		t.Fatal(err)
	}
	if !at.Equal(start) {
		t.Errorf("walked_in is timed %v, want the detector's start %v", at, start)
	}

	end := start.Add(3*time.Minute + 12*time.Second)
	det.set(DetectorEvent{ID: id, Camera: s.CameraName, Start: start, End: &end, Score: 0.9})
	w.poll(ctx)
	w.poll(ctx)

	v = readVisit(t, db, id)
	if v.ended == nil || !v.ended.Equal(end) {
		t.Fatalf("visit ended_at = %v, want %v", v.ended, end)
	}
	if n := logCount(t, db, `visit_id = $1 and action = 'walked_out'`, v.id); n != 1 {
		t.Errorf("walked_out rows = %d, want 1", n)
	}
	var summary string
	_ = db.Pool.QueryRow(ctx, `select summary from activity_log where visit_id = $1 and action = 'walked_out'`, v.id).Scan(&summary)
	if !strings.Contains(summary, "3 min 12 s") {
		t.Errorf("walked_out summary %q does not say how long", summary)
	}
	if v.clip == nil || v.snap == nil {
		t.Fatalf("the recording was not fetched: clip %v snapshot %v", v.clip, v.snap)
	}
	if !strings.HasPrefix(*v.clip, s.RecordingsDir) {
		t.Errorf("clip saved at %s, outside the recordings folder %s", *v.clip, s.RecordingsDir)
	}
	if b, _ := os.ReadFile(*v.clip); string(b) != "mp4 of "+id {
		t.Errorf("clip holds %q", b)
	}
}

// Recordings the detector never made do not hold up the ones after them:
// once a visit has used its attempts, the next poll moves on.
func TestCameraAMissingClipDoesNotStallTheQueue(t *testing.T) {
	db, det, s, _ := cameraFixture(t)
	ctx := context.Background()
	w := db.camera()
	oldAttempts := cameraMediaAttempts
	cameraMediaAttempts = 2
	t.Cleanup(func() { cameraMediaAttempts = oldAttempts })

	base := time.Now().Add(-30 * time.Minute).Truncate(time.Millisecond)
	for i := 0; i <= cameraMediaPerPoll; i++ {
		id := fmt.Sprintf("evt-%s-%d", s.CameraName, i)
		start, end := base.Add(time.Duration(i)*time.Minute), base.Add(time.Duration(i)*time.Minute+30*time.Second)
		det.set(DetectorEvent{ID: id, Camera: s.CameraName, Start: start, End: &end, Score: 0.8})
		if i < cameraMediaPerPoll {
			det.mu.Lock()
			det.noClip[id] = true
			det.mu.Unlock()
		}
	}
	for i := 0; i < cameraMediaAttempts+1; i++ {
		w.poll(ctx)
	}
	last := readVisit(t, db, fmt.Sprintf("evt-%s-%d", s.CameraName, cameraMediaPerPoll))
	if last.clip == nil {
		t.Fatal("the newest visit's clip was never fetched behind visits whose clips do not exist")
	}
}

// A flicker shorter than the minimum never becomes a visit.
func TestCameraIgnoresABlip(t *testing.T) {
	db, det, s, admin := cameraFixture(t)
	ctx := context.Background()
	five := 5
	if _, err := db.SaveCamera(ctx, admin, CameraSettingsInput{MinVisitSeconds: &five}); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-time.Minute)
	end := start.Add(2 * time.Second)
	det.set(DetectorEvent{ID: "blip-" + s.CameraName, Camera: s.CameraName, Start: start, End: &end})
	db.camera().poll(ctx)
	var n int
	_ = db.Pool.QueryRow(ctx, `select count(*) from closet_visits where camera = $1`, s.CameraName).Scan(&n)
	if n != 0 {
		t.Errorf("a two-second detection became %d visit(s)", n)
	}
}

// The camera going offline and coming back is two rows, the second naming
// the gap, and a detector that is down never stops the watcher.
func TestCameraOutageIsLoggedOnce(t *testing.T) {
	db, det, _, _ := cameraFixture(t)
	ctx := context.Background()
	w := db.camera()
	since := time.Now()

	w.poll(ctx) // online: the first observation is logged
	det.mu.Lock()
	det.online = false
	det.mu.Unlock()
	w.poll(ctx)
	w.poll(ctx)
	if n := logCount(t, db, `action = 'camera_offline' and created_at >= $1`, since); n != 1 {
		t.Errorf("camera_offline rows = %d over two offline polls, want 1", n)
	}
	st := db.cameraStatus(ctx, mustCameraSettings(t, db))
	if st.State != "camera_offline" || len(st.Warnings) == 0 {
		t.Errorf("status while offline = %s %v, want camera_offline with a warning", st.State, st.Warnings)
	}

	det.mu.Lock()
	det.online, det.down = true, true
	det.mu.Unlock()
	w.poll(ctx)
	if st := db.cameraStatus(ctx, mustCameraSettings(t, db)); st.State != "detector_down" {
		t.Errorf("status with the detector down = %s", st.State)
	}

	det.mu.Lock()
	det.down = false
	det.mu.Unlock()
	w.poll(ctx)
	var summary string
	if err := db.Pool.QueryRow(ctx, `select summary from activity_log where action = 'camera_online' and created_at >= $1
		order by created_at desc limit 1`, since).Scan(&summary); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "after") {
		t.Errorf("camera_online after an outage says %q, want the length of the gap", summary)
	}
}

func mustCameraSettings(t *testing.T, db *DB) CameraSettings {
	s, err := db.loadCameraSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// A visit the detector forgets (restarted with an empty database) is closed
// rather than left "in the closet" forever.
func TestCameraClosesAVisitTheDetectorForgot(t *testing.T) {
	db, det, s, _ := cameraFixture(t)
	ctx := context.Background()
	id := "lost-" + s.CameraName
	det.set(DetectorEvent{ID: id, Camera: s.CameraName, Start: time.Now().Add(-time.Minute)})
	db.camera().poll(ctx)
	det.mu.Lock()
	det.forgotten[id] = true
	det.mu.Unlock()
	db.camera().poll(ctx)
	if v := readVisit(t, db, id); v.ended == nil {
		t.Error("a visit the detector no longer has is still open")
	}
}

// Retention deletes old recordings, leaves the visit and its log rows, and
// leaves a recording an admin marked keep alone.
func TestCameraRetentionAndKeep(t *testing.T) {
	db, det, s, admin := cameraFixture(t)
	ctx := context.Background()
	w := db.camera()
	old := time.Now().Add(-40 * 24 * time.Hour)
	mk := func(name string) visitRow {
		// Fetched while still within the detector's reach, then aged.
		start := time.Now().Add(-2 * time.Minute)
		end := start.Add(30 * time.Second)
		det.set(DetectorEvent{ID: name, Camera: s.CameraName, Start: start, End: &end})
		w.poll(ctx)
		v := readVisit(t, db, name)
		if v.clip == nil {
			t.Fatalf("%s: no clip fetched", name)
		}
		if _, err := db.Pool.Exec(ctx, `update closet_visits set started_at = $2, ended_at = $2 where id = $1`, v.id, old); err != nil {
			t.Fatal(err)
		}
		return v
	}
	gone := mk("old-" + s.CameraName)
	kept := mk("kept-" + s.CameraName)

	if _, err := db.SetVisitKeep(ctx, admin, kept.id, true); err != nil {
		t.Fatalf("SetVisitKeep: %v", err)
	}
	if err := db.pruneRecordings(ctx, s); err != nil {
		t.Fatalf("pruneRecordings: %v", err)
	}

	if _, err := os.Stat(*gone.clip); !os.IsNotExist(err) {
		t.Errorf("an expired clip is still on disk: %v", err)
	}
	if v := readVisit(t, db, "old-"+s.CameraName); v.deleted == nil {
		t.Error("the expired visit is not marked as having lost its recording")
	}
	if n := logCount(t, db, `visit_id = $1`, gone.id); n < 2 {
		t.Errorf("the expired visit's log rows went with its recording (%d left)", n)
	}
	if _, err := os.Stat(*kept.clip); err != nil {
		t.Errorf("a clip marked keep was deleted: %v", err)
	}
	if n := logCount(t, db, `visit_id = $1 and action = 'recording_kept' and actor_id = $2`, kept.id, admin.ID); n != 1 {
		t.Errorf("keeping a recording wrote %d log rows, want 1 naming the admin", n)
	}

	// Watching it is logged; a student cannot watch it at all.
	m, err := db.OpenVisitMedia(ctx, admin, kept.id, "clip", true)
	if err != nil {
		t.Fatalf("OpenVisitMedia: %v", err)
	}
	m.File.Close()
	if n := logCount(t, db, `visit_id = $1 and action = 'recording_viewed'`, kept.id); n != 1 {
		t.Errorf("watching a recording wrote %d rows, want 1", n)
	}
	student := actorFor(insertTestProfile(t, db, false, "student-pw"))
	if _, err := db.OpenVisitMedia(ctx, student, kept.id, "clip", true); !errors.Is(err, ErrForbidden) {
		t.Errorf("a student opening a recording = %v, want ErrForbidden", err)
	}
	if _, err := db.OpenVisitMedia(ctx, admin, gone.id, "clip", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("opening an expired recording = %v, want ErrNotFound", err)
	}
}

// A recording path read back from closet_visits -- which a restore loads from
// an archive anybody can edit -- is neither served nor deleted unless it has
// the shape the watcher writes.
func TestCameraRefusesAForeignRecordingPath(t *testing.T) {
	db, det, s, admin := cameraFixture(t)
	ctx := context.Background()
	start := time.Now().Add(-2 * time.Minute)
	end := start.Add(30 * time.Second)
	name := "foreign-" + s.CameraName
	det.set(DetectorEvent{ID: name, Camera: s.CameraName, Start: start, End: &end})
	db.camera().poll(ctx)
	v := readVisit(t, db, name)

	secret := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(secret, []byte("ADMIN_PASSWORD=x"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-40 * 24 * time.Hour)
	if _, err := db.Pool.Exec(ctx, `update closet_visits set clip_path = $2, started_at = $3 where id = $1`, v.id, secret, old); err != nil {
		t.Fatal(err)
	}
	if _, err := db.OpenVisitMedia(ctx, admin, v.id, "clip", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("serving a clip_path outside the recordings layout = %v, want ErrNotFound", err)
	}
	if err := db.pruneRecordings(ctx, s); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(secret); err != nil {
		t.Errorf("retention deleted a file the watcher never wrote: %v", err)
	}
}

// The camera cannot be turned on without a recordings folder, and the
// detector must be on this machine.
func TestCameraSettingsGates(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	student := actorFor(insertTestProfile(t, db, false, "student-pw"))
	if _, err := db.GetCamera(ctx, student); !errors.Is(err, ErrForbidden) {
		t.Errorf("GetCamera as a student = %v, want ErrForbidden", err)
	}
	remote := "http://192.168.1.20:5000"
	if _, err := db.SaveCamera(ctx, admin, CameraSettingsInput{DetectorURL: &remote}); !errors.Is(err, ErrInvalid) {
		t.Errorf("a detector off this machine = %v, want ErrInvalid", err)
	}
	s := mustCameraSettings(t, db)
	if s.RecordingsDir == "" {
		on := true
		if _, err := db.SaveCamera(ctx, admin, CameraSettingsInput{Enabled: &on}); !errors.Is(err, ErrInvalid) {
			t.Errorf("turning the camera on with no folder = %v, want ErrInvalid", err)
		}
	}
}

// The Frigate connector against a server that answers the way Frigate 0.18
// does (shapes copied from a live instance on 2026-09-26).
func TestFrigateDetectorReadsFrigate(t *testing.T) {
	end := 1790405600.5
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/version":
			fmt.Fprint(w, "0.18.0-77a66e7")
		case r.URL.Path == "/api/stats":
			fmt.Fprint(w, `{"cameras":{"closet":{"camera_fps":5.1,"process_fps":5.0}},"detectors":{"cpu1":{"inference_speed":31.61}}}`)
		case r.URL.Path == "/api/events":
			if r.URL.Query().Get("cameras") != "closet" || r.URL.Query().Get("labels") != "person" {
				http.Error(w, "bad filter", 400)
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": "a", "camera": "closet", "label": "person", "start_time": 1790405566.083676, "end_time": nil,
					"has_clip": true, "has_snapshot": true, "top_score": nil, "false_positive": false,
					"data": map[string]any{"top_score": 0.82}},
				{"id": "b", "camera": "closet", "label": "person", "start_time": 1790405500.0, "end_time": end,
					"has_clip": true, "has_snapshot": true, "false_positive": false, "data": map[string]any{"top_score": 0.7}},
			})
		case r.URL.Path == "/api/events/b/clip.mp4":
			fmt.Fprint(w, "clip-bytes")
		case r.URL.Path == "/api/events/gone":
			http.Error(w, `{"success":false}`, 404)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	f := &frigateDetector{base: srv.URL, client: srv.Client()}
	ctx := context.Background()

	h, err := f.Health(ctx, "closet")
	if err != nil || !h.CameraOnline || h.Version != "0.18.0-77a66e7" || h.InferenceMS != 31.6 {
		t.Errorf("Health = %+v, %v", h, err)
	}
	if h, _ := f.Health(ctx, "garage"); h.CameraFound || !strings.Contains(h.Message, "closet") {
		t.Errorf("a missing camera = %+v, want the names Frigate does have", h)
	}
	events, err := f.PersonEvents(ctx, "closet", time.Unix(1790405000, 0))
	if err != nil || len(events) != 2 {
		t.Fatalf("PersonEvents = %v, %v", events, err)
	}
	if events[0].End != nil || events[0].Score != 0.82 {
		t.Errorf("in-progress event = %+v", events[0])
	}
	if events[1].End == nil || !events[1].End.Equal(unixFloat(end)) {
		t.Errorf("finished event end = %v", events[1].End)
	}
	body, err := f.Clip(ctx, "b")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, body)
	body.Close()
	if buf.String() != "clip-bytes" {
		t.Errorf("clip = %q", buf.String())
	}
	if _, err := f.Event(ctx, "gone"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a forgotten event = %v, want ErrNotFound", err)
	}
	down := &frigateDetector{base: "http://127.0.0.1:1", client: &http.Client{Timeout: time.Second}}
	if _, err := down.Health(ctx, "closet"); err == nil || !strings.Contains(err.Error(), "not answering") {
		t.Errorf("a detector that is down = %v", err)
	}
}
