// Package imgconv prepares a referenced image for an exporter.
//
// Neither exporter can take the bytes as they are: Word won't display WebP (which
// is what xy usually stores, since the client recompresses attachments to WebP
// q70), and typst reads only a few formats. Decoding here also yields the pixel
// dimensions the sizing maths needs.
package imgconv

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Decode decodes an image of any supported format (PNG/JPEG/GIF/WebP).
func Decode(raw []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(raw))
	return img, err
}

// ToPNG decodes and re-encodes as PNG, returning it with its pixel dimensions.
// This is the docx path: PNG is the one raster format every Word reliably shows.
func ToPNG(raw []byte) (data []byte, w, h int, err error) {
	img, err := Decode(raw)
	if err != nil {
		return nil, 0, 0, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, 0, 0, err
	}
	b := img.Bounds()
	return buf.Bytes(), b.Dx(), b.Dy(), nil
}

// PDFDPI is the resolution images are embedded at. A picture in an exported
// package is laid out at a known physical size (a few inches — see
// inline.Img.SizeInches), so anything beyond a print-quality sampling of that size
// is bytes nobody will ever see.
const PDFDPI = 200.0

// jpegQuality is what an already-lossy photo is re-encoded at. The source is
// typically a JPEG or a WebP q70 attachment, so this is not the first generation
// of loss; 85 keeps it invisible.
const jpegQuality = 85

// ForPDF encodes an image for embedding in the PDF at the size it will be drawn
// (widthIn × heightIn, in inches): it is downscaled to PDFDPI and, if it has no
// transparency, encoded as JPEG rather than PNG.
//
// Both halves matter. A PNG of a photograph is lossless and enormous — an 800 KB
// JPEG attachment came back out as a megabyte of PNG, which was most of the file —
// and the original is usually a many-megapixel photo being drawn five inches wide.
// Transparent images stay PNG (JPEG has no alpha) and are only downscaled.
//
// The returned ext ("png"/"jpg") is the extension the image must be handed to typst
// under: typst picks its decoder from the file name.
func ForPDF(raw []byte, widthIn, heightIn float64) (data []byte, ext string, err error) {
	img, err := Decode(raw)
	if err != nil {
		return nil, "", err
	}
	img = downscale(img, int(widthIn*PDFDPI+0.5), int(heightIn*PDFDPI+0.5))

	var buf bytes.Buffer
	if hasAlpha(img) {
		if err := png.Encode(&buf, img); err != nil {
			return nil, "", err
		}
		return buf.Bytes(), "png", nil
	}
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), "jpg", nil
}

// downscale resamples img to fit within maxW×maxH, preserving the aspect ratio.
// An image already at or below that size is returned untouched — upscaling only
// invents pixels and costs bytes.
func downscale(img image.Image, maxW, maxH int) image.Image {
	b := img.Bounds()
	if maxW < 1 || maxH < 1 || (b.Dx() <= maxW && b.Dy() <= maxH) {
		return img
	}
	scale := min(float64(maxW)/float64(b.Dx()), float64(maxH)/float64(b.Dy()))
	w := max(int(float64(b.Dx())*scale+0.5), 1)
	h := max(int(float64(b.Dy())*scale+0.5), 1)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	return dst
}

// hasAlpha reports whether any pixel is not fully opaque. Most decoded formats
// answer this in O(1) via Opaque(); the fallback scan is the rare case.
func hasAlpha(img image.Image) bool {
	if o, ok := img.(interface{ Opaque() bool }); ok {
		return !o.Opaque()
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a != 0xffff {
				return true
			}
		}
	}
	return false
}
