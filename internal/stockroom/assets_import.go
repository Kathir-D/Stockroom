package stockroom

import (
	"github.com/jackc/pgx/v5"

	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Importing an inventory from a CSV (TEMPLATE-TODO Phase B).
//
// A department with three hundred items cannot type them into a dialog, and
// `roster.go` already established the shape this should take: a header row
// naming the columns in any order, one row applied at a time, per-row errors
// collected with line numbers, and the rest still landing.
//
// The one thing this does that the roster does not is **name the duplicate**.
// A serial that already exists is the single most common thing to go wrong in
// a real import -- somebody imports the same file twice, or two rows share a
// serial -- and a database constraint error is useless to the person who has
// to fix it. The message names the item they already have and when it was
// added.

// AssetImportResult summarises the run.
type AssetImportResult struct {
	Created int              `json:"created"`
	Updated int              `json:"updated"`
	Failed  int              `json:"failed"`
	Rows    []AssetImportRow `json:"rows"`
}

// AssetImportRow is one CSV line's outcome. Line is the line number in the
// file, header included, so it matches what a spreadsheet shows.
type AssetImportRow struct {
	Line         int    `json:"line"`
	SerialNumber string `json:"serial_number"`
	Name         string `json:"name"`
	Action       string `json:"action"` // created | updated | failed
	Error        string `json:"error,omitempty"`
	// Note says what an update replaced, naming the item that already had
	// this serial, so an upsert is never a silent overwrite.
	Note string `json:"note,omitempty"`
}

// ImportAssets upserts units from a CSV keyed on serial_number.
//
// Columns: `serial_number` and `name` are required; `category`, `model`,
// `description`, `status` and `condition` are optional. `category` and
// `model` name nodes in the tree by name, not by id -- a person filling in a
// spreadsheet has the names, and asking for UUIDs would make the file
// unwritable by hand.
//
// Upsert rather than insert-only, because the realistic second use of this
// endpoint is re-importing a corrected file. An existing serial has its name,
// description and category updated; **its status and custody are never
// touched**, so re-importing cannot return an item somebody is holding or
// quietly un-break a unit marked unavailable.
func (db *DB) ImportAssets(ctx context.Context, actor Actor, r io.Reader) (AssetImportResult, error) {
	if err := RequireAdmin(actor); err != nil {
		return AssetImportResult{}, err
	}

	body, err := readCapped(r, maxAssetImportBytes)
	if err != nil {
		return AssetImportResult{}, err
	}
	rd := csv.NewReader(bytes.NewReader(body))
	rd.FieldsPerRecord = -1
	rd.TrimLeadingSpace = true

	header, err := rd.Read()
	if err != nil {
		return AssetImportResult{}, fmt.Errorf("%w: that file has no header row", ErrInvalid)
	}
	// The BOM is stripped because Excel writes one at the front of every UTF-8
	// CSV it exports, and it lands invisibly on the first header cell -- so
	// `serial_number` arrives as `\ufeffserial_number`, the required-column
	// check fails, and the admin is told their file has no serial_number
	// column while looking straight at one.
	cols := map[string]int{}
	for i, h := range header {
		cols[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))] = i
	}
	for _, required := range []string{"serial_number", "name"} {
		if _, ok := cols[required]; !ok {
			return AssetImportResult{}, fmt.Errorf(
				"%w: the CSV needs a %q column. Found: %s",
				ErrInvalid, required, strings.Join(header, ", "))
		}
	}

	tree, err := loadCategoryTree(ctx, db.Pool)
	if err != nil {
		return AssetImportResult{}, err
	}
	byName := categoryIDsByName(tree)

	var res AssetImportResult
	// firstLine is where each serial was first seen in this file. A repeat is
	// refused here rather than left to the database: each row commits on its
	// own, so the second would find the first and *update* it -- the file
	// overwriting itself, reported as "you already had an item".
	firstLine := map[string]int{}
	for {
		rec, err := rd.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// Every row before this one has already committed, so this is a
			// failed row in the report, not an error that hides the report: a
			// 400 here would tell the admin nothing landed when 148 items did.
			// The reader cannot resynchronise after a malformed quote, so the
			// run stops here.
			line := 0
			var pe *csv.ParseError
			if errors.As(err, &pe) {
				line = pe.StartLine
			}
			res.Failed++
			res.Rows = append(res.Rows, AssetImportRow{
				Line: line, Action: "failed",
				Error: fmt.Sprintf("this line could not be read (%v); nothing after it was imported", err),
			})
			break
		}
		// The reader's own position, not a record count: encoding/csv skips
		// blank lines and a quoted field may span several, and either would
		// make a counter name the wrong line in the spreadsheet.
		line, _ := rd.FieldPos(0)

		row := AssetImportRow{Line: line}
		get := func(name string) string {
			i, ok := cols[name]
			if !ok || i >= len(rec) {
				return ""
			}
			return strings.TrimSpace(rec[i])
		}

		row.SerialNumber = get("serial_number")
		row.Name = get("name")
		if row.SerialNumber == "" && row.Name == "" {
			// A wholly blank line, which every spreadsheet export produces at
			// least one of. Skipped silently rather than reported as a
			// failure, because a report full of "line 301: blank" trains
			// people to ignore the report.
			continue
		}
		if row.SerialNumber != "" {
			if first, dup := firstLine[row.SerialNumber]; dup {
				row.Action = "failed"
				row.Error = fmt.Sprintf("serial %q is already on line %d of this file; each serial is one item", row.SerialNumber, first)
				res.Failed++
				res.Rows = append(res.Rows, row)
				continue
			}
			firstLine[row.SerialNumber] = line
		}

		action, note, err := db.importAssetRow(ctx, actor, tree, byName, get, row)
		row.Note = note
		switch {
		case err != nil:
			row.Action = "failed"
			row.Error = err.Error()
			res.Failed++
		case action == RosterCreated:
			row.Action = "created"
			res.Created++
		default:
			row.Action = "updated"
			res.Updated++
		}
		res.Rows = append(res.Rows, row)
	}

	if len(res.Rows) == 0 {
		return res, fmt.Errorf("%w: that CSV has a header but no rows", ErrInvalid)
	}
	return res, nil
}

