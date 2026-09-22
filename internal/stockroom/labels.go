package stockroom

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/go-pdf/fpdf"
)

// The label sheet: a PDF of adhesive labels, laid out in millimetres, with
// the bars drawn as vectors. See the file comment on barcode.go for why it is
// a PDF and not a print stylesheet.

// LabelRequest is what the admin panel asks for.
type LabelRequest struct {
	// AssetIDs, in the order they will be printed. An empty list is an error
	// rather than "every asset": a mis-click that prints three hundred labels
	// costs a packet of Avery sheets.
	AssetIDs []string `json:"asset_ids"`
	// Layout is a LabelLayouts key; empty means the first one.
	Layout string `json:"layout"`
	// StartAt skips this many label positions on the first sheet, so a
	// part-used sheet can be finished instead of thrown away. Nobody thinks of
	// this until the second time they print, and by then they have wasted a
	// sheet.
	StartAt int `json:"start_at"`
}

// AssetLabelsPDF renders a sheet of barcode labels for the given assets.
//
// Admin-only, enforced here rather than in the router like every other write
// (CLAUDE.md §7). It is a read, but a read of the whole catalogue at once with
// a printer attached, and the serial is the scan key: a sheet of labels is
// effectively a sheet of keys to the cupboard.
func (db *DB) AssetLabelsPDF(ctx context.Context, actor Actor, req LabelRequest) ([]byte, error) {
	if err := RequireAdmin(actor); err != nil {
		return nil, err
	}
	layout, err := findLayout(req.Layout)
	if err != nil {
		return nil, err
	}
	if len(req.AssetIDs) == 0 {
		return nil, fmt.Errorf("%w: choose at least one item to print labels for", ErrInvalid)
	}
	if req.StartAt < 0 || req.StartAt >= layout.PerSheet {
		return nil, fmt.Errorf("%w: start position must be between 0 and %d for %s",
			ErrInvalid, layout.PerSheet-1, layout.Key)
	}

	assets, err := db.assetsForLabels(ctx, req.AssetIDs)
	if err != nil {
		return nil, err
	}

	cells := make([]labelCell, 0, len(assets))
	for _, a := range assets {
		// serial_number is `not null` in the schema (migration 20260914120000)
		// and the column is still scanned into a pointer, so this cannot
		// actually be nil -- but a label with no code is a blank sticker
		// somebody discovers at the shelf, so it is refused by name rather
		// than printed.
		if a.SerialNumber == nil || strings.TrimSpace(*a.SerialNumber) == "" {
			return nil, fmt.Errorf("%w: %q has no serial number, so it cannot have a barcode", ErrInvalid, a.Name)
		}
		cells = append(cells, labelCell{Code: *a.SerialNumber, Title: a.Name})
	}
	return renderLabelSheet(layout, cells, req.StartAt)
}

// assetsForLabels reads the rows in the order the caller asked for them.
//
// Order matters and SQL will not preserve it: an admin who selected six items
// down a list expects six labels in that order, because they are about to
// peel them off a sheet and walk down a shelf. `= any($1)` returns rows in
// whatever order the planner likes, so the result is re-sorted into the
// request's order here.
//
// An id that does not exist is an error naming it, not a silently shorter
// sheet -- a missing label is discovered at the shelf, with the sheet already
// printed.
func (db *DB) assetsForLabels(ctx context.Context, ids []string) ([]Asset, error) {
	rows, err := db.Pool.Query(ctx,
		`select `+assetColumns+` from assets a where a.id = any($1)`, ids)
	if err != nil {
		return nil, mapPgError("read assets for labels", err)
	}
	defer rows.Close()

	byID := map[string]Asset{}
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		byID[a.ID] = a
	}
	if err := rows.Err(); err != nil {
		return nil, mapPgError("read assets for labels", err)
	}

	out := make([]Asset, 0, len(ids))
	for _, id := range ids {
		a, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("%w: no item with id %s", ErrNotFound, id)
		}
		out = append(out, a)
	}
	return out, nil
}

// labelCell is one sticker: what it encodes, and what a human reads on it.
type labelCell struct {
	Code  string
	Title string
}

