package stockroom

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// Kits (TODO Phase 8): a named bundle of units that goes out and comes back
// together -- "Kit #1 = this camera, this lens, this bag".
//
// A kit is a *label over assets*, never a thing that can itself be checked
// out. Nothing here writes a custody row: adding a kit to the cart expands it
// into its asset ids in the frontend, and CheckOutAssets then commits them the
// way it commits any cart, one custody event per asset, unchanged (CLAUDE.md
// §2). That is the whole reason the feature is small: custody stays a fact
// about a unit, so the history, the overdue rule, the scan branch and the
// backup all keep working without knowing kits exist.
//
// Who may do what follows the rest of the app: reading a kit is any full
// session, because a student has to be able to put one in a cart; building and
// editing one is admin, gated here rather than in the router (CLAUDE.md §7).

// KitInput is the create/edit form. Membership is not part of it: assets go in
// and out through AddAssetToKit and RemoveAssetFromKit, one press per unit, so
// an edit to a kit's name can never silently empty it.
type KitInput struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
}

func (in *KitInput) normalize() error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return fmt.Errorf("%w: a kit name is required", ErrInvalid)
	}
	in.Description = trimOptional(in.Description)
	return nil
}

// KitDetail is a kit as every screen shows it: the row, its units as full
// browse rows (so a kit list can show status and current holder without a
// second request per unit), and the counts a caller would otherwise recompute.
type KitDetail struct {
	Kit
	// Items are in browse order -- category document order, available first --
	// the same order the shelf list uses, so a kit reads like a small browse
	// list rather than like insertion order nobody chose.
	Items []AssetListItem `json:"items"`

	Available   int `json:"available"`
	CheckedOut  int `json:"checked_out"`
	Unavailable int `json:"unavailable"`

	// Checkable is true only when every unit is on the shelf. A kit is taken
	// whole or not at all: half a kit is a camera with no lens, and the person
	// holding it finds out at the shoot. An empty kit is not checkable either,
	// because "check out nothing" is a press that does nothing.
	Checkable bool `json:"checkable"`
}

// ListKits is every kit with its units, by name. Any full session: a student
// has to be able to see what is in a kit to decide to take it.
func (db *DB) ListKits(ctx context.Context, actor Actor) ([]KitDetail, error) {
	if err := RequireFullSession(actor); err != nil {
		return nil, err
	}
	return db.kitsWhere(ctx, actor, `true`, `lower(k.name)`)
}

// GetKit is one kit, in the same shape a list row carries.
func (db *DB) GetKit(ctx context.Context, actor Actor, id string) (KitDetail, error) {
	if err := RequireFullSession(actor); err != nil {
		return KitDetail{}, err
	}
	kits, err := db.kitsWhere(ctx, actor, `k.id = $1`, `lower(k.name)`, id)
	if err != nil {
		return KitDetail{}, err
	}
	if len(kits) == 0 {
		return KitDetail{}, fmt.Errorf("%w: no kit %s", ErrNotFound, id)
	}
	return kits[0], nil
}

// CreateKit adds an empty kit. Empty on purpose: a kit is named after the bag
// it lives in, and the units go in one at a time as they are found.
func (db *DB) CreateKit(ctx context.Context, actor Actor, in KitInput) (KitDetail, error) {
	if err := RequireAdmin(actor); err != nil {
		return KitDetail{}, err
	}
	if err := in.normalize(); err != nil {
		return KitDetail{}, err
	}

	var id string
	err := db.Pool.QueryRow(ctx, `
		insert into kits (name, description) values ($1, $2) returning id`,
		in.Name, in.Description).Scan(&id)
	if err != nil {
		return KitDetail{}, mapPgError("create kit", err)
	}
	return db.GetKit(ctx, actor, id)
}

// UpdateKit renames a kit or rewrites its description. Membership is untouched
// (see KitInput).
func (db *DB) UpdateKit(ctx context.Context, actor Actor, id string, in KitInput) (KitDetail, error) {
	if err := RequireAdmin(actor); err != nil {
		return KitDetail{}, err
	}
	if err := in.normalize(); err != nil {
		return KitDetail{}, err
	}

	tag, err := db.Pool.Exec(ctx,
		`update kits set name = $2, description = $3 where id = $1`, id, in.Name, in.Description)
	if err != nil {
		return KitDetail{}, mapPgError("update kit", err)
	}
	if tag.RowsAffected() == 0 {
		return KitDetail{}, fmt.Errorf("%w: no kit %s", ErrNotFound, id)
	}
	return db.GetKit(ctx, actor, id)
}

