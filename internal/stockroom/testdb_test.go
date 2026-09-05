package stockroom

import (
	"context"
	"os"
	"testing"
	"time"
)

// testDatabaseURL is the connection used by the integration tests. It defaults
// to the local Supabase stack from CLAUDE.md §9.
func testDatabaseURL() string {
	if v := os.Getenv("DATABASE_URL"); v != "" {
		return v
	}
	return "postgresql://postgres:postgres@127.0.0.1:54322/postgres"
}

// requireTestDB opens a pool against the local Postgres. Tests that need a
// database are skipped when it isn't running, so `go test ./...` still works
// on a machine with no Docker; set STOCKROOM_REQUIRE_DB=1 (as CI should) to
// turn that skip into a failure.
func requireTestDB(t *testing.T) *DB {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db, err := Open(ctx, testDatabaseURL())
	if err != nil {
		if os.Getenv("STOCKROOM_REQUIRE_DB") == "1" {
			t.Fatalf("database required but unavailable: %v", err)
		}
		t.Skipf("skipping: local Postgres unavailable (%v); run `supabase start`", err)
	}
	t.Cleanup(db.Close)
	return db
}
