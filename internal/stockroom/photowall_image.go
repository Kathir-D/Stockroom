package stockroom

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"

	xdraw "golang.org/x/image/draw"

	// Registered for their decoders only. JPEG is what the department's
	// cameras and phones produce; PNG turns up in exported graphics; WebP
	// arrives from anything saved out of a browser.
	_ "image/png"

	_ "golang.org/x/image/webp"
)

// Normalization: every photograph that reaches the wall leaves this file as
// the same 900x600 JPEG (docs/design/signin-photo-wall.html §4).
//
// One size is what lets the marquee do no layout work and the webview do no
// scaling work. The closet PC's webview is not fast, and a 12-megapixel JPEG
// shrunk in CSS is still decoded at full resolution into memory -- sixteen
// times over, on the one screen that must never feel broken.
//
// The order of the steps is the part worth protecting. EXIF orientation is
// applied to the *measurements* before the aspect-ratio gate runs, because a
// phone photograph held upright is stored landscape with an orientation tag
// saying "rotate me". Gate first and it passes as a 3:2 landscape and then
// renders sideways on the wall, which is the single most visible way this
// feature can look broken.

const (
	// photoTileWidth and photoTileHeight are the one size every tile is.
	photoTileWidth  = 900
	photoTileHeight = 600
	// photoTileRatio is 3:2, the ratio the crop targets.
	photoTileRatio = float64(photoTileWidth) / float64(photoTileHeight)

	// photoRatioMin and photoRatioMax bound what the wall will accept, in
	// *displayed* orientation. The window admits 3:2, 16:9 and most 4:3, and
	// rejects panoramas, screenshots, square crops and portrait shots -- all
	// of which would either crop to nonsense or force the wall to handle two
	// orientations.
	photoRatioMin = 1.20
	photoRatioMax = 1.90

	// photoJPEGQuality lands a 900x600 tile at roughly 120 KB, which is what
	// the reel's ~6 MB disk budget is calculated from (§2).
	photoJPEGQuality = 82

	// photoMaxSourceBytes is the ceiling on one original. §3 skips anything
	// larger using the size the manifest already recorded, so this is the
	// backstop for a file that grew since the listing: one pathological RAW
	// must not stall the filler or the machine.
	photoMaxSourceBytes = 40 << 20
)

// errPhotoUnusable means "this file is not something the wall can show". It is
// the expected outcome for a good part of any real folder -- portraits,
// panoramas, video stills, a HEIC Go cannot decode -- so it is a routine
// answer rather than a fault, and §3 retries past it instead of reporting it.
var errPhotoUnusable = errors.New("not a photograph the wall can use")

