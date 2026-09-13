package stockroom

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// The browse screen (CLAUDE.md §1, step 2): a filtered asset list on the
// right, the category tree and a search box on the left, and a detail popup
// per item. Reads only -- checkout, check-in and scanning are Phase 4.

// FilesPrefix is where the server mounts UPLOADS_DIR (server/router.go), and
// so the prefix of every photo URL handed to a frontend. It lives here
// because the same rule applies to profile and asset photos alike and both
// are built below.
const FilesPrefix = "/files/"

// AssetFilter is the browse query. Every field is optional; the zero value
// lists the whole inventory.
type AssetFilter struct {
	// CategoryID matches that node and everything under it, so picking a
	// Type ("Lenses") shows every Model beneath it.
	CategoryID string
	// Status narrows to one asset_status. v1 uses available, checked_out
	// and unavailable.
	Status AssetStatus
	// Search is free text over name, description, serial number and asset
	// tag.
	Search string
}

// AssetListItem is an asset as the browse list shows it: the row plus the
// two things the list needs that aren't columns -- where it sits in the
// category tree, and a URL the frontend can put in an <img> tag.
type AssetListItem struct {
	Asset
	CategoryPath []CategoryRef `json:"category_path"`
	PhotoURL     *string       `json:"photo_url"`
}

// AssetDetail is the detail-popup payload: a list item plus who holds it.
// Phase 4's ScanItem answers with this same shape, so a scanned item and a
// clicked one open the identical dialog.
type AssetDetail struct {
	AssetListItem
	// Custody is the open custody event, or nil when nobody has the item.
	Custody *AssetCustody `json:"custody"`
}

// AssetCustody is who currently holds an asset, flattened for display.
type AssetCustody struct {
	CustodyEventID string     `json:"custody_event_id"`
	CustodianID    string     `json:"custodian_id"`
	CustodianName  string     `json:"custodian_name"`
	StudentNumber  *string    `json:"student_number"`
	CheckedOutAt   time.Time  `json:"checked_out_at"`
	DueAt          *time.Time `json:"due_at"`
	Overdue        bool       `json:"overdue"`
}

// ListAssets returns the assets matching filter, by name. Unavailable items
// are included: the browse screen shows them greyed out rather than hiding
// equipment that exists, and the detail popup refuses to add them to a cart.
func (db *DB) ListAssets(ctx context.Context, filter AssetFilter) ([]AssetListItem, error) {
	where, args, err := db.assetFilterSQL(ctx, filter)
	if err != nil {
		return nil, err
	}

	rows, err := db.Pool.Query(ctx,
		`select `+assetColumns+` from assets a where `+where+` order by a.name, a.asset_tag`, args...)
	if err != nil {
		return nil, mapPgError("list assets", err)
	}
	defer rows.Close()

	assets := []Asset{}
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		assets = append(assets, a)
	}
	if err := rows.Err(); err != nil {
		return nil, mapPgError("list assets", err)
	}

	paths, err := db.assetCategoryPaths(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]AssetListItem, 0, len(assets))
	for _, a := range assets {
		items = append(items, newAssetListItem(a, paths))
	}
	return items, nil
}

// GetAsset returns one asset with its category path and current custodian,
// which is what the detail popup shows. An id that is not a uuid comes back
// as ErrInvalid (from Postgres), an unknown one as ErrNotFound.
func (db *DB) GetAsset(ctx context.Context, id string) (AssetDetail, error) {
	a, err := scanAsset(db.Pool.QueryRow(ctx,
		`select `+assetColumns+` from assets a where a.id = $1`, id))
	if err != nil {
		return AssetDetail{}, mapPgError("get asset", err)
	}

	paths, err := db.assetCategoryPaths(ctx)
	if err != nil {
		return AssetDetail{}, err
	}
	detail := AssetDetail{AssetListItem: newAssetListItem(a, paths)}

	// The open custody row is read regardless of status rather than only
	// when the asset says checked_out, so a status that has drifted out of
	// step with custody still shows the truth.
	detail.Custody, err = currentCustody(ctx, db.Pool, a.ID)
	if err != nil {
		return AssetDetail{}, err
	}
	return detail, nil
}

// assetFilterSQL turns a filter into a where clause and its arguments. The
// clause is always non-empty so callers can concatenate it unconditionally.
func (db *DB) assetFilterSQL(ctx context.Context, filter AssetFilter) (string, []any, error) {
	conds := []string{"true"}
	var args []any

	if id := strings.TrimSpace(filter.CategoryID); id != "" {
		ok, err := db.categoryExists(ctx, id)
		if err != nil {
			return "", nil, err
		}
		if !ok {
			return "", nil, fmt.Errorf("%w: category %s", ErrNotFound, id)
		}
		args = append(args, id)
		// A filter on any level of the tree includes everything below it.
		// union (not union all) also makes a malformed parent chain
		// terminate instead of recursing forever.
		conds = append(conds, fmt.Sprintf(`a.category_id in (
			with recursive picked as (
				select id from categories where id = $%[1]d
				union
				select c.id from categories c join picked p on c.parent_id = p.id
			)
			select id from picked)`, len(args)))
	}

	if filter.Status != "" {
		if !filter.Status.Valid() {
			return "", nil, fmt.Errorf("%w: unknown status %q", ErrInvalid, filter.Status)
		}
		args = append(args, string(filter.Status))
		conds = append(conds, fmt.Sprintf(`a.status = $%d::asset_status`, len(args)))
	}

	if q := strings.TrimSpace(filter.Search); q != "" {
		// Two ways to match, because they cover different searches. The
		// tsquery half uses idx_assets_search and handles words and
		// stemming ("battery" finding "batteries"); the ilike half handles
		// the partial identifiers a tsquery can't ("T7iB" finding
		// T7iBat-001), which is how anyone reading a sticker types.
		args = append(args, q, "%"+escapeLike(q)+"%")
		full, like := len(args)-1, len(args)
		conds = append(conds, fmt.Sprintf(`(
			to_tsvector('english', coalesce(a.name,'') || ' ' || coalesce(a.description,'') || ' ' || coalesce(a.serial_number,''))
				@@ plainto_tsquery('english', $%[1]d)
			or a.name ilike $%[2]d
			or a.asset_tag ilike $%[2]d
			or coalesce(a.description, '') ilike $%[2]d
			or coalesce(a.serial_number, '') ilike $%[2]d)`, full, like))
	}

	return strings.Join(conds, " and "), args, nil
}

