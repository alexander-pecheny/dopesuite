package imagex

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestFit(t *testing.T) {
	cases := []struct{ w, h, max, wantW, wantH int }{
		{100, 50, 2000, 100, 50}, // smaller than the cap: untouched
		{2000, 1000, 2000, 2000, 1000},
		{4000, 2000, 2000, 2000, 1000},
		{2000, 4000, 2000, 1000, 2000}, // portrait
		{4000, 3, 2000, 2000, 1},       // a side that rounds away stays one pixel
		{0, 0, 2000, 0, 0},
	}
	for _, c := range cases {
		w, h := Fit(c.w, c.h, c.max)
		if w != c.wantW || h != c.wantH {
			t.Errorf("Fit(%d, %d, %d) = %d, %d; want %d, %d", c.w, c.h, c.max, w, h, c.wantW, c.wantH)
		}
	}
}

func pngBytes(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

func TestReencodeKeepsSmallPicturesTheirSize(t *testing.T) {
	out, err := Reencode(pngBytes(t, 40, 20, color.RGBA{R: 200, G: 30, B: 30, A: 255}))
	if err != nil {
		t.Fatalf("Reencode: %v", err)
	}
	if out.Width != 40 || out.Height != 20 {
		t.Errorf("size = %dx%d, want 40x20", out.Width, out.Height)
	}
	img, format, err := image.Decode(bytes.NewReader(out.Bytes))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if format != "jpeg" {
		t.Errorf("format = %q, want jpeg — a stored Photo is always our own JPEG", format)
	}
	if got := img.Bounds().Dx(); got != 40 {
		t.Errorf("decoded width = %d, want 40", got)
	}
}

func TestReencodeScalesTheLongSideDown(t *testing.T) {
	out, err := Reencode(pngBytes(t, MaxSide+600, (MaxSide+600)/2, color.White))
	if err != nil {
		t.Fatalf("Reencode: %v", err)
	}
	if out.Width != MaxSide {
		t.Errorf("width = %d, want %d", out.Width, MaxSide)
	}
	if out.Height != MaxSide/2 {
		t.Errorf("height = %d, want %d", out.Height, MaxSide/2)
	}
}

// A phone's JPEG carries EXIF — where and when the receipt was photographed.
// Re-encoding is what drops it, so the stored bytes must not be the sent ones.
func TestReencodeDropsWhateverRodeAlong(t *testing.T) {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("jpeg.Encode: %v", err)
	}
	// An APP1 segment (where EXIF lives) spliced in right after the SOI marker,
	// the way a camera writes one.
	original := buf.Bytes()
	exif := []byte{0xFF, 0xE1, 0x00, 0x10, 'E', 'x', 'i', 'f', 0, 0, 1, 2, 3, 4, 5, 6, 7, 8}
	withExif := append(append(append([]byte{}, original[:2]...), exif...), original[2:]...)

	out, err := Reencode(withExif)
	if err != nil {
		t.Fatalf("Reencode: %v", err)
	}
	if bytes.Contains(out.Bytes, []byte("Exif")) {
		t.Error("the re-encoded picture still carries an Exif segment")
	}
}

func TestReencodeRefusesWhatIsNotAPicture(t *testing.T) {
	if _, err := Reencode([]byte("this is a text file, not a receipt")); err != ErrNotAnImage {
		t.Errorf("Reencode of junk = %v, want ErrNotAnImage", err)
	}
	if _, err := Reencode(nil); err != ErrNotAnImage {
		t.Errorf("Reencode of nothing = %v, want ErrNotAnImage", err)
	}
}