// renderLabelSheet is the layout engine, and it is deliberately the only
// place that knows millimetres.
func renderLabelSheet(layout LabelLayout, cells []labelCell, startAt int) ([]byte, error) {
	pdf := fpdf.NewCustom(&fpdf.InitType{
		UnitStr: "mm",
		Size:    fpdf.SizeType{Wd: layout.PageW, Ht: layout.PageH},
	})
	// fpdf adds a page break when content passes the bottom margin. Every
	// position here is absolute, so an automatic break would insert a blank
	// page in the middle of a sheet and put every later label on the wrong
	// one.
	pdf.SetAutoPageBreak(false, 0)
	pdf.SetFont("Helvetica", "", 7)
	tr := latin1(pdf)

	perSheet := layout.Cols * layout.Rows
	slot := startAt
	pdf.AddPage()

	for _, cell := range cells {
		if slot >= perSheet {
			pdf.AddPage()
			slot = 0
		}
		col := slot % layout.Cols
		row := slot / layout.Cols
		x := layout.MarginL + float64(col)*layout.PitchX
		y := layout.MargT + float64(row)*layout.PitchY

		if err := drawLabel(pdf, tr, layout, x, y, cell); err != nil {
			return nil, err
		}
		slot++
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("write label pdf: %w", err)
	}
	return buf.Bytes(), nil
}

