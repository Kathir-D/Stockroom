package stockroom

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Detector is the one connector between Stockroom and whatever watches the
// camera (ROADMAP §2.1, §2.2). Frigate is the one built; Agent DVR, the
// Windows fallback, would be a second implementation of these five methods,
// and nothing above this interface would change.
//
// Polled, not pushed. Frigate can publish events over MQTT, but that needs a
// broker, which is a third container on the closet PC for a feed that changes
// a few times an hour. A poll every few seconds is simpler and cannot lose an
// event to a dropped connection: whatever happened while the server was down
// is still in the detector's list when it comes back.
type Detector interface {
	// Health answers whether the detector is up and whether it is receiving
	// frames from the named camera.
	Health(ctx context.Context, camera string) (DetectorHealth, error)
	// PersonEvents lists person events on camera that started after since,
	// in progress or finished.
	PersonEvents(ctx context.Context, camera string, since time.Time) ([]DetectorEvent, error)
	// Event re-reads one event by id; ErrNotFound when the detector no longer
	// has it.
	Event(ctx context.Context, id string) (DetectorEvent, error)
	Snapshot(ctx context.Context, id string) (io.ReadCloser, error)
	Clip(ctx context.Context, id string) (io.ReadCloser, error)
}

// DetectorHealth is what Test connection shows.
type DetectorHealth struct {
	Detector     string  `json:"detector"`
	Version      string  `json:"version"`
	CameraFound  bool    `json:"camera_found"`
	CameraOnline bool    `json:"camera_online"`
	CameraFPS    float64 `json:"camera_fps"`
	// InferenceMS is the detector's average time per detection, the number
	// ROADMAP §2.1 says to watch on a slow machine.
	InferenceMS float64 `json:"inference_ms"`
	Message     string  `json:"message"`
}

// DetectorEvent is one tracked person.
type DetectorEvent struct {
	ID            string
	Camera        string
	Start         time.Time
	End           *time.Time
	Score         float64
	HasClip       bool
	HasSnapshot   bool
	FalsePositive bool
}

// newDetector builds the connector for url. A field so a test can swap in a
// fake; nil means Frigate.
func (db *DB) newDetector(baseURL string) Detector {
	if db.detectorFactory != nil {
		return db.detectorFactory(baseURL)
	}
	// No client-wide timeout: a clip is megabytes and is bounded by its
	// caller's context. The small JSON reads bound themselves (getJSON).
	return &frigateDetector{base: strings.TrimRight(baseURL, "/"), client: detectorClient}
}

