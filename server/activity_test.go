package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The activity log and camera routes over the wire (ROADMAP §2.4, §2.5): who
// may read them, that a scan's screen reaches the log through the header,
// that the unauthenticated scan route is bounded, and that the CSV is a CSV.
func TestActivityRoutes(t *testing.T) {
	h, d := testDeps(t)
	admin := adminToken(t, h, d)
	_, studentSN := seedUser(t, d, false, "student-route-pw")
	student := login(t, h, studentSN, "student-route-pw")
	_, serial := seedAsset(t, d, "available")
	t0 := time.Now().Add(-time.Second).UTC().Format(time.RFC3339)

	for _, path := range []string{"/admin/activity", "/admin/activity.csv", "/admin/camera"} {
		if code, _ := call(t, h, http.MethodGet, path, student, nil); code != http.StatusForbidden {
			t.Errorf("GET %s as a student = %d, want 403", path, code)
		}
	}

	// A scan from the kits screen: the header lands in the row's details.
	body, _ := json.Marshal(map[string]string{"serial": serial})
	req := httptest.NewRequest(http.MethodPost, "/scan", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+student)
	req.Header.Set("X-Stockroom-Screen", "kits")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /scan = %d %s", rec.Code, rec.Body.String())
	}

	code, page := call(t, h, http.MethodGet, "/admin/activity?scans=1&q="+serial+"&from="+t0, admin, nil)
	if code != http.StatusOK {
		t.Fatalf("GET /admin/activity = %d %v", code, page)
	}
	entries, _ := page["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("scan rows = %d, want 1: %v", len(entries), page)
	}
	row := entries[0].(map[string]any)
	details := row["details"].(map[string]any)
	if details["screen"] != "kits" || details["code"] != serial || row["via_scanner"] != true {
		t.Errorf("the scan row = %v, want screen kits, the code, via_scanner", row)
	}

	if code, _ := call(t, h, http.MethodGet, "/admin/activity?type=bogus", admin, nil); code != http.StatusBadRequest {
		t.Errorf("an unknown type = %d, want 400", code)
	}
	if code, _ := call(t, h, http.MethodGet, "/admin/activity?from=yesterday", admin, nil); code != http.StatusBadRequest {
		t.Errorf("a bad time = %d, want 400", code)
	}

	req = httptest.NewRequest(http.MethodGet, "/admin/activity.csv?from="+t0, nil)
	req.Header.Set("Authorization", "Bearer "+admin)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/csv") ||
		!strings.HasPrefix(rec.Body.String(), "time,type,event,summary") {
		t.Errorf("CSV export = %d %s %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}

	// A scan with nobody signed in is logged; an invented result is refused.
	if code, b := call(t, h, http.MethodPost, "/signin/scan", "", map[string]string{"code": "CAM-1", "result": "not_signed_in"}); code != http.StatusOK {
		t.Errorf("POST /signin/scan = %d %v", code, b)
	}
	if code, _ := call(t, h, http.MethodPost, "/signin/scan", "", map[string]string{"code": "CAM-1", "result": "hello"}); code != http.StatusBadRequest {
		t.Errorf("an invented scan result = %d, want 400", code)
	}
	// A cross-origin form can send text/plain without a preflight; it may not
	// write to the log.
	req = httptest.NewRequest(http.MethodPost, "/signin/scan", strings.NewReader(`{"code":"CAM-1","result":"not_signed_in"}`))
	req.Header.Set("Content-Type", "text/plain")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("POST /signin/scan as text/plain = %d, want 415", rec.Code)
	}

	// The camera card reads and refuses a detector off this machine.
	if code, b := call(t, h, http.MethodGet, "/admin/camera", admin, nil); code != http.StatusOK || b["settings"] == nil {
		t.Errorf("GET /admin/camera = %d %v", code, b)
	}
	if code, _ := call(t, h, http.MethodPut, "/admin/camera", admin, map[string]string{"detector_url": "http://10.0.0.5:5000"}); code != http.StatusBadRequest {
		t.Errorf("a remote detector = %d, want 400", code)
	}
	if code, _ := call(t, h, http.MethodGet, "/admin/visits/00000000-0000-0000-0000-000000000000/clip.mp4", admin, nil); code != http.StatusNotFound {
		t.Errorf("a missing visit's clip = %d, want 404", code)
	}
}

// The unauthenticated scan route is a token bucket: a burst, then refusals.
func TestUnattendedScanIsRateLimited(t *testing.T) {
	b := &tokenBucket{capacity: 3, refill: time.Hour}
	for i := 0; i < 3; i++ {
		if !b.allow() {
			t.Fatalf("scan %d refused inside the burst", i+1)
		}
	}
	if b.allow() {
		t.Error("a fourth scan inside the hour was allowed")
	}
}
