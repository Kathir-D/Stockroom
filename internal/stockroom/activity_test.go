package stockroom

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// rowsFor counts the log rows of one action for one actor since t0.
func rowsFor(t *testing.T, db *DB, action, actorID string, t0 time.Time) int {
	t.Helper()
	return logCount(t, db, `action = $1 and actor_id = $2 and created_at >= $3`, action, actorID, t0)
}

// Every action in the core loop writes exactly one row, timestamped, naming
// who did it -- and every scan is one, whatever it did (ROADMAP §2.4, §2.7).
func TestEachActionWritesExactlyOneRow(t *testing.T) {
	db := requireTestDB(t)
	ctx := WithScreen(context.Background(), "browse")
	t0 := time.Now().Add(-time.Second)
	student := insertTestProfile(t, db, false, "student-pw")
	sa := actorFor(student)

	// Sign-ins: by scan, by password, and a wrong password.
	if _, err := db.LoginByScan(ctx, *student.StudentNumber); err != nil {
		t.Fatal(err)
	}
	if _, err := db.LoginByPassword(ctx, *student.StudentNumber, "student-pw"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.LoginByPassword(ctx, *student.StudentNumber, "wrong"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("wrong password = %v", err)
	}
	for action, want := range map[string]int{"signin_scan": 1, "signin_password": 1, "signin_failed": 1} {
		if got := rowsFor(t, db, action, student.ID, t0); got != want {
			t.Errorf("%s rows = %d, want %d", action, got, want)
		}
	}
	if n := logCount(t, db, `action = 'signin_scan' and actor_id = $1 and via_scanner and details->>'code' = $2`,
		student.ID, *student.StudentNumber); n != 1 {
		t.Error("a scan sign-in is not marked as a scan carrying the code read")
	}
	// A card nobody owns is still a scan in the log.
	if _, err := db.LoginByScan(ctx, "99999999999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown card = %v", err)
	}
	if n := logCount(t, db, `action = 'signin_failed' and via_scanner and details->>'code' = '99999999999' and created_at >= $1`, t0); n != 1 {
		t.Errorf("an unknown card wrote %d rows, want 1", n)
	}

	// Scanning: an available item opens, a checked-out one comes back, an
	// unknown code is refused. Three scans, three rows, all via_scanner.
	asset := insertTestAsset(t, db, student)
	serial := assetLabel(ctx, db.Pool, asset)
	if _, err := db.ScanItem(ctx, sa, serial); err != nil {
		t.Fatal(err)
	}
	due := time.Now().Add(48 * time.Hour)
	if _, err := db.CheckOutAssets(ctx, sa, CheckoutInput{AssetIDs: []string{asset}, DueAt: due}); err != nil {
		t.Fatal(err)
	}
	res, err := db.ScanItem(ctx, sa, serial)
	if err != nil || res.Action != ScanCheckedIn {
		t.Fatalf("scan to return = %+v, %v", res, err)
	}
	if _, err := db.ScanItem(ctx, sa, "NOPE-"+asset[:6]); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown serial = %v", err)
	}
	for action, want := range map[string]int{"scan_opened_item": 1, "checkout": 1, "checkin": 1, "scan_unknown": 1} {
		if got := rowsFor(t, db, action, student.ID, t0); got != want {
			t.Errorf("%s rows = %d, want %d", action, got, want)
		}
	}
	if n := logCount(t, db, `actor_id = $1 and via_scanner and created_at >= $2 and action like 'scan%'`, student.ID, t0); n != 2 {
		t.Errorf("scan rows = %d, want 2 (opened + unknown)", n)
	}
	if n := logCount(t, db, `action = 'checkin' and actor_id = $1 and via_scanner and details->>'screen' = 'browse'`, student.ID); n != 1 {
		t.Error("a check-in by scan is not marked as a scan from the browse screen")
	}
	// The status trigger did not add a second row for the same checkout or return.
	if n := logCount(t, db, `action = 'status_change' and asset_id = $1`, asset); n != 0 {
		t.Errorf("the status trigger added %d rows for actions the server already logged", n)
	}

	// Sign-out, and an idle timeout reported by the session store.
	db.Logout(ctx, sa)
	db.logIdleTimeout(Session{ProfileID: student.ID}, time.Now())
	for _, action := range []string{"signout", "idle_timeout"} {
		if got := rowsFor(t, db, action, student.ID, t0); got != 1 {
			t.Errorf("%s rows = %d, want 1", action, got)
		}
	}
}

