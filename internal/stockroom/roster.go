package stockroom

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// RosterAction is what an import did with one CSV line. It is a named type
// so the three outcomes are spelled once here rather than as literals at
// every comparison.
type RosterAction string

const (
	RosterCreated RosterAction = "created"
	RosterUpdated RosterAction = "updated"
	RosterError   RosterAction = "error"
)

// RosterRow is the outcome for one CSV line. Row is the 1-based line number
// in the file, counting the header as line 1 and blank lines too, so it
// matches what the admin sees in a spreadsheet.
type RosterRow struct {
	Row           int          `json:"row"`
	StudentNumber string       `json:"student_number"`
	Action        RosterAction `json:"action"`
	Error         string       `json:"error,omitempty"`
}

// photoStore is where roster photos come from and where they end up: a
// source named in the CSV resolves against dir when it is relative, and the
// copy lands under uploads/profiles. The pair travels through every step of
// an import, so it travels as one value.
type photoStore struct {
	dir     string // photo_dir from the request; "" means the working directory
	uploads string // UPLOADS_DIR, the root /files/ serves
}

// RosterResult summarises an import. Rows has one entry per data line in
// the order they appeared.
type RosterResult struct {
	Created int         `json:"created"`
	Updated int         `json:"updated"`
	Failed  int         `json:"failed"`
	Rows    []RosterRow `json:"rows"`
}

// ImportRoster upserts accounts from a CSV with a header row naming the
// columns first_name, last_name, student_number and (optionally) photo_path,
// in any order; other columns are ignored. Rows match existing accounts by
// student number: names are replaced, is_admin and the password are left
// alone. A photo_path is a file on this machine (absolute, or relative to
// photoDir); it must have a file extension, it is copied to
// <UploadsDir>/profiles/<student_number>.<ext>, and the path relative to
// UploadsDir is stored.
//
// Each row is applied on its own, so one bad line reports an error and the
// rest still land. Only a malformed file (no header, missing required
// columns, unbalanced quotes) fails the whole call with ErrInvalid.
func (db *DB) ImportRoster(ctx context.Context, actor Actor, r io.Reader, photoDir string) (RosterResult, error) {
	if err := RequireAdmin(actor); err != nil {
		return RosterResult{}, err
	}
	if db.UploadsDir == "" {
		// A missing UPLOADS_DIR is a server misconfiguration rather than
		// anything the caller sent, but the admin who pressed import is the
		// one who can fix it, so the message has to reach the response
		// instead of being swallowed by a generic 500.
		return RosterResult{}, fmt.Errorf("%w: UPLOADS_DIR is not set, so roster photos have nowhere to go", ErrNotConfigured)
	}
	photos := photoStore{dir: photoDir, uploads: db.UploadsDir}

	cr := csv.NewReader(r)
	cr.TrimLeadingSpace = true
	cr.FieldsPerRecord = -1 // ragged rows are reported per line, not fatal

	header, err := cr.Read()
	if err != nil {
		return RosterResult{}, fmt.Errorf("%w: roster CSV has no header row", ErrInvalid)
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))] = i
	}
	for _, need := range []string{"first_name", "last_name", "student_number"} {
		if _, ok := col[need]; !ok {
			return RosterResult{}, fmt.Errorf("%w: roster CSV is missing the %s column", ErrInvalid, need)
		}
	}
	photoCol, hasPhoto := col["photo_path"]

	res := RosterResult{Rows: []RosterRow{}}
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// A parse error on one line (bad quoting) is reported for that
			// line; csv.Reader keeps going afterwards. Blank lines are skipped
			// by the reader, so line numbers come from it, not from counting.
			line := 0
			var pe *csv.ParseError
			if errors.As(err, &pe) {
				line = pe.Line
			}
			res.Rows = append(res.Rows, RosterRow{Row: line, Action: RosterError, Error: err.Error()})
			res.Failed++
			continue
		}
		line, _ := cr.FieldPos(0)
		field := func(i int) string {
			if i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
			return ""
		}

		row := RosterRow{Row: line, StudentNumber: field(col["student_number"])}
		photo := ""
		if hasPhoto {
			photo = field(photoCol)
		}
		action, err := db.upsertRosterRow(ctx, field(col["first_name"]), field(col["last_name"]),
			row.StudentNumber, photo, photos)
		if err != nil {
			row.Action, row.Error = RosterError, err.Error()
			res.Failed++
		} else {
			row.Action = action
			if action == RosterCreated {
				res.Created++
			} else {
				res.Updated++
			}
		}
		res.Rows = append(res.Rows, row)
	}
	return res, nil
}

