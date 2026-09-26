package stockroom

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// The watcher: one goroutine that polls the detector and keeps closet_visits
// and the log in step with it (ROADMAP §2.2, §2.3).
//
// Each poll does four things, cheapest first, and each is safe to repeat,
// because a poll can be interrupted anywhere and the next one simply finds
// the same state again:
//
//  1. Health. The detector's version and whether the camera is sending
//     frames. A change is one log row -- camera offline, camera online with
//     how long it was gone -- so an outage is a gap the timeline shows, not a
//     silence nobody can tell from an empty room.
//  2. Events. Every person event since just before the newest visit on
//     record. A new one is a visit and a "walked in" row; one that has ended
//     is a "walked out" row. Both rows are written in the transaction that
//     writes the visit, and both carry the detector's own timestamps, since
//     the detector is on this machine and its clock is this clock.
//  3. Recordings. A visit that ended a little while ago gets its clip and
//     snapshot copied out of the detector into the recordings folder. The
//     detector keeps its own copy for three days only; this copy is the one
//     that retention and the keep flag govern.
//  4. Retention, hourly. Recordings older than the retention period and not
//     marked keep are deleted. The visit row and its log rows stay.
//
// The camera is mounted over the door facing into the closet (the owner's
// decision, 2026-09-26), so the room is the whole frame: a person appearing
// is somebody walking in and the same person leaving the frame is them
// walking out. No doorway zone is needed to tell the two apart.

var (
	cameraPollInterval = 5 * time.Second
	// cameraMediaDelay is how long after a walk-out the clip is fetched: the
	// detector records five seconds past the end and then finalises the file.
	cameraMediaDelay     = 20 * time.Second
	cameraMediaAttempts  = 12
	cameraRetentionEvery = time.Hour
	// cameraMediaPerPoll bounds how many recordings one poll copies, so a
	// backlog (the server was down for an afternoon) drains over a few
	// minutes without holding up the walk-ins and walk-outs behind it.
	cameraMediaPerPoll = 3
	cameraMediaTimeout = 2 * time.Minute
	// cameraDetectorKeeps is how far back the detector still has an event's
	// media (deploy/camera/camera.sh keeps three days).
	cameraDetectorKeeps = 3 * 24 * time.Hour
)

type cameraWatcher struct {
	db   *DB
	wake chan struct{}

	mu            sync.Mutex
	running       bool
	lastPoll      time.Time
	health        *DetectorHealth
	state         string
	message       string
	online        *bool
	offlineSince  time.Time
	mediaTries    map[string]int
	lastRetention time.Time
}

// camera returns the DB's one watcher, creating it on first use.
func (db *DB) camera() *cameraWatcher {
	db.cameraOnce.Do(func() {
		db.cameraW = &cameraWatcher{db: db, wake: make(chan struct{}, 1), mediaTries: map[string]int{}, state: "off"}
	})
	return db.cameraW
}

// nudge asks for a poll now rather than at the next tick, after the settings
// change.
func (w *cameraWatcher) nudge() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// StartCamera runs the watcher until ctx ends. It returns immediately. The
// loop runs whether or not the camera is enabled -- it is one settings read
// every few seconds while off -- so turning the camera on in the panel needs
// no restart.
func (db *DB) StartCamera(ctx context.Context) {
	w := db.camera()
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return
	}
	w.running = true
	w.mu.Unlock()
	go func() {
		t := time.NewTicker(cameraPollInterval)
		defer t.Stop()
		for {
			w.poll(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			case <-w.wake:
			}
		}
	}()
}

func (w *cameraWatcher) set(state, message string, h *DetectorHealth) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.state, w.message, w.health = state, message, h
	w.lastPoll = time.Now()
}

