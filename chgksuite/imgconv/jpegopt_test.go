package imgconv

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"math/rand"
	"testing"
)

// TestOptimizeJPEGIsLossless: the rewrite changes the codes, never the picture.
func TestOptimizeJPEGIsLossless(t *testing.T) {
	for _, tc := range []struct {
		name string
		img  image.Image
	}{
		{"photo-ish", noisy(320, 240, 7)},
		{"flat", flat(64, 64, color.RGBA{200, 30, 30, 255})},
		{"gradient", gradient(200, 150)},
		{"gray", gray(120, 90)},
		{"tiny", noisy(3, 3, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := jpeg.Encode(&buf, tc.img, &jpeg.Options{Quality: 80}); err != nil {
				t.Fatal(err)
			}
			original := buf.Bytes()
			optimized, err := OptimizeJPEG(original)
			if err != nil {
				t.Fatalf("optimize: %v", err)
			}
			before, err := jpeg.Decode(bytes.NewReader(original))
			if err != nil {
				t.Fatal(err)
			}
			after, err := jpeg.Decode(bytes.NewReader(optimized))
			if err != nil {
				t.Fatalf("the rewritten file does not decode: %v", err)
			}
			if before.Bounds() != after.Bounds() {
				t.Fatalf("bounds %v became %v", before.Bounds(), after.Bounds())
			}
			for y := before.Bounds().Min.Y; y < before.Bounds().Max.Y; y++ {
				for x := before.Bounds().Min.X; x < before.Bounds().Max.X; x++ {
					if before.At(x, y) != after.At(x, y) {
						t.Fatalf("pixel %d,%d changed: %v → %v", x, y, before.At(x, y), after.At(x, y))
					}
				}
			}
			if len(optimized) > len(original) {
				t.Errorf("grew from %d to %d bytes", len(original), len(optimized))
			}
			t.Logf("%d → %d bytes (%.1f%% off)", len(original), len(optimized),
				100*float64(len(original)-len(optimized))/float64(len(original)))
		})
	}
}

// TestOptimizeJPEGDeclinesWhatItCannotRead: a caller keeps its own bytes.
func TestOptimizeJPEGDeclinesWhatItCannotRead(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"not a jpeg", []byte("PNG\r\n")},
		{"truncated", []byte{0xFF, 0xD8, 0xFF}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := OptimizeJPEG(tc.data); err == nil {
				t.Error("want an error, got none")
			}
		})
	}
}

func noisy(w, h int, seed int64) image.Image {
	r := rand.New(rand.NewSource(seed))
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			m.Set(x, y, color.RGBA{uint8(r.Intn(256)), uint8(r.Intn(256)), uint8(r.Intn(256)), 255})
		}
	}
	return m
}

func flat(w, h int, c color.RGBA) image.Image {
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			m.Set(x, y, c)
		}
	}
	return m
}

func gradient(w, h int) image.Image {
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			m.Set(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), 128, 255})
		}
	}
	return m
}

func gray(w, h int) image.Image {
	m := image.NewGray(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			m.SetGray(x, y, color.Gray{uint8((x*x + y*y) % 256)})
		}
	}
	return m
}