// A checkout that fails leaves no log row: the row is in its transaction.
func TestARefusedCheckoutLogsNothing(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	t0 := time.Now().Add(-time.Second)
	student := insertTestProfile(t, db, false, "student-pw")
	asset := insertTestAsset(t, db, student)
	_, err := db.CheckOutAssets(ctx, actorFor(student), CheckoutInput{AssetIDs: []string{asset, "00000000-0000-0000-0000-000000000000"}, DueAt: time.Now().Add(time.Hour)})
	if err == nil {
		t.Fatal("a cart with a missing item checked out")
	}
	if got := rowsFor(t, db, "checkout", student.ID, t0); got != 0 {
		t.Errorf("a refused checkout left %d rows", got)
	}
}

// The log is append-only for everybody, the superuser the server connects
// as included.
func TestActivityLogIsAppendOnly(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	if err := db.logNow(ctx, LogEntry{Category: LogAdmin, Action: "test_append_only", Summary: "x"}); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`update activity_log set summary = 'edited' where action = 'test_append_only'`,
		`delete from activity_log where action = 'test_append_only'`,
		`truncate activity_log`,
	} {
		if _, err := db.Pool.Exec(ctx, sql); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%s = %v, want refused as append-only", sql, err)
		}
	}
}

// Only an admin reads the log, filters work, and the CSV export is itself
// logged.
func TestActivityIsAdminOnlyAndFilters(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	t0 := time.Now().Add(-time.Second)
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	student := insertTestProfile(t, db, false, "student-pw")
	if _, err := db.ListActivity(ctx, actorFor(student), ActivityFilter{}); !errors.Is(err, ErrForbidden) {
		t.Errorf("ListActivity as a student = %v, want ErrForbidden", err)
	}
	if err := db.ExportActivityCSV(ctx, actorFor(student), ActivityFilter{}, &bytes.Buffer{}); !errors.Is(err, ErrForbidden) {
		t.Errorf("ExportActivityCSV as a student = %v, want ErrForbidden", err)
	}
	if _, err := db.LoginByScan(ctx, *student.StudentNumber); err != nil {
		t.Fatal(err)
	}
	if err := db.LogUnattendedScan(ctx, "CAM-7", "not_signed_in"); err != nil {
		t.Fatal(err)
	}

	page, err := db.ListActivity(ctx, admin, ActivityFilter{From: &t0, PersonID: student.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || page.Entries[0].Action != "signin_scan" || deref(page.Entries[0].ActorName) == "" {
		t.Errorf("person filter = %+v", page.Entries)
	}
	scans, err := db.ListActivity(ctx, admin, ActivityFilter{From: &t0, ScansOnly: true, Query: "CAM-7"})
	if err != nil || len(scans.Entries) != 1 || scans.Entries[0].Action != "scan_refused" {
		t.Errorf("the unattended scan = %+v, %v", scans.Entries, err)
	}
	if _, err := db.ListActivity(ctx, admin, ActivityFilter{Categories: []string{"nonsense"}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("an unknown type = %v, want ErrInvalid", err)
	}
	if err := db.LogUnattendedScan(ctx, "X", "anything"); !errors.Is(err, ErrInvalid) {
		t.Errorf("an invented scan result = %v, want ErrInvalid", err)
	}

	var buf bytes.Buffer
	if err := db.ExportActivityCSV(ctx, admin, ActivityFilter{From: &t0, PersonID: student.ID}, &buf); err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(buf.String(), "\n"); lines != 2 {
		t.Errorf("CSV has %d lines, want a header and one row:\n%s", lines, buf.String())
	}
	if got := rowsFor(t, db, "activity_exported", admin.ID, t0); got != 1 {
		t.Errorf("the export wrote %d log rows, want 1", got)
	}
}

// The log outlives what it talks about: deleting the account and the item
// keeps the rows and their names.
func TestLogOutlivesTheAccountAndTheItem(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	student := insertTestProfile(t, db, false, "student-pw")
	if _, err := db.LoginByScan(ctx, *student.StudentNumber); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteUser(ctx, admin, student.ID); err != nil {
		t.Fatalf("DeleteUser of an account with log rows: %v", err)
	}
	var name *string
	if err := db.Pool.QueryRow(ctx, `select actor_name from activity_log where actor_id = $1 and action = 'signin_scan'`, student.ID).Scan(&name); err != nil {
		t.Fatalf("the deleted account's sign-in is gone from the log: %v", err)
	}
	if deref(name) == "" {
		t.Error("the row lost the name of the deleted account")
	}
	if n := rowsFor(t, db, "user_deleted", admin.ID, time.Now().Add(-time.Minute)); n != 1 {
		t.Errorf("user_deleted rows = %d, want 1", n)
	}
}