// likeEscaper neutralises the wildcards in a user's search text so a query
// containing % or _ matches those characters literally. Backslash is LIKE's
// default escape character, so it has to be escaped first.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func escapeLike(s string) string { return likeEscaper.Replace(s) }

// assetCategoryPaths reads the category tree and returns id -> path. The
// table is a few dozen rows, so one read per list call is cheaper than a
// recursive join per asset.
func (db *DB) assetCategoryPaths(ctx context.Context) (map[string][]CategoryRef, error) {
	cats, err := db.loadCategories(ctx)
	if err != nil {
		return nil, err
	}
	return categoryPaths(cats), nil
}

func newAssetListItem(a Asset, paths map[string][]CategoryRef) AssetListItem {
	item := AssetListItem{Asset: a, CategoryPath: []CategoryRef{}, PhotoURL: photoURL(a.PhotoPath)}
	if a.CategoryID != nil {
		if p, ok := paths[*a.CategoryID]; ok {
			item.CategoryPath = p
		}
	}
	return item
}

// photoURL turns a path stored relative to UPLOADS_DIR into the URL the Go
// server serves it at. A missing or blank path is nil, not an empty string,
// so the frontend tests one thing to decide whether to render an image.
func photoURL(stored *string) *string {
	if stored == nil {
		return nil
	}
	// Photos are written with forward slashes (roster.go), but a value typed
	// into the admin panel on Windows may not be.
	rel := strings.Trim(strings.ReplaceAll(*stored, `\`, "/"), "/")
	if rel == "" {
		return nil
	}
	url := FilesPrefix + path.Clean(rel)
	return &url
}

// currentCustody returns the unreturned custody event for an asset, or nil if
// it isn't out. Ordered newest-first so a stale duplicate open row (which
// the schema permits but nothing writes) reports the current holder. It takes
// a querier rather than hanging off DB because CheckInAsset reads the same
// row inside its transaction.
func currentCustody(ctx context.Context, q querier, assetID string) (*AssetCustody, error) {
	var (
		c                 AssetCustody
		full, first, last *string
	)
	err := q.QueryRow(ctx, `
		select ce.id, ce.custodian_id, p.full_name, p.first_name, p.last_name, p.student_number,
		       ce.checked_out_at, ce.due_at, (ce.due_at is not null and ce.due_at < now())
		from custody_events ce
		join profiles p on p.id = ce.custodian_id
		where ce.asset_id = $1 and ce.checked_in_at is null
		order by ce.checked_out_at desc
		limit 1`, assetID).
		Scan(&c.CustodyEventID, &c.CustodianID, &full, &first, &last, &c.StudentNumber,
			&c.CheckedOutAt, &c.DueAt, &c.Overdue)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, mapPgError("get custody", err)
	}
	c.CustodianName = displayName(first, last, full, c.StudentNumber)
	return &c, nil
}

// displayName picks the best label for a person: the split name fields, then
// the legacy full_name column, then the student number, so a row imported by
// any path still renders as something a human recognises.
func displayName(first, last, full, studentNumber *string) string {
	if name := fullName(deref(first), deref(last)); name != "" {
		return name
	}
	if name := strings.TrimSpace(deref(full)); name != "" {
		return name
	}
	return deref(studentNumber)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// assetColumns is the select list every asset query uses, in the order
// scanAsset expects. Qualified with "a" because the browse query aliases the
// table.
const assetColumns = `a.id, a.asset_tag, a.name, a.description, a.category_id, a.location_id,
	a.status, a.condition, a.serial_number, a.purchase_date, a.purchase_price,
	a.warranty_expiration, a.custom_fields, a.photo_path, a.created_by, a.created_at, a.updated_at`

func scanAsset(row pgx.Row) (Asset, error) {
	var a Asset
	err := row.Scan(&a.ID, &a.AssetTag, &a.Name, &a.Description, &a.CategoryID, &a.LocationID,
		&a.Status, &a.Condition, &a.SerialNumber, &a.PurchaseDate, &a.PurchasePrice,
		&a.WarrantyExpiration, &a.CustomFields, &a.PhotoPath, &a.CreatedBy, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Asset{}, ErrNotFound
	}
	if err != nil {
		return Asset{}, fmt.Errorf("scan asset: %w", err)
	}
	return a, nil
}