// drawLabel draws one sticker at (x, y).
//
// The layout inside a label is: the item's name across the top in small type,
// the bars, then the serial in readable text underneath. The serial appears
// twice on purpose -- once for the scanner and once for a person -- because
// the recovery path when a barcode stops scanning is somebody reading the
// number off and typing it, and a label that only a machine can read has no
// such path.
func drawLabel(pdf *fpdf.Fpdf, tr translator, layout LabelLayout, x, y float64, cell labelCell) error {
	modules, err := barcodeModules(cell.Code)
	if err != nil {
		return err
	}

	// Padding inside the label. Adhesive labels are die-cut with a tolerance
	// of about half a millimetre, and a printer's own registration adds more,
	// so ink that runs to the very edge lands on the backing sheet.
	const padX, padY = 2.0, 1.2

	// Code 128 requires a quiet zone of at least 10 modules either side. It is
	// part of the symbol, not decoration: without it a scanner cannot find the
	// start pattern, and the label simply does not read.
	const quietModules = 10
	totalModules := float64(len(modules) + 2*quietModules)

	availW := layout.LabelW - 2*padX
	availH := layout.LabelH - 2*padY
	if availW <= 0 || availH <= 0 {
		return fmt.Errorf("label layout %s is too small to draw in", layout.Key)
	}

	moduleW := availW / totalModules

	// The narrowest bar has to be wide enough for the scanner in the room.
	//
	// This is the failure this check exists to prevent: a sheet that renders
	// perfectly, prints perfectly, is stuck on thirty cameras, and does not
	// scan -- discovered by a student at the counter, one item at a time, days
	// later. A serial that is long for its label silently squeezes the modules
	// until that happens, and nothing on screen would have said so.
	//
	// minModuleMM is 0.25mm, about 10 mil, and it is **measured rather than
	// quoted**. The first version of this check used 0.19mm, the usual stated
	// floor for Code 128 under good conditions. Then the generated sheets were
	// rasterised and fed to a real decoder (`zbarimg`) at 300, 203 and 150
	// DPI, and the result was unambiguous:
	//
	//	module width   300 DPI   203 DPI   150 DPI
	//	0.518mm        reads     reads     reads
	//	0.334mm        reads     reads     reads
	//	0.245mm        reads     reads     FAILS
	//	0.194mm        reads     FAILS     FAILS
	//
	// So 0.19mm passed the check and did not decode. A threshold that admits a
	// barcode nobody can scan is worse than no threshold, because it looks
	// like it was considered.
	//
	// 0.25mm is the first width that decoded at every resolution tried bar the
	// worst, with the 0.245mm row as the evidence for where the edge actually
	// is. CLAUDE.md §10 targets plain keyboard-wedge hardware, frequently the
	// cheap kind, so the conservative side is the right side to err on.
	//
	// The practical consequence, worth knowing before picking a layout: the
	// 80-per-sheet label fits a serial of roughly nine characters. That is
	// what it is for -- batteries, SD cards, cables -- and the refusal below
	// says so in words rather than printing a sheet that fails at the counter.
	//
	// It refuses rather than shrinking the text or overflowing the label,
	// because both of those produce a sheet somebody uses.
	const minModuleMM = 0.25
	if moduleW < minModuleMM {
		return fmt.Errorf(
			"%w: %q is too long for the %s label — its barcode would print %.3fmm per bar and needs at least %.2fmm to scan reliably. Use a larger label size, or give this item a shorter serial",
			ErrInvalid, cell.Code, layout.Key, moduleW, minModuleMM)
	}

	// Type sizes and bar height are derived from the label, so the 44mm
	// battery label and the 66mm lens label are the same design at two sizes
	// rather than two designs.
	titleH := availH * 0.22
	textH := availH * 0.22
	barH := availH - titleH - textH

	// minBarMM is the height below which the item name is dropped to buy the
	// barcode more room.
	//
	// Bar height is not decoration: a scanner reads a horizontal slice, so a
	// short symbol has to be crossed almost exactly square to be read at all.
	// That is fine on a flat camera body and is the failure case on **a lens
	// barrel**, where the label curves away and only a narrow band of it faces
	// the scanner at any angle. 6mm rather than the 4mm this started at,
	// because the small layouts are precisely the ones that go on curved
	// things: on the 80-per-sheet label it moves the bars from 5.8mm to 8.0mm,
	// which is the difference between "hold it just right" and "wave it past".
	//
	// The name is what gets dropped because the serial printed underneath is
	// what a person needs (and stays), while the bars are what the scanner
	// needs. A label that a machine cannot read has no recovery path; one
	// without the item's name on it is merely less pleasant.
	const minBarMM = 6.0
	if barH < minBarMM {
		titleH = 0
		barH = availH - textH
	}

	cur := y + padY

	if titleH > 0 && cell.Title != "" {
		pdf.SetFont("Helvetica", "", ptFor(titleH))
		pdf.SetXY(x+padX, cur)
		pdf.CellFormat(availW, titleH, truncateForLabel(pdf, tr, cell.Title, availW), "", 0, "C", false, 0, "")
		cur += titleH
	}

	// The bars. Each run of set modules is one filled rectangle; adjacent set
	// modules are merged into a single wide bar rather than drawn one at a
	// time, because a stack of abutting rectangles can show hairline seams
	// where a PDF renderer antialiases each edge -- and a seam inside a wide
	// bar reads as two narrow bars.
	pdf.SetFillColor(0, 0, 0)
	barX := x + padX + float64(quietModules)*moduleW
	i := 0
	for i < len(modules) {
		if !modules[i] {
			i++
			continue
		}
		run := 0
		for i+run < len(modules) && modules[i+run] {
			run++
		}
		pdf.Rect(barX+float64(i)*moduleW, cur, float64(run)*moduleW, barH, "F")
		i += run
	}
	cur += barH

	pdf.SetFont("Helvetica", "B", ptFor(textH))
	pdf.SetXY(x+padX, cur)
	// The serial is ASCII by the time it gets here (barcodeModules refuses
	// anything else), so it needs no translation -- but it goes through the
	// same call so there is one way text reaches the page.
	pdf.CellFormat(availW, textH, tr(cell.Code), "", 0, "C", false, 0, "")

	return nil
}

// ptFor converts a height in millimetres to a font size in points that fits
// inside it, with room for descenders.
func ptFor(mm float64) float64 {
	const mmPerPoint = 25.4 / 72.0
	pt := (mm * 0.72) / mmPerPoint
	if pt < 4 {
		pt = 4
	}
	if pt > 12 {
		pt = 12
	}
	return pt
}

