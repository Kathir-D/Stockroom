package main

import (
	"bytes"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"stockroom/internal/stockroom"
)

// withScreen copies the X-Stockroom-Screen header -- which screen of the UI
// sent the request -- into the request context, so a scan's log row can say
// where it was scanned (ROADMAP §2.4) without every endpoint taking a field.
func withScreen(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if screen := r.Header.Get("X-Stockroom-Screen"); screen != "" {
			r = r.WithContext(stockroom.WithScreen(r.Context(), screen))
		}
		next.ServeHTTP(w, r)
	})
}

// activityFilter reads the timeline's query string: from, to and before as
// RFC 3339 instants, before_id as the last entry's id beside before, person and item as ids, type as a comma-separated list
// of categories, scans=1, q for text and limit.
func activityFilter(r *http.Request) (stockroom.ActivityFilter, error) {
	q := r.URL.Query()
	var f stockroom.ActivityFilter
	parseTime := func(name string) (*time.Time, error) {
		v := strings.TrimSpace(q.Get(name))
		if v == "" {
			return nil, nil
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return nil, fmt.Errorf("%w: %s must be a time like 2026-09-26T08:00:00Z", stockroom.ErrInvalid, name)
		}
		return &t, nil
	}
	var err error
	if f.From, err = parseTime("from"); err != nil {
		return f, err
	}
	if f.To, err = parseTime("to"); err != nil {
		return f, err
	}
	if f.Before, err = parseTime("before"); err != nil {
		return f, err
	}
	f.BeforeID = strings.TrimSpace(q.Get("before_id"))
	f.PersonID = strings.TrimSpace(q.Get("person"))
	f.AssetID = strings.TrimSpace(q.Get("item"))
	if t := strings.TrimSpace(q.Get("type")); t != "" {
		f.Categories = strings.Split(t, ",")
	}
	f.ScansOnly = q.Get("scans") == "1"
	f.Query = q.Get("q")
	if l := q.Get("limit"); l != "" {
		f.Limit, _ = strconv.Atoi(l)
	}
	return f, nil
}

// GET /admin/activity
func (d deps) handleListActivity(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	f, err := activityFilter(r)
	if err != nil {
		writeError(w, err)
		return
	}
	page, err := d.db.ListActivity(r.Context(), actor, f)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// GET /admin/activity.csv
func (d deps) handleExportActivity(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	f, err := activityFilter(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := stockroom.RequireAdmin(actor); err != nil {
		writeError(w, err)
		return
	}
	// Buffered, so a failure is a JSON error rather than a download with CSV
	// headers and an error body.
	var buf bytes.Buffer
	if err := d.db.ExportActivityCSV(r.Context(), actor, f, &buf); err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="stockroom-activity-%s.csv"`, time.Now().Format("2006-01-02")))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(buf.Bytes())
}

// POST /signin/scan {"code": "...", "result": "not_signed_in" | "item_at_signin" | "invalid"}
func (d deps) handleUnattendedScan(w http.ResponseWriter, r *http.Request) {
	// No session guards this route, so JSON is required: a cross-origin page
	// can send text/plain without a preflight, and would otherwise be able to
	// write rows into a log nobody can delete.
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]any{"error": "send application/json"})
		return
	}
	if !unattendedScans.allow() {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "too many scans"})
		return
	}
	var in struct {
		Code   string `json:"code"`
		Result string `json:"result"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	if err := d.db.LogUnattendedScan(r.Context(), in.Code, in.Result); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// unattendedScans bounds the unauthenticated scan log to a burst of 30 and
// one a second after that. A scanner cannot go faster than a person can
// present barcodes to it; anything above that is not a scanner.
var unattendedScans = &tokenBucket{capacity: 30, refill: time.Second}

type tokenBucket struct {
	mu       sync.Mutex
	capacity int
	refill   time.Duration
	tokens   int
	last     time.Time
}

func (b *tokenBucket) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	if b.last.IsZero() {
		b.tokens, b.last = b.capacity, now
	}
	if add := int(now.Sub(b.last) / b.refill); add > 0 {
		b.tokens = min(b.capacity, b.tokens+add)
		b.last = b.last.Add(time.Duration(add) * b.refill)
	}
	if b.tokens == 0 {
		return false
	}
	b.tokens--
	return true
}

// GET /admin/camera
func (d deps) handleGetCamera(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	out, err := d.db.GetCamera(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// PUT /admin/camera (partial update)
func (d deps) handleSaveCamera(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var in stockroom.CameraSettingsInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	out, err := d.db.SaveCamera(r.Context(), actor, in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// POST /admin/camera/test {detector_url?, camera_name?}
func (d deps) handleTestCamera(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var in stockroom.CameraSettingsInput
	if r.ContentLength != 0 {
		if err := decodeJSON(w, r, &in); err != nil {
			writeError(w, err)
			return
		}
	}
	h, err := d.db.TestCamera(r.Context(), actor, in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h)
}

// POST /admin/visits/{id}/keep {"keep": true}
func (d deps) handleVisitKeep(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var in struct {
		Keep bool `json:"keep"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	v, err := d.db.SetVisitKeep(r.Context(), actor, r.PathValue("id"), in.Keep)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (d deps) handleVisitSnapshot(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	d.serveVisitMedia(w, r, actor, "snapshot", "image/jpeg")
}

func (d deps) handleVisitClip(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	d.serveVisitMedia(w, r, actor, "clip", "video/mp4")
}

// serveVisitMedia streams a recording with Range support, so a player can
// seek. Only the first request for a clip -- no Range, or one starting at
// byte 0 -- is logged as a view; the rest are the same viewing.
func (d deps) serveVisitMedia(w http.ResponseWriter, r *http.Request, actor stockroom.Actor, kind, contentType string) {
	rng := r.Header.Get("Range")
	logView := rng == "" || strings.HasPrefix(rng, "bytes=0-")
	m, err := d.db.OpenVisitMedia(r.Context(), actor, r.PathValue("id"), kind, logView)
	if err != nil {
		writeError(w, err)
		return
	}
	defer m.File.Close()
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, "", m.ModTime, m.File)
}
