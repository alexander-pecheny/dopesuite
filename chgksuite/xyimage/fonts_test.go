package xyimage

import (
	"archive/zip"
	"bytes"
	"errors"
	"strings"
	"testing"
)

const testHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func zipOf(t *testing.T, faces map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range faces {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func withFace() *File {
	return &File{Bake: Bake{Fonts: []Face{{Zip: testHash, Path: "Inter-Bold.otf", Family: "xyf-a", Name: "Inter"}}}}
}

func TestFacesComeOutOfTheFamilyZip(t *testing.T) {
	zips := map[string][]byte{FontZipKey(testHash): zipOf(t, map[string]string{"Inter-Bold.otf": "BOLD", "Inter-Regular.otf": "REG"})}
	faces, err := withFace().Faces(zips)
	if err != nil {
		t.Fatal(err)
	}
	if len(faces) != 1 || string(faces[FontKeyPrefix+"xyf-a"]) != "BOLD" {
		t.Fatalf("want just the bold face under its family, got %v", faces)
	}
}

// A Face the export was not given fails it, naming the Family (ADR-0025).
func TestMissingFaceNamesTheFamily(t *testing.T) {
	for name, zips := range map[string]map[string][]byte{
		"no zip":  nil,
		"no face": {FontZipKey(testHash): zipOf(t, map[string]string{"Inter-Regular.otf": "REG"})},
		"not zip": {FontZipKey(testHash): []byte("junk")},
	} {
		_, err := withFace().Faces(zips)
		var mf *MissingFontError
		if !errors.As(err, &mf) || mf.Font != "Inter" {
			t.Errorf("%s: want a MissingFontError for Inter, got %v", name, err)
		}
		if msg := UserError("poster.xyimage", err).Error(); !strings.Contains(msg, "poster.xyimage") || !strings.Contains(msg, "Inter") {
			t.Errorf("%s: the message should name the picture and the font: %q", name, msg)
		}
	}
}

func TestFontZipKey(t *testing.T) {
	if !IsFontZipKey(FontZipKey(testHash)) {
		t.Fatal("a Family zip's own key is not recognised")
	}
	for _, name := range []string{"poster.png", "fonts/inter.zip", testHash + ".zip", "fonts/" + strings.ToUpper(testHash) + ".zip"} {
		if IsFontZipKey(name) {
			t.Errorf("%q is not a Family zip", name)
		}
	}
}
