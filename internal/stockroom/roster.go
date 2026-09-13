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
// <uploadsDir>/profiles/<student_number>.<ext>, and the path relative to
// uploadsDir is stored.
//
// Each row is applied on its own, so one bad line reports an error and the
// rest still land. Only a malformed file (no header, missing required
// columns, unbalanced quotes) fails the whole call with ErrInvalid.
func (db *DB) ImportRoster(ctx context.Context, actor Actor, r io.Reader, photoDir, uploadsDir string) (RosterResult, error) {
	if err := RequireAdmin(actor); err != nil {
		return RosterResult{}, err
	}
	if uploadsDir == "" {
		// A missing UPLOADS_DIR is a server misconfiguration rather than
		// anything the caller sent, but the admin who pressed import is the
		// one who can fix it, so the message has to reach the response
		// instead of being swallowed by a generic 500.
		return RosterResult{}, fmt.Errorf("%w: UPLOADS_DIR is not set, so roster photos have nowhere to go", ErrNotConfigured)
	}
	photos := photoStore{dir: photoDir, uploads: uploadsDir}

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

	var photoPath *string
	if photo != "" {
		rel, err := photos.copyFor(photo, sn)
		if err != nil {
			return "", err
		}
		photoPath = &rel
	}

	var inserted bool
	err = db.Pool.QueryRow(ctx, `
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

// copyFor copies src (absolute, or relative to the store's dir) to
// <uploads>/profiles/<studentNumber>.<ext> and returns the path relative to
// uploads, which is what goes in the database and what /files/ serves. The
// extension is required: it is what tells a browser how to render the file,
// and it is part of the stored path. Which extensions are allowed is not
// checked here; see uploadPhotoExtensions for why the upload path is
// stricter than this one.
func (ps photoStore) copyFor(src, studentNumber string) (string, error) {
	if !filepath.IsAbs(src) {
		dir := ps.dir
		if dir == "" {
			dir = "."
		}
		src = filepath.Join(dir, src)
	}
	ext := strings.ToLower(filepath.Ext(src))
	if ext == "" {
		return "", fmt.Errorf("%w: photo %s has no file extension", ErrInvalid, src)
	}
	in, err := os.Open(src)
	if err != nil {
		return "", fmt.Errorf("%w: photo %s: %v", ErrInvalid, src, err)
	}
	defer in.Close()

	return storePhoto(ps.uploads, "profiles", studentNumber, ext, in)
}
