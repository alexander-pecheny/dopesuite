package server

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// boardWithList registers a user and creates a board + list, returning the
// client and the board + list ids (as strings).
func boardWithList(t *testing.T) (c *apiClient, boardID, listID string) {
	t.Helper()
	ts, srv := newTestServer(t)
	c = registerUser(t, srv, ts, 770100, "feat")
	resp := c.do("POST", "/api/boards", map[string]string{
		"name_enc": enc("b"), "kdf_salt": enc("s"),
		"kdf_params": `{"kdf":"scrypt","N":1,"r":1,"p":1}`, "wrapped_key": enc("w"), "verify_token": enc("v"),
	})
	mustStatus(t, resp, 200)
	var b struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &b)
	boardID = itoa(b.ID)
	resp = c.do("POST", "/api/boards/"+boardID+"/lists", map[string]string{"title_enc": enc("L"), "rank": "m"})
	mustStatus(t, resp, 200)
	var l struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &l)
	return c, boardID, itoa(l.ID)
}

// TestPatchCardKind covers changing a card's kind after creation (and rejecting
// a bogus kind).
func TestPatchCardKind(t *testing.T) {
	c, boardID, listID := boardWithList(t)

	resp := c.do("POST", "/api/lists/"+listID+"/cards", map[string]string{
		"description_enc": enc("? q"), "rank": "m", "kind": "question",
	})
	mustStatus(t, resp, 200)
	var card struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &card)
	cardID := itoa(card.ID)

	resp = c.do("PATCH", "/api/cards/"+cardID, map[string]string{"kind": "heading"})
	mustStatus(t, resp, 204)

	resp = c.do("PATCH", "/api/cards/"+cardID, map[string]string{"kind": "bogus"})
	mustStatus(t, resp, 400)

	resp = c.do("GET", "/api/boards/"+boardID, nil)
	mustStatus(t, resp, 200)
	var snap boardSnapshot
	c.decode(resp, &snap)
	if len(snap.Cards) != 1 || snap.Cards[0].Kind != "heading" {
		t.Fatalf("kind not persisted: %+v", snap.Cards)
	}
}

// TestExportDocx drives the export endpoint with a fake chgksuite that asserts
// the scratch dir holds source.4s + the referenced image, then writes a .docx.
func TestExportDocx(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake not portable to windows")
	}
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 770200, "exp")

	// fake chgksuite: verifies the inputs landed, emits result.docx.
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-chgksuite")
	body := "#!/bin/sh\n" +
		"set -e\n" +
		"[ -f source.4s ] || { echo 'no source' >&2; exit 1; }\n" +
		"[ -f pic.jpg ] || { echo 'no image' >&2; exit 1; }\n" +
		"grep -q 'img pic.jpg' source.4s || { echo 'source missing img directive' >&2; exit 1; }\n" +
		"printf 'PK-fake-docx' > result.docx\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XY_CHGKSUITE_CMD", script)

	// multipart: source + one image part named "img".
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("source", "? Что на картинке?\n(img pic.jpg)\n! кот\n")
	_ = mw.WriteField("filename", "Тур 1")
	fw, _ := mw.CreateFormFile("img", "pic.jpg")
	fw.Write([]byte("\xff\xd8\xff fake jpeg bytes"))
	mw.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/api/export/docx", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for _, ck := range c.jar {
		req.AddCookie(ck)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	mustStatus(t, resp, 200)
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" {
		t.Fatalf("content-type = %q", ct)
	}
	out := new(bytes.Buffer)
	out.ReadFrom(resp.Body)
	if out.String() != "PK-fake-docx" {
		t.Fatalf("body = %q", out.String())
	}
}

// TestExportDocxRealChgksuite drives the endpoint through the actual chgksuite
// binary when one is available (XY_CHGKSUITE_TEST_BIN or the prod venv path),
// validating the real `compose docx` argv contract + a genuine .docx (zip) out.
func TestExportDocxRealChgksuite(t *testing.T) {
	bin := os.Getenv("XY_CHGKSUITE_TEST_BIN")
	if bin == "" {
		bin = "/opt/xy/.venv/bin/chgksuite"
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("chgksuite not present at %s", bin)
	}
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 770400, "real")
	t.Setenv("XY_CHGKSUITE_CMD", bin)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("source", "Чемпионат\n\n? Сколько будет 2+2?\n! 4\n@ Автор\n")
	_ = mw.WriteField("filename", "Тур 1")
	mw.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/api/export/docx", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for _, ck := range c.jar {
		req.AddCookie(ck)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	mustStatus(t, resp, 200)
	defer resp.Body.Close()
	out := new(bytes.Buffer)
	out.ReadFrom(resp.Body)
	// .docx is a zip — magic bytes "PK\x03\x04".
	if b := out.Bytes(); len(b) < 4 || b[0] != 'P' || b[1] != 'K' {
		t.Fatalf("not a zip/docx (len=%d, prefix=%x)", len(b), b[:min(4, len(b))])
	}
}

