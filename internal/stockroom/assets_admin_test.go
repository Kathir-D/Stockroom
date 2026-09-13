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

// Phase 5's asset writes against the live database. The rules worth pinning
// are the ones a UI cannot be trusted with: who may call these at all, what a
// delete does to a custody trail, and that a status toggle can never reach
// into someone's bag.

// adminAndAssetTag is the fixture every case here starts from: an admin actor
// and a tag no other row is using.
func adminAndAssetTag(t *testing.T, db *DB) (Actor, string) {
	t.Helper()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	return admin, fmt.Sprintf("ADM-%09d", rand.IntN(1_000_000_000))
}

func TestAssetWritesAreAdminOnly(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin, tag := adminAndAssetTag(t, db)
	asset, err := db.CreateAsset(ctx, admin, AssetInput{AssetTag: tag, Name: "Admin only"})
	if err != nil {
		t.Fatalf("CreateAsset: %v", err)
	}

	student := actorFor(insertTestProfile(t, db, false, "student-pw"))
	// A limited session belongs to an account that hasn't set a password yet;
	// RequireAdmin refuses it even when the profile does carry the flag.
	limitedAdmin := actorFor(insertTestProfile(t, db, true, ""))
	limitedAdmin.Limited = true

	for _, actor := range []struct {
		name  string
		actor Actor
	}{{"non-admin", student}, {"limited admin", limitedAdmin}} {
		t.Run(actor.name, func(t *testing.T) {
			calls := map[string]error{}
			_, calls["CreateAsset"] = db.CreateAsset(ctx, actor.actor, AssetInput{AssetTag: tag + "-x", Name: "No"})
			_, calls["UpdateAsset"] = db.UpdateAsset(ctx, actor.actor, asset.ID, AssetInput{AssetTag: tag, Name: "No"})
			calls["DeleteAsset"] = db.DeleteAsset(ctx, actor.actor, asset.ID)
			_, calls["SetAssetStatus"] = db.SetAssetStatus(ctx, actor.actor, asset.ID, StatusUnavailable)
			_, calls["SetAssetPhoto"] = db.SetAssetPhoto(ctx, actor.actor, asset.ID, "x.png", strings.NewReader("png"))
			for name, err := range calls {
				if !errors.Is(err, ErrForbidden) {
					t.Errorf("%s = %v, want ErrForbidden", name, err)
				}
			}
		})
	}
}

func TestCreateAsset(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin, tag := adminAndAssetTag(t, db)

	// The category is read back as a path, so the row the admin panel gets
	// back is the row the browse list would show.
	category := insertTestCategory(t, db, "Create Asset Model "+tag, nil)

	blank, price := "   ", 1299.99
	serial := "  " + tag + "-SER  "
	asset, err := db.CreateAsset(ctx, admin, AssetInput{
		AssetTag:      "  " + tag + "  ",
		Name:          "  Canon EOS R5  ",
		Description:   &blank,
		CategoryID:    &category.ID,
		SerialNumber:  &serial,
		PurchaseDate:  strptr("2026-09-13"),
		PurchasePrice: &price,
	})
	if err != nil {
		t.Fatalf("CreateAsset: %v", err)
	}

	if asset.AssetTag != tag || asset.Name != "Canon EOS R5" {
		t.Errorf("fields were not trimmed: tag %q name %q", asset.AssetTag, asset.Name)
	}
	if asset.SerialNumber == nil || *asset.SerialNumber != tag+"-SER" {
		t.Errorf("serial = %v, want it trimmed", asset.SerialNumber)
	}
	// A blank optional has to land as null, not "": the serial is unique, and
	// a description of "" is a lie the detail popup would render.
	if asset.Description != nil {
		t.Errorf("blank description = %v, want null", *asset.Description)
	}
	if asset.Status != StatusAvailable {
		t.Errorf("status = %q, want available", asset.Status)
	}
	if asset.CreatedBy == nil || *asset.CreatedBy != admin.ID {
		t.Errorf("created_by = %v, want the admin who added it", asset.CreatedBy)
	}
	if asset.Custody != nil {
		t.Errorf("a new asset is in nobody's hands, got %+v", asset.Custody)
	}
	if len(asset.CategoryPath) != 1 || asset.CategoryPath[0].ID != category.ID {
		t.Errorf("category_path = %+v, want the one node it was filed under", asset.CategoryPath)
	}
	// A date picker sends a plain date; the API hands back a timestamp. Both
	// have to be accepted, which is why the field is a string on the way in.
	if asset.PurchaseDate == nil || asset.PurchaseDate.Format("2006-01-02") != "2026-09-13" {
		t.Errorf("purchase_date = %v, want 2026-09-13", asset.PurchaseDate)
	}
	if _, err := db.CreateAsset(ctx, admin, AssetInput{
		AssetTag:     tag + "-2",
		Name:         "Round trip",
		PurchaseDate: strptr(asset.PurchaseDate.Format(time.RFC3339)),
	}); err != nil {
		t.Errorf("CreateAsset with the timestamp the API returned: %v", err)
	}
}

func strptr(s string) *string { return &s }
