package stockroom

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
)

// The admin panel's half of the asset table (design doc §8.7): enter a unit,
// edit it, put a photo on it, take it out of service, delete a mistake.
// Everything here is admin-only, gated in the package rather than the router
// so the rule holds for every caller (CLAUDE.md §7). Reads live in assets.go;
// every write below answers with the same AssetDetail those reads return, so
// the admin table can drop the response straight into the row it just edited.

// AssetInput is the create/edit form. It deliberately carries no status: a
// unit is created available, and the only status move an admin makes by hand
// is the available/unavailable toggle, which is SetAssetStatus. That keeps an
// edit to a name or a serial from ever quietly returning an item someone is
// holding.
//
// The two date fields are strings rather than time.Time so a date picker can
// send "2026-09-13"; Postgres casts both that and the RFC 3339 timestamp the
// API hands back, and rejects anything else as ErrInvalid.
type AssetInput struct {
	AssetTag           string   `json:"asset_tag"`
	Name               string   `json:"name"`
	Description        *string  `json:"description"`
	CategoryID         *string  `json:"category_id"`
	SerialNumber       *string  `json:"serial_number"`
	Condition          *string  `json:"condition"`
	PurchaseDate       *string  `json:"purchase_date"`
	PurchasePrice      *float64 `json:"purchase_price"`
	WarrantyExpiration *string  `json:"warranty_expiration"`
	PhotoPath          *string  `json:"photo_path"`
}

// normalize trims every field, requires the two that identify a unit, and
// turns blank optional strings into nil so the columns hold null rather than
// "". A blank serial has to be null and not "": the serial is unique, and two
// units with an empty string for one would collide.
func (in *AssetInput) normalize() error {
	in.AssetTag = strings.TrimSpace(in.AssetTag)
	in.Name = strings.TrimSpace(in.Name)
	if in.AssetTag == "" {
		return fmt.Errorf("%w: an asset tag is required", ErrInvalid)
	}
	if in.Name == "" {
		return fmt.Errorf("%w: a name is required", ErrInvalid)
	}
	in.Description = trimOptional(in.Description)
	in.CategoryID = trimOptional(in.CategoryID)
	in.SerialNumber = trimOptional(in.SerialNumber)
	in.Condition = trimOptional(in.Condition)
	in.PurchaseDate = trimOptional(in.PurchaseDate)
	in.WarrantyExpiration = trimOptional(in.WarrantyExpiration)
	in.PhotoPath = trimOptional(in.PhotoPath)
	if in.PurchasePrice != nil && *in.PurchasePrice < 0 {
		return fmt.Errorf("%w: purchase price cannot be negative", ErrInvalid)
	}
	return nil
}

// CreateAsset adds one physical unit. It starts available: nothing is in
// anyone's hands the moment it is typed in.
func (db *DB) CreateAsset(ctx context.Context, actor Actor, in AssetInput) (AssetDetail, error) {
	if err := RequireAdmin(actor); err != nil {
		return AssetDetail{}, err
	}
	if err := in.normalize(); err != nil {
		return AssetDetail{}, err
	}
	if err := db.requireCategory(ctx, in.CategoryID); err != nil {
		return AssetDetail{}, err
	}

	var id string
	err := db.Pool.QueryRow(ctx, `
		insert into assets (asset_tag, name, description, category_id, serial_number, condition,
		                    purchase_date, purchase_price, warranty_expiration, photo_path,
		                    status, created_by)
		values ($1, $2, $3, $4, $5, $6, $7::date, $8, $9::date, $10, 'available', $11)
		returning id`,
		in.AssetTag, in.Name, in.Description, in.CategoryID, in.SerialNumber, in.Condition,
		in.PurchaseDate, in.PurchasePrice, in.WarrantyExpiration, in.PhotoPath, actor.ID).Scan(&id)
	if err != nil {
		return AssetDetail{}, mapPgError("create asset", err)
	}
	return db.GetAsset(ctx, actor, id)
}