// normalizePhoto turns one original into one tile: 900x600 JPEG, oriented
// upright, centre-cropped to 3:2, carrying no metadata.
//
// Everything it rejects is wrapped in errPhotoUnusable so the caller can tell
// "this file is wrong" from "the download broke".
func normalizePhoto(r io.Reader) ([]byte, error) {
	// The whole original is held in memory and never written to disk (§3), so
	// it is read through a limit rather than trusted. One byte over the
	// ceiling is enough to know it is over.
	raw, err := io.ReadAll(io.LimitReader(r, photoMaxSourceBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read the photograph: %w", err)
	}
	if len(raw) > photoMaxSourceBytes {
		return nil, fmt.Errorf("%w: larger than the %d MB ceiling", errPhotoUnusable, photoMaxSourceBytes>>20)
	}

	// Step 1: the header only. DecodeConfig gives width and height without
	// decoding a single pixel, and rclone lsjson does not expose image
	// dimensions, so this is the only place the ratio gate can live. A file
	// that fails here is not an image we can use -- a .mov, a truncated
	// download, a HEIC -- and costs nothing to find out.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errPhotoUnusable, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, fmt.Errorf("%w: reports a zero dimension", errPhotoUnusable)
	}

	// Step 2: EXIF orientation, applied to the measurements *before* the gate.
	orient := exifOrientation(raw)
	dispW, dispH := cfg.Width, cfg.Height
	if orientSwapsAxes(orient) {
		dispW, dispH = dispH, dispW
	}

	// Step 3: the gate, on displayed dimensions.
	ratio := float64(dispW) / float64(dispH)
	if ratio < photoRatioMin || ratio > photoRatioMax {
		return nil, fmt.Errorf("%w: %dx%d is %.2f:1, outside %.2f-%.2f",
			errPhotoUnusable, dispW, dispH, ratio, photoRatioMin, photoRatioMax)
	}

	// Only now is it worth decoding pixels.
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errPhotoUnusable, err)
	}

	// Step 4: centre-crop to exactly 3:2, then downscale. Cropping first means
	// the resampler runs over the smallest possible pixel count.
	//
	// A centre crop is centred under all eight EXIF transforms, so the crop
	// rectangle can be taken in *stored* coordinates without first rotating
	// the full-resolution image: its dimensions are the displayed crop's,
	// swapped back when the orientation swaps axes. That ordering matters for
	// more than tidiness -- rotating 12 megapixels costs ~48 MB and a pass
	// over every pixel, where rotating the finished 900x600 tile costs half a
	// megabyte.
	cropW, cropH := cropTo(dispW, dispH, photoTileRatio)
	scaledW, scaledH := photoTileWidth, photoTileHeight
	if orientSwapsAxes(orient) {
		cropW, cropH = cropH, cropW
		scaledW, scaledH = scaledH, scaledW
	}
	b := src.Bounds()
	srcRect := image.Rect(0, 0, cropW, cropH).Add(image.Pt(
		b.Min.X+(b.Dx()-cropW)/2,
		b.Min.Y+(b.Dy()-cropH)/2,
	))

	scaled := image.NewRGBA(image.Rect(0, 0, scaledW, scaledH))
	// CatmullRom is the reason golang.org/x/image is here: the standard
	// library has no resampler at all, and nearest-neighbour on a 4x downscale
	// is visibly gritty at the size these are shown.
	xdraw.CatmullRom.Scale(scaled, scaled.Bounds(), src, srcRect, xdraw.Src, nil)

	// Step 2, second half: the rotation itself, now that it is cheap.
	out := orientImage(scaled, orient)

	// Step 5: re-encode. Dropping EXIF is a side effect worth having on
	// purpose -- originals carry GPS coordinates, camera serial numbers and
	// timestamps, and none of that belongs in a file this server hands out
	// with no session at all (§0).
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: photoJPEGQuality}); err != nil {
		return nil, fmt.Errorf("encode a tile: %w", err)
	}
	return buf.Bytes(), nil
}

// cropTo returns the largest w x h rectangle of the given ratio that fits
// inside w x h, centred. Integer arithmetic, so the result is at most a pixel
// off the exact ratio -- which the downscale to 900x600 then absorbs.
func cropTo(w, h int, ratio float64) (int, int) {
	if float64(w)/float64(h) > ratio {
		cw := int(float64(h)*ratio + 0.5)
		if cw > w {
			cw = w
		}
		return cw, h
	}
	ch := int(float64(w)/ratio + 0.5)
	if ch > h {
		ch = h
	}
	return w, ch
}

/* ------------------------------------------------------- EXIF orientation -- */

// orientSwapsAxes reports whether an orientation exchanges width and height.
// Values 5 to 8 are the quarter turns; 1 to 4 are the identity and the mirrors.
func orientSwapsAxes(o int) bool { return o >= 5 && o <= 8 }

