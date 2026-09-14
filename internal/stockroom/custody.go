package stockroom

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// The core loop (CLAUDE.md §1, steps 3 to 5): a scan decides by itself
// whether an item is coming back or going out, a cart of assets is checked
// out in one transaction, and anything checked out can be returned by anyone
// who is signed in. Custody history reads sit here too, because they read the
// same rows these writes produce.

// MaxCheckoutDays bounds how far ahead a due date may be. The frontend caps
// its date picker at the same number; this is the copy that decides, because
// a client can send anything (CLAUDE.md §7).
const MaxCheckoutDays = 7

// openCustodySQL is the one definition of "this asset is out": an unreturned
// custody row exists for it. Every check that decides between borrowed and
// on the shelf uses this predicate rather than the status column: the scan
// branch, the cart lock, the delete and status refusals. Drift between the
// two is then visible instead of duplicated (CLAUDE.md §13). The
// argument is the asset id expression, so it can name a joined column or a
// placeholder.
func openCustodySQL(assetID string) string {
	return `exists (select 1 from custody_events oc where oc.asset_id = ` + assetID + ` and oc.checked_in_at is null)`
}

// querier is the subset of pgx both *pgxpool.Pool and pgx.Tx satisfy, so the
// helpers below read the same rows inside a transaction and outside one.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// ScanAction is what the server did with a scanned barcode. The frontend
// switches on it: checked_in shows a return confirmation, detail opens the
// same popup a click opens.
type ScanAction string

const (
	ScanCheckedIn ScanAction = "checked_in"
	ScanDetail    ScanAction = "detail"
)

// ScanResult is the answer to every item scan.
type ScanResult struct {
	Action ScanAction  `json:"action"`
	Asset  AssetDetail `json:"asset"`
	// Checkable says whether this scan may lead to the cart. It is true only
	// on the detail branch for an available asset: an unavailable unit can't
	// be borrowed, and an item that was just checked in gets a confirmation
	// dialog with no add-to-cart path (design doc §8.6).
	Checkable bool `json:"checkable"`
	// ReturnedFrom is who held the item, set only when Action is
	// checked_in so the confirmation can name them.
	ReturnedFrom *AssetCustody `json:"returned_from"`
}

// CheckInResult is the outcome of a return: the asset as it now stands, plus
// the custody that was just closed.
type CheckInResult struct {
	Asset AssetDetail `json:"asset"`
	// ReturnedFrom is the custody event this check-in closed, read before it
	// was closed, so the caller can say who had the item and whether it came
	// back late.
	ReturnedFrom AssetCustody `json:"returned_from"`
}

// CheckoutInput is one cart commit. AssetIDs is the whole cart: it succeeds
// or fails as a unit.
type CheckoutInput struct {
	// CustodianID is who ends up holding the items. Blank means the actor,
	// which is the only value a non-admin may use.
	CustodianID string    `json:"custodian_id"`
	AssetIDs    []string  `json:"asset_ids"`
	DueAt       time.Time `json:"due_at"`
	// OverrideOverdue lets an admin check out to someone who already has
	// something overdue (CLAUDE.md §7). Admin-only; a non-admin sending it
	// is ErrForbidden rather than a silently ignored field.
	OverrideOverdue bool `json:"override_overdue"`
}

// CheckoutResult is what a successful cart commit hands back: enough to
// render the confirmation without a second round trip.
type CheckoutResult struct {
	CustodianID   string         `json:"custodian_id"`
	CustodianName string         `json:"custodian_name"`
	DueAt         time.Time      `json:"due_at"`
	Items         []CheckoutItem `json:"items"`
}

// CheckoutItem is one line of that confirmation.
type CheckoutItem struct {
	CustodyEventID string  `json:"custody_event_id"`
	AssetID        string  `json:"asset_id"`
	AssetTag       string  `json:"asset_tag"`
	Name           string  `json:"name"`
	SerialNumber   *string `json:"serial_number"`
}