// UpdateAsset replaces the editable fields of one unit. Status and custody
// are not editable fields: see AssetInput.
func (db *DB) UpdateAsset(ctx context.Context, actor Actor, id string, in AssetInput) (AssetDetail, error) {
	if err := RequireAdmin(actor); err != nil {
		return AssetDetail{}, err
	}
	if err := in.normalize(); err != nil {
		return AssetDetail{}, err
	}
	if err := db.requireCategory(ctx, in.CategoryID); err != nil {
		return AssetDetail{}, err
	}

	tag, err := db.Pool.Exec(ctx, `
		update assets
		set asset_tag = $2, name = $3, description = $4, category_id = $5, serial_number = $6,
		    condition = $7, purchase_date = $8::date, purchase_price = $9,
		    warranty_expiration = $10::date, photo_path = $11
		where id = $1`,
		id, in.AssetTag, in.Name, in.Description, in.CategoryID, in.SerialNumber, in.Condition,
		in.PurchaseDate, in.PurchasePrice, in.WarrantyExpiration, in.PhotoPath)
	if err != nil {
		return AssetDetail{}, mapPgError("update asset", err)
	}
	if tag.RowsAffected() == 0 {
		return AssetDetail{}, fmt.Errorf("%w: no asset %s", ErrNotFound, id)
	}
	return db.GetAsset(ctx, actor, id)
}

