package stockroom

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// An archived account keeps its history, cannot sign in, loses its open
// sessions and cannot be a custodian; restoring it undoes all of that
// (ROADMAP §3.4).
func TestArchivedAccount(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := insertTestProfile(t, db, true, "admin-password")
	student := insertTestProfile(t, db, false, "student-password")
	asset := insertTestAsset(t, db, admin)

	// A past loan: the history that makes the account undeletable.
	openCustody(t, db, asset, student, admin, time.Now().Add(time.Hour))
	if _, err := db.SetUserArchived(ctx, actorFor(admin), student.ID, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("archive while holding an item = %v, want ErrConflict", err)
	}
	if _, err := db.CheckInAsset(ctx, actorFor(admin), asset, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteUser(ctx, actorFor(admin), student.ID); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "Archive") {
		t.Errorf("delete with history = %v, want a conflict that suggests archiving", err)
	}

	res, err := db.LoginByScan(ctx, *student.StudentNumber)
	if err != nil {
		t.Fatal(err)
	}
	p, err := db.SetUserArchived(ctx, actorFor(admin), student.ID, true)
	if err != nil || p.ArchivedAt == nil {
		t.Fatalf("archive = %+v, %v", p, err)
	}
	if _, err := db.Resolve(ctx, res.Token); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("session after archiving = %v, want ErrUnauthorized", err)
	}
	if _, err := db.LoginByScan(ctx, *student.StudentNumber); !errors.Is(err, ErrForbidden) {
		t.Errorf("scan by archived account = %v, want ErrForbidden", err)
	}
	if _, err := db.LoginByPassword(ctx, *student.StudentNumber, "student-password"); !errors.Is(err, ErrForbidden) {
		t.Errorf("password by archived account = %v, want ErrForbidden", err)
	}
	other := insertTestAsset(t, db, admin)
	_, err = db.CheckOutAssets(ctx, actorFor(admin), CheckoutInput{CustodianID: student.ID, AssetIDs: []string{other}, DueAt: soon()})
	if !errors.Is(err, ErrConflict) {
		t.Errorf("checkout to archived account = %v, want ErrConflict", err)
	}
	var history int
	if err := db.Pool.QueryRow(ctx, `select count(*) from custody_events where custodian_id = $1`, student.ID).Scan(&history); err != nil || history != 1 {
		t.Errorf("custody history after archiving = %d, %v; want 1", history, err)
	}
	if _, err := db.SetUserArchived(ctx, actorFor(admin), admin.ID, true); !errors.Is(err, ErrConflict) {
		t.Errorf("archive yourself = %v, want ErrConflict", err)
	}

	if _, err := db.SetUserArchived(ctx, actorFor(admin), student.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := db.LoginByScan(ctx, *student.StudentNumber); err != nil {
		t.Errorf("scan after restoring = %v, want nil", err)
	}
}