// detectorClient has its own transport with keep-alives off. Found by
// stopping and restarting the detector under a running server: a connection
// pooled from before the restart went on answering 403 to every poll
// forever, so the camera never came back online on the admin's screen while
// curl said it was fine. A fresh loopback connection every few seconds costs
// nothing, and a detector restart can then never strand the watcher.
var detectorClient = &http.Client{
	Transport: &http.Transport{
		Proxy:             nil,
		DisableKeepAlives: true,
	},
	// A redirect is answered as it stands, never followed: the loopback check
	// on detector_url means nothing if whatever holds the port can send the
	// watcher somewhere else.
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

/* ----------------------------------------------------------------- Frigate */

// frigateDetector speaks Frigate's HTTP API on its unauthenticated internal
// port, which deploy/camera binds to 127.0.0.1 only. Verified against 0.18.
type frigateDetector struct {
	base   string
	client *http.Client
}

func (f *frigateDetector) get(ctx context.Context, path string, q url.Values) (*http.Response, error) {
	u := f.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the detector at %s is not answering", f.base)
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		resp.Body.Close()
		return nil, fmt.Errorf("the detector answered %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return resp, nil
}

// frigateJSONTimeout bounds one API read. The detector is on this machine, so
// anything slower than this is a detector that is stuck.
const frigateJSONTimeout = 20 * time.Second

func (f *frigateDetector) getJSON(ctx context.Context, path string, q url.Values, into any) error {
	ctx, cancel := context.WithTimeout(ctx, frigateJSONTimeout)
	defer cancel()
	resp, err := f.get(ctx, path, q)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(into); err != nil {
		return fmt.Errorf("the detector's answer to %s could not be read: %w", path, err)
	}
	return nil
}

func (f *frigateDetector) Health(ctx context.Context, camera string) (DetectorHealth, error) {
	h := DetectorHealth{Detector: "Frigate"}
	vctx, cancel := context.WithTimeout(ctx, frigateJSONTimeout)
	defer cancel()
	resp, err := f.get(vctx, "/api/version", nil)
	if err != nil {
		if strings.HasPrefix(err.Error(), "the detector at") {
			return h, err // nothing listening: down
		}
		// Something answered and it was not Frigate's version string --
		// another program on the port (on a Mac, AirPlay Receiver takes 5000).
		return h, fmt.Errorf("something that is not Frigate is answering at %s (%v)", f.base, err)
	}
	v, _ := io.ReadAll(io.LimitReader(resp.Body, 100))
	resp.Body.Close()
	h.Version = strings.TrimSpace(string(v))

	var stats struct {
		Cameras map[string]struct {
			CameraFPS float64 `json:"camera_fps"`
		} `json:"cameras"`
		Detectors map[string]struct {
			InferenceSpeed float64 `json:"inference_speed"`
		} `json:"detectors"`
	}
	if err := f.getJSON(ctx, "/api/stats", nil, &stats); err != nil {
		return h, err
	}
	for _, d := range stats.Detectors {
		h.InferenceMS = math.Round(d.InferenceSpeed*10) / 10
	}
	c, ok := stats.Cameras[camera]
	h.CameraFound = ok
	h.CameraFPS = c.CameraFPS
	h.CameraOnline = ok && c.CameraFPS > 0
	switch {
	case !ok:
		names := make([]string, 0, len(stats.Cameras))
		for n := range stats.Cameras {
			names = append(names, n)
		}
		h.Message = fmt.Sprintf("Frigate has no camera called %q (it has: %s)", camera, strings.Join(names, ", "))
	case !h.CameraOnline:
		h.Message = "Frigate is running but is receiving no frames from the camera. Is it plugged in?"
	default:
		h.Message = fmt.Sprintf("Frigate %s sees the camera at %.1f frames per second", h.Version, h.CameraFPS)
	}
	return h, nil
}

// frigateEvent is the part of /api/events that matters. Times are Unix
// seconds as floats; end_time is null while the person is still in view.
type frigateEvent struct {
	ID            string   `json:"id"`
	Camera        string   `json:"camera"`
	Label         string   `json:"label"`
	StartTime     float64  `json:"start_time"`
	EndTime       *float64 `json:"end_time"`
	HasClip       bool     `json:"has_clip"`
	HasSnapshot   bool     `json:"has_snapshot"`
	FalsePositive bool     `json:"false_positive"`
	TopScore      *float64 `json:"top_score"`
	Data          struct {
		TopScore float64 `json:"top_score"`
	} `json:"data"`
}

func unixFloat(f float64) time.Time {
	sec, frac := math.Modf(f)
	return time.Unix(int64(sec), int64(frac*1e9)).UTC()
}

func (e frigateEvent) toEvent() DetectorEvent {
	out := DetectorEvent{
		ID: e.ID, Camera: e.Camera, Start: unixFloat(e.StartTime),
		HasClip: e.HasClip, HasSnapshot: e.HasSnapshot, FalsePositive: e.FalsePositive,
		Score: e.Data.TopScore,
	}
	if e.TopScore != nil && *e.TopScore > out.Score {
		out.Score = *e.TopScore
	}
	if e.EndTime != nil {
		t := unixFloat(*e.EndTime)
		out.End = &t
	}
	return out
}

func (f *frigateDetector) PersonEvents(ctx context.Context, camera string, since time.Time) ([]DetectorEvent, error) {
	q := url.Values{}
	q.Set("cameras", camera)
	q.Set("labels", "person")
	q.Set("after", fmt.Sprintf("%.3f", float64(since.UnixMilli())/1000))
	// Oldest first: the watcher resumes from the newest visit it has, so a
	// newest-first page capped at the limit would skip what lay beneath it.
	q.Set("sort", "date_asc")
	q.Set("limit", "200")
	var events []frigateEvent
	if err := f.getJSON(ctx, "/api/events", q, &events); err != nil {
		return nil, err
	}
	out := make([]DetectorEvent, 0, len(events))
	for _, e := range events {
		if e.Label != "" && e.Label != "person" {
			continue
		}
		out = append(out, e.toEvent())
	}
	return out, nil
}

func (f *frigateDetector) Event(ctx context.Context, id string) (DetectorEvent, error) {
	var e frigateEvent
	if err := f.getJSON(ctx, "/api/events/"+url.PathEscape(id), nil, &e); err != nil {
		return DetectorEvent{}, err
	}
	return e.toEvent(), nil
}

func (f *frigateDetector) media(ctx context.Context, path string) (io.ReadCloser, error) {
	resp, err := f.get(ctx, path, nil)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (f *frigateDetector) Snapshot(ctx context.Context, id string) (io.ReadCloser, error) {
	return f.media(ctx, "/api/events/"+url.PathEscape(id)+"/snapshot.jpg")
}

func (f *frigateDetector) Clip(ctx context.Context, id string) (io.ReadCloser, error) {
	return f.media(ctx, "/api/events/"+url.PathEscape(id)+"/clip.mp4")
}
