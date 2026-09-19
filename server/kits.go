package main

import (
	"net/http"

	"stockroom/internal/stockroom"
)

// Kits over HTTP (TODO Phase 8). Same shape as every other handler here: a
// decode, one call into internal/stockroom, an encode. Who may read a kit and
// who may build one is decided one layer down, so a kit route is no different
// from an asset route to anyone holding a token.
//
// There is no kit checkout route, and deliberately: a kit goes out through
// POST /checkout like any other cart, because the frontend expands it into its
// asset ids before it commits (CLAUDE.md §2). One checkout path means the
// 7-day cap, the overdue block and the custodian rule cannot differ between a
// kit and a handful of units.

// GET /kits
// Every kit with its units, for the kits screen and the command palette.
func (d deps) handleListKits(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	kits, err := d.db.ListKits(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, kits)
}

// GET /kits/{id}
func (d deps) handleGetKit(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	kit, err := d.db.GetKit(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, kit)
}

// POST /kits  {"name": "Kit #1", "description": "..."}
// Admin. Creates an empty kit; the units go in one at a time below.
func (d deps) handleCreateKit(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var in stockroom.KitInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	kit, err := d.db.CreateKit(r.Context(), actor, in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, kit)
}

// PUT /kits/{id}
// Admin. Renames a kit; membership is untouched (see stockroom.KitInput).
func (d deps) handleUpdateKit(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var in stockroom.KitInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	kit, err := d.db.UpdateKit(r.Context(), actor, r.PathValue("id"), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, kit)
}

// DELETE /kits/{id}
// Admin. Removes the grouping only: no asset, status or custody row moves.
func (d deps) handleDeleteKit(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	if err := d.db.DeleteKit(r.Context(), actor, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /kits/{id}/items  {"asset_id": "..."}
// Admin. 409 when the unit is already in a kit, naming which one.
func (d deps) handleAddKitItem(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var body struct {
		AssetID string `json:"asset_id"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, err)
		return
	}
	kit, err := d.db.AddAssetToKit(r.Context(), actor, r.PathValue("id"), body.AssetID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, kit)
}

// DELETE /kits/{id}/items/{assetId}
// Admin. 404 when that unit was not in the kit, because a no-op and a removal
// look identical afterwards and only one of them is what was meant.
func (d deps) handleRemoveKitItem(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	kit, err := d.db.RemoveAssetFromKit(r.Context(), actor, r.PathValue("id"), r.PathValue("assetId"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, kit)
}

// POST /kits/{id}/checkin
// Any full session, like a single check-in. The response is per unit: what
// came back, what was already here, and what failed.
func (d deps) handleCheckInKit(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	result, err := d.db.CheckInKit(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
