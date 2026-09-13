package stockroom

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
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
			_, calls["SetAssetPhoto"] = db.SetAssetPhoto(ctx, actor.actor, asset.ID, "x.png", strings.NewReader("png"), t.TempDir())
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

func TestCreateAssetRejects(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin, tag := adminAndAssetTag(t, db)

	serial := tag + "-SER"
	if _, err := db.CreateAsset(ctx, admin, AssetInput{AssetTag: tag, Name: "First", SerialNumber: &serial}); err != nil {
		t.Fatalf("CreateAsset: %v", err)
	}
	price := -1.0
	unknown := "0f3d3a9e-0000-4000-8000-000000000000"

	cases := []struct {
		name   string
		in     AssetInput
		want   error
		substr string
	}{
		{"no tag", AssetInput{Name: "Nameless tag"}, ErrInvalid, "asset tag"},
		{"no name", AssetInput{AssetTag: tag + "-a"}, ErrInvalid, "name is required"},
		{"duplicate tag", AssetInput{AssetTag: tag, Name: "Second"}, ErrConflict, "asset tag already in use"},
		{"duplicate serial", AssetInput{AssetTag: tag + "-b", Name: "Second", SerialNumber: &serial}, ErrConflict, "serial number already in use"},
		{"unknown category", AssetInput{AssetTag: tag + "-c", Name: "Orphan", CategoryID: &unknown}, ErrNotFound, "category"},
		{"negative price", AssetInput{AssetTag: tag + "-d", Name: "Free", PurchasePrice: &price}, ErrInvalid, "negative"},
		{"unparseable date", AssetInput{AssetTag: tag + "-e", Name: "Someday", PurchaseDate: strptr("last tuesday")}, ErrInvalid, "date"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := db.CreateAsset(ctx, admin, c.in)
			if !errors.Is(err, c.want) {
				t.Fatalf("CreateAsset = %v, want %v", err, c.want)
			}
			if !strings.Contains(err.Error(), c.substr) {
				t.Errorf("error = %q, want it to mention %q", err, c.substr)
			}
		})
	}
}

