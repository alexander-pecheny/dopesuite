package docx

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"math/rand"
	"strings"
	"testing"

	"xy/internal/chgk/fsource"
)

// photo is a noisy gradient: it compresses like a photograph, not like a
// screenshot, which is what makes the PNG-vs-JPEG choice matter.
func photo(w, h int, alpha uint8) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewSource(3))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x/8 + r.Intn(30)), uint8(y/8 + r.Intn(30)), 90, alpha})
		}
	}
	return img
}

// mediaParts returns the word/media/* entries of a docx, and its [Content_Types].xml.
func mediaParts(t *testing.T, doc []byte) (parts map[string]int, contentTypes string) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(doc), int64(len(doc)))
	if err != nil {
		t.Fatalf("not a docx zip: %v", err)
	}
	parts = map[string]int{}
	for _, f := range zr.File {
		switch {
		case strings.HasPrefix(f.Name, "word/media/"):
			parts[f.Name] = int(f.UncompressedSize64)
		case f.Name == "[Content_Types].xml":
			rc, _ := f.Open()
			d, _ := io.ReadAll(rc)
			rc.Close()
			contentTypes = string(d)
		}
	}
	return parts, contentTypes
}

// A photograph must not go into the .docx as a full-resolution lossless PNG: that
// was making the export several times bigger than the pictures in it. Word reads
// JPEG perfectly well — the re-encode exists for WebP, not for Word.
func TestPhotoEmbedsAsJPEG(t *testing.T) {
	var src bytes.Buffer
	if err := jpeg.Encode(&src, photo(2000, 1500, 255), &jpeg.Options{Quality: 92}); err != nil {
		t.Fatal(err)
	}
	out, err := Export(fsource.Parse("? Что на фото? (img photo.jpg)\n! Ничего\n", "chgk"),
		map[string][]byte{"photo.jpg": src.Bytes()})
	if err != nil {
		t.Fatal(err)
	}
	parts, ct := mediaParts(t, out)
	if len(parts) != 1 {
		t.Fatalf("want exactly one media part, got %v", parts)
	}
	for name, size := range parts {
		if !strings.HasSuffix(name, ".jpg") {
			t.Errorf("photo embedded as %s, want a .jpg", name)
		}
		if size > src.Len()/2 {
			t.Errorf("%s is %d KB — the %d KB source barely shrank", name, size/1024, src.Len()/1024)
		}
	}
	// Word refuses a part whose extension has no declared content type.
	if !strings.Contains(ct, `Extension="jpg" ContentType="image/jpeg"`) {
		t.Errorf("[Content_Types].xml does not declare jpg:\n%s", ct)
	}
}

// Transparency can't survive JPEG, so those images stay PNG — and then the png
// content type is the one that has to be declared.
func TestTransparentImageStaysPNG(t *testing.T) {
	var src bytes.Buffer
	if err := png.Encode(&src, photo(400, 300, 128)); err != nil {
		t.Fatal(err)
	}
	out, err := Export(fsource.Parse("? Что тут? (img pic.png)\n! Ничего\n", "chgk"),
		map[string][]byte{"pic.png": src.Bytes()})
	if err != nil {
		t.Fatal(err)
	}
	parts, ct := mediaParts(t, out)
	for name := range parts {
		if !strings.HasSuffix(name, ".png") {
			t.Errorf("transparent image embedded as %s, want a .png", name)
		}
	}
	if !strings.Contains(ct, `Extension="png" ContentType="image/png"`) {
		t.Errorf("[Content_Types].xml does not declare png:\n%s", ct)
	}
}

// A missing image degrades to a marker run rather than failing the export.
func TestMissingImageDegrades(t *testing.T) {
	out, err := Export(fsource.Parse("? Где картинка? (img gone.png)\n! Нигде\n", "chgk"), nil)
	if err != nil {
		t.Fatal(err)
	}
	parts, _ := mediaParts(t, out)
	if len(parts) != 0 {
		t.Errorf("a missing image produced media parts: %v", parts)
	}
	if !strings.Contains(docText(t, out), "нет изображения: gone.png") {
		t.Error("no marker run for the missing image")
	}
}