// truncateForLabel shortens a name until it fits, with an ellipsis.
//
// Measured with the PDF's own string width rather than a character count,
// because "Canon EF 70-200mm f/2.8L IS III USM" and "Sony A7 III" have very
// different widths at the same length, and a name that overflows does not wrap
// -- it prints across the next label.
func truncateForLabel(pdf *fpdf.Fpdf, tr translator, s string, maxW float64) string {
	s = tr(strings.TrimSpace(s))
	if pdf.GetStringWidth(s) <= maxW {
		return s
	}
	runes := []rune(s)
	for len(runes) > 1 {
		runes = runes[:len(runes)-1]
		candidate := strings.TrimSpace(string(runes)) + "…"
		if pdf.GetStringWidth(candidate) <= maxW {
			return candidate
		}
	}
	return ""
}

/* --------------------------------------------------------------- ID cards ---- */

// cardLayout is a sheet of student ID cards, 2 across and 5 down on Letter.
//
// 85.6 × 54 mm is the CR80 size every wallet, lanyard holder and existing ID
// card already is, so a card printed here goes in the sleeve the school
// already owns. Printed on card stock and guillotined, or on paper and
// laminated -- both are what a school actually does.
var cardLayout = LabelLayout{
	Key:   "id-card",
	Label: "Student ID cards — 10 per sheet (85 × 54 mm)",
	Cols:  2, Rows: 5,
	PageW: 215.9, PageH: 279.4,
	MarginL: 20.35, MargT: 4.7,
	LabelW: 85.6, LabelH: 54.0,
	PitchX: 89.6, PitchY: 54.0,
}

// UserCardsPDF renders printable ID cards carrying each student's number as a
// Code 128 barcode.
//
// This is not a nicety. Scan-to-sign-in is the headline feature (CLAUDE.md
// §1.1), and it assumes the school's existing ID cards encode the student
// number as a barcode. Plenty do not: some carry a magnetic stripe, some a
// proprietary format, some nothing at all. For those schools scan login simply
// does not work, and there was no way to fix it from inside Stockroom.
//
// Admin-only, and more obviously so than the asset labels: a student number
// signs its owner in with no password (CLAUDE.md §7), so this endpoint prints
// working credentials. That is exactly what an ID card is, which is why it is
// allowed at all, and exactly why only an admin may ask for one.
func (db *DB) UserCardsPDF(ctx context.Context, actor Actor, userIDs []string) ([]byte, error) {
	if err := RequireAdmin(actor); err != nil {
		return nil, err
	}
	if len(userIDs) == 0 {
		return nil, fmt.Errorf("%w: choose at least one person to print a card for", ErrInvalid)
	}

	rows, err := db.Pool.Query(ctx,
		`select `+profileColumns+` from profiles p where p.id = any($1)`, userIDs)
	if err != nil {
		return nil, mapPgError("read people for ID cards", err)
	}
	defer rows.Close()

	byID := map[string]Profile{}
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		byID[p.ID] = p
	}
	if err := rows.Err(); err != nil {
		return nil, mapPgError("read people for ID cards", err)
	}

	cards := make([]labelCell, 0, len(userIDs))
	for _, id := range userIDs {
		p, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("%w: no person with id %s", ErrNotFound, id)
		}
		// A roster-imported account always has a student number; one created
		// by hand in the panel might not, and a card with no number is a card
		// nobody can sign in with. Named, not skipped.
		if p.StudentNumber == nil || strings.TrimSpace(*p.StudentNumber) == "" {
			return nil, fmt.Errorf("%w: %s has no student number, so there is nothing to put on a card",
				ErrInvalid, displayName(p.FirstName, p.LastName, p.FullName, p.StudentNumber))
		}
		cards = append(cards, labelCell{Code: *p.StudentNumber, Title: displayName(p.FirstName, p.LastName, p.FullName, p.StudentNumber)})
	}

	return renderCardSheet(cards)
}