// maxAssetImportBytes bounds the upload. Three hundred rows is about 30 KB;
// eight megabytes is a whole school's inventory many times over.
const maxAssetImportBytes = 8 << 20

// readCapped reads a whole upload, refusing one over max rather than cutting
// it off. A LimitReader alone ends at the limit as if the file did, so the
// rows past it would vanish from an import that reports success -- and each
// row commits on its own, so the file has to be refused before any of it is
// applied.
func readCapped(r io.Reader, max int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, fmt.Errorf("%w: could not read the file: %v", ErrInvalid, err)
	}
	if int64(len(body)) > max {
		return nil, fmt.Errorf("%w: that file is over %d MB; split it and import the parts", ErrInvalid, max>>20)
	}
	return body, nil
}

func (db *DB) importAssetRow(ctx context.Context, actor Actor, tree categoryTree, byName map[string][]string, get func(string) string, row AssetImportRow) (RosterAction, string, error) {
	if row.SerialNumber == "" {
		return "", "", errors.New("no serial number, and the serial is what the barcode encodes")
	}
	if row.Name == "" {
		return "", "", errors.New("no name")
	}

	categoryID, err := resolveImportCategory(tree, byName, get("category"), get("model"))
	if err != nil {
		return "", "", err
	}

	desc := optional(get("description"))
	cond := optional(get("condition"))

	// Read the existing row first so a duplicate can be reported with the name
	// and date of what is already there. Doing it as an ON CONFLICT would be
	// one round trip fewer and could not produce that sentence.
	var existingID, existingName string
	var createdAt time.Time
	err = db.Pool.QueryRow(ctx,
		`select id, name, created_at from assets where serial_number = $1`, row.SerialNumber).
		Scan(&existingID, &existingName, &createdAt)

	switch {
	case err == nil:
		_, err := db.Pool.Exec(ctx, `
			update assets
			   set name = $2,
			       description = coalesce($3, description),
			       condition = coalesce($4, condition),
			       category_id = coalesce($5, category_id)
			 where id = $1`,
			existingID, row.Name, desc, cond, categoryID)
		if err != nil {
			return "", "", mapPgError("update asset", err)
		}
		// The sentence the work list asked for: what was already there, and
		// since when, so a serial typed twice by mistake is visible in the
		// report rather than discovered as a renamed camera next term.
		return RosterUpdated, fmt.Sprintf("You already had an item with this serial: %s (added %s). It was updated to match this row.",
			existingName, createdAt.Format("2 Jan 2006")), nil

	// pgx.ErrNoRows, not mapPgError: that maps constraint codes and passes
	// ErrNoRows through untouched, so matching ErrNotFound here sent every
	// NEW serial to the default branch and the import could only ever
	// update -- a file of three hundred new items failed three hundred times.
	case errors.Is(err, pgx.ErrNoRows):
		status := strings.ToLower(get("status"))
		if status == "" {
			status = string(StatusAvailable)
		}
		if status != string(StatusAvailable) && status != string(StatusUnavailable) {
			return "", "", fmt.Errorf("status %q is not one this import accepts (available or unavailable); an item becomes checked_out by being checked out, not by a spreadsheet", get("status"))
		}
		_, err := db.Pool.Exec(ctx, `
			insert into assets (name, description, serial_number, condition, category_id, status, created_by)
			values ($1, $2, $3, $4, $5, $6, $7)`,
			row.Name, desc, row.SerialNumber, cond, categoryID, status, actor.ID)
		if err != nil {
			mapped := mapPgError("create asset", err)
			if errors.Is(mapped, ErrConflict) {
				// Somebody else created this serial between the read above
				// and this insert. A repeat within the file never gets here;
				// ImportAssets refuses it first.
				return "", "", fmt.Errorf("serial %q was added by somebody else during this import; run it again", row.SerialNumber)
			}
			return "", "", mapped
		}
		return RosterCreated, "", nil

	default:
		return "", "", mapPgError("read asset", err)
	}
}

// resolveImportCategory turns the `category` and `model` columns into an id.
//
// Names, not ids, and matched case-insensitively. A name that matches nothing
// is an error naming it rather than a silent file-under-nothing: an asset with
// no category is invisible in the browse sidebar, which is the screen the
// whole product is used through, and discovering three hundred of them later
// is worse than fixing one spelling now.
func resolveImportCategory(tree categoryTree, byName map[string][]string, category, model string) (*string, error) {
	want := model
	if want == "" {
		want = category
	}
	if want == "" {
		return nil, nil
	}

	ids := byName[strings.ToLower(want)]
	switch len(ids) {
	case 0:
		return nil, fmt.Errorf("there is no category called %q — import your category tree first, or correct the spelling", want)
	case 1:
		id := ids[0]
		return &id, nil
	default:
		// categories.name is unique across the table (CLAUDE.md §6.2), so this
		// should be unreachable. Kept because "should be unreachable" and
		// "silently files under the wrong one" are one migration apart.
		return nil, fmt.Errorf("%q matches more than one category", want)
	}
}

func categoryIDsByName(tree categoryTree) map[string][]string {
	out := map[string][]string{}
	for id, c := range tree.byID {
		key := strings.ToLower(strings.TrimSpace(c.Name))
		out[key] = append(out[key], id)
	}
	return out
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
