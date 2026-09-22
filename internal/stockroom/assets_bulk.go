package stockroom

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Adding N units of one model at once (TEMPLATE-TODO Phase B).
//
// Linear stock -- batteries, SD cards, bags, cables -- is most of the unit
// count in a real department and all of the tedium: twelve identical
// batteries is twelve trips through the same dialog, differing only in the
// last three digits of a serial the admin is generating in their head.
//
// Two things make this safe rather than merely fast:
//
//  1. **It previews.** `BulkPreview` returns the exact serials without writing
//     anything, so the admin sees `LPE6-001 … LPE6-012` before committing.
//     A generator that writes first is one whose off-by-one is discovered as
//     twelve wrong stickers.
//  2. **It continues the numbering.** The next run of the same prefix starts
//     after the highest number already in use, so next year's re-order
//     extends the series instead of colliding with it. That is the failure
//     this would otherwise have: `LPE6-001` already exists, the whole insert
//     fails on the unique index, and the admin has to work out by hand where
//     to start.

// BulkAddInput describes a run of identical units.
type BulkAddInput struct {
	// Name is what every unit is called; they are identical by definition.
	Name string `json:"name"`
	// Prefix is the serial stem, e.g. "LPE6". The number and separator are
	// generated.
	Prefix string `json:"prefix"`
	// Count is how many units to make.
	Count int `json:"count"`
	// CategoryID is where they file. Optional, like AssetInput's.
	CategoryID *string `json:"category_id"`
	// Digits is how many digits the number is padded to; 0 means 3.
	Digits int `json:"digits"`
	// StartAt overrides where the numbering begins. Zero means "after the
	// highest that already exists", which is what makes a re-order continue
	// the series rather than collide with it.
	StartAt int `json:"start_at"`
}

// BulkPreview is what the confirm step shows.
type BulkPreview struct {
	Serials []string `json:"serials"`
	// Existing lists serials in the generated range that are already taken.
	// It should always be empty, because the start point is chosen to avoid
	// them -- it is returned so that if it ever is not, the admin sees it
	// before pressing the button rather than as a failed insert afterwards.
	Existing []string `json:"existing"`
	Name     string   `json:"name"`
}

const (
	defaultSerialDigits = 3
	maxBulkCount        = 500
)

// prefixPattern is what a serial stem may contain.
//
// Letters, digits, dash and underscore. Not free text, because the prefix
// becomes a Code 128 barcode (barcode.go refuses non-ASCII) and because a
// prefix with a space or a slash in it produces serials that are awkward to
// type back in when a sticker falls off -- and typing it back in is the
// documented recovery path.
var prefixPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

func (in *BulkAddInput) normalize() error {
	in.Name = strings.TrimSpace(in.Name)
	in.Prefix = strings.TrimSpace(in.Prefix)

	if in.Name == "" {
		return fmt.Errorf("%w: give the units a name", ErrInvalid)
	}
	if !prefixPattern.MatchString(in.Prefix) {
		return fmt.Errorf("%w: a serial prefix may only use letters, numbers, dashes and underscores (got %q)", ErrInvalid, in.Prefix)
	}
	if in.Count < 1 {
		return fmt.Errorf("%w: how many units?", ErrInvalid)
	}
	if in.Count > maxBulkCount {
		// Not a technical limit. A mis-typed count is the one mistake this
		// feature can make at scale, and undoing it means deleting each unit,
		// so the bound is where "obviously a typo" starts.
		return fmt.Errorf("%w: %d units at once is more than this is meant for; the limit is %d", ErrInvalid, in.Count, maxBulkCount)
	}
	if in.Digits == 0 {
		in.Digits = defaultSerialDigits
	}
	if in.Digits < 1 || in.Digits > 6 {
		return fmt.Errorf("%w: pad the number to between 1 and 6 digits", ErrInvalid)
	}
	if in.StartAt < 0 {
		return fmt.Errorf("%w: start numbering at 1 or more", ErrInvalid)
	}
	return nil
}

