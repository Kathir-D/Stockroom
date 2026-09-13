package stockroom

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// The browse screen (CLAUDE.md §1, step 2): a filtered asset list on the
// right, the category tree and a search box on the left, and a detail popup
// per item. Reads only -- checkout, check-in and scanning are Phase 4.

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
// three things the list needs that aren't columns -- where it sits in the
// category tree, a URL the frontend can put in an <img> tag, and who has it.
type AssetListItem struct {
	Asset
	CategoryPath []CategoryRef `json:"category_path"`
	PhotoURL     *string       `json:"photo_url"`
	// Custody is the open custody event, or nil when nobody has the item.
	// Every row carries it, for every actor: a unit row has to be able to say
	// who holds the lens without a fetch per row (design doc §8.2, decided
	// 2026-09-12).
	Custody *AssetCustody `json:"custody"`
}

// AssetDetail is the detail-popup payload. It is the list row itself: since
// the list started carrying the current holder there is nothing detail knows
// that a row doesn't, and the alias keeps the name the API and the design doc
// use for the dialog. ScanItem answers with this same shape, so a scanned item
// and a clicked one open the identical dialog.
type AssetDetail = AssetListItem

// AssetCustody is who currently holds an asset, flattened for display. Who
// holds a named item is open to every signed-in user (CLAUDE.md §7, decided
// 2026-09-12) -- but only as a name. See forViewer for the number.
type AssetCustody struct {
	CustodyEventID string `json:"custody_event_id"`
	CustodianID    string `json:"custodian_id"`
	CustodianName  string `json:"custodian_name"`
	// StudentNumber is admin-only: it is the scan-login key, so handing it to
	// every browsing student turns "who has the lens" into a list of other
	// people's credentials. Blanked by forViewer for everyone else.
	StudentNumber *string    `json:"student_number"`
	CheckedOutAt  time.Time  `json:"checked_out_at"`
	DueAt         *time.Time `json:"due_at"`
	Overdue       bool       `json:"overdue"`
}

// unnamedCustodian labels a holder no better label exists for. Rare -- every
// path that creates a user asks for a name -- but every field displayName
// reads is nullable, so a hand-inserted profile can leave it with nothing to
// return, and a row has to say something rather than nothing.
const unnamedCustodian = "Someone"

// forViewer trims a custody record to what actor is allowed to see. Every path
// that builds one goes through here rather than each caller remembering, and
// it happens in the package rather than the UI because a field the UI hides is
// still one fetch away (CLAUDE.md §7).
func (c *AssetCustody) forViewer(actor Actor) {
	if c == nil {
		return
	}
	// An empty label is nobody's privacy rule, so it is fixed for every
	// viewer: with first_name, last_name, full_name and student_number all
	// null or blank, displayName has nothing left to fall back to.
	if strings.TrimSpace(c.CustodianName) == "" {
		c.CustodianName = unnamedCustodian
	}
	if actor.IsAdmin {
		return
	}
	// displayName's last resort is the student number itself, which would
	// hand it to a non-admin under a different key.
	if c.StudentNumber != nil && c.CustodianName == *c.StudentNumber {
		c.CustodianName = unnamedCustodian
	}
	c.StudentNumber = nil
}

// ListAssets returns the assets matching filter, in browse order (see
// sortBrowseList). Unavailable items are included: the browse screen shows
// them greyed out rather than hiding equipment that exists, and the detail
// popup refuses to add them to a cart.
func (db *DB) ListAssets(ctx context.Context, actor Actor, filter AssetFilter) ([]AssetListItem, error) {
	if err := RequireFullSession(actor); err != nil {
		return nil, err
	}
	tree, err := loadCategoryTree(ctx, db.Pool)
	if err != nil {
		return nil, err
	}
	where, args, err := assetFilterSQL(tree, filter)
	if err != nil {
		return nil, err
	}

	// No order by: the list is sorted in Go, below, because its first key is
	// the asset's position in the category tree and that is exactly what the
	// one category read already knows.
	rows, err := db.Pool.Query(ctx,
		`select `+assetColumns+` from assets a where `+where, args...)
	if err != nil {
		return nil, mapPgError("list assets", err)
	}
	defer rows.Close()

	assets := []Asset{}
	ids := []string{}
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		assets = append(assets, a)
		ids = append(ids, a.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, mapPgError("list assets", err)
	}

	// One query for the whole page rather than one per row: a list of two
	// hundred units would otherwise be two hundred round trips.
	held, err := holdersByAsset(ctx, db.Pool, ids, actor)
	if err != nil {
		return nil, err
	}

	items := make([]AssetListItem, 0, len(assets))
	for _, a := range assets {
		item := newAssetListItem(a, tree)
		item.Custody = held[a.ID]
		items = append(items, item)
	}
	sortBrowseList(items, tree)
	return items, nil
}

// sortBrowseList puts the list in the order the browse screen reads in
// (design doc §8.2, decided 2026-09-12): category first, in Catagories.md's
// document order rather than alphabetically, then available units ahead of the
// ones nobody can take today, then by name. asset_tag breaks the last tie so
// two identically named units never swap places between two requests.
func sortBrowseList(items []AssetListItem, tree categoryTree) {
	slices.SortFunc(items, func(x, y AssetListItem) int {
		if c := compareCategoryKeys(tree.keyFor(x.CategoryID), tree.keyFor(y.CategoryID)); c != 0 {
			return c
		}
		if c := cmp.Compare(browseRank(x.Status), browseRank(y.Status)); c != 0 {
			return c
		}
		if c := cmp.Compare(x.Name, y.Name); c != 0 {
			return c
		}
		return cmp.Compare(x.AssetTag, y.AssetTag)
	})
}

