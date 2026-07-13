// Package imgconv normalizes a referenced image to PNG.
//
// Both exporters need this and neither can take the bytes as they are: Word won't
// display WebP (which is what xy usually stores, since the client recompresses
// attachments to WebP q70), and typst's image() only reads a few formats. Decoding
// once, here, also yields the pixel dimensions the sizing maths needs.
package imgconv

import (
	"bytes"
	"image"
	"image/png"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"
)

// ToPNG decodes an image of any supported format (PNG/JPEG/GIF/WebP) and
// re-encodes it as PNG, returning it with its pixel dimensions.
func ToPNG(raw []byte) (data []byte, w, h int, err error) {
	img, _, err := image.Decode(bytes.NewReader(raw))
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