// DeleteKit removes the bundle and nothing else.
//
// It is allowed even while the units are out, unlike DeleteAsset, and the
// asymmetry is the point: deleting an asset would cascade its custody_events
// and take the trail with it, while a kit owns no custody at all. kit_items
// cascades, so what disappears is the grouping; every unit, its status and its
// whole history are exactly as they were.
func (db *DB) DeleteKit(ctx context.Context, actor Actor, id string) error {
	if err := RequireAdmin(actor); err != nil {
		return err
	}
	tag, err := db.Pool.Exec(ctx, `delete from kits where id = $1`, id)
	if err != nil {
		return mapPgError("delete kit", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: no kit %s", ErrNotFound, id)
	}
	return nil
}

// AddAssetToKit puts one unit in a kit.
//
// An asset belongs to at most one kit, enforced by kit_items_asset_key
// (migration 20260918090000). The refusal is read back here rather than
// pre-checked, so there is no window between deciding a unit is free and
// claiming it, and the message names the kit that already has it -- which is
// the only thing the admin needs in order to fix it.
func (db *DB) AddAssetToKit(ctx context.Context, actor Actor, kitID, assetID string) (KitDetail, error) {
	if err := RequireAdmin(actor); err != nil {
		return KitDetail{}, err
	}

	_, err := db.Pool.Exec(ctx,
		`insert into kit_items (kit_id, asset_id) values ($1, $2)`, kitID, assetID)
	if err != nil {
		return KitDetail{}, db.explainKitInsert(ctx, kitID, assetID, err)
	}
	return db.GetKit(ctx, actor, kitID)
}

// RemoveAssetFromKit takes one unit out. A unit that was not in the kit is
// ErrNotFound rather than a silent success: the two look identical afterwards,
// and only one of them means the press did what was intended.
func (db *DB) RemoveAssetFromKit(ctx context.Context, actor Actor, kitID, assetID string) (KitDetail, error) {
	if err := RequireAdmin(actor); err != nil {
		return KitDetail{}, err
	}

	tag, err := db.Pool.Exec(ctx,
		`delete from kit_items where kit_id = $1 and asset_id = $2`, kitID, assetID)
	if err != nil {
		return KitDetail{}, mapPgError("remove from kit", err)
	}
	if tag.RowsAffected() == 0 {
		// Two reasons, and they want different answers: the kit is gone, or
		// the unit was never in it.
		var exists bool
		if err := db.Pool.QueryRow(ctx,
			`select exists (select 1 from kits where id = $1)`, kitID).Scan(&exists); err != nil {
			return KitDetail{}, mapPgError("remove from kit", err)
		}
		if !exists {
			return KitDetail{}, fmt.Errorf("%w: no kit %s", ErrNotFound, kitID)
		}
		return KitDetail{}, fmt.Errorf("%w: that item is not in this kit", ErrNotFound)
	}
	return db.GetKit(ctx, actor, kitID)
}

// KitItemRef names one unit of a kit in a result, without the whole row.
type KitItemRef struct {
	AssetID      string  `json:"asset_id"`
	Name         string  `json:"name"`
	SerialNumber *string `json:"serial_number"`
}

// KitReturnProblem is a unit CheckInKit could not return, and why.
type KitReturnProblem struct {
	KitItemRef
	Reason string `json:"reason"`
}

// KitCheckInResult is what a kit return did, unit by unit. Three buckets
// rather than one count, because the three mean different things to the person
// standing at the closet: these came back, these were already here, and this
// one needs somebody to look at it.
type KitCheckInResult struct {
	Kit       Kit                `json:"kit"`
	Returned  []CheckInResult    `json:"returned"`
	AlreadyIn []KitItemRef       `json:"already_in"`
	Failed    []KitReturnProblem `json:"failed"`
}

// CheckInKit returns every unit of a kit that is currently out. Any full
// session, like CheckInAsset: whoever carries the bag back is often not whoever
// signed it out (CLAUDE.md §7).
//
// Deliberately *not* one transaction. A checkout is all-or-nothing because a
// half-filled cart is a decision nobody made; a return is the opposite -- the
// units are physically on the counter, and refusing all four because one of
// them was already back would leave the database claiming somebody still holds
// items they returned. So each unit is checked in on its own and the result
// says which is which, with the same per-item honesty the backup surfaces use.
func (db *DB) CheckInKit(ctx context.Context, actor Actor, kitID string) (KitCheckInResult, error) {
	if err := RequireFullSession(actor); err != nil {
		return KitCheckInResult{}, err
	}
	kit, err := db.GetKit(ctx, actor, kitID)
	if err != nil {
		return KitCheckInResult{}, err
	}

	out := KitCheckInResult{
		Kit:       kit.Kit,
		Returned:  []CheckInResult{},
		AlreadyIn: []KitItemRef{},
		Failed:    []KitReturnProblem{},
	}
	for _, item := range kit.Items {
		ref := KitItemRef{AssetID: item.ID, Name: item.Name, SerialNumber: item.SerialNumber}
		if item.Custody == nil {
			out.AlreadyIn = append(out.AlreadyIn, ref)
			continue
		}
		// No damage note: a kit return is one press over several units, so
		// there is no unit for a note to be about. A note goes on the unit
		// through AnnotateCustodyEvent afterwards, which is where the scan
		// flow already puts it (CLAUDE.md §13, 2026-09-14).
		done, err := db.CheckInAsset(ctx, actor, item.ID, nil)
		if err != nil {
			out.Failed = append(out.Failed, KitReturnProblem{KitItemRef: ref, Reason: err.Error()})
			continue
		}
		out.Returned = append(out.Returned, done)
	}
	return out, nil
}

// kitsWhere is the one read behind every kit payload: the kits matching where,
// then their units in a single second query rather than one per kit, then one
// category tree and one holder read for the whole page. A list of kits costs
// four round trips whatever its length.
func (db *DB) kitsWhere(ctx context.Context, actor Actor, where, orderBy string, args ...any) ([]KitDetail, error) {
	rows, err := db.Pool.Query(ctx,
		`select k.id, k.name, k.description, k.created_at from kits k where `+where+` order by `+orderBy, args...)
	if err != nil {
		return nil, mapPgError("list kits", err)
	}
	defer rows.Close()

	kits := []KitDetail{}
	index := map[string]int{}
	for rows.Next() {
		var k Kit
		if err := rows.Scan(&k.ID, &k.Name, &k.Description, &k.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan kit: %w", err)
		}
		index[k.ID] = len(kits)
		kits = append(kits, KitDetail{Kit: k, Items: []AssetListItem{}})
	}
	if err := rows.Err(); err != nil {
		return nil, mapPgError("list kits", err)
	}
	if len(kits) == 0 {
		return kits, nil
	}

	ids := make([]string, 0, len(kits))
	for _, k := range kits {
		ids = append(ids, k.ID)
	}
	members, assetIDs, err := kitMembers(ctx, db.Pool, ids)
	if err != nil {
		return nil, err
	}

	tree, err := loadCategoryTree(ctx, db.Pool)
	if err != nil {
		return nil, err
	}
	held, err := holdersByAsset(ctx, db.Pool, assetIDs, actor)
	if err != nil {
		return nil, err
	}

	for kitID, assets := range members {
		kit := &kits[index[kitID]]
		for _, a := range assets {
			item := newAssetListItem(a, tree)
			item.Custody = held[a.ID]
			kit.Items = append(kit.Items, item)
		}
		sortBrowseList(kit.Items, tree)
	}
	for i := range kits {
		kits[i].summarize()
	}
	return kits, nil
}

// summarize counts the three states and decides whether the kit can go in a
// cart. A unit with an open custody row counts as out whatever its status
// column says, which is the rule the rest of the app follows (CLAUDE.md §13).
func (k *KitDetail) summarize() {
	for _, item := range k.Items {
		switch {
		case item.Custody != nil || item.Status == StatusCheckedOut:
			k.CheckedOut++
		case item.Status == StatusAvailable:
			k.Available++
		default:
			k.Unavailable++
		}
	}
	k.Checkable = len(k.Items) > 0 && k.Available == len(k.Items)
}

// kitMembers reads the units of every kit, keyed by kit id, and returns the
// flat asset-id list the holder read needs.
//
// Two queries rather than one join, because the join would put kit_id in front
// of assetColumns and scanAsset reads that list positionally: every future
// column added to assets would have to be added here too, in the right place,
// or the scan would silently shift. The membership table is a couple of
// hundred rows at most, so the second round trip costs nothing worth that.
func kitMembers(ctx context.Context, q querier, kitIDs []string) (map[string][]Asset, []string, error) {
	rows, err := q.Query(ctx,
		`select kit_id, asset_id from kit_items where kit_id = any($1)`, kitIDs)
	if err != nil {
		return nil, nil, mapPgError("list kit items", err)
	}
	defer rows.Close()

	membership := map[string][]string{}
	assetIDs := []string{}
	for rows.Next() {
		var kitID, assetID string
		if err := rows.Scan(&kitID, &assetID); err != nil {
			return nil, nil, fmt.Errorf("scan kit item: %w", err)
		}
		membership[kitID] = append(membership[kitID], assetID)
		assetIDs = append(assetIDs, assetID)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, mapPgError("list kit items", err)
	}
	if len(assetIDs) == 0 {
		return map[string][]Asset{}, assetIDs, nil
	}

	assets, err := assetsByID(ctx, q, assetIDs)
	if err != nil {
		return nil, nil, err
	}

	members := map[string][]Asset{}
	for kitID, ids := range membership {
		for _, id := range ids {
			// A kit_items row whose asset vanished is impossible (the foreign
			// key cascades), so a miss here would be a bug rather than a state
			// to render; skipping keeps the list drawable either way.
			if a, ok := assets[id]; ok {
				members[kitID] = append(members[kitID], a)
			}
		}
	}
	return members, assetIDs, nil
}

// assetsByID reads a set of assets in one query, keyed by id.
func assetsByID(ctx context.Context, q querier, ids []string) (map[string]Asset, error) {
	rows, err := q.Query(ctx, `select `+assetColumns+` from assets a where a.id = any($1)`, ids)
	if err != nil {
		return nil, mapPgError("list assets", err)
	}
	defer rows.Close()

	out := make(map[string]Asset, len(ids))
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		out[a.ID] = a
	}
	if err := rows.Err(); err != nil {
		return nil, mapPgError("list assets", err)
	}
	return out, nil
}

