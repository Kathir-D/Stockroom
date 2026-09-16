package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"stockroom/internal/stockroom"
)

func testDatabaseURL() string {
	if v := os.Getenv("DATABASE_URL"); v != "" {
		return v
	}
	return "postgresql://postgres:postgres@127.0.0.1:54322/postgres"
}

// openTestDB returns a pool against the local Postgres, skipping when it isn't
// running unless STOCKROOM_REQUIRE_DB=1 demands it (CI). The caller owns Close.
func openTestDB(t *testing.T) *stockroom.DB {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db, err := stockroom.Open(ctx, testDatabaseURL(), stockroom.Options{SessionIdle: time.Hour})
	if err != nil {
		if os.Getenv("STOCKROOM_REQUIRE_DB") == "1" {
			t.Fatalf("database required but unavailable: %v", err)
		}
		t.Skipf("skipping: local Postgres unavailable (%v); run `supabase start`", err)
	}
	return db
}

func do(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestHealthOK(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	rec := do(newRouter(deps{db: db}), http.MethodGet, "/health")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var body struct {
		OK   bool      `json:"ok"`
		DB   string    `json:"db"`
		Time time.Time `json:"time"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not the documented shape: %v (%q)", err, rec.Body.String())
	}
	if !body.OK || body.DB != "ok" {
		t.Errorf("body = %+v, want ok=true db=\"ok\"", body)
	}
	if body.Time.IsZero() || time.Since(body.Time) > time.Minute {
		t.Errorf("time = %v, want a fresh timestamp", body.Time)
	}
}

// TestOriginAllowed pins the four Wails webview origins.
//
// A wrong answer here is invisible in the app: the browser blocks the request
// before any JavaScript runs, and the frontend reports "cannot reach the
// Stockroom server" — the same message it shows when the server really is down.
// That cost an afternoon once (the macOS dev origin carries a host and a port,
// wails://wails.localhost:34115, and only wails://wails was allowed), so every
// platform and mode is listed rather than trusted to a comment.
func TestOriginAllowed(t *testing.T) {
	allowed := []string{
		"http://localhost:5173",         // web-app, vite dev
		"http://127.0.0.1:5174",         // vite's next free port
		"http://localhost:34115",        // wails dev, opened in a browser
		"wails://wails",                 // macOS/Linux, wails build
		"wails://wails.localhost:34115", // macOS/Linux, wails dev
		"http://wails.localhost",        // Windows, wails build
		"http://wails.localhost:34115",  // Windows, wails dev
	}
	for _, origin := range allowed {
		if !originAllowed(origin) {
			t.Errorf("originAllowed(%q) = false, want true", origin)
		}
	}

	refused := []string{
		"",
		"http://example.com",
		// The host check is exact, not a prefix: this is somebody else's domain.
		"http://wails.localhost.example.com",
		// A LAN address, which CLAUDE.md §2 keeps out even on the right port.
		"http://192.168.1.20:5173",
		"https://localhost:5173",
	}
	for _, origin := range refused {
		if originAllowed(origin) {
			t.Errorf("originAllowed(%q) = true, want false", origin)
		}
	}
}

// TestLogBlockedOriginIsBounded pins the cap on the remembered-origin set.
//
// Origin is a caller-controlled header read before any session check, so the
// set that makes the warning fire once per origin is also a map an outsider can
// grow a key at a time. The cap is the whole point of the map's shape; without
// it, `sync.Map` and one log line per distinct value is unbounded in both.
func TestLogBlockedOriginIsBounded(t *testing.T) {
	prev := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(prev) })

	blockedOrigins.mu.Lock()
	blockedOrigins.seen = make(map[string]struct{})
	blockedOrigins.mu.Unlock()

	// One repeat first: the same origin twice must still be one entry.
	logBlockedOrigin("http://example.com")
	logBlockedOrigin("http://example.com")

	for i := 0; i < maxBlockedOrigins+50; i++ {
		logBlockedOrigin(fmt.Sprintf("http://attacker-%d.example", i))
	}

	blockedOrigins.mu.Lock()
	n := len(blockedOrigins.seen)
	blockedOrigins.mu.Unlock()

	if n != maxBlockedOrigins {
		t.Errorf("remembered %d origins, want exactly the cap %d", n, maxBlockedOrigins)
	}
}