// browseRank is the availability half of that order: what a borrower can take
// now, then what is out and will come back, then what is out of service.
func browseRank(s AssetStatus) int {
	switch s {
	case StatusAvailable:
		return 0
	case StatusCheckedOut:
		return 1
	default:
		return 2
	}
}

// GetAsset returns one asset with its category path and current custodian,
// which is what the detail popup shows. An id that is not a uuid comes back
// as ErrInvalid (from Postgres), an unknown one as ErrNotFound.
func (db *DB) GetAsset(ctx context.Context, actor Actor, id string) (AssetDetail, error) {
	if err := RequireFullSession(actor); err != nil {
		return AssetDetail{}, err
	}
	a, err := scanAsset(db.Pool.QueryRow(ctx,
		`select `+assetColumns+` from assets a where a.id = $1`, id))
	if errors.Is(err, ErrNotFound) {
		return AssetDetail{}, fmt.Errorf("%w: no asset %s", ErrNotFound, id)
	}
	if err != nil {
		return AssetDetail{}, mapPgError("get asset", err)
	}

	tree, err := loadCategoryTree(ctx, db.Pool)
	if err != nil {
		return AssetDetail{}, err
	}
	detail := newAssetListItem(a, tree)

	// The open custody row is read regardless of status rather than only
	// when the asset says checked_out, so a status that has drifted out of
	// step with custody still shows the truth.
	detail.Custody, err = currentCustody(ctx, db.Pool, a.ID, actor)
	if err != nil {
		return AssetDetail{}, err
	}
	return detail, nil
}

// assetFilterSQL turns a filter into a where clause and its arguments. The
// clause is always non-empty so callers can concatenate it unconditionally.
func assetFilterSQL(tree categoryTree, filter AssetFilter) (string, []any, error) {
	conds := []string{"true"}
	var args []any

	if id := strings.TrimSpace(filter.CategoryID); id != "" {
		if _, err := tree.require(id); err != nil {
			return "", nil, err
		}
		args = append(args, tree.descendants(id))
		conds = append(conds, fmt.Sprintf(`a.category_id = any($%d::uuid[])`, len(args)))
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

func newAssetListItem(a Asset, tree categoryTree) AssetListItem {
	return AssetListItem{Asset: a, CategoryPath: tree.pathOf(a.CategoryID), PhotoURL: photoURL(a.PhotoPath)}
}

// holderColumns and holderFrom are the open-custody read that the single
// asset and the whole list share, so a holder means the same thing in the
// detail dialog and in the row behind it. They are narrower than custody.go's
// custodyColumns, which reads a whole event trail for the admin screens.
const holderColumns = `ce.asset_id, ce.id, ce.custodian_id, p.full_name, p.first_name, p.last_name,
	p.student_number, ce.checked_out_at, ce.due_at, (ce.due_at is not null and ce.due_at < now())`

const holderFrom = `
	from custody_events ce
	join profiles p on p.id = ce.custodian_id
	where ce.checked_in_at is null`

// currentCustody returns the unreturned custody event for an asset, or nil if
// it isn't out. Ordered newest-first so a stale duplicate open row (which
// the schema permits but nothing writes) reports the current holder. It takes
// a querier rather than hanging off DB because CheckInAsset reads the same
// row inside its transaction.
func currentCustody(ctx context.Context, q querier, assetID string, actor Actor) (*AssetCustody, error) {
	row := q.QueryRow(ctx, `select `+holderColumns+holderFrom+`
		and ce.asset_id = $1
		order by ce.checked_out_at desc
		limit 1`, assetID)

	_, c, err := scanHolder(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, mapPgError("get custody", err)
	}
	c.forViewer(actor)
	return &c, nil
}

// holdersByAsset is the same read for a whole browse page: one query for every
// asset in the list, keyed by asset id, instead of a round trip per row.
// distinct on keeps the newest open row per asset, matching currentCustody.
func holdersByAsset(ctx context.Context, q querier, assetIDs []string, actor Actor) (map[string]*AssetCustody, error) {
	held := map[string]*AssetCustody{}
	if len(assetIDs) == 0 {
		return held, nil
	}

	rows, err := q.Query(ctx, `select distinct on (ce.asset_id) `+holderColumns+holderFrom+`
		and ce.asset_id = any($1)
		order by ce.asset_id, ce.checked_out_at desc`, assetIDs)
	if err != nil {
		return nil, mapPgError("list custody", err)
	}
	defer rows.Close()

	for rows.Next() {
		assetID, c, err := scanHolder(rows)
		if err != nil {
			return nil, mapPgError("list custody", err)
		}
		c.forViewer(actor)
		held[assetID] = &c
	}
	if err := rows.Err(); err != nil {
		return nil, mapPgError("list custody", err)
	}
	return held, nil
}

// scanHolder reads one holderColumns row. The custodian's label is resolved
// here rather than in SQL so every screen falls back the same way.
func scanHolder(row pgx.Row) (string, AssetCustody, error) {
	var (
		assetID           string
		c                 AssetCustody
		full, first, last *string
	)
	err := row.Scan(&assetID, &c.CustodyEventID, &c.CustodianID, &full, &first, &last,
		&c.StudentNumber, &c.CheckedOutAt, &c.DueAt, &c.Overdue)
	if err != nil {
		return "", AssetCustody{}, err
	}
	c.CustodianName = displayName(first, last, full, c.StudentNumber)
	return assetID, c, nil
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
