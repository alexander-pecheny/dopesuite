// Package imagex is what happens to a Photo between the phone and the disk:
// it is decoded, scaled down and re-encoded as JPEG, so what is stored is the
// picture and nothing else. A phone's JPEG carries EXIF, and EXIF carries the
// place and the time the receipt was photographed; re-encoding drops all of it,
// which is the point rather than a side effect.
package imagex

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"

	// The formats a phone camera and a screenshot actually produce. Decoding is
	// registered by importing them for their side effect.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

// MaxUploadBytes caps an upload BEFORE it is decoded: a decode bomb is cheap to
// send and expensive to open, so the reader is bounded first and the image
// package never sees more than this.
const MaxUploadBytes = 15 << 20

// MaxSide is the longest edge a stored Photo may have. A receipt is read on a
// phone; 2000px is more than the screen can show and small enough that a Group
// of bill photographers does not fill a disk.
const MaxSide = 2000

// Quality is the JPEG quality re-encoding uses: high enough that the small
// print on a receipt survives, low enough to be worth the re-encode.
const Quality = 82

// ErrNotAnImage says the bytes did not decode as a picture in any format we
// read. It is a User Error at the edge: the app words it.
var ErrNotAnImage = errors.New("imagex: not an image")

// Encoded is a re-encoded picture: the JPEG bytes and the size they came out.
type Encoded struct {
	Bytes  []byte
	Width  int
	Height int
}

// Reencode decodes whatever arrived, scales it so neither side is longer than
// MaxSide, and writes it back as JPEG. It never scales UP: a small receipt
// stays the size it was.
func Reencode(raw []byte) (Encoded, error) {
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return Encoded{}, ErrNotAnImage
	}
	bounds := src.Bounds()
	w, h := Fit(bounds.Dx(), bounds.Dy(), MaxSide)

	var out image.Image = src
	if w != bounds.Dx() || h != bounds.Dy() {
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		scale(dst, src)
		out = dst
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: Quality}); err != nil {
		return Encoded{}, err
	}
	return Encoded{Bytes: buf.Bytes(), Width: w, Height: h}, nil
}

// Fit is the size a w×h picture becomes when its long side is capped at max,
// keeping the aspect ratio and never growing. A side that rounds to nothing is
// kept at one pixel, because a zero-width image is not an image.
func Fit(w, h, max int) (int, int) {
	if w <= 0 || h <= 0 {
		return w, h
	}
	long := w
	if h > long {
		long = h
	}
	if long <= max {
		return w, h
	}
	nw, nh := w*max/long, h*max/long
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	return nw, nh
}

// scale is a plain box filter: for each destination pixel, average the source
// pixels it covers. It is not the prettiest resampler in the world, but it has
// no dependency and no ringing, and a receipt read on a phone cannot tell the
// difference from a Lanczos one.
func scale(dst *image.RGBA, src image.Image) {
	sb := src.Bounds()
	db := dst.Bounds()
	dw, dh := db.Dx(), db.Dy()
	if dw == 0 || dh == 0 {
		return
	}
	for y := range dh {
		y0 := sb.Min.Y + y*sb.Dy()/dh
		y1 := sb.Min.Y + (y+1)*sb.Dy()/dh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := range dw {
			x0 := sb.Min.X + x*sb.Dx()/dw
			x1 := sb.Min.X + (x+1)*sb.Dx()/dw
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, b, a, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					pr, pg, pb, pa := src.At(sx, sy).RGBA()
					r += uint64(pr)
					g += uint64(pg)
					b += uint64(pb)
					a += uint64(pa)
					n++
				}
			}
			if n == 0 {
				continue
			}
			dst.Set(db.Min.X+x, db.Min.Y+y, color.RGBA64{
				R: uint16(r / n), G: uint16(g / n), B: uint16(b / n), A: uint16(a / n),
			})
		}
	}
}
