package stockroom

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// Tests for the normalizer (docs/design/signin-photo-wall.html §4). Nothing
// here needs a network, rclone or Postgres: the whole file is one pure
// function from bytes to bytes.

// testJPEG encodes a solid-ish image of the given size. The left half is red
// and the right half blue, which is enough to tell an orientation transform
// apart from the absence of one after JPEG has been through it.
func testJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{R: 220, G: 30, B: 30, A: 255}
			if x >= w/2 {
				c = color.RGBA{R: 30, G: 30, B: 220, A: 255}
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 92}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// withEXIF splices an APP1 segment carrying the given orientation, and a GPS
// IFD, in after the SOI marker -- which is exactly where a camera puts one.
func withEXIF(t *testing.T, jpg []byte, orientation int) []byte {
	t.Helper()

	// A little-endian TIFF header, IFD0 with two entries, then a GPS IFD.
	// Offsets are counted from the start of the TIFF header.
	const ifd0 = 8
	entries := 2
	ifd0End := ifd0 + 2 + entries*12 + 4
	gps := ifd0End

	tiff := make([]byte, gps+2+12+4)
	copy(tiff, "II")
	binary.LittleEndian.PutUint16(tiff[2:], 42)
	binary.LittleEndian.PutUint32(tiff[4:], ifd0)

	binary.LittleEndian.PutUint16(tiff[ifd0:], uint16(entries))
	put := func(at int, tag, typ uint16, count uint32, value uint32) {
		binary.LittleEndian.PutUint16(tiff[at:], tag)
		binary.LittleEndian.PutUint16(tiff[at+2:], typ)
		binary.LittleEndian.PutUint32(tiff[at+4:], count)
		binary.LittleEndian.PutUint32(tiff[at+8:], value)
	}
	put(ifd0+2, 0x0112, 3, 1, uint32(orientation))     // Orientation, a SHORT
	put(ifd0+2+12, 0x8825, 4, 1, uint32(gps))          // GPSInfo -> the GPS IFD
	binary.LittleEndian.PutUint32(tiff[ifd0End-4:], 0) // no IFD1

	binary.LittleEndian.PutUint16(tiff[gps:], 1)
	put(gps+2, 0x0001, 2, 2, uint32('N')) // GPSLatitudeRef = "N"

	payload := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))

	out := append([]byte{}, jpg[:2]...)
	out = append(out, seg...)
	out = append(out, payload...)
	return append(out, jpg[2:]...)
}

// decodeTile decodes a normalizer result back to an image.
func decodeTile(t *testing.T, tile []byte) image.Image {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(tile))
	if err != nil {
		t.Fatalf("the tile is not a decodable JPEG: %v", err)
	}
	return img
}

// isRed reports whether a pixel is nearer the test image's red than its blue.
// JPEG is lossy and these are compared after a resample, so the question can
// only ever be which of two very different colours it is closer to.
func isRed(c color.Color) bool {
	r, _, b, _ := c.RGBA()
	return r > b
}

// TestNormalizeHappyPath is §4's whole purpose: every photograph leaves as the
// same 900x600 JPEG, so the marquee does no layout work and the closet PC's
// webview never decodes a 12-megapixel image sixteen times over.
func TestNormalizeHappyPath(t *testing.T) {
	tile, err := normalizePhoto(bytes.NewReader(testJPEG(t, 3000, 2000)))
	if err != nil {
		t.Fatalf("normalizePhoto: %v", err)
	}
	got := decodeTile(t, tile).Bounds()
	if got.Dx() != photoTileWidth || got.Dy() != photoTileHeight {
		t.Fatalf("tile is %dx%d, want %dx%d", got.Dx(), got.Dy(), photoTileWidth, photoTileHeight)
	}
}

// TestNormalizeRatioGate is §4 step 3. The window admits 3:2, 16:9 and most
// 4:3 and turns away everything that would either crop to nonsense or force
// the wall to handle two orientations.
func TestNormalizeRatioGate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		w, h   int
		accept bool
	}{
		{"square", 800, 800, false},
		{"panorama", 2100, 900, false},
		{"portrait", 900, 1200, false},
		{"four by three", 1200, 900, true},
		{"sixteen by nine", 1600, 900, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tile, err := normalizePhoto(bytes.NewReader(testJPEG(t, tc.w, tc.h)))
			if !tc.accept {
				if !errors.Is(err, errPhotoUnusable) {
					t.Fatalf("want errPhotoUnusable, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizePhoto: %v", err)
			}
			// Accepted means cropped to the one size, not passed through.
			if b := decodeTile(t, tile).Bounds(); b.Dx() != photoTileWidth || b.Dy() != photoTileHeight {
				t.Fatalf("tile is %dx%d, want %dx%d", b.Dx(), b.Dy(), photoTileWidth, photoTileHeight)
			}
		})
	}
}

