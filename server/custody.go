package main

import (
	"net/http"

	"stockroom/internal/stockroom"
)

// The core loop over HTTP (TODO Phase 4). Every handler here is a decode,
// one call into internal/stockroom, and an encode: the rules about who may
// check out what, and when, all live one layer down so both frontends and
// any curl get the same answers.

// POST /scan  {"serial": "T7i-002"}
// The one endpoint behind every item barcode. Which barcode kind a scan is
// gets decided by the active screen, not here (CLAUDE.md §10): the sign-in
// screen posts to /auth/scan, everything else posts here.
func (d deps) handleScan(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var body struct {
		Serial string `json:"serial"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, err)
		return
	}
	result, err := d.db.ScanItem(r.Context(), actor, body.Serial)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// POST /checkout  {"asset_ids": [...], "due_at": "...", "custodian_id": "...", "override_overdue": false}
// The cart commit, called once per checkout. It either takes the whole cart
// or none of it, so a 409 here means nothing changed.
func (d deps) handleCheckout(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var in stockroom.CheckoutInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	result, err := d.db.CheckOutAssets(r.Context(), actor, in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// POST /assets/{id}/checkin  {"note": "lens cap missing"}
// Returning an item by hand, for the admin panel's overdue list and the
// detail dialog. A scan reaches the same code through /scan; the note is
// optional in both.
func (d deps) handleCheckIn(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var body struct {
		Note *string `json:"note"`
	}
	// An empty body is a check-in with no damage note, which is the common
	// case, so it is allowed rather than a 400.
	if r.ContentLength != 0 {
		if err := decodeJSON(w, r, &body); err != nil {
			writeError(w, err)
			return
		}
	}
	result, err := d.db.CheckInAsset(r.Context(), actor, r.PathValue("id"), body.Note)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// GET /custody/active
// Everything currently out, soonest due first. Admin only.
func (d deps) handleActiveCustody(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	records, err := d.db.ListActiveCustody(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, records)
}

// GET /custody/overdue
// The admin overdue screen, most late first.
func (d deps) handleOverdueCustody(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	records, err := d.db.ListOverdueCustody(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, records)
}

// GET /assets/{id}/history
// One asset's full custody trail. Admin only, unlike the current custodian
// that /assets/{id} shows everyone (CLAUDE.md §7).
func (d deps) handleAssetHistory(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	records, err := d.db.GetAssetHistory(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, records)
}

// GET /users/{id}/history
// One user's full custody trail. A non-admin may read only their own.
func (d deps) handleUserHistory(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	records, err := d.db.GetUserHistory(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, records)
}
