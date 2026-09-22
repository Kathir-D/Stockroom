package stockroom

import (
	"bytes"
	"fmt"
	"image/png"
	"strings"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/code128"
)

// Barcodes, which is the one thing the whole product assumes exists and had
// no way to produce. Scanning is the single "track it" action (CLAUDE.md
// §1.5), so until this file a school could install Stockroom, enter three
// hundred items, and own a system nobody could use.
//
// Two outputs from one generator:
//
//   - a PNG, for the asset dialog on screen and for somebody who wants to drop
//     one barcode into a document;
//   - a PDF sheet of adhesive labels, at exact physical dimensions.
//
// **Why a server-generated PDF and not a print stylesheet.** A browser print
// page is the obvious answer and it does not survive contact with a sheet of
// adhesive labels. Browsers apply their own page margins, their own scaling
// ("shrink to fit"), and a user-visible margin setting that defaults
// differently across Chrome, Safari and Firefox; two millimetres of drift puts
// every label on the sheet slightly further off than the last, and the sheet
// is ruined rather than merely ugly. A PDF laid out in millimetres prints the
// same everywhere, needs no internet, and lives inside the one binary.
//
// **Why the bars are vector rectangles and not an image.** A barcode is a
// pattern of exact widths, and a scanner reads width ratios. Scaling a bitmap
// into a PDF box resamples those widths -- a module that should be 0.33mm
// becomes 0.31mm here and 0.35mm there depending on where the pixel grid
// lands, and the failure is a label that scans on the developer's laser
// printer and not on the school's. Drawing each bar as a filled rectangle
// keeps the ratios exact at any printer resolution.

// Code128 is the symbology. It encodes the full ASCII range, so a serial like
// `T7IBAT-001` fits, and it is denser than Code 39 for the same data -- which
// matters on a battery, where the label is 44mm wide. Every keyboard-wedge
// scanner on the market reads it without configuration (CLAUDE.md §10).

// LabelLayout is one sheet geometry. Everything is millimetres, and the
// numbers are the manufacturer's rather than measured, because the whole point
// is to match a sheet somebody bought.
type LabelLayout struct {
	// Key is what the API takes and the UI sends.
	Key string `json:"key"`
	// Label is how the picker names it, in the words on the packet.
	Label string `json:"label"`
	// Paper is "US Letter" or "A4", shown in the picker because choosing the
	// wrong one is the single easiest way to waste a sheet and the hardest to
	// notice before printing.
	Paper string `json:"paper"`
	// PerSheet is Cols*Rows, precomputed because every caller wants it.
	PerSheet int `json:"per_sheet"`
	// MaxSerialChars is roughly how long a serial may be before the bars get
	// too narrow to scan on this label (see minModuleMM in labels.go). It is
	// computed, not typed, so it cannot drift from the rule that enforces it.
	MaxSerialChars int `json:"max_serial_chars"`
	// Hint is what the picker says underneath the name: what the size is for,
	// in terms of the gear it goes on. It deliberately states no serial
	// length -- MaxSerialChars above is computed from the geometry, and a
	// number typed into prose here drifted from it within an hour of being
	// written.
	Hint string `json:"hint"`

	PageW, PageH   float64 `json:"-"` // page size
	MarginL, MargT float64 `json:"-"` // to the top-left corner of the first label
	LabelW, LabelH float64 `json:"-"`
	PitchX, PitchY float64 `json:"-"` // corner to corner, so it includes the gap
	Cols, Rows     int     `json:"-"`
}

// Paper sizes. Both are offered rather than one, because "standard printer
// paper" means 8.5x11 inches in North America and A4 almost everywhere else,
// and a sheet of adhesive labels laid out for the wrong one is ruined on the
// first print -- every label shifted further than the last down the page.
const (
	paperLetterW, paperLetterH = 215.9, 279.4 // 8.5in x 11in
	paperA4W, paperA4H         = 210.0, 297.0
)