// poll is one pass. Every error is carried into the status the admin screen
// reads; none of them stops the loop.
func (w *cameraWatcher) poll(ctx context.Context) {
	db := w.db
	s, err := db.loadCameraSettings(ctx)
	if err != nil {
		w.set("error", "Could not read the camera settings: "+err.Error(), nil)
		return
	}
	if !s.Enabled {
		w.mu.Lock()
		// Forget the last known state, so turning the camera back on logs
		// where it stands then rather than comparing against last week.
		w.online, w.offlineSince = nil, time.Time{}
		w.mu.Unlock()
		w.set("off", "The closet camera is turned off.", nil)
		return
	}
	det := db.newDetector(s.DetectorURL)

	pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	h, herr := det.Health(pctx, s.CameraName)
	online := herr == nil && h.CameraOnline
	reason := ""
	switch {
	case herr != nil:
		reason = herr.Error()
	case !online:
		reason = h.Message
	}
	w.transition(ctx, online, reason)

	if herr != nil {
		w.set("detector_down", reason, nil)
		return
	}
	if online {
		w.set("online", h.Message, &h)
	} else {
		w.set("camera_offline", reason, &h)
	}

	// Events are read even while the camera is offline: a person who was in
	// view when the feed dropped still ends, and the detector says when.
	if err := w.syncEvents(pctx, det, s); err != nil {
		w.set("error", "Reading the detector's events failed: "+err.Error(), &h)
		return
	}
	// Media gets its own clock: a two-minute clip can take longer to cut than
	// the whole of the rest of a poll.
	w.fetchMedia(ctx, det, s)
	w.mu.Lock()
	due := time.Since(w.lastRetention) >= cameraRetentionEvery
	w.mu.Unlock()
	if due {
		if err := db.pruneRecordings(ctx, s); err != nil {
			log.Printf("camera: retention: %v", err)
		} else {
			w.mu.Lock()
			w.lastRetention = time.Now()
			w.mu.Unlock()
		}
	}
}

// transition logs a change in whether the camera is delivering, once.
func (w *cameraWatcher) transition(ctx context.Context, online bool, reason string) {
	w.mu.Lock()
	prev := w.online
	if prev != nil && *prev == online {
		w.mu.Unlock()
		return
	}
	w.online = &online
	since := w.offlineSince
	now := time.Now()
	if !online {
		w.offlineSince = now
	} else {
		w.offlineSince = time.Time{}
	}
	w.mu.Unlock()

	if online {
		summary := "Closet camera online"
		details := map[string]any{}
		if !since.IsZero() {
			gap := now.Sub(since).Round(time.Second)
			summary = fmt.Sprintf("Closet camera back online after %s offline", humanDuration(gap))
			details["offline_since"] = since
			details["offline_seconds"] = int(gap / time.Second)
		}
		w.db.logBestEffort(ctx, LogEntry{Category: LogCloset, Action: "camera_online", Summary: summary, Details: details})
		return
	}
	w.db.logBestEffort(ctx, LogEntry{
		Category: LogCloset, Action: "camera_offline",
		Summary: "Closet camera offline: " + reason, Details: map[string]any{"reason": reason},
	})
}

type openVisit struct {
	id, eventID string
	started     time.Time
}