// A roster import asked to archive the missing archives every active student
// the file does not name, leaves admins and anyone holding an item alone,
// and a later file naming an archived student brings them back.
func TestRosterArchivesMissingStudents(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	db.UploadsDir = t.TempDir()
	admin := insertTestProfile(t, db, true, "admin-password")
	stays := insertTestProfile(t, db, false, "")
	leaves := insertTestProfile(t, db, false, "")
	holding := insertTestProfile(t, db, false, "")
	openCustody(t, db, insertTestAsset(t, db, admin), holding, admin, time.Now().Add(time.Hour))

	// Every other student in the shared database is "missing" from this
	// file too, so put back whatever the import archives.
	var before []string
	rows, err := db.Pool.Query(ctx, `select id::text from profiles where archived_at is null`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		before = append(before, id)
	}
	rows.Close()
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `update profiles set archived_at = null where id = any($1::uuid[])`, before)
	})

	file := "first_name,last_name,student_number\nStays,Here," + *stays.StudentNumber + "\n"
	res, err := db.ImportRosterWith(ctx, actorFor(admin), strings.NewReader(file), RosterOptions{ArchiveMissing: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Archived == 0 || res.ArchiveRefused != "" {
		t.Fatalf("result = %+v, want some archived", res)
	}
	archived := func(p Profile) bool {
		got, err := db.profileByID(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.ArchivedAt != nil
	}
	if archived(stays) || !archived(leaves) || archived(holding) || archived(admin) {
		t.Errorf("archived: stays=%v leaves=%v holding=%v admin=%v; want false true false false",
			archived(stays), archived(leaves), archived(holding), archived(admin))
	}
	found := false
	for _, n := range res.ArchiveSkipped {
		found = found || n == "Test User"
	}
	if !found {
		t.Errorf("archive_skipped = %v, want the student holding an item", res.ArchiveSkipped)
	}

	// A file with a bad row archives nobody.
	bad := "first_name,last_name,student_number\nStays,Here," + *stays.StudentNumber + "\nBad,Row,not-a-number\n"
	res, err = db.ImportRosterWith(ctx, actorFor(admin), strings.NewReader(bad), RosterOptions{ArchiveMissing: true})
	if err != nil || res.Archived != 0 || res.ArchiveRefused == "" {
		t.Errorf("import with a failed row = %+v, %v; want nobody archived and a reason", res, err)
	}

	back := "first_name,last_name,student_number\nLeaves,Back," + *leaves.StudentNumber + "\n"
	if _, err := db.ImportRoster(ctx, actorFor(admin), strings.NewReader(back), ""); err != nil {
		t.Fatal(err)
	}
	if archived(leaves) {
		t.Error("a roster naming an archived student did not bring them back")
	}
}

// A retired item keeps its history, leaves browse and its kit, cannot be
// made available or put in a kit, and comes back available (ROADMAP §3.4).
func TestRetiredAsset(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := insertTestProfile(t, db, true, "admin-password")
	student := insertTestProfile(t, db, false, "student-password")
	id, serial := insertScannableAsset(t, db, admin, StatusAvailable)
	kit, err := db.CreateKit(ctx, actorFor(admin), KitInput{Name: "Retire test kit " + serial})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Pool.Exec(ctx, `delete from kits where id = $1`, kit.ID) })
	if _, err := db.AddAssetToKit(ctx, actorFor(admin), kit.ID, id); err != nil {
		t.Fatal(err)
	}

	openCustody(t, db, id, student, admin, time.Now().Add(time.Hour))
	if _, err := db.SetAssetRetired(ctx, actorFor(admin), id, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("retire while out = %v, want ErrConflict", err)
	}
	if _, err := db.CheckInAsset(ctx, actorFor(admin), id, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteAsset(ctx, actorFor(admin), id); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "Retire") {
		t.Errorf("delete with history = %v, want a conflict that suggests retiring", err)
	}

	a, err := db.SetAssetRetired(ctx, actorFor(admin), id, true)
	if err != nil || a.RetiredAt == nil || a.Status != StatusUnavailable {
		t.Fatalf("retire = %+v, %v", a, err)
	}
	list, err := db.ListAssets(ctx, actorFor(student), AssetFilter{Search: serial, IncludeRetired: true})
	if err != nil || len(list) != 0 {
		t.Errorf("student browse found the retired item (%d rows, %v)", len(list), err)
	}
	list, err = db.ListAssets(ctx, actorFor(admin), AssetFilter{Search: serial, IncludeRetired: true})
	if err != nil || len(list) != 1 {
		t.Errorf("admin list with retired = %d rows, %v; want 1", len(list), err)
	}
	var inKit bool
	_ = db.Pool.QueryRow(ctx, `select exists (select 1 from kit_items where asset_id = $1)`, id).Scan(&inKit)
	if inKit {
		t.Error("a retired item is still in its kit")
	}
	if _, err := db.AddAssetToKit(ctx, actorFor(admin), kit.ID, id); !errors.Is(err, ErrConflict) {
		t.Errorf("add retired item to a kit = %v, want ErrConflict", err)
	}
	if _, err := db.SetAssetStatus(ctx, actorFor(admin), id, StatusAvailable); !errors.Is(err, ErrConflict) {
		t.Errorf("make a retired item available = %v, want ErrConflict", err)
	}

	a, err = db.SetAssetRetired(ctx, actorFor(admin), id, false)
	if err != nil || a.RetiredAt != nil || a.Status != StatusAvailable {
		t.Errorf("bring back = %+v, %v", a, err)
	}
}
