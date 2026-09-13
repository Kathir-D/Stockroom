package stockroom

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
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
		insert into assets (asset_tag, name, serial_number, status, created_by)
		values ('TEST-' || substr(md5(random()::text), 1, 12), 'Test scannable', $1, $2, $3)
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

func TestCheckDueAt(t *testing.T) {
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		dueAt   time.Time
		wantErr bool
	}{
		{"zero value is refused", time.Time{}, true},
		{"in the past", now.Add(-time.Hour), true},
		{"exactly now", now, true},
		{"a minute out", now.Add(time.Minute), false},
		{"three days out", now.Add(3 * 24 * time.Hour), false},
		{"exactly the cap", now.Add(MaxCheckoutDays * 24 * time.Hour), false},
		{"a second past the cap", now.Add(MaxCheckoutDays*24*time.Hour + time.Second), true},
		{"a month out", now.Add(30 * 24 * time.Hour), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkDueAt(tc.dueAt, now)
			if tc.wantErr && !errors.Is(err, ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("want no error, got %v", err)
			}
		})
	}
}

// The cap is quoted in the message because a date picker offering "seven days
// out" at end of day lands past it, and its user has to be told why.
func TestCheckDueAtNamesTheLatestInstant(t *testing.T) {
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	err := checkDueAt(now.Add(30*24*time.Hour), now)
	if err == nil || !strings.Contains(err.Error(), "7 days") ||
		!strings.Contains(err.Error(), "2026-09-19T10:00:00Z") {
		t.Fatalf("message should name the cap and the latest instant, got %v", err)
	}
}

// --- cart normalization ----------------------------------------------------

func TestNormalizeCartIDs(t *testing.T) {
	// A cart is a set: the same item added twice is still one item, not a
	// request for two, and a checkout must not fail because of it.
	got, err := normalizeCartIDs([]string{" a ", "b", "a", "", "  ", "b"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a", "b"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v (order must be preserved)", got, want)
		}
	}

	for _, empty := range [][]string{nil, {}, {"", "   "}} {
		if _, err := normalizeCartIDs(empty); !errors.Is(err, ErrInvalid) {
			t.Fatalf("empty cart %q: want ErrInvalid, got %v", empty, err)
		}
	}
}

// --- checkout --------------------------------------------------------------

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

