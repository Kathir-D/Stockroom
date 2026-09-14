package stockroom

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"
)

// Phase 4, the core loop. These run against the live database (see
// requireTestDB) because every rule here is about rows: one custody event per
// asset, a status that flips with it, and a cart that is all or nothing.
// Fixtures are additive and clean themselves up, so the suite never disturbs
// the seed the pgTAP files assert on.

// insertScannableAsset creates an asset with a unique serial and a chosen
// status, owned by creator so the profile cleanup removes it.
func insertScannableAsset(t *testing.T, db *DB, creator Profile, status AssetStatus) (id, serial string) {
	t.Helper()
	serial = fmt.Sprintf("TEST-%09d", rand.IntN(1_000_000_000))
	err := db.Pool.QueryRow(context.Background(), `
		insert into assets (name, serial_number, status, created_by)
		values ('Test scannable', $1, $2, $3)
		returning id`, serial, string(status), creator.ID).Scan(&id)
	if err != nil {
		t.Fatalf("insert scannable asset: %v", err)
	}
	return id, serial
}

// assetStatus reads the status column straight back, for asserting that a
// checkout or check-in moved it.
func assetStatus(t *testing.T, db *DB, assetID string) AssetStatus {
	t.Helper()
	var s AssetStatus
	if err := db.Pool.QueryRow(context.Background(),
		`select status from assets where id = $1`, assetID).Scan(&s); err != nil {
		t.Fatalf("read status: %v", err)
	}
	return s
}

// countCustody returns how many custody rows exist for an asset, and how many
// of those are still open. A cart that failed must leave both at zero.
func countCustody(t *testing.T, db *DB, assetID string) (total, open int) {
	t.Helper()
	err := db.Pool.QueryRow(context.Background(), `
		select count(*), count(*) filter (where checked_in_at is null)
		from custody_events where asset_id = $1`, assetID).Scan(&total, &open)
	if err != nil {
		t.Fatalf("count custody: %v", err)
	}
	return total, open
}

func soon() time.Time { return time.Now().Add(3 * 24 * time.Hour) }

// --- due dates -------------------------------------------------------------

func TestCheckOutAssetsToSelf(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	student := insertTestProfile(t, db, false, "student-password")
	actor := actorFor(student)
	a1, _ := insertScannableAsset(t, db, student, StatusAvailable)
	a2, _ := insertScannableAsset(t, db, student, StatusAvailable)

	due := soon()
	res, err := db.CheckOutAssets(ctx, actor, CheckoutInput{AssetIDs: []string{a1, a2}, DueAt: due})
	if err != nil {
		t.Fatalf("check out: %v", err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("want 2 items, got %d", len(res.Items))
	}
	if res.CustodianID != student.ID || res.CustodianName != "Test User" {
		t.Fatalf("custodian: got %s / %q", res.CustodianID, res.CustodianName)
	}
	if !res.DueAt.Equal(due) {
		t.Fatalf("due at: got %v, want %v", res.DueAt, due)
	}
	// The cart order is what the confirmation lists, not the id order the
	// row lock needed.
	if res.Items[0].AssetID != a1 || res.Items[1].AssetID != a2 {
		t.Fatalf("items came back out of cart order: %+v", res.Items)
	}

	for _, id := range []string{a1, a2} {
		if s := assetStatus(t, db, id); s != StatusCheckedOut {
			t.Fatalf("asset %s: status %s, want checked_out", id, s)
		}
		total, open := countCustody(t, db, id)
		if total != 1 || open != 1 {
			t.Fatalf("asset %s: %d custody rows (%d open), want exactly 1 open", id, total, open)
		}
	}
}

func TestCheckOutAssetsRefusesOtherCustodianForNonAdmin(t *testing.T) {
	db := requireTestDB(t)
	student := insertTestProfile(t, db, false, "pw-student-a")
	other := insertTestProfile(t, db, false, "pw-student-b")
	asset, _ := insertScannableAsset(t, db, student, StatusAvailable)

	_, err := db.CheckOutAssets(context.Background(), actorFor(student),
		CheckoutInput{CustodianID: other.ID, AssetIDs: []string{asset}, DueAt: soon()})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
	if total, _ := countCustody(t, db, asset); total != 0 {
		t.Fatalf("refused checkout wrote %d custody rows", total)
	}
}

func TestScanItemChecksInACheckedOutItem(t *testing.T) {
	db := requireTestDB(t)
	holder := insertTestProfile(t, db, false, "pw-scan-holder")
	scanner := insertTestProfile(t, db, false, "pw-scanner")
	asset, serial := insertScannableAsset(t, db, holder, StatusAvailable)
	openCustody(t, db, asset, holder, holder, time.Now().Add(24*time.Hour))

	// Scanners send the code with a trailing Enter; stray whitespace must not
	// turn a real item into "not a Stockroom item".
	res, err := db.ScanItem(context.Background(), actorFor(scanner), "  "+serial+"\n")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if res.Action != ScanCheckedIn {
		t.Fatalf("action: got %q, want %q", res.Action, ScanCheckedIn)
	}
	if res.Checkable {
		t.Fatal("a just-returned item must not offer the cart")
	}
	if res.ReturnedFrom == nil || res.ReturnedFrom.CustodianID != holder.ID {
		t.Fatalf("returned_from: %+v, want the holder", res.ReturnedFrom)
	}
	if res.Asset.Status != StatusAvailable {
		t.Fatalf("asset status: got %s, want available", res.Asset.Status)
	}
	if s := assetStatus(t, db, asset); s != StatusAvailable {
		t.Fatalf("db status: got %s, want available", s)
	}
}

func TestCustodyListsAreAdminOnly(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	student := insertTestProfile(t, db, false, "pw-lists")
	actor := actorFor(student)

	if _, err := db.ListActiveCustody(ctx, actor); !errors.Is(err, ErrForbidden) {
		t.Fatalf("active: want ErrForbidden, got %v", err)
	}
	if _, err := db.ListOverdueCustody(ctx, actor); !errors.Is(err, ErrForbidden) {
		t.Fatalf("overdue: want ErrForbidden, got %v", err)
	}
}

// The scan flow checks an item in before the surface offering "Add a note" is
// on screen, so the note has to be able to land on a *closed* event. The gate
// in the same test: an event still open refuses, because a note on an item in
// someone's bag is a mis-click rather than a damage report.
func TestAnnotateCustodyEvent(t *testing.T) {
	db := requireTestDB(t)
	holder := insertTestProfile(t, db, false, "pw-note-holder")
	asset, serial := insertScannableAsset(t, db, holder, StatusAvailable)
	openCustody(t, db, asset, holder, holder, time.Now().Add(24*time.Hour))

	ctx := context.Background()
	actor := actorFor(holder)

	// Open: refused.
	open, err := db.GetAsset(ctx, actor, asset)
	if err != nil {
		t.Fatalf("get asset: %v", err)
	}
	if _, err := db.AnnotateCustodyEvent(ctx, actor, open.Custody.CustodyEventID, "dent"); !errors.Is(err, ErrConflict) {
		t.Fatalf("annotate an open event: got %v, want ErrConflict", err)
	}

	// Closed by the scan, then annotated.
	res, err := db.ScanItem(ctx, actor, serial)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	record, err := db.AnnotateCustodyEvent(ctx, actor, res.ReturnedFrom.CustodyEventID, "  lens cap missing  ")
	if err != nil {
		t.Fatalf("annotate: %v", err)
	}
	if record.ConditionIn == nil || *record.ConditionIn != "lens cap missing" {
		t.Fatalf("condition_in: %v, want the trimmed note", record.ConditionIn)
	}
}
