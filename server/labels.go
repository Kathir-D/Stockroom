package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"stockroom/internal/stockroom"
)

// Barcodes and printable sheets (TEMPLATE-TODO Phase B).
//
// Three routes, all admin-only inside the package. Two of them answer with a
// PDF and one with a PNG, which makes them the only handlers here that do not
// write JSON -- so they set Content-Disposition themselves and say what the
// file is called. A browser that downloads `download.pdf` for every sheet is
// a downloads folder nobody can use a week later.

// GET /labels/layouts
// The picker's options. Served from the package so the frontend cannot invent
// a layout key the renderer does not have.
func (d deps) handleLabelLayouts(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	if err := stockroom.RequireAdmin(actor); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stockroom.LabelLayouts())
}

// POST /assets/labels.pdf
// A sheet of barcode labels for the chosen items.
//
// POST rather than GET with `?ids=`, which is what the original plan said.
// A department printing labels for two hundred newly-imported items would put
// two hundred UUIDs in a query string -- about 7 KB -- and while Go's own
// default header limit would take it, every proxy, browser and access log in
// between has its own opinion, and the failure is a truncated list that prints
// a shorter sheet than was asked for. A body has no such limit. The response
// is still a file; only the request shape changed.
func (d deps) handlePrintAssetLabels(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var req stockroom.LabelRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	pdf, err := d.db.AssetLabelsPDF(r.Context(), actor, req)
	if err != nil {
		writeError(w, err)
		return
	}
	writePDF(w, fmt.Sprintf("stockroom-labels-%d.pdf", len(req.AssetIDs)), pdf)
}

// POST /users/cards.pdf
// Printable student ID cards carrying each number as a barcode.
func (d deps) handlePrintUserCards(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	var req struct {
		UserIDs []string `json:"user_ids"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	pdf, err := d.db.UserCardsPDF(r.Context(), actor, req.UserIDs)
	if err != nil {
		writeError(w, err)
		return
	}
	writePDF(w, fmt.Sprintf("stockroom-id-cards-%d.pdf", len(req.UserIDs)), pdf)
}

// GET /assets/{id}/barcode.png?width=600
// One barcode, as an image.
//
// This is the asset dialog's inline render and the download button behind it:
// one endpoint, two features. It is also the recovery path for the failure a
// school will actually have -- a sticker peels off a lens barrel, and they
// find the item by name, read the serial and print one replacement.
func (d deps) handleAssetBarcodePNG(w http.ResponseWriter, r *http.Request, actor stockroom.Actor) {
	asset, err := d.db.GetAsset(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if asset.SerialNumber == nil || strings.TrimSpace(*asset.SerialNumber) == "" {
		writeError(w, fmt.Errorf("%w: this item has no serial number, so it has no barcode", stockroom.ErrInvalid))
		return
	}

	width, _ := strconv.Atoi(r.URL.Query().Get("width"))

	// Revalidated every time rather than cached for an hour. The URL names
	// the asset, but the image is the serial, and an admin can change the
	// serial: a cached PNG would then be the old barcode on the one screen
	// that exists to replace a lost sticker, scanning as the wrong item. The
	// tag is a hash so the serial -- a scan key -- stays out of the header.
	etag := barcodeETag(*asset.SerialNumber, width)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	png, err := stockroom.BarcodePNG(*asset.SerialNumber, width)
	if err != nil {
		writeError(w, err)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	// inline, not attachment: the common case by far is an <img> in the asset
	// dialog. The download button adds ?download=1 when somebody wants a file.
	disposition := "inline"
	if r.URL.Query().Get("download") != "" {
		disposition = "attachment"
	}
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("%s; filename=%q", disposition, safeFilename(*asset.SerialNumber)+".png"))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(png)
}

// barcodeETag names one rendering: the serial and the requested width.
func barcodeETag(serial string, width int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s", width, serial)))
	return `"` + hex.EncodeToString(sum[:12]) + `"`
}

// etagMatches reports whether an If-None-Match header names tag.
func etagMatches(header, tag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "W/")
		if candidate == tag || candidate == "*" {
			return true
		}
	}
	return false
}

func writePDF(w http.ResponseWriter, filename string, body []byte) {
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	// A label sheet is generated per request and must never be re-served: the
	// admin who prints, fixes a typo and prints again has to get the second
	// sheet.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// safeFilename keeps a serial usable as a download name.
//
// A serial is admin-entered free text, and it lands in a Content-Disposition
// header. Quoting alone is not enough: a CR or LF in the value would end the
// header and let the rest be read as another one, which is header injection.
// Anything that is not a plain filename character becomes a dash.
func safeFilename(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "barcode"
	}
	return out
}