// TestNormalizeOrientationPrecedesTheGate is the regression that matters most
// (§4 step 2, §10). A phone photograph held upright is *stored* landscape with
// an orientation tag saying "turn me". Measure the ratio before applying the
// tag and it sails through the 3:2 gate and then renders sideways on the wall,
// which is the single most visible way this feature can look broken.
func TestNormalizeOrientationPrecedesTheGate(t *testing.T) {
	stored := testJPEG(t, 3000, 2000) // passes the gate on its stored shape

	if _, err := normalizePhoto(bytes.NewReader(stored)); err != nil {
		t.Fatalf("the same bytes without a tag must be accepted: %v", err)
	}

	// Orientation 6 is a quarter turn clockwise, so this displays as a
	// 2000x3000 portrait and has no business on the wall.
	_, err := normalizePhoto(bytes.NewReader(withEXIF(t, stored, 6)))
	if !errors.Is(err, errPhotoUnusable) {
		t.Fatalf("a landscape tagged orientation 6 displays portrait and must be rejected, got %v", err)
	}
}

// TestNormalizeAppliesOrientation is the other half: a photograph stored
// portrait and tagged orientation 6 *is* a landscape once turned, and has to
// come out actually turned rather than merely measured as though it were.
func TestNormalizeAppliesOrientation(t *testing.T) {
	// Stored 600x900 with a red left half. A quarter turn clockwise puts the
	// stored left column along the displayed top edge.
	tile, err := normalizePhoto(bytes.NewReader(withEXIF(t, testJPEG(t, 600, 900), 6)))
	if err != nil {
		t.Fatalf("normalizePhoto: %v", err)
	}
	img := decodeTile(t, tile)
	if b := img.Bounds(); b.Dx() != photoTileWidth || b.Dy() != photoTileHeight {
		t.Fatalf("tile is %dx%d, want %dx%d", b.Dx(), b.Dy(), photoTileWidth, photoTileHeight)
	}
	if !isRed(img.At(450, 100)) {
		t.Error("the top of the tile should carry what was the stored left edge")
	}
	if isRed(img.At(450, 500)) {
		t.Error("the bottom of the tile should carry what was the stored right edge")
	}
}

// TestNormalizeStripsMetadata is §4 step 5. Originals carry GPS coordinates,
// camera serial numbers and timestamps, and a tile is served over HTTP with no
// session at all (§0), so none of it may survive the re-encode.
func TestNormalizeStripsMetadata(t *testing.T) {
	in := withEXIF(t, testJPEG(t, 1200, 800), 1)
	if !bytes.Contains(in, []byte("Exif\x00\x00")) {
		t.Fatal("the test input was supposed to carry EXIF")
	}

	tile, err := normalizePhoto(bytes.NewReader(in))
	if err != nil {
		t.Fatalf("normalizePhoto: %v", err)
	}
	if bytes.Contains(tile, []byte("Exif\x00\x00")) {
		t.Error("the tile still carries an EXIF block")
	}
	if o := exifOrientation(tile); o != 1 {
		t.Errorf("the tile still carries an orientation tag: %d", o)
	}
}

// TestNormalizeRejectsRubbish covers §4 step 6: a truncated download, a video,
// anything Go cannot decode. Each is a routine answer rather than a fault, so
// it has to be distinguishable from a broken download -- the source retries
// past the first and reports the second.
func TestNormalizeRejectsRubbish(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []byte
	}{
		{"not an image", []byte("this is a text file, not a photograph")},
		{"truncated jpeg", testJPEG(t, 1200, 800)[:40]},
		{"empty", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := normalizePhoto(bytes.NewReader(tc.in)); !errors.Is(err, errPhotoUnusable) {
				t.Fatalf("want errPhotoUnusable, got %v", err)
			}
		})
	}
}

// TestEXIFOrientationFailsSoft: an unreadable tag has to read as "upright"
// rather than as an error. A camera whose EXIF writer this hand-rolled parser
// does not understand should cost one sideways tile out of forty, never a wall
// that quietly stays empty.
func TestEXIFOrientationFailsSoft(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []byte
	}{
		{"no exif at all", testJPEG(t, 1200, 800)},
		{"not a jpeg", []byte("plain text")},
		{"truncated segment", []byte{0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x40, 'E', 'x'}},
		{"nothing", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if o := exifOrientation(tc.in); o != 1 {
				t.Fatalf("orientation = %d, want 1", o)
			}
		})
	}
}