// orientImage applies an EXIF orientation, returning src untouched for the
// common case of 1 (or an absent tag, which reads as 1).
//
// The mapping is the EXIF specification's "row 0 / column 0" table read
// literally: value 6 is "right, top", meaning the stored image's first row is
// displayed as the rightmost column, which is a quarter turn clockwise.
func orientImage(src image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return src
	}
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dw, dh := sw, sh
	if orientSwapsAxes(o) {
		dw, dh = sh, sw
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			var sx, sy int
			switch o {
			case 2: // mirrored horizontally
				sx, sy = sw-1-x, y
			case 3: // rotated 180
				sx, sy = sw-1-x, sh-1-y
			case 4: // mirrored vertically
				sx, sy = x, sh-1-y
			case 5: // transposed
				sx, sy = y, x
			case 6: // rotated 90 clockwise
				sx, sy = y, sh-1-x
			case 7: // transversed
				sx, sy = sw-1-y, sh-1-x
			case 8: // rotated 90 anticlockwise
				sx, sy = sw-1-y, x
			}
			dst.Set(x, y, src.At(b.Min.X+sx, b.Min.Y+sy))
		}
	}
	return dst
}

// exifOrientation reads the orientation tag out of a JPEG's APP1 segment,
// returning 1 -- "upright, do nothing" -- for anything it cannot find or
// cannot parse.
//
// This is hand-rolled rather than pulled from a library on purpose: §4 names
// golang.org/x/image/draw as the feature's only new dependency, and the whole
// of what this feature needs from EXIF is one 16-bit number. Everything else
// in the file is metadata the normalizer deliberately throws away.
//
// Failing soft is the right answer at every step. A tag that cannot be read is
// indistinguishable, for our purposes, from a photograph that was never
// rotated, and the cost of guessing wrong is one sideways tile out of forty --
// where refusing the file outright would quietly empty a wall fed by a camera
// whose EXIF writer this parser does not happen to understand.
func exifOrientation(data []byte) int {
	const upright = 1
	// Only JPEG carries EXIF here. PNG and WebP orientation tags exist but
	// are vanishingly rare and are not what phones produce.
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return upright
	}

	for off := 2; off+4 <= len(data); {
		if data[off] != 0xFF {
			return upright // not a segment boundary: give up rather than guess
		}
		marker := data[off+1]
		switch {
		case marker == 0xD8 || marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7):
			off += 2 // standalone markers carry no length
			continue
		case marker == 0xDA || marker == 0xD9:
			return upright // start of scan: the metadata is behind us
		}
		size := int(binary.BigEndian.Uint16(data[off+2 : off+4]))
		if size < 2 || off+2+size > len(data) {
			return upright
		}
		if marker == 0xE1 {
			if o, ok := exifOrientationFromAPP1(data[off+4 : off+2+size]); ok {
				return o
			}
		}
		off += 2 + size
	}
	return upright
}

// exifOrientationFromAPP1 parses one APP1 payload: the "Exif\0\0" signature,
// then a TIFF header, then IFD0's entries looking for tag 0x0112.
func exifOrientationFromAPP1(payload []byte) (int, bool) {
	const sigLen = 6
	if len(payload) < sigLen+8 || string(payload[:sigLen]) != "Exif\x00\x00" {
		return 0, false // an APP1 that is XMP, not EXIF
	}
	tiff := payload[sigLen:]

	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 0, false
	}
	if order.Uint16(tiff[2:4]) != 42 {
		return 0, false
	}

	ifd := int(order.Uint32(tiff[4:8]))
	if ifd < 8 || ifd+2 > len(tiff) {
		return 0, false
	}
	count := int(order.Uint16(tiff[ifd : ifd+2]))
	for i := 0; i < count; i++ {
		e := ifd + 2 + i*12
		if e+12 > len(tiff) {
			return 0, false
		}
		if order.Uint16(tiff[e:e+2]) != 0x0112 {
			continue
		}
		// Orientation is a SHORT, so the value sits in the first two bytes of
		// the entry's value field rather than at an offset elsewhere.
		o := int(order.Uint16(tiff[e+8 : e+10]))
		if o < 1 || o > 8 {
			return 0, false
		}
		return o, true
	}
	return 0, false
}
