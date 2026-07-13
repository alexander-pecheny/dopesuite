package imgconv_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"testing"

	"xy/internal/chgk/imgconv"
)

// photo is a 1600×1200 noisy gradient: it compresses like a photograph (badly, as
// PNG) rather than like a screenshot.
func photo(alpha uint8) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 1600, 1200))
	r := rand.New(rand.NewSource(1))
	for y := range 1200 {
		for x := range 1600 {
			img.Set(x, y, color.RGBA{
				uint8(x/3 + r.Intn(40)), uint8(y/5 + r.Intn(40)), uint8(128 + r.Intn(40)), alpha})
		}
	}
	return img
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// The regression this exists for: a photograph embedded losslessly at full
// resolution is most of the exported file. Drawn ~4in wide, it has no business
// being megabytes.
func TestForPDFShrinksPhotos(t *testing.T) {
	raw := encodePNG(t, photo(255))
	data, ext, err := imgconv.ForExport(raw, 4, 3) // drawn 4in × 3in
	if err != nil {
		t.Fatal(err)
	}
	if ext != "jpg" {
		t.Errorf("an opaque photo should go in as JPEG, got %q", ext)
	}
	if len(data) > len(raw)/10 {
		t.Errorf("photo barely shrank: %d KB → %d KB", len(raw)/1024, len(data)/1024)
	}
	// It is downscaled to the drawn size at ExportDPI, not left at 1600×1200.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if want := int(4 * imgconv.ExportDPI); cfg.Width != want {
		t.Errorf("width = %d px, want %d (4in at %.0f dpi)", cfg.Width, want, imgconv.ExportDPI)
	}
	t.Logf("%d KB PNG → %d KB %s at %d×%d", len(raw)/1024, len(data)/1024, ext, cfg.Width, cfg.Height)
}

// Transparency can't survive JPEG, so those stay PNG (still downscaled).
func TestForPDFKeepsAlphaAsPNG(t *testing.T) {
	raw := encodePNG(t, photo(128))
	data, ext, err := imgconv.ForExport(raw, 4, 3)
	if err != nil {
		t.Fatal(err)
	}
	if ext != "png" {
		t.Fatalf("a transparent image must stay PNG, got %q", ext)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != int(4*imgconv.ExportDPI) {
		t.Errorf("transparent image was not downscaled: %d px wide", cfg.Width)
	}
}

// A small image is left at its own resolution — upscaling only invents pixels.
func TestForPDFDoesNotUpscale(t *testing.T) {
	small := image.NewRGBA(image.Rect(0, 0, 64, 48))
	data, _, err := imgconv.ForExport(encodePNG(t, small), 4, 3)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 64 || cfg.Height != 48 {
		t.Errorf("a 64×48 image came out %d×%d", cfg.Width, cfg.Height)
	}
}