// An edit must never move an item between hands. A unit that is out stays out,
// with its custody row untouched, however the form is filled in.
func TestUpdateAssetLeavesCustodyAlone(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	adminP := insertTestProfile(t, db, true, "admin-pw")
	admin := actorFor(adminP)
	student := insertTestProfile(t, db, false, "student-pw")

	id := insertTestAsset(t, db, adminP)
	openCustody(t, db, id, student, adminP, time.Now().Add(24*time.Hour))

	before, err := db.GetAsset(ctx, admin, id)
	if err != nil {
		t.Fatal(err)
	}
	after, err := db.UpdateAsset(ctx, admin, id, AssetInput{
		AssetTag:  before.AssetTag,
		Name:      "Renamed while out",
		Condition: strptr("scuffed"),
	})
	if err != nil {
		t.Fatalf("UpdateAsset: %v", err)
	}
	if after.Name != "Renamed while out" || after.Condition == nil || *after.Condition != "scuffed" {
		t.Errorf("edit did not land: %+v", after)
	}
	if after.Status != StatusCheckedOut {
		t.Errorf("status = %q, want it untouched at checked_out", after.Status)
	}
	if after.Custody == nil || after.Custody.CustodyEventID != before.Custody.CustodyEventID {
		t.Errorf("custody changed under an edit: %+v", after.Custody)
	}

	// Clearing an optional is a real edit, not a no-op: a serial typed into
	// the wrong row has to be removable so the right row can take it.
	cleared, err := db.UpdateAsset(ctx, admin, id, AssetInput{
		AssetTag:     before.AssetTag,
		Name:         "Renamed while out",
		SerialNumber: strptr("  "),
	})
	if err != nil {
		t.Fatalf("UpdateAsset: %v", err)
	}
	if cleared.SerialNumber != nil {
		t.Errorf("serial = %v, want null after a blank edit", *cleared.SerialNumber)
	}

	if _, err := db.UpdateAsset(ctx, admin, "0f3d3a9e-0000-4000-8000-000000000000",
		AssetInput{AssetTag: "nope", Name: "nope"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateAsset on an unknown id = %v, want ErrNotFound", err)
	}
}

// Delete is for a mistyped row. custody_events cascades from assets, so
// anything with a trail is refused rather than quietly taking the trail with
// it.
func TestDeleteAsset(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	adminP := insertTestProfile(t, db, true, "admin-pw")
	admin := actorFor(adminP)
	student := insertTestProfile(t, db, false, "student-pw")

	t.Run("a unit nobody has ever held", func(t *testing.T) {
		id := insertTestAsset(t, db, adminP)
		if err := db.DeleteAsset(ctx, admin, id); err != nil {
			t.Fatalf("DeleteAsset: %v", err)
		}
		if _, err := db.GetAsset(ctx, admin, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("the asset is still there: %v", err)
		}
	})

	t.Run("checked out", func(t *testing.T) {
		id := insertTestAsset(t, db, adminP)
		openCustody(t, db, id, student, adminP, time.Now().Add(24*time.Hour))
		err := db.DeleteAsset(ctx, admin, id)
		if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "checked out") {
			t.Errorf("DeleteAsset on a checked-out item = %v, want a conflict naming it", err)
		}
	})

	t.Run("returned, but with a trail", func(t *testing.T) {
		id := insertTestAsset(t, db, adminP)
		openCustody(t, db, id, student, adminP, time.Now().Add(24*time.Hour))
		if _, err := db.CheckInAsset(ctx, admin, id, nil); err != nil {
			t.Fatal(err)
		}
		err := db.DeleteAsset(ctx, admin, id)
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("DeleteAsset on an item with history = %v, want ErrConflict", err)
		}
		if !strings.Contains(err.Error(), "unavailable") {
			t.Errorf("error = %q, want it to point at the unavailable toggle instead", err)
		}
		var events int
		if err := db.Pool.QueryRow(ctx,
			`select count(*) from custody_events where asset_id = $1`, id).Scan(&events); err != nil {
			t.Fatal(err)
		}
		if events != 1 {
			t.Errorf("custody rows = %d, want the trail intact", events)
		}
	})

	t.Run("unknown id", func(t *testing.T) {
		if err := db.DeleteAsset(ctx, admin, "0f3d3a9e-0000-4000-8000-000000000000"); !errors.Is(err, ErrNotFound) {
			t.Errorf("DeleteAsset on an unknown id = %v, want ErrNotFound", err)
		}
	})
}

func TestSetAssetStatus(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	adminP := insertTestProfile(t, db, true, "admin-pw")
	admin := actorFor(adminP)
	student := insertTestProfile(t, db, false, "student-pw")

	t.Run("toggles both ways", func(t *testing.T) {
		id := insertTestAsset(t, db, adminP)
		out, err := db.SetAssetStatus(ctx, admin, id, StatusUnavailable)
		if err != nil || out.Status != StatusUnavailable {
			t.Fatalf("SetAssetStatus(unavailable) = %q, %v", out.Status, err)
		}
		back, err := db.SetAssetStatus(ctx, admin, id, StatusAvailable)
		if err != nil || back.Status != StatusAvailable {
			t.Fatalf("SetAssetStatus(available) = %q, %v", back.Status, err)
		}
	})

	t.Run("refuses to touch an item someone is holding", func(t *testing.T) {
		id := insertTestAsset(t, db, adminP)
		openCustody(t, db, id, student, adminP, time.Now().Add(24*time.Hour))
		_, err := db.SetAssetStatus(ctx, admin, id, StatusUnavailable)
		if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "checked out") {
			t.Errorf("SetAssetStatus on a held item = %v, want a conflict", err)
		}
	})

	// The open custody row decides, not the column. A column left at
	// checked_out with no row behind it is drift, and this is how an admin
	// clears it.
	t.Run("repairs a drifted status", func(t *testing.T) {
		id := insertTestAsset(t, db, adminP)
		if _, err := db.Pool.Exec(ctx, `update assets set status = 'checked_out' where id = $1`, id); err != nil {
			t.Fatal(err)
		}
		fixed, err := db.SetAssetStatus(ctx, admin, id, StatusAvailable)
		if err != nil || fixed.Status != StatusAvailable {
			t.Fatalf("SetAssetStatus on a drifted row = %q, %v", fixed.Status, err)
		}
	})

	t.Run("checked_out is not a value an admin may set", func(t *testing.T) {
		id := insertTestAsset(t, db, adminP)
		for _, status := range []AssetStatus{StatusCheckedOut, StatusRetired, "", "banana"} {
			_, err := db.SetAssetStatus(ctx, admin, id, status)
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("SetAssetStatus(%q) = %v, want ErrInvalid", status, err)
			}
		}
	})

	t.Run("unknown id", func(t *testing.T) {
		_, err := db.SetAssetStatus(ctx, admin, "0f3d3a9e-0000-4000-8000-000000000000", StatusUnavailable)
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("SetAssetStatus on an unknown id = %v, want ErrNotFound", err)
		}
	})
}

