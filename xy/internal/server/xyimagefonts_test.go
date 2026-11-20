package server

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/xyimage"
)

// editedWithFont is an Edited Image whose text is set in an uploaded Face, and
// the Family zip that holds it (ADR-0025).
func editedWithFont(t *testing.T) (img, family []byte, hash string) {
	t.Helper()
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	w, _ := zw.Create("Inter-Bold.otf")
	w.Write([]byte("not really a font"))
	zw.Close()
	family = zb.Bytes()
	sum := sha256.Sum256(family)
	hash = hex.EncodeToString(sum[:])
	recipe, _ := json.Marshal(map[string]any{"v": 1, "bake": xyimage.Bake{
		W: 40, H: 20, BG: "#fff",
		Layers: []xyimage.Layer{{Kind: "svg", Over: 0}},
		Fonts:  []xyimage.Face{{Zip: hash, Path: "Inter-Bold.otf", Family: "xyf-test", Name: "Inter"}},
	}})
	f := &xyimage.File{
		Recipe:   recipe,
		Overlays: [][]byte{[]byte(`<svg viewBox="0 0 40 20"><text font-family="xyf-test">Hi</text></svg>`)},
		Raster:   tinyPNG(t),
	}
	return f.Encode(), family, hash
}

func postEdited(t *testing.T, ts *httptest.Server, c *apiClient, path, formats string, img, family []byte) (*http.Response, []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("source", "? Что на плакате? (img poster.xyimage)\n! кот\n")
	_ = mw.WriteField("filename", "Тур 1")
	if formats != "" {
		_ = mw.WriteField("formats", formats)
	}
	fw, _ := mw.CreateFormFile("img", "poster.xyimage")
	fw.Write(img)
	if family != nil {
		fw, _ = mw.CreateFormFile("font", "whatever.zip")
		fw.Write(family)
	}
	mw.Close()
	req, _ := http.NewRequest("POST", ts.URL+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for _, ck := range c.jar {
		req.AddCookie(ck)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

// The PDF export hands typst the Face under the image's family name, and the
// .4s carries the Family zip in fonts/, named by its hash.
func TestExportCarriesUploadedFonts(t *testing.T) {
	ts, srv := newTestServer(t)
	fake := stubTypst(t, srv)
	c := registerUser(t, srv, ts, 770230, "xyfonts")
	img, family, hash := editedWithFont(t)

	resp, _ := postEdited(t, ts, c, "/api/export/pdf", "", img, family)
	mustStatus(t, resp, http.StatusOK)
	if !fake.hasImage(xyimage.FontKeyPrefix + "xyf-test") {
		t.Fatal("typst was not handed the Face the image is set in")
	}

	resp, b := postEdited(t, ts, c, "/api/export/pack", "4s,docx", img, family)
	mustStatus(t, resp, http.StatusOK)
	names := strings.Join(zipNames(t, b), " ")
	if !strings.Contains(names, "fonts/"+hash+".zip") || !strings.Contains(names, "poster.xyimage") {
		t.Fatalf("the .4s should ship the image and its Family by hash, got %s", names)
	}
}

// Without its font the vector export stops, naming the picture and the font.
func TestExportWithoutTheFontFails(t *testing.T) {
	ts, srv := newTestServer(t)
	stubTypst(t, srv)
	c := registerUser(t, srv, ts, 770231, "xynofont")
	img, _, _ := editedWithFont(t)

	resp, b := postEdited(t, ts, c, "/api/export/pdf", "", img, nil)
	mustStatus(t, resp, http.StatusBadRequest)
	if !strings.Contains(string(b), "poster.xyimage") || !strings.Contains(string(b), "Inter") {
		t.Fatalf("the error should name the picture and the font: %s", b)
	}
}