// upsertRosterRow validates one line, copies its photo if any, then upserts
// the profile. Returns RosterCreated or RosterUpdated.
func (db *DB) upsertRosterRow(ctx context.Context, first, last, studentNumber, photo string, photos photoStore) (RosterAction, error) {
	sn, err := NormalizeStudentNumber(studentNumber)
	if err != nil {
		return "", err
	}
	if first == "" && last == "" {
		return "", fmt.Errorf("%w: a first or last name is required", ErrInvalid)
	}

	if photo == "" {
		return db.upsertProfileRow(ctx, sn, first, last, nil)
	}

	src, ext, err := photos.openFor(photo)
	if err != nil {
		return "", err
	}
	defer src.Close()

	// The row is written inside the photo's lock, after the new file is in
	// place and before the copy it replaced is dropped — the order
	// SetAssetPhoto uses, for the same reason (photos.go). Two imports naming
	// this student under different extensions would otherwise each find the
	// other's file stale and delete it on commit, and whichever upsert landed
	// after that would leave the row naming a path that is already gone.
	var action RosterAction
	_, err = storePhoto(photos.uploads, "profiles", sn, ext, src, func(rel string) error {
		var err error
		action, err = db.upsertProfileRow(ctx, sn, first, last, &rel)
		return err
	})
	if err != nil {
		return "", err
	}
	return action, nil
}

// upsertProfileRow writes one roster line to profiles. A nil photoPath leaves
// whatever photo the account already had.
func (db *DB) upsertProfileRow(ctx context.Context, sn, first, last string, photoPath *string) (RosterAction, error) {
	var inserted bool
	err := db.Pool.QueryRow(ctx, `
		insert into profiles (student_number, first_name, last_name, full_name, photo_path)
		values ($1, $2, $3, $4, $5)
		on conflict (student_number) do update
		set first_name = excluded.first_name,
		    last_name  = excluded.last_name,
		    full_name  = excluded.full_name,
		    photo_path = coalesce(excluded.photo_path, profiles.photo_path)
		returning (xmax = 0)`,
		sn, first, last, fullName(first, last), photoPath).Scan(&inserted)
	if err != nil {
		return "", mapPgError("import roster row", err)
	}
	if inserted {
		return RosterCreated, nil
	}
	return RosterUpdated, nil
}

// openFor opens src (absolute, or relative to the store's dir) and returns it
// alongside its lowercased extension; the caller closes the file. The photo
// lands at <uploads>/profiles/<studentNumber>.<ext>, so the extension is
// required: it is what tells a browser how to render the file, and it is part
// of the stored path. It must also be one of uploadPhotoExtensions, for the
// reason given there -- the extension decides the Content-Type /files/ serves
// the copy under, and that is a property of what this server publishes rather
// than of who supplied the bytes.
func (ps photoStore) openFor(src string) (*os.File, string, error) {
	if !filepath.IsAbs(src) {
		dir := ps.dir
		if dir == "" {
			dir = "."
		}
		src = filepath.Join(dir, src)
	}
	ext := strings.ToLower(filepath.Ext(src))
	if ext == "" {
		return nil, "", fmt.Errorf("%w: photo %s has no file extension", ErrInvalid, src)
	}
	if !uploadPhotoExtensions[ext] {
		return nil, "", fmt.Errorf("%w: photo %s is not a photo; use a .jpg, .png, .gif or .webp file",
			ErrInvalid, src)
	}
	in, err := os.Open(src)
	if err != nil {
		return nil, "", fmt.Errorf("%w: photo %s: %v", ErrInvalid, src, err)
	}
	return in, ext, nil
}
