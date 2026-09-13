package stockroom

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportRosterRequiresAdmin(t *testing.T) {
	db := requireTestDB(t)
	student := actorFor(insertTestProfile(t, db, false, "student-pw"))
	_, err := db.ImportRoster(context.Background(), student, strings.NewReader("first_name,last_name,student_number\n"), "", t.TempDir())
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("ImportRoster as non-admin = %v, want ErrForbidden", err)
	}
}

func TestImportRosterMalformedFile(t *testing.T) {
	db := requireTestDB(t)
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	for name, body := range map[string]string{
		"empty":          "",
		"missing column": "first_name,last_name\nA,B\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := db.ImportRoster(context.Background(), admin, strings.NewReader(body), "", t.TempDir())
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("ImportRoster = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestImportRosterCreatesUpdatesAndReportsRows(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	existing := insertTestProfile(t, db, true, "keep-me-password") // admin with a password: both must survive
	fresh := testStudentNumber(t, db)

	photoDir := t.TempDir()
	uploads := t.TempDir()
	if err := os.WriteFile(filepath.Join(photoDir, "fresh.JPG"), []byte("jpeg bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Columns in a different order than documented, an extra column, a BOM,
	// a Windows line ending, a blank line, and a mix of good and bad rows.
	csv := "\ufeffstudent_number,photo_path,first_name,last_name,homeroom\r\n" +
		fresh + ",fresh.JPG,New,Student,B12\r\n" +
		*existing.StudentNumber + ",,Updated,Name,B12\r\n" +
		"\r\n" +
		"12ab,,Bad,Number,B12\r\n" +
		"912000001,missing.jpg,No,Photo,B12\r\n" +
		"912000002,,,,B12\r\n"

	res, err := db.ImportRoster(ctx, admin, strings.NewReader(csv), photoDir, uploads)
	if err != nil {
		t.Fatalf("ImportRoster: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `delete from profiles where student_number in ('912000001','912000002')`)
	})

	if res.Created != 1 || res.Updated != 1 || res.Failed != 3 {
		t.Errorf("counts = created %d updated %d failed %d; want 1/1/3 (%+v)", res.Created, res.Updated, res.Failed, res.Rows)
	}
	if len(res.Rows) != 5 {
		t.Fatalf("rows = %d, want 5 (blank line skipped)", len(res.Rows))
	}
	wantActions := []RosterAction{RosterCreated, RosterUpdated, RosterError, RosterError, RosterError}
	wantRowNums := []int{2, 3, 5, 6, 7}
	for i, r := range res.Rows {
		if r.Action != wantActions[i] || r.Row != wantRowNums[i] {
			t.Errorf("row %d = %+v, want action %s at line %d", i, r, wantActions[i], wantRowNums[i])
		}
	}
	if !strings.Contains(res.Rows[2].Error, "digits") {
		t.Errorf("bad number error = %q", res.Rows[2].Error)
	}
	if !strings.Contains(res.Rows[3].Error, "photo") {
		t.Errorf("missing photo error = %q", res.Rows[3].Error)
	}
	if !strings.Contains(res.Rows[4].Error, "name") {
		t.Errorf("no-name error = %q", res.Rows[4].Error)
	}

	// The new account: photo copied under uploads/profiles/<sn>.jpg, path
	// stored relative to uploads, no password, not admin.
	p, err := db.profileByStudentNumber(ctx, fresh)
	if err != nil {
		t.Fatalf("created user missing: %v", err)
	}
	if p.PhotoPath == nil || *p.PhotoPath != "profiles/"+fresh+".jpg" {
		t.Errorf("photo_path = %v, want profiles/%s.jpg", p.PhotoPath, fresh)
	}
	if b, err := os.ReadFile(filepath.Join(uploads, "profiles", fresh+".jpg")); err != nil || string(b) != "jpeg bytes" {
		t.Errorf("photo not copied into uploads: %v", err)
	}
	if p.PasswordHash != nil || p.IsAdmin || *p.FullName != "New Student" {
		t.Errorf("created profile = %+v", p)
	}

	// The existing account: renamed, everything else untouched.
	e, _ := db.profileByID(ctx, existing.ID)
	if *e.FirstName != "Updated" || *e.LastName != "Name" {
		t.Errorf("existing user not renamed: %+v", e)
	}
	if !e.IsAdmin {
		t.Error("import cleared is_admin")
	}
	if err := CheckPassword(e.PasswordHash, "keep-me-password"); err != nil {
		t.Errorf("import touched the password: %v", err)
	}

	// The two failed rows must not have been written.
	for _, sn := range []string{"912000001", "912000002"} {
		if _, err := db.profileByStudentNumber(ctx, sn); !errors.Is(err, ErrNotFound) {
			t.Errorf("failed row %s was written (%v)", sn, err)
		}
	}
}

// A blank photo_path on re-import keeps the photo from the previous import.
func TestImportRosterKeepsPhotoWhenBlank(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	sn := testStudentNumber(t, db)
	photoDir, uploads := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(photoDir, "p.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	header := "first_name,last_name,student_number,photo_path\n"
	if _, err := db.ImportRoster(ctx, admin, strings.NewReader(header+"A,B,"+sn+",p.png\n"), photoDir, uploads); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ImportRoster(ctx, admin, strings.NewReader(header+"A,B,"+sn+",\n"), photoDir, uploads); err != nil {
		t.Fatal(err)
	}
	p, _ := db.profileByStudentNumber(ctx, sn)
	if p.PhotoPath == nil || *p.PhotoPath != "profiles/"+sn+".png" {
		t.Errorf("photo_path = %v, want the first import's photo kept", p.PhotoPath)
	}
}

// A photo named by the roster must have a file extension, and re-importing
// the same student with a different one replaces the stored copy instead of
// leaving the old file orphaned under uploads/profiles.
func TestImportRosterPhotoExtensions(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	sn := testStudentNumber(t, db)
	photoDir, uploads := t.TempDir(), t.TempDir()
	for _, name := range []string{"p.png", "p.jpg", "noext"} {
		if err := os.WriteFile(filepath.Join(photoDir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	header := "first_name,last_name,student_number,photo_path\n"

	if _, err := db.ImportRoster(ctx, admin, strings.NewReader(header+"A,B,"+sn+",p.png\n"), photoDir, uploads); err != nil {
		t.Fatal(err)
	}
	res, err := db.ImportRoster(ctx, admin, strings.NewReader(header+"A,B,"+sn+",p.jpg\n"), photoDir, uploads)
	if err != nil || res.Updated != 1 {
		t.Fatalf("re-import = %+v, %v", res, err)
	}
	p, _ := db.profileByStudentNumber(ctx, sn)
	if p.PhotoPath == nil || *p.PhotoPath != "profiles/"+sn+".jpg" {
		t.Errorf("photo_path = %v, want the new extension", p.PhotoPath)
	}
	if _, err := os.Stat(filepath.Join(uploads, "profiles", sn+".png")); !os.IsNotExist(err) {
		t.Errorf("the old .png copy was left behind: %v", err)
	}

	res, err = db.ImportRoster(ctx, admin, strings.NewReader(header+"A,B,"+sn+",noext\n"), photoDir, uploads)
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 1 || res.Rows[0].Action != RosterError || !strings.Contains(res.Rows[0].Error, "extension") {
		t.Errorf("extension-less photo = %+v", res.Rows)
	}
}

func TestImportRosterAbsolutePhotoPath(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	sn := testStudentNumber(t, db)
	src := filepath.Join(t.TempDir(), "abs.jpeg")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	uploads := t.TempDir()
	res, err := db.ImportRoster(ctx, admin, strings.NewReader("first_name,last_name,student_number,photo_path\nA,B,"+sn+","+src+"\n"), "/nonexistent", uploads)
	if err != nil || res.Created != 1 {
		t.Fatalf("ImportRoster = %+v, %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(uploads, "profiles", sn+".jpeg")); err != nil {
		t.Errorf("absolute photo path was not copied: %v", err)
	}
}

// One malformed line is reported against its line number and the rest of the
// file still lands. An admin importing a 400-line roster exported from a
// spreadsheet should not lose 399 good rows to one stray quote.
func TestImportRosterReportsPerLineParseErrors(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	before := testStudentNumber(t, db)
	after := testStudentNumber(t, db)

	// The middle line has a bare quote inside an unquoted field, which
	// encoding/csv rejects for that record only.
	csv := "first_name,last_name,student_number\n" +
		"Before,Row," + before + "\n" +
		`Bad,Ro"w,912345678` + "\n" +
		"After,Row," + after + "\n"

	res, err := db.ImportRoster(ctx, admin, strings.NewReader(csv), "", t.TempDir())
	if err != nil {
		t.Fatalf("ImportRoster: %v", err)
	}
	if res.Created != 2 || res.Failed != 1 {
		t.Errorf("result = %+v; want 2 created and 1 failed", res)
	}

	var bad *RosterRow
	for i := range res.Rows {
		if res.Rows[i].Action == RosterError {
			bad = &res.Rows[i]
		}
	}
	if bad == nil {
		t.Fatalf("no error row reported: %+v", res.Rows)
	}
	// Line 3 of the file, counting the header, is what the admin sees in a
	// spreadsheet.
	if bad.Row != 3 {
		t.Errorf("error reported on line %d, want 3", bad.Row)
	}

	// Both good rows really landed.
	for _, sn := range []string{before, after} {
		if _, err := db.profileByStudentNumber(ctx, sn); err != nil {
			t.Errorf("row %s did not import: %v", sn, err)
		}
	}
}

// An unset UPLOADS_DIR is a server misconfiguration, not bad input from the
// admin. It must not come back as ErrInvalid, or the UI would blame the file
// the user just picked for a problem in .env.
func TestImportRosterWithoutUploadsDirIsNotAClientError(t *testing.T) {
	db := requireTestDB(t)
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))

	_, err := db.ImportRoster(context.Background(), admin,
		strings.NewReader("first_name,last_name,student_number\n"), "", "")
	if err == nil {
		t.Fatal("ImportRoster with no uploads dir: want an error, got nil")
	}
	if errors.Is(err, ErrInvalid) {
		t.Errorf("error = %v, want it not to wrap ErrInvalid (that would answer 400)", err)
	}
}

// The destination filename is built from the student number, which
// NormalizeStudentNumber has already restricted to digits. A photo_path that
// climbs out of the photo directory can therefore only ever read a file the
// server user could already read; it can never write outside
// uploads/profiles. Pin that, because the roster is the one admin input that
// touches the filesystem.
func TestImportRosterPhotoCannotEscapeUploadsDir(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	sn := testStudentNumber(t, db)

	root := t.TempDir()
	photoDir := filepath.Join(root, "photos")
	if err := os.MkdirAll(photoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A real file one level above the photo directory.
	outside := filepath.Join(root, "outside.png")
	if err := os.WriteFile(outside, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	uploads := t.TempDir()

	res, err := db.ImportRoster(ctx, admin,
		strings.NewReader("first_name,last_name,student_number,photo_path\nA,B,"+sn+",../outside.png\n"),
		photoDir, uploads)
	if err != nil {
		t.Fatalf("ImportRoster: %v", err)
	}
	if res.Created != 1 {
		t.Fatalf("result = %+v, want the row to import", res)
	}

	// The copy landed under uploads/profiles named for the student number,
	// not anywhere the CSV asked for.
	p, err := db.profileByStudentNumber(ctx, sn)
	if err != nil {
		t.Fatal(err)
	}
	if p.PhotoPath == nil || *p.PhotoPath != "profiles/"+sn+".png" {
		t.Errorf("photo_path = %v, want profiles/%s.png", p.PhotoPath, sn)
	}
	if strings.Contains(*p.PhotoPath, "..") {
		t.Errorf("stored photo_path %q contains a traversal segment", *p.PhotoPath)
	}
	if _, err := os.Stat(filepath.Join(uploads, "profiles", sn+".png")); err != nil {
		t.Errorf("the copy is not inside uploads/profiles: %v", err)
	}
}

// The profile row is written between publishing the new photo and dropping
// the copy it replaced, so a row write that fails puts the old photo back
// rather than leaving the column naming a file the import already destroyed.
// A cancelled context is the cheapest way to fail the upsert at exactly that
// point; a concurrent import replacing the same student's photo is the real
// one, and it is why the whole sequence runs inside the photo's lock.
func TestImportRosterKeepsTheOldPhotoWhenTheRowWriteFails(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	sn := testStudentNumber(t, db)
	photoDir, uploads := t.TempDir(), t.TempDir()
	for _, name := range []string{"first.png", "second.jpg"} {
		if err := os.WriteFile(filepath.Join(photoDir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	header := "first_name,last_name,student_number,photo_path\n"

	if _, err := db.ImportRoster(ctx, admin, strings.NewReader(header+"A,B,"+sn+",first.png\n"), photoDir, uploads); err != nil {
		t.Fatal(err)
	}

	dead, cancel := context.WithCancel(ctx)
	cancel()
	res, err := db.ImportRoster(dead, admin, strings.NewReader(header+"A,B,"+sn+",second.jpg\n"), photoDir, uploads)
	if err != nil {
		t.Fatalf("ImportRoster: %v", err)
	}
	if res.Failed != 1 {
		t.Fatalf("result = %+v, want the row to fail", res)
	}

	// The photo of record is still the one the row names, and the abandoned
	// upload left nothing behind.
	p, err := db.profileByStudentNumber(ctx, sn)
	if err != nil {
		t.Fatal(err)
	}
	if p.PhotoPath == nil || *p.PhotoPath != "profiles/"+sn+".png" {
		t.Fatalf("photo_path = %v, want profiles/%s.png", p.PhotoPath, sn)
	}
	got, err := os.ReadFile(filepath.Join(uploads, *p.PhotoPath))
	if err != nil {
		t.Fatalf("the photo the row names is gone: %v", err)
	}
	if string(got) != "first.png" {
		t.Errorf("%s = %q, want the original photo back", *p.PhotoPath, got)
	}
	entries, err := os.ReadDir(filepath.Join(uploads, "profiles"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("uploads/profiles holds %v, want only the original photo", names)
	}
}
