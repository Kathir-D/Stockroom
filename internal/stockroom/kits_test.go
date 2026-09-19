package stockroom

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

// Phase 8, kits. The rule for this suite is one happy path per module plus its
// permission gate (TESTING.md), and kits add two rules of their own that are
// worth pinning: a unit belongs to one kit, and a kit return is per unit rather
// than all-or-nothing.

// insertTestKit creates a kit with a name nothing else uses and deletes it when
// the test ends. kit_items cascades, so one delete clears the membership too.
func insertTestKit(t *testing.T, db *DB, name string) string {
	t.Helper()
	ctx := context.Background()
	full := fmt.Sprintf("%s %09d", name, rand.IntN(1_000_000_000))
	var id string
	if err := db.Pool.QueryRow(ctx,
		`insert into kits (name) values ($1) returning id`, full).Scan(&id); err != nil {
		t.Fatalf("insert test kit: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Pool.Exec(ctx, `delete from kits where id = $1`, id) })
	return id
}

func TestKitWritesRefuseAStudent(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := insertTestProfile(t, db, true, "pw-kit-admin")
	student := insertTestProfile(t, db, false, "pw-kit-student")
	actor := actorFor(student)
	kitID := insertTestKit(t, db, "Gate kit")
	assetID := insertTestAsset(t, db, admin)

	if _, err := db.CreateKit(ctx, actor, KitInput{Name: "Nope"}); !errors.Is(err, ErrForbidden) {
		t.Errorf("CreateKit: want ErrForbidden, got %v", err)
	}
	if _, err := db.UpdateKit(ctx, actor, kitID, KitInput{Name: "Nope"}); !errors.Is(err, ErrForbidden) {
		t.Errorf("UpdateKit: want ErrForbidden, got %v", err)
	}
	if err := db.DeleteKit(ctx, actor, kitID); !errors.Is(err, ErrForbidden) {
		t.Errorf("DeleteKit: want ErrForbidden, got %v", err)
	}
	if _, err := db.AddAssetToKit(ctx, actor, kitID, assetID); !errors.Is(err, ErrForbidden) {
		t.Errorf("AddAssetToKit: want ErrForbidden, got %v", err)
	}
	if _, err := db.RemoveAssetFromKit(ctx, actor, kitID, assetID); !errors.Is(err, ErrForbidden) {
		t.Errorf("RemoveAssetFromKit: want ErrForbidden, got %v", err)
	}

	// Reading one is not a write: a student has to see what is in a kit to
	// decide to take it (CLAUDE.md §7).
	if _, err := db.GetKit(ctx, actor, kitID); err != nil {
		t.Errorf("GetKit as a student: %v", err)
	}
	if _, err := db.ListKits(ctx, actor); err != nil {
		t.Errorf("ListKits as a student: %v", err)
	}
}

// The whole admin path: build a kit, read it back with its units, and take one
// out again.
func TestCreateKitAddAndRemoveItems(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := insertTestProfile(t, db, true, "pw-kit-build")
	actor := actorFor(admin)
	a1, _ := insertScannableAsset(t, db, admin, StatusAvailable)
	a2, _ := insertScannableAsset(t, db, admin, StatusAvailable)

	name := fmt.Sprintf("Kit build %09d", rand.IntN(1_000_000_000))
	kit, err := db.CreateKit(ctx, actor, KitInput{Name: name})
	if err != nil {
		t.Fatalf("create kit: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Pool.Exec(ctx, `delete from kits where id = $1`, kit.ID) })
	if len(kit.Items) != 0 || kit.Checkable {
		t.Fatalf("a new kit is empty and not checkable, got %d items, checkable=%v", len(kit.Items), kit.Checkable)
	}

	if _, err := db.AddAssetToKit(ctx, actor, kit.ID, a1); err != nil {
		t.Fatalf("add first item: %v", err)
	}
	kit, err = db.AddAssetToKit(ctx, actor, kit.ID, a2)
	if err != nil {
		t.Fatalf("add second item: %v", err)
	}
	if len(kit.Items) != 2 || kit.Available != 2 || !kit.Checkable {
		t.Fatalf("kit after two adds: %d items, %d available, checkable=%v", len(kit.Items), kit.Available, kit.Checkable)
	}

	// The list read and the single read agree, because both go through
	// kitsWhere.
	kits, err := db.ListKits(ctx, actor)
	if err != nil {
		t.Fatalf("list kits: %v", err)
	}
	var found *KitDetail
	for i := range kits {
		if kits[i].ID == kit.ID {
			found = &kits[i]
		}
	}
	if found == nil {
		t.Fatal("the new kit is missing from ListKits")
	}
	if len(found.Items) != 2 {
		t.Fatalf("ListKits carries %d items for the kit, want 2", len(found.Items))
	}

	kit, err = db.RemoveAssetFromKit(ctx, actor, kit.ID, a1)
	if err != nil {
		t.Fatalf("remove item: %v", err)
	}
	if len(kit.Items) != 1 || kit.Items[0].ID != a2 {
		t.Fatalf("after removing one: %+v", kit.Items)
	}
	// Removing it twice is ErrNotFound, not a silent success: the two look
	// identical afterwards and only one of them is what the press meant.
	if _, err := db.RemoveAssetFromKit(ctx, actor, kit.ID, a1); !errors.Is(err, ErrNotFound) {
		t.Errorf("removing a non-member: want ErrNotFound, got %v", err)
	}

	// Deleting the kit leaves the unit, its status and its history alone.
	if err := db.DeleteKit(ctx, actor, kit.ID); err != nil {
		t.Fatalf("delete kit: %v", err)
	}
	if s := assetStatus(t, db, a2); s != StatusAvailable {
		t.Errorf("asset status after its kit was deleted = %s, want available", s)
	}
}

// One kit per asset. The refusal names the kit that already has it, because
// that is the only thing the admin needs to fix it.
func TestAddAssetToKitRefusesASecondKit(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := insertTestProfile(t, db, true, "pw-kit-shared")
	actor := actorFor(admin)
	asset, _ := insertScannableAsset(t, db, admin, StatusAvailable)

	firstID := insertTestKit(t, db, "First kit")
	secondID := insertTestKit(t, db, "Second kit")
	if _, err := db.AddAssetToKit(ctx, actor, firstID, asset); err != nil {
		t.Fatalf("add to the first kit: %v", err)
	}

	_, err := db.AddAssetToKit(ctx, actor, secondID, asset)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("adding to a second kit: want ErrConflict, got %v", err)
	}
	var firstName string
	if err := db.Pool.QueryRow(ctx, `select name from kits where id = $1`, firstID).Scan(&firstName); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(err.Error(), firstName) {
		t.Errorf("the refusal does not name the kit that has the item: %v", err)
	}

	// The same kit twice is its own message, so the admin is not sent looking
	// for another kit that does not exist.
	_, err = db.AddAssetToKit(ctx, actor, firstID, asset)
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "already in this kit") {
		t.Errorf("re-adding to the same kit: got %v", err)
	}
}

// A kit return is per unit: what is out comes back, what was already on the
// shelf is reported rather than refused, and the whole call does not fail
// because one of four units was already returned.
func TestCheckInKitReturnsOnlyWhatIsOut(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := insertTestProfile(t, db, true, "pw-kit-return")
	holder := insertTestProfile(t, db, false, "pw-kit-holder")
	actor := actorFor(admin)
	out, _ := insertScannableAsset(t, db, admin, StatusAvailable)
	in, _ := insertScannableAsset(t, db, admin, StatusAvailable)

	kitID := insertTestKit(t, db, "Return kit")
	for _, id := range []string{out, in} {
		if _, err := db.AddAssetToKit(ctx, actor, kitID, id); err != nil {
			t.Fatalf("add %s: %v", id, err)
		}
	}
	openCustody(t, db, out, holder, holder, soon())

	// A kit with a unit in somebody's bag cannot go into a cart whole.
	kit, err := db.GetKit(ctx, actor, kitID)
	if err != nil {
		t.Fatalf("get kit: %v", err)
	}
	if kit.Checkable || kit.CheckedOut != 1 || kit.Available != 1 {
		t.Fatalf("kit with one unit out: checkable=%v, out=%d, available=%d",
			kit.Checkable, kit.CheckedOut, kit.Available)
	}

	// Any signed-in user may return a kit, exactly as with a single item.
	result, err := db.CheckInKit(ctx, actorFor(holder), kitID)
	if err != nil {
		t.Fatalf("check in kit: %v", err)
	}
	if len(result.Returned) != 1 || result.Returned[0].Asset.ID != out {
		t.Fatalf("returned = %+v, want just the unit that was out", result.Returned)
	}
	if len(result.AlreadyIn) != 1 || result.AlreadyIn[0].AssetID != in {
		t.Fatalf("already_in = %+v, want the unit that never left", result.AlreadyIn)
	}
	if len(result.Failed) != 0 {
		t.Fatalf("failed = %+v, want none", result.Failed)
	}
	if s := assetStatus(t, db, out); s != StatusAvailable {
		t.Errorf("returned unit status = %s, want available", s)
	}
	if _, open := countCustody(t, db, out); open != 0 {
		t.Errorf("returned unit still has %d open custody rows", open)
	}

	// Running it again is not an error either: everything is simply already in.
	result, err = db.CheckInKit(ctx, actorFor(holder), kitID)
	if err != nil {
		t.Fatalf("second check in: %v", err)
	}
	if len(result.Returned) != 0 || len(result.AlreadyIn) != 2 {
		t.Fatalf("second check in: returned=%d already_in=%d", len(result.Returned), len(result.AlreadyIn))
	}
}