// explainKitInsert turns the failure of an INSERT into kit_items into a
// message an admin can act on. The database refuses three different things
// through two error codes, and "conflict" alone leaves the panel unable to say
// which.
func (db *DB) explainKitInsert(ctx context.Context, kitID, assetID string, err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return mapPgError("add to kit", err)
	}

	switch pgErr.Code {
	case "23505": // unique_violation: the asset is already in a kit
		var (
			otherKit  string
			sameKit   bool
			assetName string
		)
		readErr := db.Pool.QueryRow(ctx, `
			select k.name, k.id = $2, a.name
			from kit_items ki
			join kits k on k.id = ki.kit_id
			join assets a on a.id = ki.asset_id
			where ki.asset_id = $1`, assetID, kitID).Scan(&otherKit, &sameKit, &assetName)
		if readErr != nil {
			// The row that caused the violation is gone already, so say the
			// plain thing rather than inventing a kit name.
			return fmt.Errorf("%w: that item is already in a kit", ErrConflict)
		}
		if sameKit {
			return fmt.Errorf("%w: %s is already in this kit", ErrConflict, assetName)
		}
		return fmt.Errorf("%w: %s is already in %q; an item belongs to one kit", ErrConflict, assetName, otherKit)
	case "23503": // foreign_key_violation: the kit or the asset does not exist
		if strings.Contains(pgErr.ConstraintName, "asset") {
			return fmt.Errorf("%w: no asset %s", ErrNotFound, assetID)
		}
		return fmt.Errorf("%w: no kit %s", ErrNotFound, kitID)
	}
	return mapPgError("add to kit", err)
}
