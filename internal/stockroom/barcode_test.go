package stockroom

import (
	"bytes"
	"strings"
	"testing"
)

// TestLabelSheetRenders is the happy path: a sheet of labels comes out as a
// PDF with the right number of pages.
func TestLabelSheetRenders(t *testing.T) {
	layout, err := findLayout("medium-letter")
	if err != nil {
		t.Fatal(err)
	}
	// One more than a sheet holds, so the page break is exercised too -- an
	// off-by-one there silently drops the last label, which is discovered at
	// the shelf.
	cells := make([]labelCell, layout.Cols*layout.Rows+1)
	for i := range cells {
		cells[i] = labelCell{Code: "SR-000" + string(rune('0'+i%10)), Title: "Test item"}
	}

	pdf, err := renderLabelSheet(layout, cells, 0)
	if err != nil {
		t.Fatalf("renderLabelSheet: %v", err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatal("output is not a PDF")
	}
	if got := bytes.Count(pdf, []byte("/Type /Page\n")); got != 2 {
		t.Errorf("31 labels on a 30-up sheet produced %d pages, want 2", got)
	}
}

// TestLabelRefusesUnscannableBarcode is the gate, and the one that earns its
// place. A serial too long for its label squeezes the bars until a scanner
// cannot read them, and every other layer -- the render, the print, the
// sticking-on -- succeeds. The failure lands on a student at the counter days
// later. See minModuleMM for the decoder measurements behind the threshold.
func TestLabelRefusesUnscannableBarcode(t *testing.T) {
	layout, err := findLayout("small-letter")
	if err != nil {
		t.Fatal(err)
	}
	long := labelCell{Code: "CANON-EF-70-200-F28L-IS-III-USM-01", Title: "Too long"}

	_, err = renderLabelSheet(layout, []labelCell{long}, 0)
	if err == nil {
		t.Fatal("rendered a barcode too narrow to scan; want a refusal")
	}
	// The message has to name the item and say what to do, because the admin
	// reading it is the person who chose both the serial and the label size.
	for _, want := range []string{long.Code, "too long", "shorter serial"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q: %v", want, err)
		}
	}
}

// TestBarcodeRejectsNonASCII guards the worst failure this feature has: a
// barcode that scans as something other than what is printed under it.
func TestBarcodeRejectsNonASCII(t *testing.T) {
	if _, err := barcodeModules("CAMÉRA-01"); err == nil {
		t.Fatal("encoded a non-ASCII serial; want a refusal")
	}
	if _, err := barcodeModules("   "); err == nil {
		t.Fatal("encoded a blank serial; want a refusal")
	}
}

// TestLabelLayoutsAreConsistent checks the geometry against the page it claims
// to fit on. A layout whose columns run off the right edge prints a sheet
// where the last column is half on the paper, and that is not visible until
// somebody buys the labels.
func TestLabelLayoutsAreConsistent(t *testing.T) {
	for _, l := range LabelLayouts() {
		if l.PerSheet != l.Cols*l.Rows {
			t.Errorf("%s: PerSheet = %d, want %d", l.Key, l.PerSheet, l.Cols*l.Rows)
		}
		right := l.MarginL + float64(l.Cols-1)*l.PitchX + l.LabelW
		if right > l.PageW+0.01 {
			t.Errorf("%s: columns end at %.2fmm on a %.2fmm page", l.Key, right, l.PageW)
		}
		bottom := l.MargT + float64(l.Rows-1)*l.PitchY + l.LabelH
		if bottom > l.PageH+0.01 {
			t.Errorf("%s: rows end at %.2fmm on a %.2fmm page", l.Key, bottom, l.PageH)
		}
		if l.Hint == "" {
			t.Errorf("%s: no hint; the picker would show a bare name", l.Key)
		}
	}
}
