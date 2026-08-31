package dbtext

import (
	"bytes"
	"image"
	"image/png"

	"golang.org/x/image/draw"

	"xy/internal/chgk/imgconv"
)

// resize is the PIL half of parse_and_upload_image: a picture whose directive
// asks for a size is published at that size. nil means it is already right.
// The resampling is Go's rather than Pillow's LANCZOS, so the bytes differ from
// chgksuite's while the dimensions do not.
func resize(data []byte, width, height float64) ([]byte, error) {
	img, err := imgconv.Decode(data)
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	w, h := float64(b.Dx()), float64(b.Dy())
	switch {
	case width > 0 && height <= 0:
		height = h * (width / w)
	case width <= 0 && height > 0:
		width = w * (height / h)
	}
	if int(width) == b.Dx() && int(height) == b.Dy() {
		return nil, nil
	}
	dst := image.NewRGBA(image.Rect(0, 0, int(width), int(height)))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
