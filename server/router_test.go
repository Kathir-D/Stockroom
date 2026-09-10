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

	db, err := stockroom.Open(ctx, testDatabaseURL())
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

// The point of /health is that it fails when Postgres is gone, because the
// start scripts poll it. A closed pool stands in for a stopped database.
func TestHealthFailsWhenDatabaseIsDown(t *testing.T) {
	db := openTestDB(t)
	db.Close()

	rec := do(newRouter(deps{db: db}), http.MethodGet, "/health")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 when the database is unreachable", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v (%q)", err, rec.Body.String())
	}
	if body["error"] != "internal error" {
		t.Errorf("error = %q, want the generic message", body["error"])
	}
}

// Routing is asserted without a database: neither case reaches the handler, so
// a nil pool is safe here and keeps the test hermetic.
func TestRouterRejectsWrongMethod(t *testing.T) {
	h := newRouter(deps{})
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		if rec := do(h, m, "/health"); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /health: status = %d, want 405", m, rec.Code)
		}
	}
}

func TestRouterUnknownPath(t *testing.T) {
	h := newRouter(deps{})
	for _, p := range []string{"/", "/nope", "/health/extra", "/HEALTH"} {
		if rec := do(h, http.MethodGet, p); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want 404", p, rec.Code)
		}
	}
}

// HEAD is served by the GET pattern in Go's ServeMux; confirm the health check
// answers it rather than 405-ing a monitoring script.
func TestHealthAnswersHEAD(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	if rec := do(newRouter(deps{db: db}), http.MethodHead, "/health"); rec.Code != http.StatusOK {
		t.Errorf("HEAD /health: status = %d, want 200", rec.Code)
	}
}