func TestCheckOutAssetsDeduplicatesTheCart(t *testing.T) {
	db := requireTestDB(t)
	student := insertTestProfile(t, db, false, "pw-dedupe")
	asset, _ := insertScannableAsset(t, db, student, StatusAvailable)

	res, err := db.CheckOutAssets(context.Background(), actorFor(student),
		CheckoutInput{AssetIDs: []string{asset, asset}, DueAt: soon()})
	if err != nil {
		t.Fatalf("check out: %v", err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("want 1 item, got %d", len(res.Items))
	}
	if total, open := countCustody(t, db, asset); total != 1 || open != 1 {
		t.Fatalf("%d custody rows (%d open), want exactly 1 open", total, open)
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

func TestCheckOutAssetsAdminChoosesCustodian(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := insertTestProfile(t, db, true, "pw-admin")
	student := insertTestProfile(t, db, false, "pw-student")
	asset, _ := insertScannableAsset(t, db, admin, StatusAvailable)

	res, err := db.CheckOutAssets(ctx, actorFor(admin),
		CheckoutInput{CustodianID: student.ID, AssetIDs: []string{asset}, DueAt: soon()})
	if err != nil {
		t.Fatalf("check out: %v", err)
	}
	if res.CustodianID != student.ID {
		t.Fatalf("custodian: got %s, want %s", res.CustodianID, student.ID)
	}
	// The custodian holds it, but the admin is on record as having handed it
	// over: both columns matter for the history screens.
	var custodian, by string
	err = db.Pool.QueryRow(ctx,
		`select custodian_id, checked_out_by from custody_events where asset_id = $1`, asset).
		Scan(&custodian, &by)
	if err != nil {
		t.Fatal(err)
	}
	if custodian != student.ID || by != admin.ID {
		t.Fatalf("custody_events: custodian %s, checked_out_by %s", custodian, by)
	}
}

func TestCheckOutAssetsUnknownCustodian(t *testing.T) {
	db := requireTestDB(t)
	admin := insertTestProfile(t, db, true, "pw-admin")
	asset, _ := insertScannableAsset(t, db, admin, StatusAvailable)

	_, err := db.CheckOutAssets(context.Background(), actorFor(admin), CheckoutInput{
		CustodianID: "00000000-0000-0000-0000-0000000009ff",
		AssetIDs:    []string{asset},
		DueAt:       soon(),
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestCheckOutAssetsBoundsTheDueDate(t *testing.T) {
	db := requireTestDB(t)
	student := insertTestProfile(t, db, false, "pw-due-date")
	asset, _ := insertScannableAsset(t, db, student, StatusAvailable)

	for _, due := range []time.Time{
		{},
		time.Now().Add(-time.Hour),
		time.Now().Add((MaxCheckoutDays + 1) * 24 * time.Hour),
	} {
		_, err := db.CheckOutAssets(context.Background(), actorFor(student),
			CheckoutInput{AssetIDs: []string{asset}, DueAt: due})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("due %v: want ErrInvalid, got %v", due, err)
		}
	}
	if s := assetStatus(t, db, asset); s != StatusAvailable {
		t.Fatalf("asset moved to %s despite every checkout being refused", s)
	}
}

func TestCheckOutAssetsBlocksOverdueCustodian(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	student := insertTestProfile(t, db, false, "pw-overdue")
	admin := insertTestProfile(t, db, true, "pw-admin")

	late, _ := insertScannableAsset(t, db, student, StatusAvailable)
	openCustody(t, db, late, student, student, time.Now().Add(-48*time.Hour))
	wanted, _ := insertScannableAsset(t, db, student, StatusAvailable)

	_, err := db.CheckOutAssets(ctx, actorFor(student),
		CheckoutInput{AssetIDs: []string{wanted}, DueAt: soon()})
	if !errors.Is(err, ErrOverdueBlocked) {
		t.Fatalf("want ErrOverdueBlocked, got %v", err)
	}
	// The message names what to bring back; "conflict" alone leaves the UI
	// with nothing to show.
	if !strings.Contains(err.Error(), "Test scannable") {
		t.Fatalf("error should name the overdue item, got %q", err)
	}
	if total, _ := countCustody(t, db, wanted); total != 0 {
		t.Fatalf("blocked checkout wrote %d custody rows", total)
	}

	// A non-admin cannot lift their own block.
	_, err = db.CheckOutAssets(ctx, actorFor(student),
		CheckoutInput{AssetIDs: []string{wanted}, DueAt: soon(), OverrideOverdue: true})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin override: want ErrForbidden, got %v", err)
	}

	// An admin can, per checkout.
	if _, err := db.CheckOutAssets(ctx, actorFor(admin), CheckoutInput{
		CustodianID:     student.ID,
		AssetIDs:        []string{wanted},
		DueAt:           soon(),
		OverrideOverdue: true,
	}); err != nil {
		t.Fatalf("admin override: %v", err)
	}
	if s := assetStatus(t, db, wanted); s != StatusCheckedOut {
		t.Fatalf("after override: status %s, want checked_out", s)
	}

	// Without the override flag the admin is blocked too: the flag is the
	// decision, not the role.
	another, _ := insertScannableAsset(t, db, student, StatusAvailable)
	_, err = db.CheckOutAssets(ctx, actorFor(admin),
		CheckoutInput{CustodianID: student.ID, AssetIDs: []string{another}, DueAt: soon()})
	if !errors.Is(err, ErrOverdueBlocked) {
		t.Fatalf("admin without override: want ErrOverdueBlocked, got %v", err)
	}
}

// An item due later today is not overdue, so it must not block anything.
func TestCheckOutAssetsAllowsCustodianWithNothingLate(t *testing.T) {
	db := requireTestDB(t)
	student := insertTestProfile(t, db, false, "pw-not-late")
	held, _ := insertScannableAsset(t, db, student, StatusAvailable)
	openCustody(t, db, held, student, student, time.Now().Add(2*time.Hour))
	wanted, _ := insertScannableAsset(t, db, student, StatusAvailable)

	if _, err := db.CheckOutAssets(context.Background(), actorFor(student),
		CheckoutInput{AssetIDs: []string{wanted}, DueAt: soon()}); err != nil {
		t.Fatalf("check out: %v", err)
	}
}

func TestCheckOutAssetsFailsTheWholeCart(t *testing.T) {
	db := requireTestDB(t)
	student := insertTestProfile(t, db, false, "pw-whole-cart")
	good, _ := insertScannableAsset(t, db, student, StatusAvailable)

	for _, blocked := range []AssetStatus{StatusCheckedOut, StatusUnavailable} {
		t.Run(string(blocked), func(t *testing.T) {
			bad, _ := insertScannableAsset(t, db, student, blocked)
			_, err := db.CheckOutAssets(context.Background(), actorFor(student),
				CheckoutInput{AssetIDs: []string{good, bad}, DueAt: soon()})
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("want ErrConflict, got %v", err)
			}
			// Nothing at all happened: no custody row for the good item, and
			// its status untouched. A half-committed cart is the failure this
			// test exists for.
			if total, _ := countCustody(t, db, good); total != 0 {
				t.Fatalf("partial checkout: %d custody rows for the available item", total)
			}
			if s := assetStatus(t, db, good); s != StatusAvailable {
				t.Fatalf("partial checkout: available item moved to %s", s)
			}
		})
	}
}

func TestCheckOutAssetsUnknownAssetFailsTheCart(t *testing.T) {
	db := requireTestDB(t)
	student := insertTestProfile(t, db, false, "pw-unknown")
	good, _ := insertScannableAsset(t, db, student, StatusAvailable)

	_, err := db.CheckOutAssets(context.Background(), actorFor(student), CheckoutInput{
		AssetIDs: []string{good, "00000000-0000-0000-0000-0000000009ff"},
		DueAt:    soon(),
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if total, _ := countCustody(t, db, good); total != 0 {
		t.Fatalf("partial checkout: %d custody rows written", total)
	}
}

func TestCheckOutAssetsRefusesLimitedSession(t *testing.T) {
	db := requireTestDB(t)
	student := insertTestProfile(t, db, false, "")
	asset, _ := insertScannableAsset(t, db, student, StatusAvailable)
	limited := actorFor(student)
	limited.Limited = true

	_, err := db.CheckOutAssets(context.Background(), limited,
		CheckoutInput{AssetIDs: []string{asset}, DueAt: soon()})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}

// --- check-in --------------------------------------------------------------

func TestCheckInAssetByAnotherUser(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	holder := insertTestProfile(t, db, false, "pw-holder")
	returner := insertTestProfile(t, db, false, "pw-returner")
	asset, _ := insertScannableAsset(t, db, holder, StatusAvailable)
	openCustody(t, db, asset, holder, holder, time.Now().Add(24*time.Hour))

	// Anyone signed in can return anything: the person walking the camera
	// back to the closet is often not the person who signed it out.
	note := "  lens cap missing  "
	res, err := db.CheckInAsset(ctx, actorFor(returner), asset, &note)
	if err != nil {
		t.Fatalf("check in: %v", err)
	}
	if res.ReturnedFrom.CustodianID != holder.ID {
		t.Fatalf("returned_from: got %s, want %s", res.ReturnedFrom.CustodianID, holder.ID)
	}
	if res.Asset.Status != StatusAvailable || res.Asset.Custody != nil {
		t.Fatalf("asset after check-in: status %s, custody %+v", res.Asset.Status, res.Asset.Custody)
	}
	if s := assetStatus(t, db, asset); s != StatusAvailable {
		t.Fatalf("status: got %s, want available", s)
	}

	var (
		in   *time.Time
		by   *string
		cond *string
	)
	err = db.Pool.QueryRow(ctx,
		`select checked_in_at, checked_in_by, condition_in from custody_events where asset_id = $1`, asset).
		Scan(&in, &by, &cond)
	if err != nil {
		t.Fatal(err)
	}
	if in == nil || by == nil || *by != returner.ID {
		t.Fatalf("closed row: checked_in_at %v, checked_in_by %v", in, by)
	}
	// The damage note belongs to this trip, trimmed, not to the asset.
	if cond == nil || *cond != "lens cap missing" {
		t.Fatalf("condition_in: got %v, want the trimmed note", cond)
	}
	if total, open := countCustody(t, db, asset); total != 1 || open != 0 {
		t.Fatalf("%d custody rows (%d open) after check-in, want 1 closed", total, open)
	}
}

func TestCheckInAssetWithoutNote(t *testing.T) {
	db := requireTestDB(t)
	holder := insertTestProfile(t, db, false, "pw-nonote")
	asset, _ := insertScannableAsset(t, db, holder, StatusAvailable)
	openCustody(t, db, asset, holder, holder, time.Now().Add(24*time.Hour))

	if _, err := db.CheckInAsset(context.Background(), actorFor(holder), asset, nil); err != nil {
		t.Fatalf("check in: %v", err)
	}
	var cond *string
	if err := db.Pool.QueryRow(context.Background(),
		`select condition_in from custody_events where asset_id = $1`, asset).Scan(&cond); err != nil {
		t.Fatal(err)
	}
	// A blank note is null, not "", so the history screen tests one thing.
	if cond != nil {
		t.Fatalf("condition_in: got %q, want null", *cond)
	}
}

func TestCheckInAssetTwiceIsAConflict(t *testing.T) {
	db := requireTestDB(t)
	holder := insertTestProfile(t, db, false, "pw-twice")
	asset, _ := insertScannableAsset(t, db, holder, StatusAvailable)
	openCustody(t, db, asset, holder, holder, time.Now().Add(24*time.Hour))
	actor := actorFor(holder)

	if _, err := db.CheckInAsset(context.Background(), actor, asset, nil); err != nil {
		t.Fatalf("first check in: %v", err)
	}
	_, err := db.CheckInAsset(context.Background(), actor, asset, nil)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("second check in: want ErrConflict, got %v", err)
	}
	if total, _ := countCustody(t, db, asset); total != 1 {
		t.Fatalf("second check-in wrote a duplicate row: %d total", total)
	}
}

func TestCheckInAssetUnknownAndLimited(t *testing.T) {
	db := requireTestDB(t)
	holder := insertTestProfile(t, db, false, "pw-unknown-in")
	asset, _ := insertScannableAsset(t, db, holder, StatusAvailable)
	openCustody(t, db, asset, holder, holder, time.Now().Add(24*time.Hour))

	_, err := db.CheckInAsset(context.Background(), actorFor(holder),
		"00000000-0000-0000-0000-0000000009ff", nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown asset: want ErrNotFound, got %v", err)
	}

	limited := actorFor(holder)
	limited.Limited = true
	if _, err := db.CheckInAsset(context.Background(), limited, asset, nil); !errors.Is(err, ErrForbidden) {
		t.Fatalf("limited session: want ErrForbidden, got %v", err)
	}
}

// An asset whose status drifted to available while a custody row is still
// open is still out. The open row is the truth, both here and in the detail
// popup.
func TestCheckInAssetFollowsTheOpenRowNotTheStatus(t *testing.T) {
	db := requireTestDB(t)
	holder := insertTestProfile(t, db, false, "pw-drift")
	asset, _ := insertScannableAsset(t, db, holder, StatusAvailable)
	openCustody(t, db, asset, holder, holder, time.Now().Add(24*time.Hour))
	if _, err := db.Pool.Exec(context.Background(),
		`update assets set status = 'available' where id = $1`, asset); err != nil {
		t.Fatal(err)
	}

	if _, err := db.CheckInAsset(context.Background(), actorFor(holder), asset, nil); err != nil {
		t.Fatalf("check in: %v", err)
	}
	if _, open := countCustody(t, db, asset); open != 0 {
		t.Fatalf("the open row survived a check-in")
	}
}

// --- scanning --------------------------------------------------------------

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

func TestScanItemOpensDetail(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	student := insertTestProfile(t, db, false, "pw-scan-detail")
	actor := actorFor(student)

	available, availableSerial := insertScannableAsset(t, db, student, StatusAvailable)
	res, err := db.ScanItem(ctx, actor, availableSerial)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if res.Action != ScanDetail || !res.Checkable || res.ReturnedFrom != nil {
		t.Fatalf("available scan: %+v", res)
	}
	// The scan and click paths must be indistinguishable to the frontend.
	detail, err := db.GetAsset(ctx, actor, available)
	if err != nil {
		t.Fatal(err)
	}
	if res.Asset.ID != detail.ID || res.Asset.Status != detail.Status ||
		len(res.Asset.CategoryPath) != len(detail.CategoryPath) {
		t.Fatalf("scan payload differs from GetAsset:\n scan %+v\n get  %+v", res.Asset, detail)
	}

	_, unavailableSerial := insertScannableAsset(t, db, student, StatusUnavailable)
	res, err = db.ScanItem(ctx, actor, unavailableSerial)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	// Same dialog, but the flag closes the path to the cart.
	if res.Action != ScanDetail || res.Checkable {
		t.Fatalf("unavailable scan: action %q checkable %v", res.Action, res.Checkable)
	}
}

// Which branch a scan takes is decided by the open custody row, not by the
// status column: someone standing at the closet with the item in their hand
// needs it returned, whatever the column drifted to.
func TestScanItemFollowsTheOpenRowNotTheStatus(t *testing.T) {
	db := requireTestDB(t)
	holder := insertTestProfile(t, db, false, "pw-scan-drift")
	asset, serial := insertScannableAsset(t, db, holder, StatusAvailable)
	openCustody(t, db, asset, holder, holder, time.Now().Add(24*time.Hour))
	if _, err := db.Pool.Exec(context.Background(),
		`update assets set status = 'available' where id = $1`, asset); err != nil {
		t.Fatal(err)
	}

	res, err := db.ScanItem(context.Background(), actorFor(holder), serial)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if res.Action != ScanCheckedIn {
		t.Fatalf("action: got %q, want %q", res.Action, ScanCheckedIn)
	}
	if _, open := countCustody(t, db, asset); open != 0 {
		t.Fatal("the open row survived the scan")
	}
}

func TestScanItemUnknownSerial(t *testing.T) {
	db := requireTestDB(t)
	student := insertTestProfile(t, db, false, "pw-scan-unknown")
	actor := actorFor(student)

	if _, err := db.ScanItem(context.Background(), actor, "NOT-A-REAL-SERIAL"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, err := db.ScanItem(context.Background(), actor, "   "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("blank serial: want ErrInvalid, got %v", err)
	}

	limited := actorFor(student)
	limited.Limited = true
	if _, err := db.ScanItem(context.Background(), limited, "anything"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("limited session: want ErrForbidden, got %v", err)
	}
}

// --- custody lists and history ---------------------------------------------

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

func TestCustodyLists(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := insertTestProfile(t, db, true, "pw-list-admin")
	student := insertTestProfile(t, db, false, "pw-list-student")

	late, _ := insertScannableAsset(t, db, student, StatusAvailable)
	openCustody(t, db, late, student, admin, time.Now().Add(-50*time.Hour))
	soonDue, _ := insertScannableAsset(t, db, student, StatusAvailable)
	openCustody(t, db, soonDue, student, admin, time.Now().Add(24*time.Hour))

	active, err := db.ListActiveCustody(ctx, actorFor(admin))
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	byAsset := map[string]CustodyRecord{}
	for _, r := range active {
		byAsset[r.AssetID] = r
	}
	if _, ok := byAsset[late]; !ok {
		t.Fatal("active list is missing the overdue item")
	}
	if _, ok := byAsset[soonDue]; !ok {
		t.Fatal("active list is missing the not-yet-due item")
	}
	// Names, not raw ids: these lists are what an admin reads.
	if got := byAsset[late].CustodianName; got != "Test User" {
		t.Fatalf("custodian name: %q", got)
	}
	if got := byAsset[late].CheckedOutByName; got != "Test User" {
		t.Fatalf("checked_out_by name: %q", got)
	}
	if !byAsset[late].Overdue || byAsset[late].DaysOverdue != 2 {
		t.Fatalf("overdue flags: overdue=%v days=%d, want true / 2",
			byAsset[late].Overdue, byAsset[late].DaysOverdue)
	}
	if byAsset[soonDue].Overdue || byAsset[soonDue].DaysOverdue != 0 {
		t.Fatalf("a future due date must not read as overdue: %+v", byAsset[soonDue])
	}

	overdue, err := db.ListOverdueCustody(ctx, actorFor(admin))
	if err != nil {
		t.Fatalf("overdue: %v", err)
	}
	seen := map[string]bool{}
	for _, r := range overdue {
		seen[r.AssetID] = true
	}
	if !seen[late] {
		t.Fatal("overdue list is missing the late item")
	}
	if seen[soonDue] {
		t.Fatal("overdue list includes an item that is not due yet")
	}

	// A return drops it from both lists, however late it was.
	if _, err := db.CheckInAsset(ctx, actorFor(admin), late, nil); err != nil {
		t.Fatal(err)
	}
	overdue, err = db.ListOverdueCustody(ctx, actorFor(admin))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range overdue {
		if r.AssetID == late {
			t.Fatal("a returned item is still on the overdue list")
		}
	}
}

func TestGetAssetHistory(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := insertTestProfile(t, db, true, "pw-hist-admin")
	student := insertTestProfile(t, db, false, "pw-hist-student")
	asset, _ := insertScannableAsset(t, db, admin, StatusAvailable)

	// Two trips: one closed with a damage note, one still open.
	openCustody(t, db, asset, student, admin, time.Now().Add(-72*time.Hour))
	note := "scratched hood"
	if _, err := db.CheckInAsset(ctx, actorFor(admin), asset, &note); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CheckOutAssets(ctx, actorFor(admin), CheckoutInput{
		CustodianID: student.ID, AssetIDs: []string{asset}, DueAt: soon(),
	}); err != nil {
		t.Fatal(err)
	}

	// The trail is admin-only, unlike the current holder that GetAsset shows
	// everyone (CLAUDE.md §7, 2026-09-12).
	if _, err := db.GetAssetHistory(ctx, actorFor(student), asset); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin: want ErrForbidden, got %v", err)
	}

	history, err := db.GetAssetHistory(ctx, actorFor(admin), asset)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("want 2 records, got %d", len(history))
	}
	// Newest first.
	if history[0].CheckedOutAt.Before(history[1].CheckedOutAt) {
		t.Fatal("history is not newest-first")
	}
	if history[0].CheckedInAt != nil {
		t.Fatal("the open trip should still be open")
	}
	closed := history[1]
	if closed.CheckedInAt == nil || closed.CheckedInByName == nil || *closed.CheckedInByName != "Test User" {
		t.Fatalf("closed trip: %+v", closed)
	}
	if closed.ConditionIn == nil || *closed.ConditionIn != note {
		t.Fatalf("damage note missing from history: %+v", closed.ConditionIn)
	}
	// Returned three days late: the record survives the return, even though
	// the item is no longer "overdue".
	if closed.Overdue {
		t.Fatal("a returned item must not read as overdue")
	}
	if closed.DaysOverdue != 3 {
		t.Fatalf("days overdue: got %d, want 3", closed.DaysOverdue)
	}

	if _, err := db.GetAssetHistory(ctx, actorFor(admin),
		"00000000-0000-0000-0000-0000000009ff"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown asset: want ErrNotFound, got %v", err)
	}
}

func TestGetUserHistory(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := insertTestProfile(t, db, true, "pw-uhist-admin")
	student := insertTestProfile(t, db, false, "pw-uhist-student")
	other := insertTestProfile(t, db, false, "pw-uhist-other")
	asset, _ := insertScannableAsset(t, db, admin, StatusAvailable)
	openCustody(t, db, asset, student, admin, time.Now().Add(24*time.Hour))

	mine, err := db.GetUserHistory(ctx, actorFor(student), student.ID)
	if err != nil {
		t.Fatalf("own history: %v", err)
	}
	if len(mine) != 1 || mine[0].AssetID != asset {
		t.Fatalf("own history: %+v", mine)
	}

	if _, err := db.GetUserHistory(ctx, actorFor(other), student.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("another user's history: want ErrForbidden, got %v", err)
	}
	theirs, err := db.GetUserHistory(ctx, actorFor(admin), student.ID)
	if err != nil {
		t.Fatalf("admin reading a user's history: %v", err)
	}
	if len(theirs) != 1 {
		t.Fatalf("admin history: %+v", theirs)
	}

	if _, err := db.GetUserHistory(ctx, actorFor(admin),
		"00000000-0000-0000-0000-0000000009ff"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown user: want ErrNotFound, got %v", err)
	}

	limited := actorFor(student)
	limited.Limited = true
	if _, err := db.GetUserHistory(ctx, limited, student.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("limited session: want ErrForbidden, got %v", err)
	}
}
