package stockroom

import (
	"context"
	"fmt"
	"math/rand/v2"
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

	db, err := Open(ctx, testDatabaseURL(), Options{})
	if err != nil {
		if os.Getenv("STOCKROOM_REQUIRE_DB") == "1" {
			t.Fatalf("database required but unavailable: %v", err)
		}
		t.Skipf("skipping: local Postgres unavailable (%v); run `supabase start`", err)
	}
	t.Cleanup(db.Close)
	return db
}

// testStudentNumber returns a fresh 9-digit number in a range the seed never
// uses, and deletes the profile that ends up holding it when the test is done.
func testStudentNumber(t *testing.T, db *DB) string {
	t.Helper()
	sn := fmt.Sprintf("9%08d", rand.IntN(100_000_000))
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `delete from profiles where student_number = $1`, sn)
	})
	return sn
}

// insertTestProfile creates an account with a fresh student number (see
// testStudentNumber) and removes it, along with any custody rows and assets
// the test hung off it, when the test ends. password == "" leaves the hash
// null, the roster-import shape.
func insertTestProfile(t *testing.T, db *DB, isAdmin bool, password string) Profile {
	t.Helper()
	ctx := context.Background()
	sn := testStudentNumber(t, db)
	var hash *string
	if password != "" {
		h, err := HashPassword(password)
		if err != nil {
			t.Fatal(err)
		}
		hash = &h
	}
	p, err := scanProfile(db.Pool.QueryRow(ctx, `
		insert into profiles (student_number, first_name, last_name, full_name, is_admin, password_hash)
		values ($1, 'Test', 'User', 'Test User', $2, $3)
		returning `+profileColumns, sn, isAdmin, hash))
	if err != nil {
		t.Fatalf("insert test profile: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `delete from custody_events where custodian_id = $1 or checked_out_by = $1 or checked_in_by = $1`, p.ID)
		_, _ = db.Pool.Exec(ctx, `delete from assets where created_by = $1`, p.ID)
	})
	return p
}

// actorFor builds the Actor that DB.Resolve would produce for p with a
// full (not limited) session, for calling package functions directly.
func actorFor(p Profile) Actor {
	sn := ""
	if p.StudentNumber != nil {
		sn = *p.StudentNumber
	}
	return Actor{ID: p.ID, StudentNumber: sn, IsAdmin: p.IsAdmin, Token: "test-" + p.ID}
}

// insertTestAsset creates an asset owned by creator (so the profile cleanup
// removes it) and returns its id.
func insertTestAsset(t *testing.T, db *DB, creator Profile) string {
	t.Helper()
	var id string
	err := db.Pool.QueryRow(context.Background(), `
		insert into assets (name, serial_number, created_by)
		values ('Test asset', 'TEST-' || substr(md5(random()::text), 1, 12), $1)
		returning id`, creator.ID).Scan(&id)
	if err != nil {
		t.Fatalf("insert test asset: %v", err)
	}
	return id
}

// openCustody checks assetID out to custodian, due at dueAt (past = overdue).
func openCustody(t *testing.T, db *DB, assetID string, custodian, by Profile, dueAt time.Time) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.Pool.Exec(ctx, `update assets set status = 'checked_out' where id = $1`, assetID); err != nil {
		t.Fatal(err)
	}
	_, err := db.Pool.Exec(ctx, `
		insert into custody_events (asset_id, custodian_id, checked_out_by, due_at)
		values ($1, $2, $3, $4)`, assetID, custodian.ID, by.ID, dueAt)
	if err != nil {
		t.Fatalf("open custody: %v", err)
	}
}
