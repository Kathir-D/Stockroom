package main

import (
	"net/http"

	"stockroom/internal/stockroom"
)

// GET /assets?category=<uuid>&status=<asset_status>&q=<text>
// The browse list. Every parameter is optional; with none of them the whole
// inventory comes back. category matches that node and everything under it,
// so a Type filters to every Model beneath it. Rows carry the current holder,
// which is why the actor reaches the package: what a row shows of that holder
// depends on who is asking.
func (d deps) handleListAssets(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	q := r.URL.Query()
	assets, err := d.db.ListAssets(r.Context(), actor, stockroom.AssetFilter{
		CategoryID: q.Get("category"),
		Status:     stockroom.AssetStatus(q.Get("status")),
		Search:     q.Get("q"),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, assets)
}

// GET /assets/{id}
// The detail popup: the asset, its category path, and who holds it. Same shape
// as one row of the list above.
func (d deps) handleGetAsset(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	asset, err := d.db.GetAsset(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, asset)
}

// GET /categories/tree
// The left-hand filter tree, Type -> Category -> Model, in one call.
func (d deps) handleCategoryTree(w http.ResponseWriter, r *http.Request, _ stockroom.Actor) {
	tree, err := d.db.GetCategoryTree(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tree)
}