// labelLayouts are the shapes a media department actually needs, in two paper
// sizes.
//
// **Named by what they go on, not by part number.** They were called "Avery
// 5160" and "Avery 5167" first, which is the wrong frame for the person using
// this: a teacher choosing a label knows they are labelling a tripod, and does
// not know which Avery number is 2.625 inches. The part number is still there,
// after the name, because it is what they type into a shop.
//
// Three sizes, deliberately:
//
//	Small   batteries, SD cards, cables, lens barrels
//	Medium  camera bodies, lens cases, bags — the everyday one
//	Large   tripods, light stands, flight cases — about the length of a short
//	        pencil, which is what a leg or a stand has room for
//
// **On small gear, which is the constraint that decides the bottom end.** The
// limit is physics rather than taste: Code 128 needs about 0.25mm per module
// to scan (measured -- see minModuleMM), and a six-character serial is 101
// modules plus a 20-module quiet zone. That is 30mm of bars before any
// padding, so **no usable barcode label is narrower than about 38mm**,
// whatever it goes on. Small below sits just above that floor; going smaller
// does not produce a smaller label, it produces one that does not scan. The
// way to label a genuinely tiny thing is therefore a shorter serial, which is
// what MaxSerialChars tells the picker.
var labelLayouts = []LabelLayout{
	{
		Key:   "small-letter",
		Label: "Small — batteries, SD cards, cables (Avery 5167)",
		Paper: "US Letter",
		Hint:  "44 × 13 mm, 80 to a sheet. Also the one that fits around a lens barrel.",
		Cols:  4, Rows: 20,
		PageW: paperLetterW, PageH: paperLetterH,
		MarginL: 7.62, MargT: 12.7, // 0.3in, 0.5in
		LabelW: 44.45, LabelH: 12.7, // 1.75in x 0.5in
		PitchX: 52.07, PitchY: 12.7, // 2.05in, 0.5in
	},
	{
		Key:   "medium-letter",
		Label: "Medium — camera bodies, lens cases, bags (Avery 5160)",
		Paper: "US Letter",
		Hint:  "67 × 25 mm, 30 to a sheet. The everyday size; start here.",
		Cols:  3, Rows: 10,
		PageW: paperLetterW, PageH: paperLetterH,
		MarginL: 4.7625, MargT: 12.7, // 0.1875in, 0.5in
		LabelW: 66.675, LabelH: 25.4, // 2.625in x 1in
		PitchX: 69.85, PitchY: 25.4, // 2.75in, 1in (no vertical gap)
	},
	{
		Key:   "large-letter",
		Label: "Large — tripods, light stands, cases (Avery 5162)",
		Paper: "US Letter",
		Hint:  "102 × 34 mm, 14 to a sheet. About the length of a short pencil, so it reads along a leg or a stand.",
		Cols:  2, Rows: 7,
		PageW: paperLetterW, PageH: paperLetterH,
		MarginL: 4.0, MargT: 21.2, // 0.1575in, 0.835in
		LabelW: 101.6, LabelH: 33.87, // 4in x 1.333in
		PitchX: 106.36, PitchY: 33.87, // 4.1875in, 1.333in
	},
	{
		Key:   "small-a4",
		Label: "Small — batteries, SD cards, cables (Avery L7651)",
		Paper: "A4",
		Hint:  "38 × 21 mm, 65 to a sheet. About as small as a scannable barcode goes.",
		Cols:  5, Rows: 13,
		PageW: paperA4W, PageH: paperA4H,
		MarginL: 4.65, MargT: 10.7,
		LabelW: 38.1, LabelH: 21.2,
		PitchX: 40.6, PitchY: 21.2,
	},
	{
		Key:   "medium-a4",
		Label: "Medium — camera bodies, lens cases, bags (Avery L7160)",
		Paper: "A4",
		Hint:  "64 × 38 mm, 21 to a sheet. The everyday size; start here.",
		Cols:  3, Rows: 7,
		PageW: paperA4W, PageH: paperA4H,
		MarginL: 7.2, MargT: 15.1,
		LabelW: 63.5, LabelH: 38.1,
		PitchX: 66.0, PitchY: 38.1,
	},
	{
		Key:   "large-a4",
		Label: "Large — tripods, light stands, cases (Avery L7163)",
		Paper: "A4",
		Hint:  "99 × 38 mm, 14 to a sheet. About the length of a short pencil, so it reads along a leg or a stand.",
		Cols:  2, Rows: 7,
		PageW: paperA4W, PageH: paperA4H,
		MarginL: 5.0, MargT: 15.1,
		LabelW: 99.1, LabelH: 38.1,
		PitchX: 101.6, PitchY: 38.1,
	},
	{
		// One label per page. For a single replacement sticker, which is the
		// common case the day after a label falls off a lens barrel.
		Key:   "single-letter",
		Label: "One per page — a single replacement",
		Paper: "US Letter",
		Hint:  "Medium size on a blank sheet. Print it, cut it out, stick it on.",
		Cols:  1, Rows: 1,
		PageW: paperLetterW, PageH: paperLetterH,
		MarginL: 20, MargT: 20,
		LabelW: 66.675, LabelH: 25.4,
		PitchX: 0, PitchY: 0,
	},
	{
		Key:   "single-a4",
		Label: "One per page — a single replacement",
		Paper: "A4",
		Hint:  "Medium size on a blank sheet. Print it, cut it out, stick it on.",
		Cols:  1, Rows: 1,
		PageW: paperA4W, PageH: paperA4H,
		MarginL: 20, MargT: 20,
		LabelW: 63.5, LabelH: 38.1,
		PitchX: 0, PitchY: 0,
	},
}