func (w *cameraWatcher) syncEvents(ctx context.Context, det Detector, s CameraSettings) error {
	db := w.db
	since := time.Now().Add(-time.Hour)
	var latest *time.Time
	if err := db.Pool.QueryRow(ctx, `select max(started_at) from closet_visits where camera = $1`, s.CameraName).Scan(&latest); err != nil {
		return err
	}
	if latest != nil {
		since = latest.Add(-time.Minute)
	}
	if floor := time.Now().Add(-cameraDetectorKeeps); since.Before(floor) {
		since = floor
	}
	events, err := det.PersonEvents(ctx, s.CameraName, since)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, e := range events {
		seen[e.ID] = true
	}

	// A visit still open on our side but not in the listing started before
	// the window: ask about it by id.
	rows, err := db.Pool.Query(ctx, `select id, detector_event_id, started_at from closet_visits where ended_at is null and camera = $1`, s.CameraName)
	if err != nil {
		return err
	}
	var open []openVisit
	for rows.Next() {
		var v openVisit
		if err := rows.Scan(&v.id, &v.eventID, &v.started); err != nil {
			rows.Close()
			return err
		}
		open = append(open, v)
	}
	rows.Close()
	for _, v := range open {
		if seen[v.eventID] {
			continue
		}
		e, err := det.Event(ctx, v.eventID)
		if errors.Is(err, ErrNotFound) {
			// The detector forgot it (restarted with a fresh database, say).
			// Close it now, saying so, rather than leave somebody "in the
			// closet" forever.
			now := time.Now()
			if err := db.closeVisit(ctx, v.id, now, map[string]any{"lost": true}); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		events = append(events, e)
	}

	sort.Slice(events, func(i, j int) bool { return events[i].Start.Before(events[j].Start) })
	for _, e := range events {
		if err := db.applyDetectorEvent(ctx, s, e); err != nil {
			return err
		}
	}
	return nil
}

// applyDetectorEvent brings one visit up to date with one event.
func (db *DB) applyDetectorEvent(ctx context.Context, s CameraSettings, e DetectorEvent) error {
	if e.FalsePositive {
		return nil
	}
	var (
		id    string
		ended *time.Time
	)
	err := db.Pool.QueryRow(ctx, `select id, ended_at from closet_visits where detector_event_id = $1`, e.ID).Scan(&id, &ended)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		end := time.Now()
		if e.End != nil {
			end = *e.End
		}
		if end.Sub(e.Start) < time.Duration(s.MinVisitSeconds)*time.Second {
			// Too short to be a person. Still in progress: wait and see.
			// Finished: it was a flicker, and it never becomes a visit.
			return nil
		}
		return db.withLoggedTx(ctx, "", "record visit", func(tx pgx.Tx) error {
			err := tx.QueryRow(ctx, `
				insert into closet_visits (detector_event_id, camera, started_at, top_score)
				values ($1, $2, $3, $4)
				on conflict (detector_event_id) do nothing
				returning id`, e.ID, s.CameraName, e.Start, e.Score).Scan(&id)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil // another poll got there first
			}
			if err != nil {
				return mapPgError("record visit", err)
			}
			if err := writeLog(ctx, tx, LogEntry{
				Category: LogCloset, Action: "walked_in", VisitID: id, At: e.Start,
				Summary: "Someone walked into the closet",
				Details: map[string]any{"detector_event_id": e.ID, "score": roundScore(e.Score)},
			}); err != nil {
				return err
			}
			if e.End != nil {
				return closeVisitTx(ctx, tx, id, e.Start, *e.End, nil)
			}
			return nil
		})
	case err != nil:
		return mapPgError("read visit", err)
	case ended == nil && e.End != nil:
		return db.closeVisit(ctx, id, *e.End, nil)
	}
	return nil
}

func (db *DB) closeVisit(ctx context.Context, id string, end time.Time, extra map[string]any) error {
	return db.withLoggedTx(ctx, "", "close visit", func(tx pgx.Tx) error {
		var started time.Time
		err := tx.QueryRow(ctx, `select started_at from closet_visits where id = $1 and ended_at is null for update`, id).Scan(&started)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return closeVisitTx(ctx, tx, id, started, end, extra)
	})
}

func closeVisitTx(ctx context.Context, tx pgx.Tx, id string, started, end time.Time, extra map[string]any) error {
	if end.Before(started) {
		end = started
	}
	tag, err := tx.Exec(ctx, `update closet_visits set ended_at = $2 where id = $1 and ended_at is null`, id, end)
	if err != nil {
		return mapPgError("close visit", err)
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	dur := end.Sub(started).Round(time.Second)
	details := map[string]any{"duration_seconds": int(dur / time.Second)}
	for k, v := range extra {
		details[k] = v
	}
	summary := fmt.Sprintf("Someone walked out of the closet after %s", humanDuration(dur))
	if extra["lost"] == true {
		summary = "A closet visit was closed: the detector no longer has it"
	}
	return writeLog(ctx, tx, LogEntry{
		Category: LogCloset, Action: "walked_out", VisitID: id, At: end,
		Summary: summary, Details: details,
	})
}

func roundScore(f float64) float64 { return float64(int(f*100+0.5)) / 100 }

// humanDuration is "45 s", "3 min 12 s", "2 h 5 min".
func humanDuration(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d/time.Second))
	case d < time.Hour:
		m := int(d / time.Minute)
		s := int((d % time.Minute) / time.Second)
		if s == 0 {
			return fmt.Sprintf("%d min", m)
		}
		return fmt.Sprintf("%d min %d s", m, s)
	default:
		h := int(d / time.Hour)
		m := int((d % time.Hour) / time.Minute)
		return fmt.Sprintf("%d h %d min", h, m)
	}
}