// TestHandoutsPDF drives the handout-PDF endpoint with a fake chgksuite that
// asserts the scratch dir holds source.hndt + the referenced image and the
// hndt2pdf argv, then emits a result.pdf.
func TestHandoutsPDF(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake not portable to windows")
	}
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 770500, "hndt")

	dir := t.TempDir()
	script := filepath.Join(dir, "fake-chgksuite")
	body := "#!/bin/sh\n" +
		"set -e\n" +
		"[ \"$1\" = handouts ] || { echo 'not handouts' >&2; exit 1; }\n" +
		"[ \"$2\" = hndt2pdf ] || { echo 'not hndt2pdf' >&2; exit 1; }\n" +
		"[ -f source.hndt ] || { echo 'no source' >&2; exit 1; }\n" +
		"[ -f pic.jpg ] || { echo 'no image' >&2; exit 1; }\n" +
		"grep -q 'for_question: 1' source.hndt || { echo 'bad source' >&2; exit 1; }\n" +
		"printf '%%PDF-fake' > result.pdf\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XY_CHGKSUITE_CMD", script)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("source", "for_question: 1\ncolumns: 3\n\nimage: pic.jpg\n")
	_ = mw.WriteField("filename", "Тур 1")
	fw, _ := mw.CreateFormFile("img", "pic.jpg")
	fw.Write([]byte("\xff\xd8\xff fake jpeg bytes"))
	mw.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/api/handouts/pdf", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for _, ck := range c.jar {
		req.AddCookie(ck)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	mustStatus(t, resp, 200)
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "application/pdf" {
		t.Fatalf("content-type = %q", ct)
	}
	out := new(bytes.Buffer)
	out.ReadFrom(resp.Body)
	if out.String() != "%PDF-fake" {
		t.Fatalf("body = %q", out.String())
	}
}

// TestHandoutsPDFRejectsEmpty checks the empty-source guard.
func TestHandoutsPDFRejectsEmpty(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 770600, "hndt2")
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("source", "  ")
	mw.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/handouts/pdf", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for _, ck := range c.jar {
		req.AddCookie(ck)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	mustStatus(t, resp, 400)
}

// TestHandoutMetaRoundTrip covers create-with-meta, patch-set, patch-clear and
// snapshot exposure of cards.handout_meta_enc.
func TestHandoutMetaRoundTrip(t *testing.T) {
	c, boardID, listID := boardWithList(t)

	resp := c.do("POST", "/api/lists/"+listID+"/cards", map[string]string{
		"description_enc": enc("? q"), "rank": "m", "kind": "question",
		"handout_meta_enc": enc("columns: 2"),
	})
	mustStatus(t, resp, 200)
	var card struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &card)
	cardID := itoa(card.ID)

	snap := getSnapshotFor(t, c, boardID)
	if len(snap.Cards) != 1 || snap.Cards[0].HandoutMetaEnc == nil || *snap.Cards[0].HandoutMetaEnc != enc("columns: 2") {
		t.Fatalf("handout_meta not persisted on create: %+v", snap.Cards)
	}

	// patch to a new value
	resp = c.do("PATCH", "/api/cards/"+cardID, map[string]string{"handout_meta_enc": enc("columns: 4\nrows: 2")})
	mustStatus(t, resp, 204)
	snap = getSnapshotFor(t, c, boardID)
	if snap.Cards[0].HandoutMetaEnc == nil || *snap.Cards[0].HandoutMetaEnc != enc("columns: 4\nrows: 2") {
		t.Fatalf("handout_meta not updated: %+v", snap.Cards[0])
	}

	// clear with empty string
	resp = c.do("PATCH", "/api/cards/"+cardID, map[string]string{"handout_meta_enc": ""})
	mustStatus(t, resp, 204)
	snap = getSnapshotFor(t, c, boardID)
	if snap.Cards[0].HandoutMetaEnc != nil {
		t.Fatalf("handout_meta not cleared: %+v", snap.Cards[0])
	}
}

func getSnapshotFor(t *testing.T, c *apiClient, boardID string) boardSnapshot {
	t.Helper()
	resp := c.do("GET", "/api/boards/"+boardID, nil)
	mustStatus(t, resp, 200)
	var snap boardSnapshot
	c.decode(resp, &snap)
	return snap
}

// TestExportDocxRejectsEmpty checks the empty-source guard.
func TestExportDocxRejectsEmpty(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 770300, "exp2")
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("source", "   ")
	mw.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/export/docx", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for _, ck := range c.jar {
		req.AddCookie(ck)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	mustStatus(t, resp, 400)
}
