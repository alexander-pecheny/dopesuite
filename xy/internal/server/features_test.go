package server

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
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

// TestListGroups covers the list_of_lists lifecycle: link several lists into a
// group, see group_id + the group reflected in the snapshot, rename it, and
// dissolve it (members released, group gone).
func TestListGroups(t *testing.T) {
	c, boardID, listID1 := boardWithList(t)
	mkList := func(rank string) int64 {
		resp := c.do("POST", "/api/boards/"+boardID+"/lists", map[string]string{"title_enc": enc("L" + rank), "rank": rank})
		mustStatus(t, resp, 200)
		var l struct {
			ID int64 `json:"id"`
		}
		c.decode(resp, &l)
		return l.ID
	}
	// listID1 has rank "m"; add two more so we have three consecutive lists.
	id1 := mustAtoi(t, listID1)
	id2 := mkList("n")
	id3 := mkList("o")

	// Reject a single-list group.
	resp := c.do("POST", "/api/boards/"+boardID+"/list-groups", map[string]any{"name_enc": enc("solo"), "list_ids": []int64{id1}})
	mustStatus(t, resp, 400)

	// Link the three into a group.
	resp = c.do("POST", "/api/boards/"+boardID+"/list-groups", map[string]any{"name_enc": enc("Тур 1"), "list_ids": []int64{id1, id2, id3}})
	mustStatus(t, resp, 200)
	var grp struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &grp)
	if grp.ID == 0 {
		t.Fatal("no group id")
	}

	// Snapshot: every list carries group_id, and the group is listed by name.
	snap := getSnap(t, c, boardID)
	if len(snap.Groups) != 1 || dec(snap.Groups[0].NameEnc) != "Тур 1" {
		t.Fatalf("groups = %+v", snap.Groups)
	}
	for _, l := range snap.Lists {
		if l.GroupID == nil || *l.GroupID != grp.ID {
			t.Fatalf("list %d not in group: %+v", l.ID, l)
		}
	}

	// Rename.
	resp = c.do("PATCH", "/api/list-groups/"+itoa(grp.ID), map[string]string{"name_enc": enc("Тур I")})
	mustStatus(t, resp, 204)
	snap = getSnap(t, c, boardID)
	if dec(snap.Groups[0].NameEnc) != "Тур I" {
		t.Fatalf("rename not applied: %q", dec(snap.Groups[0].NameEnc))
	}

	// Dissolve: members released, group gone.
	resp = c.do("DELETE", "/api/list-groups/"+itoa(grp.ID), nil)
	mustStatus(t, resp, 204)
	snap = getSnap(t, c, boardID)
	if len(snap.Groups) != 0 {
		t.Fatalf("group still present: %+v", snap.Groups)
	}
	for _, l := range snap.Lists {
		if l.GroupID != nil {
			t.Fatalf("list %d still grouped after dissolve", l.ID)
		}
	}
}

func getSnap(t *testing.T, c *apiClient, boardID string) boardSnapshot {
	t.Helper()
	resp := c.do("GET", "/api/boards/"+boardID, nil)
	mustStatus(t, resp, 200)
	var snap boardSnapshot
	c.decode(resp, &snap)
	return snap
}

func mustAtoi(t *testing.T, s string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
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

// TestExportDocx drives the export endpoint with a real PNG and verifies the Go
// exporter embeds it: the response is a valid docx zip carrying a word/media
// image part and a <w:drawing> referencing it. (Image-bearing docx used to fall
// back to chgksuite; it's now pure Go.)
func TestExportDocx(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 770200, "exp")

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("source", "? Что на картинке? (img pic.png)\n! кот\n@ Автор\n")
	_ = mw.WriteField("filename", "Тур 1")
	fw, _ := mw.CreateFormFile("img", "pic.png")
	fw.Write(tinyPNG(t))
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
	b := out.Bytes()

	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("response is not a docx zip: %v", err)
	}
	var hasMedia, hasDrawing bool
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "word/media/image") {
			hasMedia = true
		}
		if f.Name == "word/document.xml" {
			rc, _ := f.Open()
			d, _ := io.ReadAll(rc)
			rc.Close()
			if strings.Contains(string(d), "<w:drawing>") && strings.Contains(string(d), "r:embed=") {
				hasDrawing = true
			}
		}
	}
	if !hasMedia {
		t.Error("docx has no embedded image part")
	}
	if !hasDrawing {
		t.Error("document.xml has no <w:drawing> referencing the image")
	}
}

// tinyPNG returns a minimal valid PNG for image-embedding tests.
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
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

// TestHandoutsPDF drives the handout-PDF endpoint with a fake typst that writes
// a PDF to its output argument, verifying the in-process Go render wiring (the
// .typ generation itself is covered by the handout package's TypParity test).
func TestHandoutsPDF(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake not portable to windows")
	}
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 770500, "hndt")

	dir := t.TempDir()
	script := filepath.Join(dir, "fake-typst")
	// typst is invoked as: compile --root / --font-path <fonts> source.typ source.pdf
	// (cwd = render scratch dir). Write a fake PDF to the last argument.
	body := "#!/bin/sh\n" +
		"set -e\n" +
		"[ \"$1\" = compile ] || { echo 'not compile' >&2; exit 1; }\n" +
		"eval \"out=\\${$#}\"\n" +
		"printf '%%PDF-fake' > \"$out\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XY_TYPST_CMD", script)

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

// TestHandoutsPDFMultiBlockCRLF guards the CRLF regression: browsers send
// textarea values as CRLF in multipart, which used to collapse the "---"-
// separated .hndt blocks into one. The fake typst asserts 3 #handout blocks
// survived into the generated source.typ.
func TestHandoutsPDFMultiBlockCRLF(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake not portable to windows")
	}
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 770550, "hndtm")

	dir := t.TempDir()
	script := filepath.Join(dir, "fake-typst")
	body := "#!/bin/sh\n" +
		"set -e\n" +
		"n=$(grep -c '#handout(' source.typ || true)\n" +
		"[ \"$n\" = 3 ] || { echo \"expected 3 blocks, got $n\" >&2; exit 1; }\n" +
		"eval \"out=\\${$#}\"\n" +
		"printf '%%PDF-fake' > \"$out\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XY_TYPST_CMD", script)

	// CRLF line endings, как их шлёт браузер.
	src := "for_question: 2\r\ncolumns: 3\r\n\r\nПервый\r\n---\r\nfor_question: 7\r\ncolumns: 3\r\n\r\nВторой\r\n---\r\nfor_question: 13\r\ncolumns: 3\r\n\r\nТретий\r\n"
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("source", src)
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
	resp.Body.Close()
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
