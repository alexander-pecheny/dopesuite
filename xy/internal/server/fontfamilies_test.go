package server

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func uploadFontFamily(t *testing.T, c *apiClient, ts *httptest.Server, boardID int64, meta string, cipher []byte) int64 {
	t.Helper()
	body := new(bytes.Buffer)
	mw := multipart.NewWriter(body)
	_ = mw.WriteField("meta", meta)
	fw, _ := mw.CreateFormFile("blob", "blob")
	fw.Write(cipher)
	mw.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/boards/"+itoa(boardID)+"/fonts", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for _, ck := range c.jar {
		req.AddCookie(ck)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	mustStatus(t, resp, http.StatusOK)
	var out struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &out)
	return out.ID
}

func fontBlobRef(t *testing.T, srv *server, id int64) string {
	t.Helper()
	var ref string
	if err := srv.db.QueryRow(`select blob_ref from font_families where id = ?`, id).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	return ref
}

// A Family goes up as opaque meta and an opaque zip, comes down in the board's
// snapshot (meta only) and on its own (the zip), counts towards the quota,
// survives the orphan sweep, and once deleted is reaped with its blob.
func TestFontFamilyLifecycle(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 771100, "fonts-user")
	bID, _, _ := makeBoardCard(t, c, "b")
	before := storageUsed(t, c)

	zip := []byte("xy1-an-encrypted-zip-of-faces")
	meta := enc("an encrypted name, hash and faces")
	id := uploadFontFamily(t, c, ts, bID, meta, zip)

	resp := c.do("GET", "/api/boards/"+itoa(bID), nil)
	mustStatus(t, resp, http.StatusOK)
	var snap struct {
		Fonts []fontFamilyDTO `json:"fonts"`
	}
	c.decode(resp, &snap)
	if len(snap.Fonts) != 1 || snap.Fonts[0].ID != id || snap.Fonts[0].Meta != meta || snap.Fonts[0].Size != int64(len(zip)) {
		t.Fatalf("snapshot fonts = %+v", snap.Fonts)
	}

	resp = c.do("GET", "/api/fonts/"+itoa(id), nil)
	mustStatus(t, resp, http.StatusOK)
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Equal(got, zip) {
		t.Fatalf("zip came back as %q", got)
	}

	if grew := storageUsed(t, c) - before; grew < int64(len(zip)) {
		t.Fatalf("storage grew by %d, want at least the zip's %d bytes", grew, len(zip))
	}

	ref := fontBlobRef(t, srv, id)
	if _, err := srv.sweepOrphanBlobs(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if !blobExists(t, srv, ref) {
		t.Fatal("the orphan sweep took a live Family's zip")
	}

	mustStatus(t, c.do("DELETE", "/api/fonts/"+itoa(id), nil), http.StatusNoContent)
	mustStatus(t, c.do("DELETE", "/api/fonts/"+itoa(id), nil), http.StatusNoContent)
	mustStatus(t, c.do("GET", "/api/fonts/"+itoa(id), nil), http.StatusNotFound)
	if used := storageUsed(t, c); used != before {
		t.Fatalf("a deleted Family still counts: %d, want %d", used, before)
	}

	if _, err := srv.reapOnce(context.Background(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if blobExists(t, srv, ref) {
		t.Fatal("the reaped Family's zip is still on disk")
	}
}

// Another board's member cannot read a Family.
func TestFontFamilyIsTheBoards(t *testing.T) {
	ts, srv := newTestServer(t)
	owner := registerUser(t, srv, ts, 771101, "fonts-owner")
	other := registerUser(t, srv, ts, 771102, "fonts-other")
	bID, _, _ := makeBoardCard(t, owner, "b")
	id := uploadFontFamily(t, owner, ts, bID, enc("m"), []byte("xy1-zip"))
	if resp := other.do("GET", "/api/fonts/"+itoa(id), nil); resp.StatusCode == http.StatusOK {
		t.Fatal("a stranger read the board's font")
	}
}
