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
		// ViaScanner says the code arrived at scanner speed. Missing means
		// yes, which is what every client sent before it existed. False marks
		// a student's return for review (CLAUDE.md §7).
		ViaScanner *bool `json:"via_scanner"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, err)
		return
	}
	result, err := d.db.ScanItemVia(r.Context(), actor, body.Serial, body.ViaScanner == nil || *body.ViaScanner)
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
		// Scanned is true when this confirms a scan that asked "Return it?"
		// (ScanConfirmReturn): the item was scanned, so the return is not
		// marked as unscanned.
		Scanned bool `json:"scanned"`
	}
	// An empty body is a check-in with no damage note, which is the common
	// case, so it is allowed rather than a 400.
	if r.ContentLength != 0 {
		if err := decodeJSON(w, r, &body); err != nil {
			writeError(w, err)
			return
		}
	}
	via := stockroom.ReturnedByButton
	if body.Scanned {
		via = stockroom.ReturnedByScan
	}
	result, err := d.db.CheckInAssetVia(r.Context(), actor, r.PathValue("id"), body.Note, via)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// POST /custody/{id}/note
// Attach a damage note to a custody event that is already closed. The scan
// flow checks an item in the moment the barcode is read (CLAUDE.md §1.5), so
// the note field on the confirmation surface has no check-in call left to ride
// along with; this is where it writes.
func (d deps) handleAnnotateCustody(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var body struct {
		Note string `json:"note"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, err)
		return
	}
	record, err := d.db.AnnotateCustodyEvent(r.Context(), actor, r.PathValue("id"), body.Note)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, record)
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

// POST /assets/{id}/lost {"note": "..."}
// An admin closes a loan without the item (CLAUDE.md §7).
func (d deps) handleMarkLost(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var body struct {
		Note *string `json:"note"`
	}
	if r.ContentLength != 0 {
		if err := decodeJSON(w, r, &body); err != nil {
			writeError(w, err)
			return
		}
	}
	result, err := d.db.MarkAssetLost(r.Context(), actor, r.PathValue("id"), body.Note)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// POST /assets/{id}/readd
// An admin closes a checkout from the borrower's custody history and puts
// the item back in the inventory.
func (d deps) handleReaddAsset(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	result, err := d.db.ReaddAsset(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// GET /custody/review
// Returns an admin has not looked at yet (CLAUDE.md §7).
func (d deps) handleNeedsReview(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	rows, err := d.db.ListNeedsReview(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// POST /custody/{id}/reviewed
func (d deps) handleResolveReview(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	rec, err := d.db.ResolveReview(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}