// CustodyRecord is one custody event as every list and history screen shows
// it: the event, the asset it is about, and the three people involved
// resolved to names. The same row type serves the active list, the overdue
// list and both history reads, so those four screens can't drift apart.
type CustodyRecord struct {
	ID           string  `json:"id"`
	AssetID      string  `json:"asset_id"`
	AssetName    string  `json:"asset_name"`
	AssetTag     string  `json:"asset_tag"`
	SerialNumber *string `json:"serial_number"`

	CustodianID            string  `json:"custodian_id"`
	CustodianName          string  `json:"custodian_name"`
	CustodianStudentNumber *string `json:"custodian_student_number"`

	CheckedOutBy     string    `json:"checked_out_by"`
	CheckedOutByName string    `json:"checked_out_by_name"`
	CheckedOutAt     time.Time `json:"checked_out_at"`

	DueAt           *time.Time `json:"due_at"`
	CheckedInAt     *time.Time `json:"checked_in_at"`
	CheckedInBy     *string    `json:"checked_in_by"`
	CheckedInByName *string    `json:"checked_in_by_name"`

	ConditionOut *string `json:"condition_out"`
	ConditionIn  *string `json:"condition_in"`
	Notes        *string `json:"notes"`

	// Overdue is true only while the item is still out and past due. A late
	// return is not overdue any more; DaysOverdue keeps the record of it.
	Overdue bool `json:"overdue"`
	// DaysOverdue is whole days past the due date, counted to the return for
	// a closed event and to now for an open one. Zero when it was on time or
	// had no due date, which is what the admin list sorts on.
	DaysOverdue int `json:"days_overdue"`
}

// ScanItem is the single action behind every item barcode (CLAUDE.md §1.5).
// One scan, one decision, made from the item's current state rather than from
// anything the frontend sends:
//
//   - checked out -> returned on the spot, by whoever is signed in
//   - available or unavailable -> the detail popup, exactly as a click gives
//
// An unknown serial is ErrNotFound so the UI can say "not a Stockroom item"
// rather than failing silently.
func (db *DB) ScanItem(ctx context.Context, actor Actor, serial string) (ScanResult, error) {
	if err := RequireFullSession(actor); err != nil {
		return ScanResult{}, err
	}
	serial = strings.TrimSpace(serial)
	if serial == "" {
		return ScanResult{}, fmt.Errorf("%w: no serial number scanned", ErrInvalid)
	}

	// The open custody row decides which branch this is, not the status
	// column. They agree in every case the app itself writes; when they have
	// drifted, an item with an open row is out, and answering "not checked
	// out" to someone standing there holding it would be useless.
	var (
		id   string
		open bool
	)
	err := db.Pool.QueryRow(ctx, `
		select a.id, `+openCustodySQL("a.id")+`
		from assets a where a.serial_number = $1`, serial).Scan(&id, &open)
	if errors.Is(err, pgx.ErrNoRows) {
		return ScanResult{}, fmt.Errorf("%w: no item with serial %q", ErrNotFound, serial)
	}
	if err != nil {
		return ScanResult{}, mapPgError("scan item", err)
	}

	if open {
		// No confirmation step: a scanned item that is out is a return, and
		// the dialog opens already showing that it happened (design doc §8.6).
		in, err := db.CheckInAsset(ctx, actor, id, nil)
		if err != nil {
			return ScanResult{}, err
		}
		returned := in.ReturnedFrom
		return ScanResult{Action: ScanCheckedIn, Asset: in.Asset, ReturnedFrom: &returned}, nil
	}

	asset, err := db.GetAsset(ctx, actor, id)
	if err != nil {
		return ScanResult{}, err
	}
	return ScanResult{
		Action:    ScanDetail,
		Asset:     asset,
		Checkable: asset.Status == StatusAvailable,
	}, nil
}