// DeleteAsset removes a unit outright. It is for a mistyped row, not for gear
// that is leaving the closet: custody_events.asset_id cascades, so deleting an
// asset that has ever been checked out would take its trail with it, and the
// trail is most of the point of the system. So a unit with any custody
// history at all is refused, with the same advice the UI should show: mark it
// unavailable instead. That matches how a user with history behaves
// (DeleteUser), where Postgres refuses the delete outright.
func (db *DB) DeleteAsset(ctx context.Context, actor Actor, id string) error {
	if err := RequireAdmin(actor); err != nil {
		return err
	}

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("delete asset: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock the row before looking at its custody, the way SetAssetStatus
	// does. A checkout that lands between the check and the delete would
	// otherwise open a custody row that the cascade then takes away with the
	// asset -- which is exactly the trail this refusal exists to protect.
	var locked string
	err = tx.QueryRow(ctx, `select id from assets where id = $1 for update`, id).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: no asset %s", ErrNotFound, id)
	}
	if err != nil {
		return mapPgError("delete asset", err)
	}

	var open, everHeld bool
	err = tx.QueryRow(ctx, `
		select exists (select 1 from custody_events where asset_id = $1 and checked_in_at is null),
		       exists (select 1 from custody_events where asset_id = $1)`, id).Scan(&open, &everHeld)
	if err != nil {
		return mapPgError("delete asset", err)
	}
	switch {
	case open:
		return fmt.Errorf("%w: item is checked out; check it in first", ErrConflict)
	case everHeld:
		return fmt.Errorf("%w: item has custody history; mark it unavailable instead of deleting it", ErrConflict)
	}

	if _, err := tx.Exec(ctx, `delete from assets where id = $1`, id); err != nil {
		return mapPgError("delete asset", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("delete asset: %w", err)
	}
	return nil
}

// SetAssetStatus is the available/unavailable toggle: the one status change an
// admin makes by hand. checked_out is not a value it accepts and not a state
// it will move an item out of, because that would take the item away from
// whoever is holding it on paper while they still have it in a bag.
//
// What counts as "out" is the open custody row, not the status column, the
// same rule the core loop follows (CLAUDE.md §13). An asset whose column drifted
// to checked_out with no open row can therefore be set back to available here,
// which is how an admin repairs that drift.
func (db *DB) SetAssetStatus(ctx context.Context, actor Actor, id string, status AssetStatus) (AssetDetail, error) {
	if err := RequireAdmin(actor); err != nil {
		return AssetDetail{}, err
	}
	if status != StatusAvailable && status != StatusUnavailable {
		return AssetDetail{}, fmt.Errorf("%w: status must be %s or %s", ErrInvalid, StatusAvailable, StatusUnavailable)
	}

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return AssetDetail{}, fmt.Errorf("set asset status: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock the row so a checkout landing at the same moment queues behind
	// this and cannot slip an open custody row past the check below.
	var current AssetStatus
	err = tx.QueryRow(ctx, `select status from assets where id = $1 for update`, id).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return AssetDetail{}, fmt.Errorf("%w: no asset %s", ErrNotFound, id)
	}
	if err != nil {
		return AssetDetail{}, mapPgError("set asset status", err)
	}

	var open bool
	err = tx.QueryRow(ctx,
		`select exists (select 1 from custody_events where asset_id = $1 and checked_in_at is null)`,
		id).Scan(&open)
	if err != nil {
		return AssetDetail{}, mapPgError("set asset status", err)
	}
	if open {
		return AssetDetail{}, fmt.Errorf("%w: item is checked out; check it in first", ErrConflict)
	}

	if _, err := tx.Exec(ctx, `update assets set status = $2 where id = $1`, id, string(status)); err != nil {
		return AssetDetail{}, mapPgError("set asset status", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AssetDetail{}, fmt.Errorf("set asset status: %w", err)
	}
	return db.GetAsset(ctx, actor, id)
}

// SetAssetPhoto stores an uploaded picture as <uploads>/assets/<asset id>.<ext>
// and points the row at it. Naming the file after the id rather than the asset
// tag means renaming a tag never orphans a photo, and re-uploading replaces
// the old file instead of piling up copies (storePhoto).
//
// filename is the name the upload arrived under; only its extension is used,
// and only the handful in uploadPhotoExtensions are accepted.
func (db *DB) SetAssetPhoto(ctx context.Context, actor Actor, id, filename string, src io.Reader, uploadsDir string) (AssetDetail, error) {
	if err := RequireAdmin(actor); err != nil {
		return AssetDetail{}, err
	}
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(filename)))
	if !uploadPhotoExtensions[ext] {
		return AssetDetail{}, fmt.Errorf("%w: %q is not a photo; upload a .jpg, .png, .gif or .webp file",
			ErrInvalid, filename)
	}

	// Confirm the asset exists before writing anything, so a typo in an id
	// cannot leave a file in the uploads directory with no row pointing at it.
	var exists bool
	if err := db.Pool.QueryRow(ctx,
		`select exists (select 1 from assets where id = $1)`, id).Scan(&exists); err != nil {
		return AssetDetail{}, mapPgError("set asset photo", err)
	}
	if !exists {
		return AssetDetail{}, fmt.Errorf("%w: no asset %s", ErrNotFound, id)
	}

	// Staged, then published, then the row, then commit (photos.go). The
	// order is what keeps the file and the column agreeing: until the UPDATE
	// lands the old picture is still the one of record, and any failure after
	// it has been moved aside puts it straight back.
	staged, err := stagePhoto(uploadsDir, "assets", id, ext, src)
	if err != nil {
		return AssetDetail{}, err
	}
	if err := staged.publish(); err != nil {
		return AssetDetail{}, err
	}
	tag, err := db.Pool.Exec(ctx, `update assets set photo_path = $2 where id = $1`, id, staged.rel)
	if err != nil {
		staged.rollback()
		return AssetDetail{}, mapPgError("set asset photo", err)
	}
	// The asset can still have been deleted since the check above, in which
	// case nothing points at the file and it goes back the way it came.
	if tag.RowsAffected() == 0 {
		staged.rollback()
		return AssetDetail{}, fmt.Errorf("%w: no asset %s", ErrNotFound, id)
	}
	staged.commit()
	return db.GetAsset(ctx, actor, id)
}

// requireCategory rejects a category id that names nothing, so a unit is
// never filed under a node the browse tree has no branch for. A nil id is
// fine: an asset with no category is listed, just last.
//
// The node's depth is not checked. Every seeded unit hangs off a Model
// (CLAUDE.md §6.2) and the admin panel offers those, but a Type with no
// Categories under it yet would otherwise have nowhere to put a unit at all,
// and the browse list already handles a short path.
func (db *DB) requireCategory(ctx context.Context, id *string) error {
	if id == nil {
		return nil
	}
	ok, err := db.categoryExists(ctx, *id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: category %s", ErrNotFound, *id)
	}
	return nil
}
