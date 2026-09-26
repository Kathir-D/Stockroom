package stockroom

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// The closet camera (ROADMAP §2): a USB webcam over the closet door, facing
// in, watched by an existing open-source person detector. Stockroom does not
// detect anybody itself. It reads the detector's events through one small
// connector (camera_detector.go), turns each tracked person into a visit --
// walked in, walked out, how long -- with one log row at each end, copies the
// visit's clip and snapshot into a local folder it owns, and deletes them
// after a retention period unless an admin marks them keep.
//
// Everything here is optional and must never be in the way. The camera is off
// until an admin turns it on; a detector that is down, a camera that is
// unplugged and a full disk each cost the timeline its closet rows and put a
// sentence on the admin's screens, and nothing else -- sign-in and checkout
// never wait on any of it.

// CameraSettings is the camera_settings row.
type CameraSettings struct {
	Enabled         bool      `json:"enabled"`
	DetectorURL     string    `json:"detector_url"`
	CameraName      string    `json:"camera_name"`
	RecordingsDir   string    `json:"recordings_dir"`
	RetentionDays   int       `json:"retention_days"`
	MinFreeGB       int       `json:"min_free_gb"`
	MinVisitSeconds int       `json:"min_visit_seconds"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// CameraSettingsInput is a partial update, like SettingsInput.
type CameraSettingsInput struct {
	Enabled         *bool   `json:"enabled"`
	DetectorURL     *string `json:"detector_url"`
	CameraName      *string `json:"camera_name"`
	RecordingsDir   *string `json:"recordings_dir"`
	RetentionDays   *int    `json:"retention_days"`
	MinFreeGB       *int    `json:"min_free_gb"`
	MinVisitSeconds *int    `json:"min_visit_seconds"`
}

const cameraSettingsColumns = `enabled, detector_url, camera_name, recordings_dir,
	retention_days, min_free_gb, min_visit_seconds, updated_at`

func scanCameraSettings(row pgx.Row) (CameraSettings, error) {
	var s CameraSettings
	var dir *string
	err := row.Scan(&s.Enabled, &s.DetectorURL, &s.CameraName, &dir,
		&s.RetentionDays, &s.MinFreeGB, &s.MinVisitSeconds, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return CameraSettings{}, ErrNotFound
	}
	if err != nil {
		return CameraSettings{}, fmt.Errorf("scan camera settings: %w", err)
	}
	s.RecordingsDir = deref(dir)
	return s, nil
}

func (db *DB) loadCameraSettings(ctx context.Context) (CameraSettings, error) {
	s, err := scanCameraSettings(db.Pool.QueryRow(ctx, `select `+cameraSettingsColumns+` from camera_settings where id = true`))
	if errors.Is(err, ErrNotFound) {
		if _, ierr := db.Pool.Exec(ctx, `insert into camera_settings (id) values (true) on conflict (id) do nothing`); ierr != nil {
			return CameraSettings{}, fmt.Errorf("recreate camera settings row: %w", ierr)
		}
		return scanCameraSettings(db.Pool.QueryRow(ctx, `select `+cameraSettingsColumns+` from camera_settings where id = true`))
	}
	return s, err
}

// CameraOverview is GET /admin/camera: the settings and what the watcher last
// saw, so the card can say "online, 3 visits today" or why it is not.
type CameraOverview struct {
	Settings CameraSettings `json:"settings"`
	Status   CameraStatus   `json:"status"`
}

// GetCamera is the admin read.
func (db *DB) GetCamera(ctx context.Context, actor Actor) (CameraOverview, error) {
	if err := RequireAdmin(actor); err != nil {
		return CameraOverview{}, err
	}
	s, err := db.loadCameraSettings(ctx)
	if err != nil {
		return CameraOverview{}, err
	}
	return CameraOverview{Settings: s, Status: db.cameraStatus(ctx, s)}, nil
}

// SaveCamera applies a partial update in one locked transaction, with the
// admin log row inside it.
func (db *DB) SaveCamera(ctx context.Context, actor Actor, in CameraSettingsInput) (CameraOverview, error) {
	if err := RequireAdmin(actor); err != nil {
		return CameraOverview{}, err
	}
	var saved CameraSettings
	err := db.withLoggedTx(ctx, actorLogID(actor), "save camera settings", func(tx pgx.Tx) error {
		cur, err := scanCameraSettings(tx.QueryRow(ctx,
			`select `+cameraSettingsColumns+` from camera_settings where id = true for update`))
		if err != nil {
			return err
		}
		next := cur
		changed := []string{}
		if in.Enabled != nil && *in.Enabled != cur.Enabled {
			next.Enabled = *in.Enabled
			changed = append(changed, "enabled")
		}
		if in.DetectorURL != nil {
			u, err := normalizeDetectorURL(*in.DetectorURL)
			if err != nil {
				return err
			}
			if u != cur.DetectorURL {
				next.DetectorURL = u
				changed = append(changed, "detector_url")
			}
		}
		if in.CameraName != nil {
			n := strings.TrimSpace(*in.CameraName)
			if n == "" || len(n) > 64 || strings.ContainsAny(n, "/?#&") {
				return fmt.Errorf("%w: the camera name is the one in the detector's config, e.g. closet", ErrInvalid)
			}
			if n != cur.CameraName {
				next.CameraName = n
				changed = append(changed, "camera_name")
			}
		}
		if in.RecordingsDir != nil {
			d := strings.TrimSpace(*in.RecordingsDir)
			if d != "" {
				if err := validateDir("recordings_dir", d); err != nil {
					return err
				}
			}
			if d != cur.RecordingsDir {
				next.RecordingsDir = d
				changed = append(changed, "recordings_dir")
			}
		}
		if err := intSetting(in.RetentionDays, &next.RetentionDays, "retention_days", "Keep recordings for", 1, 3650, &changed); err != nil {
			return err
		}
		if err := intSetting(in.MinFreeGB, &next.MinFreeGB, "min_free_gb", "Warn below", 0, 100000, &changed); err != nil {
			return err
		}
		if err := intSetting(in.MinVisitSeconds, &next.MinVisitSeconds, "min_visit_seconds", "Ignore visits shorter than", 0, 600, &changed); err != nil {
			return err
		}
		if next.Enabled && next.RecordingsDir == "" {
			return fmt.Errorf("%w: choose a folder for the recordings before turning the camera on", ErrInvalid)
		}
		saved, err = scanCameraSettings(tx.QueryRow(ctx, `
			update camera_settings set enabled = $1, detector_url = $2, camera_name = $3,
				recordings_dir = $4, retention_days = $5, min_free_gb = $6, min_visit_seconds = $7,
				updated_at = now()
			where id = true
			returning `+cameraSettingsColumns,
			next.Enabled, next.DetectorURL, next.CameraName, nullable(next.RecordingsDir),
			next.RetentionDays, next.MinFreeGB, next.MinVisitSeconds))
		if err != nil {
			return mapPgError("save camera settings", err)
		}
		if len(changed) == 0 {
			return nil
		}
		summary := "Changed the closet camera settings"
		if slicesContains(changed, "enabled") {
			if next.Enabled {
				summary = "Turned the closet camera on"
			} else {
				summary = "Turned the closet camera off"
			}
		}
		return writeLog(ctx, tx, LogEntry{
			Category: LogAdmin, Action: "camera_settings_changed", ActorID: actorLogID(actor),
			Summary: summary, Details: map[string]any{"changed": changed},
		})
	})
	if err != nil {
		return CameraOverview{}, err
	}
	db.camera().nudge()
	return CameraOverview{Settings: saved, Status: db.cameraStatus(ctx, saved)}, nil
}

func intSetting(in *int, dst *int, field, label string, lo, hi int, changed *[]string) error {
	if in == nil {
		return nil
	}
	if *in < lo || *in > hi {
		return fmt.Errorf("%w: %s must be between %d and %d", ErrInvalid, label, lo, hi)
	}
	if *in != *dst {
		*dst = *in
		*changed = append(*changed, field)
	}
	return nil
}

// normalizeDetectorURL accepts only a loopback http URL. The detector's API
// has no authentication of its own (deploy/camera/docker-compose.yml), so a
// URL pointing anywhere else would be the server fetching video from, or
// sending requests to, a machine nobody configured it to trust.
func normalizeDetectorURL(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("%w: the detector address looks like http://127.0.0.1:5055", ErrInvalid)
	}
	host := u.Hostname()
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return "", fmt.Errorf("%w: the detector must run on this machine (127.0.0.1)", ErrInvalid)
	}
	if u.Path != "" || u.RawQuery != "" {
		return "", fmt.Errorf("%w: give the detector's address only, e.g. http://127.0.0.1:5055", ErrInvalid)
	}
	return raw, nil
}

// TestCamera is POST /admin/camera/test: ask the detector, right now, whether
// it answers and whether it sees the camera. Uses the given URL and name when
// set, so the card can test before saving.
func (db *DB) TestCamera(ctx context.Context, actor Actor, in CameraSettingsInput) (DetectorHealth, error) {
	if err := RequireAdmin(actor); err != nil {
		return DetectorHealth{}, err
	}
	s, err := db.loadCameraSettings(ctx)
	if err != nil {
		return DetectorHealth{}, err
	}
	if in.DetectorURL != nil {
		if s.DetectorURL, err = normalizeDetectorURL(*in.DetectorURL); err != nil {
			return DetectorHealth{}, err
		}
	}
	if in.CameraName != nil && strings.TrimSpace(*in.CameraName) != "" {
		s.CameraName = strings.TrimSpace(*in.CameraName)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	h, err := db.newDetector(s.DetectorURL).Health(ctx, s.CameraName)
	if err != nil {
		return DetectorHealth{}, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	return h, nil
}

/* ------------------------------------------------------------------ visits */

// ClosetVisit is one person tracked from walking in to walking out. The file
// paths are never sent: the recordings are served by id, admin-only.
type ClosetVisit struct {
	ID              string     `json:"id"`
	StartedAt       time.Time  `json:"started_at"`
	EndedAt         *time.Time `json:"ended_at"`
	DurationSeconds *int       `json:"duration_seconds"`
	HasSnapshot     bool       `json:"has_snapshot"`
	HasClip         bool       `json:"has_clip"`
	ClipBytes       *int64     `json:"clip_bytes"`
	Keep            bool       `json:"keep"`
	RecordingGone   bool       `json:"recording_deleted"`
}

// visitColumnsNullable is the visit half of the activity query, which
// left-joins closet_visits and so reads every column through a pointer.
const visitColumnsNullable = `v.id, v.started_at, v.ended_at, v.snapshot_path is not null,
	v.clip_path is not null, v.clip_bytes, v.keep, v.recording_deleted_at is not null`

type nullableVisit struct {
	id             *string
	started, ended *time.Time
	snap, clip     *bool
	bytes          *int64
	keep, deleted  *bool
}

func (v *nullableVisit) dest() []any {
	return []any{&v.id, &v.started, &v.ended, &v.snap, &v.clip, &v.bytes, &v.keep, &v.deleted}
}

func (v *nullableVisit) visit() *ClosetVisit {
	if v.id == nil {
		return nil
	}
	out := &ClosetVisit{ID: *v.id, EndedAt: v.ended, ClipBytes: v.bytes}
	if v.started != nil {
		out.StartedAt = *v.started
	}
	out.HasSnapshot = v.snap != nil && *v.snap
	out.HasClip = v.clip != nil && *v.clip
	out.Keep = v.keep != nil && *v.keep
	out.RecordingGone = v.deleted != nil && *v.deleted
	if out.RecordingGone {
		out.HasClip, out.HasSnapshot = false, false
	}
	if v.ended != nil && v.started != nil {
		d := int(v.ended.Sub(*v.started).Round(time.Second) / time.Second)
		out.DurationSeconds = &d
	}
	return out
}

// SetVisitKeep is POST /admin/visits/{id}/keep: mark a recording as evidence
// so retention leaves it alone, or release it back to retention. Logged,
// because whether a recording was deliberately kept -- and by whom -- is part
// of the record of an investigation.
func (db *DB) SetVisitKeep(ctx context.Context, actor Actor, id string, keep bool) (ClosetVisit, error) {
	if err := RequireAdmin(actor); err != nil {
		return ClosetVisit{}, err
	}
	var out ClosetVisit
	err := db.withLoggedTx(ctx, actorLogID(actor), "keep recording", func(tx pgx.Tx) error {
		var v nullableVisit
		err := tx.QueryRow(ctx, `
			update closet_visits v set keep = $2,
				kept_by = case when $2 then $3::uuid else null end,
				kept_at = case when $2 then now() else null end
			where v.id = $1
			returning `+visitColumnsNullable, id, keep, nullIfBlank(actorLogID(actor))).Scan(v.dest()...)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: no such visit", ErrNotFound)
		}
		if err != nil {
			return mapPgError("keep recording", err)
		}
		out = *v.visit()
		if keep && out.RecordingGone {
			return fmt.Errorf("%w: that recording has already been deleted by retention", ErrConflict)
		}
		action, summary := "recording_kept", "Marked a closet recording keep"
		if !keep {
			action, summary = "recording_released", "Released a closet recording back to retention"
		}
		return writeLog(ctx, tx, LogEntry{
			Category: LogAdmin, Action: action, ActorID: actorLogID(actor), VisitID: out.ID,
			Summary: fmt.Sprintf("%s (visit at %s)", summary, out.StartedAt.Local().Format("Jan 2 15:04")),
		})
	})
	return out, err
}
