package main

import (
	"net/http"

	"stockroom/internal/stockroom"
)

// GET /assets?category=<uuid>&status=<asset_status>&q=<text>
// The browse list. Every parameter is optional; with none of them the whole
// inventory comes back. category matches that node and everything under it,
// so a Type filters to every Model beneath it.
func (d deps) handleListAssets(w http.ResponseWriter, r *http.Request, _ stockroom.Actor) {
	q := r.URL.Query()
	assets, err := d.db.ListAssets(r.Context(), stockroom.AssetFilter{
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
// The detail popup: the asset, its category path, and who holds it.
func (d deps) handleGetAsset(w http.ResponseWriter, r *http.Request, _ stockroom.Actor) {
	asset, err := d.db.GetAsset(r.Context(), r.PathValue("id"))
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