func renderCardSheet(cards []labelCell) ([]byte, error) {
	layout := cardLayout
	pdf := fpdf.NewCustom(&fpdf.InitType{
		UnitStr: "mm",
		Size:    fpdf.SizeType{Wd: layout.PageW, Ht: layout.PageH},
	})
	pdf.SetAutoPageBreak(false, 0)
	tr := latin1(pdf)

	perSheet := layout.Cols * layout.Rows
	slot := 0
	pdf.AddPage()

	for _, card := range cards {
		if slot >= perSheet {
			pdf.AddPage()
			slot = 0
		}
		x := layout.MarginL + float64(slot%layout.Cols)*layout.PitchX
		y := layout.MargT + float64(slot/layout.Cols)*layout.PitchY
		if err := drawCard(pdf, tr, layout, x, y, card); err != nil {
			return nil, err
		}
		slot++
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("write ID card pdf: %w", err)
	}
	return buf.Bytes(), nil
}

// drawCard draws one ID card: a cut guide, the name, the barcode, the number.
//
// The cut guide is a hairline rectangle rather than crop marks. Crop marks are
// what a print shop wants; a teacher with scissors wants a line to cut along,
// and a 0.1mm grey line disappears into the cut.
func drawCard(pdf *fpdf.Fpdf, tr translator, layout LabelLayout, x, y float64, card labelCell) error {
	modules, err := barcodeModules(card.Code)
	if err != nil {
		return err
	}

	pdf.SetDrawColor(180, 180, 180)
	pdf.SetLineWidth(0.1)
	pdf.Rect(x, y, layout.LabelW, layout.LabelH, "D")

	const pad = 5.0
	availW := layout.LabelW - 2*pad

	pdf.SetTextColor(0, 0, 0)
	pdf.SetFont("Helvetica", "B", 13)
	pdf.SetXY(x+pad, y+pad)
	pdf.CellFormat(availW, 7, truncateForLabel(pdf, tr, card.Title, availW), "", 0, "C", false, 0, "")

	pdf.SetFont("Helvetica", "", 8)
	pdf.SetTextColor(110, 110, 110)
	pdf.SetXY(x+pad, y+pad+7)
	pdf.CellFormat(availW, 4, tr("Stockroom - scan to sign in"), "", 0, "C", false, 0, "")
	pdf.SetTextColor(0, 0, 0)

	const quietModules = 10
	totalModules := float64(len(modules) + 2*quietModules)
	moduleW := availW / totalModules
	barY := y + pad + 13
	barH := 20.0

	pdf.SetFillColor(0, 0, 0)
	barX := x + pad + float64(quietModules)*moduleW
	i := 0
	for i < len(modules) {
		if !modules[i] {
			i++
			continue
		}
		run := 0
		for i+run < len(modules) && modules[i+run] {
			run++
		}
		pdf.Rect(barX+float64(i)*moduleW, barY, float64(run)*moduleW, barH, "F")
		i += run
	}

	pdf.SetFont("Helvetica", "B", 11)
	pdf.SetXY(x+pad, barY+barH+1)
	pdf.CellFormat(availW, 5, tr(card.Code), "", 0, "C", false, 0, "")
	return nil
}

/* -------------------------------------------------------------- encoding ---- */

// translator converts a Go UTF-8 string into the byte encoding the PDF's
// built-in fonts actually use.
type translator = func(string) string

// latin1 builds the translator for a document.
//
// This is not a nicety either, and the bug it fixes was visible on the very
// first card sheet: fpdf's core fonts are single-byte, so a UTF-8 string
// written straight to the page comes out as mojibake -- "Stockroom — scan"
// printed as "Stockroom â€" scan". Our own copy is now plain ASCII, but the
// names are not ours: a school with a student called José, Zoë or Müller would
// have printed that student a card with their name mangled on it, which is a
// worse thing to hand somebody than almost any other bug in this file.
//
// The limit, stated rather than hidden: this maps the Windows-1252 range,
// which covers Western European Latin and not Greek, Cyrillic, Arabic or CJK.
// A name outside it loses those characters rather than becoming nonsense, and
// the barcode -- which is the part that has to work -- is unaffected, because
// it encodes the student number and that is ASCII by construction. Supporting
// the rest means embedding a Unicode TTF in the binary, which is a real option
// if a school ever needs it and is several megabytes for a case none has yet.
func latin1(pdf *fpdf.Fpdf) translator {
	return pdf.UnicodeTranslatorFromDescriptor("")
}