/* -------------------------------------------------------------- recordings */

// fetchMedia copies finished visits' clips and snapshots out of the detector.
func (w *cameraWatcher) fetchMedia(ctx context.Context, det Detector, s CameraSettings) {
	db := w.db
	if s.RecordingsDir == "" {
		return
	}
	// Visits that have used up their attempts are left out of the query, not
	// skipped after it: under the per-poll limit, a few recordings Frigate
	// never made would otherwise hold the head of the queue and stall every
	// newer one for good.
	w.mu.Lock()
	gaveUp := make([]string, 0, len(w.mediaTries))
	for id, n := range w.mediaTries {
		if n >= cameraMediaAttempts {
			gaveUp = append(gaveUp, id)
		}
	}
	w.mu.Unlock()
	rows, err := db.Pool.Query(ctx, `
		select v.id, v.detector_event_id, v.started_at, v.snapshot_path is null,
		       v.clip_path is null and v.ended_at is not null
		from closet_visits v
		where v.camera = $4 and v.started_at > $2 and v.recording_deleted_at is null
		  and v.id <> all($5::uuid[])
		  and (
		    -- finished a little while ago and still missing something
		    (v.ended_at < $1 and (v.snapshot_path is null or v.clip_path is null))
		    -- or still in the closet with no snapshot yet: the timeline shows
		    -- who is in there now, not only after they leave
		    or (v.ended_at is null and v.snapshot_path is null and v.started_at < now() - interval '5 seconds')
		  )
		-- newest first: after downtime, whoever is in the closet now gets a
		-- snapshot before the backlog does, and the backlog drains behind them
		order by v.started_at desc
		limit $3`, time.Now().Add(-cameraMediaDelay), time.Now().Add(-cameraDetectorKeeps), cameraMediaPerPoll,
		s.CameraName, gaveUp)
	if err != nil {
		log.Printf("camera: list visits needing recordings: %v", err)
		return
	}
	type need struct {
		id, eventID string
		started     time.Time
		snap, clip  bool
	}
	var todo []need
	for rows.Next() {
		var n need
		if err := rows.Scan(&n.id, &n.eventID, &n.started, &n.snap, &n.clip); err == nil {
			todo = append(todo, n)
		}
	}
	rows.Close()

	for _, n := range todo {
		w.mu.Lock()
		tries := w.mediaTries[n.id]
		if tries >= cameraMediaAttempts {
			w.mu.Unlock()
			continue
		}
		w.mediaTries[n.id] = tries + 1
		w.mu.Unlock()

		mctx, cancel := context.WithTimeout(ctx, cameraMediaTimeout)
		dir := filepath.Join(s.RecordingsDir, n.started.Local().Format("2006-01-02"))
		if n.snap {
			if p, _, err := saveMedia(mctx, dir, n.id+".jpg", func() (io.ReadCloser, error) { return det.Snapshot(mctx, n.eventID) }); err == nil {
				_, _ = db.Pool.Exec(ctx, `update closet_visits set snapshot_path = $2 where id = $1`, n.id, p)
			} else if !errors.Is(err, ErrNotFound) {
				log.Printf("camera: snapshot for visit %s: %v", n.id, err)
			}
		}
		if n.clip {
			if p, size, err := saveMedia(mctx, dir, n.id+".mp4", func() (io.ReadCloser, error) { return det.Clip(mctx, n.eventID) }); err == nil {
				_, _ = db.Pool.Exec(ctx, `update closet_visits set clip_path = $2, clip_bytes = $3 where id = $1`, n.id, p, size)
				w.mu.Lock()
				delete(w.mediaTries, n.id)
				w.mu.Unlock()
			} else if !errors.Is(err, ErrNotFound) {
				log.Printf("camera: clip for visit %s: %v", n.id, err)
			}
		}
		cancel()
	}
}