// BulkPreviewSerials works out the serials without writing anything.
func (db *DB) BulkPreviewSerials(ctx context.Context, actor Actor, in BulkAddInput) (BulkPreview, error) {
	if err := RequireAdmin(actor); err != nil {
		return BulkPreview{}, err
	}
	if err := in.normalize(); err != nil {
		return BulkPreview{}, err
	}

	start := in.StartAt
	if start == 0 {
		next, err := db.nextSerialNumber(ctx, in.Prefix)
		if err != nil {
			return BulkPreview{}, err
		}
		start = next
	}

	serials := make([]string, 0, in.Count)
	for i := 0; i < in.Count; i++ {
		serials = append(serials, formatSerial(in.Prefix, start+i, in.Digits))
	}

	taken, err := db.existingSerials(ctx, serials)
	if err != nil {
		return BulkPreview{}, err
	}
	return BulkPreview{Serials: serials, Existing: taken, Name: in.Name}, nil
}

// BulkAddAssets creates the units, in one transaction.
//
// All-or-nothing, unlike the CSV import. The rows are identical by
// construction, so there is no such thing as one bad row here -- a failure
// means the range was wrong, and half a range of stickers is worse than none.
func (db *DB) BulkAddAssets(ctx context.Context, actor Actor, in BulkAddInput) ([]Asset, error) {
	if err := RequireAdmin(actor); err != nil {
		return nil, err
	}
	preview, err := db.BulkPreviewSerials(ctx, actor, in)
	if err != nil {
		return nil, err
	}
	if len(preview.Existing) > 0 {
		return nil, fmt.Errorf("%w: %s already exist", ErrConflict, strings.Join(preview.Existing, ", "))
	}
	if in.CategoryID != nil {
		tree, err := loadCategoryTree(ctx, db.Pool)
		if err != nil {
			return nil, err
		}
		if _, err := tree.require(*in.CategoryID); err != nil {
			return nil, err
		}
	}

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return nil, mapPgError("bulk add", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	out := make([]Asset, 0, len(preview.Serials))
	for _, serial := range preview.Serials {
		a, err := scanAsset(tx.QueryRow(ctx, `
			insert into assets (name, serial_number, category_id)
			values ($1, $2, $3)
			returning `+assetColumns, in.Name, serial, in.CategoryID))
		if err != nil {
			mapped := mapPgError("bulk add", err)
			if errors.Is(mapped, ErrConflict) {
				return nil, fmt.Errorf("%w: serial %s is already taken", ErrConflict, serial)
			}
			return nil, mapped
		}
		out = append(out, a)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, mapPgError("bulk add", err)
	}
	return out, nil
}

// nextSerialNumber finds the first unused number for a prefix.
//
// It reads every serial that starts with the prefix and takes the highest
// numeric suffix, rather than counting rows: units get deleted, and a count
// would hand back a number already on a sticker somewhere.
func (db *DB) nextSerialNumber(ctx context.Context, prefix string) (int, error) {
	rows, err := db.Pool.Query(ctx,
		`select serial_number from assets where serial_number like $1`, prefix+"-%")
	if err != nil {
		return 0, mapPgError("read serials", err)
	}
	defer rows.Close()

	highest := 0
	for rows.Next() {
		var serial *string
		if err := rows.Scan(&serial); err != nil {
			return 0, mapPgError("read serials", err)
		}
		if serial == nil {
			continue
		}
		suffix := strings.TrimPrefix(*serial, prefix+"-")
		n, err := strconv.Atoi(suffix)
		if err != nil {
			// A serial sharing the prefix but not the shape, e.g. `LPE6-SPARE`.
			// Ignored rather than failing: it is somebody's deliberate
			// one-off and it does not participate in the numbering.
			continue
		}
		if n > highest {
			highest = n
		}
	}
	if err := rows.Err(); err != nil {
		return 0, mapPgError("read serials", err)
	}
	return highest + 1, nil
}

func (db *DB) existingSerials(ctx context.Context, serials []string) ([]string, error) {
	rows, err := db.Pool.Query(ctx,
		`select serial_number from assets where serial_number = any($1)`, serials)
	if err != nil {
		return nil, mapPgError("check serials", err)
	}
	defer rows.Close()

	var taken []string
	for rows.Next() {
		var s *string
		if err := rows.Scan(&s); err != nil {
			return nil, mapPgError("check serials", err)
		}
		if s != nil {
			taken = append(taken, *s)
		}
	}
	return taken, rows.Err()
}

func formatSerial(prefix string, n, digits int) string {
	return fmt.Sprintf("%s-%0*d", prefix, digits, n)
}
