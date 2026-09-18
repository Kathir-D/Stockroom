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
	db.UploadsDir = t.TempDir()
	student := actorFor(insertTestProfile(t, db, false, "student-pw"))
	_, err := db.ImportRoster(context.Background(), student, strings.NewReader("first_name,last_name,student_number\n"), "")
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("ImportRoster as non-admin = %v, want ErrForbidden", err)
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
	db.UploadsDir = uploads
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

	res, err := db.ImportRoster(ctx, admin, strings.NewReader(csv), photoDir)
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

// TestOpenForAllowsOnlyPhotoExtensions: the copy openFor opens ends up under
// the uploads directory, which /files/ serves to anyone with no session, and
// http.FileServer takes the Content-Type from the extension. A roster row
// naming an .svg or .html file on the admin's disk would publish it as a page
// on the app's own origin, so the extension is gated here and not only on the
// upload path. Needs no database: openFor is a file operation and a check.
func TestOpenForAllowsOnlyPhotoExtensions(t *testing.T) {
	dir := t.TempDir()
	ps := photoStore{dir: dir, uploads: t.TempDir()}

	for _, name := range []string{"ok.JPG", "ok.png", "bad.svg", "bad.html", "bad.pdf", "noext"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("bytes"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct {
		name string
		ok   bool
		want string // a substring of the error, when there is one
	}{
		{name: "ok.JPG", ok: true},
		{name: "ok.png", ok: true},
		{name: "bad.svg", want: "not a photo"},
		{name: "bad.html", want: "not a photo"},
		{name: "bad.pdf", want: "not a photo"},
		{name: "noext", want: "no file extension"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, ext, err := ps.openFor(tc.name)
			if tc.ok {
				if err != nil {
					t.Fatalf("openFor(%s) = %v, want it accepted", tc.name, err)
				}
				f.Close()
				if ext != strings.ToLower(filepath.Ext(tc.name)) {
					t.Errorf("ext = %q", ext)
				}
				return
			}
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("openFor(%s) = %v, want ErrInvalid", tc.name, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
			if f != nil {
				f.Close()
				t.Error("the file was opened despite being rejected")
			}
		})
	}
}