// saveMedia writes what open returns into dir/name through a temp file, so a
// half-downloaded clip never sits where the player would find it.
func saveMedia(ctx context.Context, dir, name string, open func() (io.ReadCloser, error)) (string, int64, error) {
	body, err := open()
	if err != nil {
		return "", 0, err
	}
	defer body.Close()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", 0, err
	}
	tmp, err := os.CreateTemp(dir, ".part-*")
	if err != nil {
		return "", 0, err
	}
	n, err := io.Copy(tmp, body)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil && n == 0 {
		err = errors.New("the detector sent an empty file")
	}
	if err != nil {
		os.Remove(tmp.Name())
		return "", 0, err
	}
	final := filepath.Join(dir, name)
	if err := os.Rename(tmp.Name(), final); err != nil {
		os.Remove(tmp.Name())
		return "", 0, err
	}
	return final, n, nil
}

// pruneRecordings deletes the files of visits older than the retention
// period that nobody marked keep. The visit and its log rows stay: the fact
// that somebody was in the closet for four minutes a year ago is kept, the
// video of it is not.
func (db *DB) pruneRecordings(ctx context.Context, s CameraSettings) error {
	cutoff := time.Now().Add(-time.Duration(s.RetentionDays) * 24 * time.Hour)
	rows, err := db.Pool.Query(ctx, `
		select id, snapshot_path, clip_path from closet_visits
		where not keep and recording_deleted_at is null and started_at < $1`, cutoff)
	if err != nil {
		return err
	}
	type victim struct {
		id         string
		snap, clip *string
	}
	var victims []victim
	for rows.Next() {
		var v victim
		if err := rows.Scan(&v.id, &v.snap, &v.clip); err == nil {
			victims = append(victims, v)
		}
	}
	rows.Close()
	deleted := 0
	for _, v := range victims {
		ok := true
		for _, p := range []*string{v.snap, v.clip} {
			if p == nil {
				continue
			}
			if err := os.Remove(*p); err != nil && !errors.Is(err, os.ErrNotExist) {
				log.Printf("camera: retention could not delete %s: %v", *p, err)
				ok = false
			}
		}
		if !ok {
			continue
		}
		if _, err := db.Pool.Exec(ctx, `update closet_visits set recording_deleted_at = now() where id = $1`, v.id); err != nil {
			return err
		}
		if v.snap != nil || v.clip != nil {
			deleted++
		}
	}
	if deleted > 0 {
		db.logBestEffort(ctx, LogEntry{
			Category: LogCloset, Action: "recordings_expired",
			Summary: fmt.Sprintf("Deleted %s older than %d days", plural(deleted, "closet recording", "closet recordings"), s.RetentionDays),
			Details: map[string]any{"count": deleted, "retention_days": s.RetentionDays},
		})
	}
	return nil
}

// VisitMedia is a recording file an admin asked for.
type VisitMedia struct {
	Path    string
	ModTime time.Time
	Size    int64
}

// OpenVisitMedia resolves a visit's clip ("clip") or snapshot ("snapshot")
// for GET /admin/visits/{id}/.... Admin only. Watching a clip is logged
// (ROADMAP §2.5: "viewing a recording is itself logged"); a snapshot is the
// timeline's thumbnail and is not, or opening the timeline would write a row
// per visit on screen. logView is false for the continuation requests a
// browser makes while seeking in a video that is already open.
func (db *DB) OpenVisitMedia(ctx context.Context, actor Actor, id, kind string, logView bool) (VisitMedia, error) {
	if err := RequireAdmin(actor); err != nil {
		return VisitMedia{}, err
	}
	var path *string
	var deleted *time.Time
	var started time.Time
	col := "snapshot_path"
	if kind == "clip" {
		col = "clip_path"
	}
	err := db.Pool.QueryRow(ctx, `select `+col+`, recording_deleted_at, started_at from closet_visits where id = $1`, id).Scan(&path, &deleted, &started)
	if errors.Is(err, pgx.ErrNoRows) {
		return VisitMedia{}, fmt.Errorf("%w: no such visit", ErrNotFound)
	}
	if err != nil {
		return VisitMedia{}, mapPgError("open recording", err)
	}
	if deleted != nil {
		return VisitMedia{}, fmt.Errorf("%w: that recording was deleted by retention", ErrNotFound)
	}
	if path == nil {
		return VisitMedia{}, fmt.Errorf("%w: no recording has been saved for that visit yet", ErrNotFound)
	}
	st, err := os.Stat(*path)
	if err != nil {
		return VisitMedia{}, fmt.Errorf("%w: the recording file is missing from the recordings folder", ErrNotFound)
	}
	if kind == "clip" && logView {
		if err := db.logNow(ctx, LogEntry{
			Category: LogAdmin, Action: "recording_viewed", ActorID: actorLogID(actor), VisitID: id,
			Summary: fmt.Sprintf("Watched the closet recording from %s", started.Local().Format("Jan 2 15:04")),
		}); err != nil {
			return VisitMedia{}, err
		}
	}
	return VisitMedia{Path: *path, ModTime: st.ModTime(), Size: st.Size()}, nil
}

