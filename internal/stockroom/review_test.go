package stockroom

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// reasonsOf reads a custody event's review reasons straight back.
func reasonsOf(t *testing.T, db *DB, assetID string) (reasons []string, via *string) {
	t.Helper()
	err := db.Pool.QueryRow(context.Background(), `
		select review_reasons, returned_via from custody_events
		 where asset_id = $1 order by checked_out_at desc limit 1`, assetID).Scan(&reasons, &via)
	if err != nil {
		t.Fatal(err)
	}
	return reasons, via
}

// A scan returns an item with nothing to review; a typed serial or a button
// press by a student marks the return, and a damage note flags the item for
// every viewer until an admin clears it (CLAUDE.md §7).
func TestReturnsNeedingReview(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := insertTestProfile(t, db, true, "admin-password")
	student := insertTestProfile(t, db, false, "student-password")
	past := time.Now().Add(-time.Hour)

	scanned, scannedSerial := insertScannableAsset(t, db, admin, StatusAvailable)
	openCustody(t, db, scanned, student, admin, past.Add(48*time.Hour))
	backdate(t, db, scanned)
	if _, err := db.ScanItemVia(ctx, actorFor(student), scannedSerial, true); err != nil {
		t.Fatal(err)
	}
	if r, via := reasonsOf(t, db, scanned); len(r) != 0 || via == nil || *via != ReturnedByScan {
		t.Errorf("scanned return: reasons %v via %v, want none via scan", r, via)
	}

	typed, typedSerial := insertScannableAsset(t, db, admin, StatusAvailable)
	openCustody(t, db, typed, student, admin, past.Add(48*time.Hour))
	backdate(t, db, typed)
	if _, err := db.ScanItemVia(ctx, actorFor(student), typedSerial, false); err != nil {
		t.Fatal(err)
	}
	if r, via := reasonsOf(t, db, typed); !slices.Equal(r, []string{ReviewNotScanned}) || via == nil || *via != ReturnedByTyped {
		t.Errorf("typed return: reasons %v via %v, want not_scanned via typed", r, via)
	}

	damaged := insertTestAsset(t, db, admin)
	openCustody(t, db, damaged, student, admin, past.Add(48*time.Hour))
	note := "cracked lens hood"
	if _, err := db.CheckInAsset(ctx, actorFor(student), damaged, &note); err != nil {
		t.Fatal(err)
	}
	if r, _ := reasonsOf(t, db, damaged); !slices.Equal(r, []string{ReviewDamage, ReviewNotScanned}) {
		t.Errorf("button return with a note: reasons %v", r)
	}
	a, err := db.GetAsset(ctx, actorFor(student), damaged)
	if err != nil || a.DamageReport == nil || *a.DamageReport != note || a.Status != StatusAvailable {
		t.Errorf("damaged item = status %s report %v (%v); want available with the note", a.Status, a.DamageReport, err)
	}

	// An admin's own button return is theirs to vouch for.
	adminReturn := insertTestAsset(t, db, admin)
	openCustody(t, db, adminReturn, student, admin, past.Add(48*time.Hour))
	if _, err := db.CheckInAsset(ctx, actorFor(admin), adminReturn, nil); err != nil {
		t.Fatal(err)
	}
	if r, _ := reasonsOf(t, db, adminReturn); len(r) != 0 {
		t.Errorf("admin's return: reasons %v, want none", r)
	}

	if _, err := db.ListNeedsReview(ctx, actorFor(student)); !errors.Is(err, ErrForbidden) {
		t.Errorf("student reads the review list = %v, want ErrForbidden", err)
	}
	list, err := db.ListNeedsReview(ctx, actorFor(admin))
	if err != nil {
		t.Fatal(err)
	}
	var damagedEvent string
	for _, rec := range list {
		if rec.AssetID == damaged {
			damagedEvent = rec.ID
		}
		if rec.AssetID == scanned || rec.AssetID == adminReturn {
			t.Errorf("review list holds a return that needs nothing: %s", rec.AssetID)
		}
	}
	if damagedEvent == "" {
		t.Fatal("the damaged return is not on the review list")
	}
	if notice := db.adminNoticeFor(ctx, true); notice == nil || !strings.Contains(*notice, "to be checked") {
		t.Errorf("admin notice = %v, want a count of returns to check", notice)
	}
	if _, err := db.ResolveReview(ctx, actorFor(admin), damagedEvent); err != nil {
		t.Fatal(err)
	}
	if a, _ := db.GetAsset(ctx, actorFor(student), damaged); a.DamageReport != nil {
		t.Error("the damage report outlived the admin's review")
	}
	if _, err := db.ResolveReview(ctx, actorFor(admin), damagedEvent); !errors.Is(err, ErrNotFound) {
		t.Errorf("clearing twice = %v, want ErrNotFound", err)
	}
}

// backdate moves an asset's open loan out of the "just checked out" window.
func backdate(t *testing.T, db *DB, assetID string) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), `
		update custody_events set checked_out_at = now() - interval '1 hour'
		 where asset_id = $1 and checked_in_at is null`, assetID); err != nil {
		t.Fatal(err)
	}
}