// LabelLayouts is the picker's list. Returned rather than hardcoded in the
// frontend so the two cannot disagree about what `avery5167` means.
func LabelLayouts() []LabelLayout {
	out := make([]LabelLayout, 0, len(labelLayouts))
	for _, l := range labelLayouts {
		out = append(out, l.computed())
	}
	return out
}

// computed fills the derived fields. Kept in one place so PerSheet and
// MaxSerialChars can never disagree with the geometry above them.
func (l LabelLayout) computed() LabelLayout {
	l.PerSheet = l.Cols * l.Rows
	l.MaxSerialChars = l.maxSerialChars()
	return l
}

// maxSerialChars is how many characters fit before drawLabel would refuse.
//
// It inverts the same arithmetic drawLabel does, so the number the picker
// shows and the rule that refuses a sheet come from one definition. Code 128
// spends 11 modules per character plus a start, a checksum and a 13-module
// stop; the 20 is the quiet zone, 10 modules either side, which is part of the
// symbol rather than margin.
func (l LabelLayout) maxSerialChars() int {
	const (
		padX          = 2.0 // matches drawLabel
		quietModules  = 20
		modulesPerSym = 11
		stopModules   = 13
		minModuleMM   = 0.25 // matches drawLabel
	)
	avail := l.LabelW - 2*padX
	if avail <= 0 {
		return 0
	}
	budget := int(avail/minModuleMM) - quietModules - stopModules
	// start symbol + checksum are two symbols that are not the serial.
	n := budget/modulesPerSym - 2
	if n < 0 {
		return 0
	}
	return n
}

func findLayout(key string) (LabelLayout, error) {
	if key == "" {
		key = labelLayouts[0].Key
	}
	for _, l := range labelLayouts {
		if l.Key == key {
			return l.computed(), nil
		}
	}
	names := make([]string, 0, len(labelLayouts))
	for _, l := range labelLayouts {
		names = append(names, l.Key)
	}
	return LabelLayout{}, fmt.Errorf("%w: unknown label layout %q (have %s)",
		ErrInvalid, key, strings.Join(names, ", "))
}

// barcodeModules turns a string into the run of black/white modules Code 128
// encodes it as, one bool per module.
//
// This is the seam between the barcode library and the PDF: everything below
// draws modules, and nothing below knows what a symbology is. It also means
// the PNG and the PDF are provably the same barcode, because they come from
// the same call.
func barcodeModules(text string) ([]bool, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("%w: cannot make a barcode of an empty string", ErrInvalid)
	}
	// Code 128 encodes the ASCII range. A serial with a character outside it
	// is refused here rather than producing a barcode that encodes something
	// else, which would scan as the wrong item -- the worst possible failure
	// for this feature.
	for _, r := range text {
		if r > 127 {
			return nil, fmt.Errorf("%w: %q cannot be encoded as a barcode; serials must be plain ASCII", ErrInvalid, text)
		}
	}

	bc, err := code128.Encode(text)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot encode %q as a barcode: %v", ErrInvalid, text, err)
	}
	bounds := bc.Bounds()
	modules := make([]bool, 0, bounds.Dx())
	for x := bounds.Min.X; x < bounds.Max.X; x++ {
		// A 1-D barcode is one pixel tall at scale 1; any row is the pattern.
		r, g, b, _ := bc.At(x, bounds.Min.Y).RGBA()
		modules = append(modules, r == 0 && g == 0 && b == 0)
	}
	return modules, nil
}

// BarcodePNG renders text as a Code 128 PNG, width pixels wide.
//
// For the screen and for a download, not for the label sheet -- the sheet
// draws vectors. The height is a third of the width, clamped, which is the
// ratio a 1-D barcode is normally printed at and tall enough that a scanner
// held at a slight angle still crosses every bar.
func BarcodePNG(text string, width int) ([]byte, error) {
	if width <= 0 {
		width = 600
	}
	if width > 4000 {
		// Not a security bound -- an admin asking for a big PNG is fine -- but
		// an unbounded one is a way to make the server allocate 2 GB from a
		// query string.
		width = 4000
	}

	bc, err := code128.Encode(text)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot encode %q as a barcode: %v", ErrInvalid, text, err)
	}
	// Scale to a whole multiple of the module count where possible, so every
	// bar is the same number of pixels wide. An uneven scale makes some bars a
	// pixel wider than others, which is exactly the width distortion a scanner
	// is reading.
	modules := bc.Bounds().Dx()
	if modules > 0 {
		if scale := width / modules; scale >= 1 {
			width = scale * modules
		}
	}
	height := width / 3
	if height < 60 {
		height = 60
	}

	scaled, err := barcode.Scale(bc, width, height)
	if err != nil {
		return nil, fmt.Errorf("scale barcode: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, scaled); err != nil {
		return nil, fmt.Errorf("encode barcode png: %w", err)
	}
	return buf.Bytes(), nil
}