/* ------------------------------------------------------------------ status */

// CameraStatus is what the watcher last saw, plus a few counts.
type CameraStatus struct {
	// State: off | starting | online | camera_offline | detector_down | error.
	State        string          `json:"state"`
	Message      string          `json:"message"`
	Detector     *DetectorHealth `json:"detector"`
	LastPoll     *time.Time      `json:"last_poll"`
	OfflineSince *time.Time      `json:"offline_since"`
	InCloset     int             `json:"in_closet"`
	VisitsToday  int             `json:"visits_today"`
	Recordings   int             `json:"recordings"`
	FreeBytes    *int64          `json:"free_bytes"`
	Warnings     []string        `json:"warnings"`
}

func (db *DB) cameraStatus(ctx context.Context, s CameraSettings) CameraStatus {
	w := db.camera()
	w.mu.Lock()
	st := CameraStatus{State: w.state, Message: w.message, Detector: w.health, Warnings: []string{}}
	if !w.lastPoll.IsZero() {
		t := w.lastPoll
		st.LastPoll = &t
	}
	if !w.offlineSince.IsZero() {
		t := w.offlineSince
		st.OfflineSince = &t
	}
	w.mu.Unlock()

	if s.Enabled && (st.LastPoll == nil || st.State == "off") {
		st.State, st.Message = "starting", "Waiting for the first check of the detector."
	}
	if !s.Enabled {
		st.State, st.Message = "off", "The closet camera is turned off."
	}
	y, m, d := time.Now().Date()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, time.Local)
	_ = db.Pool.QueryRow(ctx, `
		select count(*) filter (where ended_at is null and started_at > now() - interval '12 hours'),
		       count(*) filter (where started_at >= $1),
		       count(*) filter (where clip_path is not null and recording_deleted_at is null)
		from closet_visits where camera = $2`, midnight, s.CameraName).Scan(&st.InCloset, &st.VisitsToday, &st.Recordings)

	if s.Enabled {
		switch st.State {
		case "detector_down":
			st.Warnings = append(st.Warnings, "The closet camera's detector is not running: "+st.Message)
		case "camera_offline":
			st.Warnings = append(st.Warnings, "The closet camera is offline: "+st.Message)
		case "error":
			st.Warnings = append(st.Warnings, st.Message)
		}
		if s.RecordingsDir == "" {
			st.Warnings = append(st.Warnings, "No folder is set for closet recordings.")
		} else if free, err := freeSpace(s.RecordingsDir); err == nil {
			st.FreeBytes = &free
			if s.MinFreeGB > 0 && free < int64(s.MinFreeGB)<<30 {
				st.Warnings = append(st.Warnings, fmt.Sprintf(
					"The closet recordings folder has %.1f GB free, below the %d GB warning level. Shorten how long recordings are kept, or free up space.",
					float64(free)/(1<<30), s.MinFreeGB))
			}
		} else {
			st.Warnings = append(st.Warnings, "The closet recordings folder cannot be read: "+err.Error())
		}
	}
	return st
}

// cameraWarningFor is the sentence an admin sees at sign-in, beside the
// backup warning, when the camera needs attention. Students never see it.
func (db *DB) cameraWarningFor(ctx context.Context, isAdmin bool) *string {
	if !isAdmin {
		return nil
	}
	s, err := db.loadCameraSettings(ctx)
	if err != nil || !s.Enabled {
		return nil
	}
	st := db.cameraStatus(ctx, s)
	if len(st.Warnings) == 0 {
		return nil
	}
	msg := st.Warnings[0]
	return &msg
}
