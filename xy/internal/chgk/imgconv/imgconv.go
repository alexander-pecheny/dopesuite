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

// Decode decodes an image of any supported format (PNG/JPEG/GIF/WebP). Callers
// need this before ForExport, because the size a picture is drawn at is derived
// from its ORIGINAL pixel dimensions (inline.Img.SizeInches).
func Decode(raw []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(raw))
	return img, err
}

// ExportDPI is the resolution images are embedded at. A picture in an exported
// package is laid out at a known physical size (a few inches — see
// inline.Img.SizeInches), so anything beyond a print-quality sampling of that size
// is bytes nobody will ever see.
const ExportDPI = 200.0

// jpegQuality is what an already-lossy photo is re-encoded at. The source is
// typically a photo straight off a camera or phone (already JPEG), so 85 is a
// second generation of loss at worst, and an invisible one.
const jpegQuality = 85

// ForExport encodes an image for embedding at the size it will be drawn
// (widthIn × heightIn, in inches): downscaled to ExportDPI and, if it has no
// transparency, encoded as JPEG rather than PNG.
//
// Both halves matter, and both exporters want both. A PNG of a photograph is
// lossless and enormous — an 800 KB JPEG attachment came back out as a megabyte of
// PNG, which was most of the exported file — and the original is usually a
// many-megapixel photo being drawn five inches wide. Transparent images stay PNG
// (JPEG has no alpha) and are only downscaled.
//
// It is also the WebP escape hatch: neither Word nor typst reads WebP, and an
// attachment may well be one (the client can compress to WebP q70 on upload).
// Decoding here means both exporters get a format they can display.
//
// The returned ext ("png"/"jpg") is the extension the image must be stored under:
// typst picks its decoder from the file name, and the docx declares a content type
// per extension.
func ForExport(raw []byte, widthIn, heightIn float64) (data []byte, ext string, err error) {
	img, err := Decode(raw)
	if err != nil {
		return nil, "", err
	}
	img = downscale(img, int(widthIn*ExportDPI+0.5), int(heightIn*ExportDPI+0.5))

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
// HasAlpha reports whether a picture carries transparency, which decides whether
// it can be re-encoded as a JPEG.
func HasAlpha(img image.Image) bool { return hasAlpha(img) }

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

// Optimize is optimize_raster_image_data, what chgksuite's --optimize_size runs
// over every picture a finished .docx or .pptx embeds: re-encode, and keep
// whichever candidate actually beats the bytes that were there. A picture with
// transparency stays a PNG; anything else becomes a JPEG, which is where the
// saving is.
//
// The bytes are Go's encoders', not Pillow's, so a package built with this on
// is no longer byte-comparable to chgksuite's — but the sizes are, to a tenth
// of a percent: encodeJPEG rebuilds the Huffman tables Pillow's optimize=True
// would have, and PNGs are written at Go's best compression.
func Optimize(data []byte, ext string, quality int) ([]byte, string, bool) {
	img, err := Decode(data)
	if err != nil {
		return nil, "", false
	}
	type candidate struct {
		ext  string
		data []byte
	}
	var candidates []candidate
	if HasAlpha(img) {
		if out, err := EncodePNG(img); err == nil {
			candidates = append(candidates, candidate{"png", out})
		}
	} else {
		if out, err := EncodeJPEG(img, quality); err == nil {
			candidates = append(candidates, candidate{"jpg", out})
		}
		if ext == "png" {
			if out, err := EncodePNG(img); err == nil {
				candidates = append(candidates, candidate{"png", out})
			}
		}
	}
	best := candidate{}
	for _, c := range candidates {
		if len(c.data) >= len(data) {
			continue
		}
		if best.data == nil || len(c.data) < len(best.data) {
			best = c
		}
	}
	if best.data == nil {
		return nil, "", false
	}
	return best.data, best.ext, true
}

// EncodeJPEG is Pillow's `save(quality=…, optimize=True)`: Go's encoder writes
// the fixed Huffman tables, and OptimizeJPEG replaces them with the ones this
// picture earns. Without that second pass a package comes out a tenth larger
// than chgksuite's; with it, the two are within a rounding error.
func EncodeJPEG(img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	optimized, err := OptimizeJPEG(buf.Bytes())
	if err != nil {
		// A file it will not rewrite is still a good file.
		return buf.Bytes(), nil //nolint:nilerr // the unoptimized bytes are the fallback
	}
	return optimized, nil
}

// EncodePNG is Pillow's `save(optimize=True, compress_level=9)`.
func EncodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
