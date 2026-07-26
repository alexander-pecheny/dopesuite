package server

import (
	"archive/zip"
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

const packSource = "? Что на картинке? (img pic.png)\n! кот\n@ Автор\n"

// postPack drives the pack endpoint with one 4s source, the given format list
// and optionally the image the source references, and hands back the response
// body plus its Content-Disposition.
func postPack(t *testing.T, ts *httptest.Server, c *apiClient, formats string, withImage bool) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("source", packSource)
	_ = mw.WriteField("filename", "Тур 1")
	_ = mw.WriteField("formats", formats)
	if withImage {
		fw, _ := mw.CreateFormFile("img", "pic.png")
		fw.Write(tinyPNG(t))
	}
	mw.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/api/export/pack", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for _, ck := range c.jar {
		req.AddCookie(ck)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	mustStatus(t, resp, 200)
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return b, resp.Header.Get("Content-Disposition")
}

func zipNames(t *testing.T, b []byte) []string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("response is not a zip: %v", err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	return names
}

// TestExportPackSingleFormatIsBare checks the naming rule: one format asked for
// is one file downloaded, not a zip of one.
func TestExportPackSingleFormatIsBare(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 770210, "packone")

	b, disp := postPack(t, ts, c, "4s", false)
	if string(b) != packSource {
		t.Errorf("4s body = %q, want the source verbatim", b)
	}
	if !strings.Contains(disp, `filename="Тур 1.4s"`) {
		t.Errorf("Content-Disposition = %q", disp)
	}
}

// TestExportPackZipsSeveral checks that two formats come back as one zip, that
// the .4s carries its image alongside (it can only reference it by name), and
// that the .docx does not duplicate that image at the zip root.
func TestExportPackZipsSeveral(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 770211, "packzip")

	b, disp := postPack(t, ts, c, "4s,docx", true)
	if !strings.Contains(disp, `filename="Тур 1.zip"`) {
		t.Errorf("Content-Disposition = %q", disp)
	}
	got := zipNames(t, b)
	want := []string{"pic.png", "Тур 1.4s", "Тур 1.docx"}
	sort.Strings(want)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("zip entries = %v, want %v", got, want)
	}
}

// TestExportPackImagesOnlyWithFourS: docx embeds its own copy, so a pack without
// the .4s has no reason to carry loose image files.
func TestExportPackImagesOnlyWithFourS(t *testing.T) {
	ts, srv := newTestServer(t)
	stubTypst(t, srv)
	c := registerUser(t, srv, ts, 770212, "packimg")

	b, _ := postPack(t, ts, c, "docx,pdf", true)
	for _, n := range zipNames(t, b) {
		if n == "pic.png" {
			t.Error("pack without .4s should not carry loose images")
		}
	}
}

// TestExportPackMobileIsItsOwnFile checks both PDF layouts can be asked for at
// once and land under distinct names.
func TestExportPackMobileIsItsOwnFile(t *testing.T) {
	ts, srv := newTestServer(t)
	stubTypst(t, srv)
	c := registerUser(t, srv, ts, 770213, "packmob")

	b, _ := postPack(t, ts, c, "pdf,pdf_mobile", true)
	got := zipNames(t, b)
	want := []string{"Тур 1.pdf", "Тур 1_mobile.pdf"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("zip entries = %v, want %v", got, want)
	}
}

// TestExportPackRejectsNoFormats: an empty selection is a client bug, not an
// empty download.
func TestExportPackRejectsNoFormats(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 770214, "packnone")

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("source", packSource)
	_ = mw.WriteField("formats", "")
	mw.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/export/pack", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for _, ck := range c.jar {
		req.AddCookie(ck)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	mustStatus(t, resp, 400)
}