func TestSetAssetPhoto(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	adminP := insertTestProfile(t, db, true, "admin-pw")
	admin := actorFor(adminP)
	uploads := t.TempDir()
	id := insertTestAsset(t, db, adminP)

	asset, err := db.SetAssetPhoto(ctx, admin, id, "IMG_0042.JPG", strings.NewReader("jpeg bytes"), uploads)
	if err != nil {
		t.Fatalf("SetAssetPhoto: %v", err)
	}
	// The file is named after the id, so renaming the asset tag later cannot
	// orphan it.
	if asset.PhotoPath == nil || *asset.PhotoPath != "assets/"+id+".jpg" {
		t.Fatalf("photo_path = %v, want assets/%s.jpg", asset.PhotoPath, id)
	}
	if asset.PhotoURL == nil || *asset.PhotoURL != FilesPrefix+"assets/"+id+".jpg" {
		t.Errorf("photo_url = %v, want it under %s", asset.PhotoURL, FilesPrefix)
	}
	if b, err := os.ReadFile(filepath.Join(uploads, "assets", id+".jpg")); err != nil || string(b) != "jpeg bytes" {
		t.Errorf("stored file = %q, %v", b, err)
	}

	// Re-uploading in another format replaces the copy rather than leaving
	// the old file behind with nothing pointing at it.
	if _, err := db.SetAssetPhoto(ctx, admin, id, "replacement.png", strings.NewReader("png bytes"), uploads); err != nil {
		t.Fatalf("SetAssetPhoto: %v", err)
	}
	if _, err := os.Stat(filepath.Join(uploads, "assets", id+".jpg")); !os.IsNotExist(err) {
		t.Errorf("the old .jpg is still on disk: %v", err)
	}

	// /files/ has no session in front of it and http.FileServer types the
	// response from the extension, so an .html or .svg upload would run as a
	// page on the app's own origin.
	for _, name := range []string{"payload.html", "payload.svg", "notes.txt", "noextension"} {
		if _, err := db.SetAssetPhoto(ctx, admin, id, name, strings.NewReader("x"), uploads); !errors.Is(err, ErrInvalid) {
			t.Errorf("SetAssetPhoto(%q) = %v, want ErrInvalid", name, err)
		}
	}

	t.Run("unknown asset writes nothing", func(t *testing.T) {
		empty := t.TempDir()
		_, err := db.SetAssetPhoto(ctx, admin, "0f3d3a9e-0000-4000-8000-000000000000", "x.png", strings.NewReader("x"), empty)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("SetAssetPhoto on an unknown id = %v, want ErrNotFound", err)
		}
		if entries, _ := os.ReadDir(empty); len(entries) != 0 {
			t.Errorf("a file was written for an asset that does not exist: %v", entries)
		}
	})

	t.Run("no uploads dir configured", func(t *testing.T) {
		_, err := db.SetAssetPhoto(ctx, admin, id, "x.png", strings.NewReader("x"), "")
		if !errors.Is(err, ErrNotConfigured) {
			t.Errorf("SetAssetPhoto with no UPLOADS_DIR = %v, want ErrNotConfigured", err)
		}
	})
}

func strptr(s string) *string { return &s }