// CheckOutAssets commits a cart: one custody event per asset and one status
// flip per asset, all inside a single transaction, so a cart is never half
// checked out (CLAUDE.md §1.4). Every rule is enforced here rather than in
// the UI, because the UI is a client like any other:
//
//   - a non-admin may only check out to themselves
//   - the due date must be in the future and at most MaxCheckoutDays away
//   - a custodian with anything overdue is refused, unless an admin overrides
//   - every asset must be available, or the whole cart fails naming which
//     ones weren't
func (db *DB) CheckOutAssets(ctx context.Context, actor Actor, in CheckoutInput) (CheckoutResult, error) {
	if err := RequireFullSession(actor); err != nil {
		return CheckoutResult{}, err
	}

	custodianID := strings.TrimSpace(in.CustodianID)
	if custodianID == "" {
		custodianID = actor.ID
	}
	if custodianID != actor.ID && !actor.IsAdmin {
		return CheckoutResult{}, fmt.Errorf("%w: only an admin can check out to someone else", ErrForbidden)
	}
	if in.OverrideOverdue && !actor.IsAdmin {
		return CheckoutResult{}, fmt.Errorf("%w: only an admin can override an overdue block", ErrForbidden)
	}

	ids, err := normalizeCartIDs(in.AssetIDs)
	if err != nil {
		return CheckoutResult{}, err
	}
	if err := checkDueAt(in.DueAt, time.Now()); err != nil {
		return CheckoutResult{}, err
	}

	custodian, err := db.profileByID(ctx, custodianID)
	if errors.Is(err, ErrNotFound) {
		return CheckoutResult{}, fmt.Errorf("%w: no such user", ErrNotFound)
	}
	if err != nil {
		return CheckoutResult{}, mapPgError("check out", err)
	}

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return CheckoutResult{}, fmt.Errorf("check out: %w", err)
	}
	// Rollback after a successful Commit is a no-op, so this covers every
	// early return below without a flag to track.
	defer func() { _ = tx.Rollback(ctx) }()

	if !(actor.IsAdmin && in.OverrideOverdue) {
		overdue, err := listCustody(ctx, tx, `ce.id in (select id from overdue_custody) and ce.custodian_id = $1`,
			`ce.due_at`, custodianID)
		if err != nil {
			return CheckoutResult{}, err
		}
		if len(overdue) > 0 {
			return CheckoutResult{}, fmt.Errorf("%w: return %s first", ErrOverdueBlocked, describeOverdue(overdue))
		}
	}

	items, err := lockCartAssets(ctx, tx, ids)
	if err != nil {
		return CheckoutResult{}, err
	}

	out := CheckoutResult{
		CustodianID:   custodian.ID,
		CustodianName: displayName(custodian.FirstName, custodian.LastName, custodian.FullName, custodian.StudentNumber),
		DueAt:         in.DueAt,
		Items:         make([]CheckoutItem, 0, len(items)),
	}
	for _, item := range items {
		var eventID string
		err := tx.QueryRow(ctx, `
			insert into custody_events (asset_id, custodian_id, checked_out_by, due_at, condition_out)
			values ($1, $2, $3, $4, (select condition from assets where id = $1))
			returning id`, item.AssetID, custodianID, actor.ID, in.DueAt).Scan(&eventID)
		if err != nil {
			return CheckoutResult{}, mapPgError("check out", err)
		}
		item.CustodyEventID = eventID
		out.Items = append(out.Items, item)

		if _, err := tx.Exec(ctx,
			`update assets set status = 'checked_out' where id = $1`, item.AssetID); err != nil {
			return CheckoutResult{}, mapPgError("check out", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return CheckoutResult{}, fmt.Errorf("check out: %w", err)
	}
	return out, nil
}

// CheckInAsset returns one item. Any signed-in user may return any item, not
// just their own (CLAUDE.md §7): the person carrying the camera back to the
// closet is often not the person who signed it out. The damage note, when
// given, lands on the custody event's condition_in, so it stays attached to
// that trip rather than overwriting the asset's own condition.
//
// An item that is not out is ErrConflict, not a second custody row.
func (db *DB) CheckInAsset(ctx context.Context, actor Actor, assetID string, damageNote *string) (CheckInResult, error) {
	if err := RequireFullSession(actor); err != nil {
		return CheckInResult{}, err
	}
	note := trimOptional(damageNote)

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return CheckInResult{}, fmt.Errorf("check in: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock the asset first so a concurrent check-in or checkout of the same
	// unit queues behind this one instead of both seeing it as open.
	var status AssetStatus
	err = tx.QueryRow(ctx, `select status from assets where id = $1 for update`, assetID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return CheckInResult{}, fmt.Errorf("%w: no such asset", ErrNotFound)
	}
	if err != nil {
		return CheckInResult{}, mapPgError("check in", err)
	}

	// The open custody row decides, not the status column: an asset whose
	// status drifted still has exactly one truth about whether it is out.
	held, err := currentCustody(ctx, tx, assetID, actor)
	if err != nil {
		return CheckInResult{}, err
	}
	if held == nil {
		return CheckInResult{}, fmt.Errorf("%w: item is not checked out", ErrConflict)
	}

	if _, err := tx.Exec(ctx, `
		update custody_events
		set checked_in_at = now(), checked_in_by = $2, condition_in = $3
		where id = $1`, held.CustodyEventID, actor.ID, note); err != nil {
		return CheckInResult{}, mapPgError("check in", err)
	}
	if _, err := tx.Exec(ctx,
		`update assets set status = 'available' where id = $1`, assetID); err != nil {
		return CheckInResult{}, mapPgError("check in", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return CheckInResult{}, fmt.Errorf("check in: %w", err)
	}

	asset, err := db.GetAsset(ctx, actor, assetID)
	if err != nil {
		return CheckInResult{}, err
	}
	return CheckInResult{Asset: asset, ReturnedFrom: *held}, nil
}

// ListActiveCustody is everything currently out, soonest due first. Admin
// only: the open decision of 2026-09-12 makes the *current holder of a named
// item* visible to everyone (through ListAssets, GetAsset and ScanItem), not
// the roster of who has what, which is an admin-panel view.
func (db *DB) ListActiveCustody(ctx context.Context, actor Actor) ([]CustodyRecord, error) {
	if err := RequireAdmin(actor); err != nil {
		return nil, err
	}
	return listCustody(ctx, db.Pool, `ce.id in (select id from active_custody)`,
		`ce.due_at nulls last, a.name`)
}

// ListOverdueCustody is the admin overdue screen: everything still out past
// its due date, most late first. It reads through the overdue_custody view so
// "overdue" has exactly one definition, shared with the sign-in warning and
// the checkout block.
func (db *DB) ListOverdueCustody(ctx context.Context, actor Actor) ([]CustodyRecord, error) {
	if err := RequireAdmin(actor); err != nil {
		return nil, err
	}
	return listCustody(ctx, db.Pool, `ce.id in (select id from overdue_custody)`,
		`ce.due_at, a.name`)
}

// GetAssetHistory is one asset's full custody trail, newest first. Admin only
// (CLAUDE.md §7, 2026-09-12): who holds an item *now* is open to everyone,
// but the trail of everyone who has held it is not, and the refusal is here
// rather than a hidden field in the UI because a hidden field is one fetch
// away.
func (db *DB) GetAssetHistory(ctx context.Context, actor Actor, assetID string) ([]CustodyRecord, error) {
	if err := RequireAdmin(actor); err != nil {
		return nil, err
	}
	var exists bool
	if err := db.Pool.QueryRow(ctx,
		`select exists (select 1 from assets where id = $1)`, assetID).Scan(&exists); err != nil {
		return nil, mapPgError("asset history", err)
	}
	if !exists {
		return nil, fmt.Errorf("%w: no such asset", ErrNotFound)
	}
	return listCustody(ctx, db.Pool, `ce.asset_id = $1`, `ce.checked_out_at desc`, assetID)
}

// GetUserHistory is one user's full custody trail, newest first. A non-admin
// may read their own and nobody else's; an admin may read anyone's.
func (db *DB) GetUserHistory(ctx context.Context, actor Actor, userID string) ([]CustodyRecord, error) {
	if err := RequireFullSession(actor); err != nil {
		return nil, err
	}
	if userID != actor.ID && !actor.IsAdmin {
		return nil, fmt.Errorf("%w: only an admin can read another user's history", ErrForbidden)
	}
	if _, err := db.profileByID(ctx, userID); err != nil {
		return nil, mapPgError("user history", err)
	}
	return listCustody(ctx, db.Pool, `ce.custodian_id = $1`, `ce.checked_out_at desc`, userID)
}

// normalizeCartIDs trims a cart to the distinct ids it names, preserving the
// order they were sent in. A cart is a set, so the same item twice is the
// frontend saying "this one", not a request for two.
func normalizeCartIDs(ids []string) ([]string, error) {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: no items to check out", ErrInvalid)
	}
	return out, nil
}

// checkDueAt bounds the due date. Both ends matter: a date in the past would
// be overdue the moment it was written, and the seven-day cap is the whole
// point of asking for a date at all. The message names the latest acceptable
// instant, because a date picker that offers "seven days out" at end of day
// is past the cap and its user needs to be told why.
func checkDueAt(dueAt, now time.Time) error {
	if dueAt.IsZero() {
		return fmt.Errorf("%w: a due date is required", ErrInvalid)
	}
	if !dueAt.After(now) {
		return fmt.Errorf("%w: due date must be in the future", ErrInvalid)
	}
	latest := now.Add(MaxCheckoutDays * 24 * time.Hour)
	if dueAt.After(latest) {
		return fmt.Errorf("%w: due date must be within %d days (on or before %s)",
			ErrInvalid, MaxCheckoutDays, latest.UTC().Format(time.RFC3339))
	}
	return nil
}

// lockCartAssets reads every asset in the cart with FOR UPDATE, in id order
// so two carts sharing an item can't deadlock, and refuses the whole cart
// unless all of them are available. Both refusals name the offending items:
// "conflict" alone leaves the UI unable to say which line to remove.
func lockCartAssets(ctx context.Context, q querier, ids []string) ([]CheckoutItem, error) {
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}

	rows, err := q.Query(ctx, `
		select a.id, a.asset_tag, a.name, a.serial_number, a.status, `+openCustodySQL("a.id")+`
		from assets a
		where a.id in (`+strings.Join(placeholders, ", ")+`)
		order by a.id
		for update`, args...)
	if err != nil {
		return nil, mapPgError("check out", err)
	}
	defer rows.Close()

	found := make(map[string]CheckoutItem, len(ids))
	var unavailable []string
	for rows.Next() {
		var (
			item   CheckoutItem
			status AssetStatus
			open   bool
		)
		if err := rows.Scan(&item.AssetID, &item.AssetTag, &item.Name, &item.SerialNumber, &status, &open); err != nil {
			return nil, fmt.Errorf("scan cart asset: %w", err)
		}
		found[item.AssetID] = item
		// An open custody row on an asset that calls itself available is
		// drift, not a free item; refusing keeps a second open row for the
		// same unit from ever being written.
		if status != StatusAvailable || open {
			unavailable = append(unavailable, fmt.Sprintf("%s (%s)", item.Name, statusLabel(status, open)))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, mapPgError("check out", err)
	}

	var missing []string
	for _, id := range ids {
		if _, ok := found[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: no such asset: %s", ErrNotFound, strings.Join(missing, ", "))
	}
	if len(unavailable) > 0 {
		slices.Sort(unavailable)
		return nil, fmt.Errorf("%w: not available: %s", ErrConflict, strings.Join(unavailable, ", "))
	}

	// Back into cart order, so the confirmation lists items the way the user
	// added them rather than in the id order the lock needed.
	items := make([]CheckoutItem, 0, len(ids))
	for _, id := range ids {
		items = append(items, found[id])
	}
	return items, nil
}

// statusLabel describes why an asset can't be checked out. An asset that says
// available but has an open custody row reads as checked out, because that is
// what the shelf will show.
func statusLabel(status AssetStatus, openCustody bool) string {
	if openCustody {
		return string(StatusCheckedOut)
	}
	return string(status)
}

// describeOverdue names the items blocking a checkout, capped so the message
// stays readable when someone is holding a dozen.
func describeOverdue(records []CustodyRecord) string {
	const max = 3
	names := make([]string, 0, max)
	for _, r := range records[:min(len(records), max)] {
		names = append(names, r.AssetName)
	}
	if len(records) > max {
		return fmt.Sprintf("%s and %d more", strings.Join(names, ", "), len(records)-max)
	}
	return strings.Join(names, ", ")
}

// custodyColumns is the select list behind every CustodyRecord read: the
// event, its asset, and the three people on it resolved through the same
// name fallback the detail popup uses.
const custodyColumns = `
	ce.id, ce.asset_id, a.name, a.asset_tag, a.serial_number,
	ce.custodian_id, cu.first_name, cu.last_name, cu.full_name, cu.student_number,
	ce.checked_out_by, ob.first_name, ob.last_name, ob.full_name, ob.student_number,
	ce.checked_out_at, ce.due_at, ce.checked_in_at,
	ce.checked_in_by, ib.first_name, ib.last_name, ib.full_name, ib.student_number,
	ce.condition_out, ce.condition_in, ce.notes,
	(ce.checked_in_at is null and ce.due_at is not null and ce.due_at < now()),
	case when ce.due_at is null then 0 else greatest(0, floor(
		extract(epoch from (coalesce(ce.checked_in_at, now()) - ce.due_at)) / 86400)::int) end`

const custodyFrom = `
	from custody_events ce
	join assets a on a.id = ce.asset_id
	join profiles cu on cu.id = ce.custodian_id
	join profiles ob on ob.id = ce.checked_out_by
	left join profiles ib on ib.id = ce.checked_in_by`

// listCustody runs one custody read. where and orderBy are package
// constants, never client input; args fill the placeholders in where.
func listCustody(ctx context.Context, q querier, where, orderBy string, args ...any) ([]CustodyRecord, error) {
	rows, err := q.Query(ctx,
		`select`+custodyColumns+custodyFrom+` where `+where+` order by `+orderBy, args...)
	if err != nil {
		return nil, mapPgError("list custody", err)
	}
	defer rows.Close()

	records := []CustodyRecord{}
	for rows.Next() {
		var (
			r                       CustodyRecord
			cuFirst, cuLast, cuFull *string
			obFirst, obLast, obFull *string
			obStudent               *string
			ibFirst, ibLast, ibFull *string
			ibStudent               *string
		)
		err := rows.Scan(&r.ID, &r.AssetID, &r.AssetName, &r.AssetTag, &r.SerialNumber,
			&r.CustodianID, &cuFirst, &cuLast, &cuFull, &r.CustodianStudentNumber,
			&r.CheckedOutBy, &obFirst, &obLast, &obFull, &obStudent,
			&r.CheckedOutAt, &r.DueAt, &r.CheckedInAt,
			&r.CheckedInBy, &ibFirst, &ibLast, &ibFull, &ibStudent,
			&r.ConditionOut, &r.ConditionIn, &r.Notes,
			&r.Overdue, &r.DaysOverdue)
		if err != nil {
			return nil, fmt.Errorf("scan custody: %w", err)
		}
		r.CustodianName = displayName(cuFirst, cuLast, cuFull, r.CustodianStudentNumber)
		r.CheckedOutByName = displayName(obFirst, obLast, obFull, obStudent)
		if r.CheckedInBy != nil {
			name := displayName(ibFirst, ibLast, ibFull, ibStudent)
			r.CheckedInByName = &name
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, mapPgError("list custody", err)
	}
	return records, nil
}

// AnnotateCustodyEvent attaches a damage note to a custody event that has
// already been closed.
//
// It exists because of the one asymmetry in the scan flow: scanning a
// checked-out item checks it in *immediately*, with no confirm press
// (CLAUDE.md §1.5), so by the time the surface offering "Add a note" is on
// screen the check-in has already committed and CheckInAsset's own note
// parameter is gone. Without this, the note field specced in
// docs/design/design-system.md §8.6 would have nowhere to write.
//
// The note lands on condition_in, the same column CheckInAsset writes, so
// there is one place a return's condition lives however it was entered. Any
// signed-in user may write it, matching who may check an item in at all: the
// person noticing the dent is often not the person who signed the camera out.
//
// Only a closed event takes one. An open row is ErrConflict rather than a
// silent no-op, because a note on an item still in someone's bag is either a
// mis-click or a misunderstanding of what the field is for.
func (db *DB) AnnotateCustodyEvent(ctx context.Context, actor Actor, custodyEventID string, note string) (CustodyRecord, error) {
	if err := RequireFullSession(actor); err != nil {
		return CustodyRecord{}, err
	}
	trimmed := strings.TrimSpace(note)
	if trimmed == "" {
		return CustodyRecord{}, fmt.Errorf("%w: a note is required", ErrInvalid)
	}

	var closed bool
	err := db.Pool.QueryRow(ctx,
		`select checked_in_at is not null from custody_events where id = $1`,
		custodyEventID).Scan(&closed)
	if errors.Is(err, pgx.ErrNoRows) {
		return CustodyRecord{}, fmt.Errorf("%w: no such custody event", ErrNotFound)
	}
	if err != nil {
		return CustodyRecord{}, mapPgError("annotate custody", err)
	}
	if !closed {
		return CustodyRecord{}, fmt.Errorf(
			"%w: that item is still checked out; the note goes on at check-in", ErrConflict)
	}

	if _, err := db.Pool.Exec(ctx,
		`update custody_events set condition_in = $2 where id = $1`,
		custodyEventID, trimmed); err != nil {
		return CustodyRecord{}, mapPgError("annotate custody", err)
	}

	records, err := listCustody(ctx, db.Pool, `ce.id = $1`, `ce.checked_out_at desc`, custodyEventID)
	if err != nil {
		return CustodyRecord{}, err
	}
	if len(records) == 0 {
		return CustodyRecord{}, fmt.Errorf("%w: no such custody event", ErrNotFound)
	}
	return records[0], nil
}