// Scanning an item you checked out a minute ago asks instead of returning
// it; confirming through the check-in route counts as a scan.
func TestScanJustAfterCheckoutAsks(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	student := insertTestProfile(t, db, false, "student-password")
	other := insertTestProfile(t, db, false, "other-password")
	id, serial := insertScannableAsset(t, db, student, StatusAvailable)
	if _, err := db.CheckOutAssets(ctx, actorFor(student), CheckoutInput{AssetIDs: []string{id}, DueAt: soon()}); err != nil {
		t.Fatal(err)
	}
	res, err := db.ScanItem(ctx, actorFor(student), serial)
	if err != nil || res.Action != ScanConfirmReturn || res.ReturnedFrom == nil {
		t.Fatalf("scan just after checkout = %+v, %v; want confirm_return", res, err)
	}
	if s := assetStatus(t, db, id); s != StatusCheckedOut {
		t.Fatalf("status after the question = %s, want still checked out", s)
	}
	// Somebody else scanning it is a return, as ever.
	res, err = db.ScanItem(ctx, actorFor(other), serial)
	if err != nil || res.Action != ScanCheckedIn {
		t.Fatalf("scan by another student = %+v, %v; want checked_in", res.Action, err)
	}
}

// Marking a lost item closes the loan as lost, takes the item off the shelf
// and ends the borrower's overdue block (CLAUDE.md §7).
func TestMarkAssetLost(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := insertTestProfile(t, db, true, "admin-password")
	student := insertTestProfile(t, db, false, "student-password")
	id := insertTestAsset(t, db, admin)
	if _, err := db.MarkAssetLost(ctx, actorFor(admin), id, nil); !errors.Is(err, ErrConflict) {
		t.Errorf("lost while on the shelf = %v, want ErrConflict", err)
	}
	openCustody(t, db, id, student, admin, time.Now().Add(-time.Hour))
	if overdue, _ := db.hasOverdue(ctx, student.ID); !overdue {
		t.Fatal("fixture: the student should be overdue")
	}
	if _, err := db.MarkAssetLost(ctx, actorFor(student), id, nil); !errors.Is(err, ErrForbidden) {
		t.Errorf("student marks lost = %v, want ErrForbidden", err)
	}
	note := "left on the bus"
	a, err := db.MarkAssetLost(ctx, actorFor(admin), id, &note)
	if err != nil || a.Status != StatusUnavailable || a.Custody != nil {
		t.Fatalf("mark lost = %+v, %v", a, err)
	}
	hist, err := db.GetAssetHistory(ctx, actorFor(admin), id)
	if err != nil || len(hist) != 1 || hist[0].Outcome != "lost" || hist[0].ConditionIn == nil || *hist[0].ConditionIn != note {
		t.Errorf("history after lost = %+v, %v", hist, err)
	}
	if overdue, _ := db.hasOverdue(ctx, student.ID); overdue {
		t.Error("the borrower is still overdue on a lost item")
	}
}

// A typed serial of your own fresh checkout is a deliberate return, not a
// pile scanned twice: it is returned and goes to review rather than asking.
func TestTypedScanJustAfterCheckoutReturns(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	student := insertTestProfile(t, db, false, "student-password")
	id, serial := insertScannableAsset(t, db, student, StatusAvailable)
	if _, err := db.CheckOutAssets(ctx, actorFor(student), CheckoutInput{AssetIDs: []string{id}, DueAt: soon()}); err != nil {
		t.Fatal(err)
	}
	res, err := db.ScanItemVia(ctx, actorFor(student), serial, false)
	if err != nil || res.Action != ScanCheckedIn {
		t.Fatalf("typed scan just after checkout = %+v, %v; want checked_in", res.Action, err)
	}
	if r, _ := reasonsOf(t, db, id); !slices.Equal(r, []string{ReviewNotScanned}) {
		t.Errorf("reasons %v, want not_scanned", r)
	}
}

// A student may add a damage note only to a fresh return: not to one an
// admin already reviewed, not to a lost loan, and not long after. An admin
// may annotate any return.
func TestDamageNoteCannotReopenAReview(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := insertTestProfile(t, db, true, "admin-password")
	student := insertTestProfile(t, db, false, "student-password")

	returned := insertTestAsset(t, db, admin)
	openCustody(t, db, returned, student, admin, time.Now().Add(48*time.Hour))
	in, err := db.CheckInAsset(ctx, actorFor(admin), returned, nil)
	if err != nil {
		t.Fatal(err)
	}
	event := in.ReturnedFrom.CustodyEventID
	if _, err := db.AnnotateCustodyEvent(ctx, actorFor(student), event, "dent on the hood"); err != nil {
		t.Fatalf("note on a fresh return = %v, want nil", err)
	}
	if _, err := db.ResolveReview(ctx, actorFor(admin), event); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AnnotateCustodyEvent(ctx, actorFor(student), event, "still dented"); !errors.Is(err, ErrForbidden) {
		t.Errorf("student reopens a reviewed return = %v, want ErrForbidden", err)
	}
	if _, err := db.Pool.Exec(ctx, `update custody_events set reviewed_at = null, review_reasons = '{}',
		checked_in_at = now() - interval '2 hours' where id = $1`, event); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AnnotateCustodyEvent(ctx, actorFor(student), event, "old dent"); !errors.Is(err, ErrForbidden) {
		t.Errorf("student notes an old return = %v, want ErrForbidden", err)
	}
	if _, err := db.AnnotateCustodyEvent(ctx, actorFor(admin), event, "admin note"); err != nil {
		t.Errorf("admin notes an old return = %v, want nil", err)
	}

	lost := insertTestAsset(t, db, admin)
	openCustody(t, db, lost, student, admin, time.Now().Add(48*time.Hour))
	if _, err := db.MarkAssetLost(ctx, actorFor(admin), lost, nil); err != nil {
		t.Fatal(err)
	}
	hist, err := db.GetAssetHistory(ctx, actorFor(admin), lost)
	if err != nil || len(hist) != 1 {
		t.Fatalf("history = %v, %v", hist, err)
	}
	if _, err := db.AnnotateCustodyEvent(ctx, actorFor(admin), hist[0].ID, "found it?"); !errors.Is(err, ErrConflict) {
		t.Errorf("note on a lost loan = %v, want ErrConflict", err)
	}
}
