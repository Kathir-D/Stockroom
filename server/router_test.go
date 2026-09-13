package main

import (
	"context"
	"encoding/json"
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
