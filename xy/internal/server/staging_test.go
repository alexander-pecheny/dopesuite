package server

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestHandoutStaging covers the staging lifecycle: stage an image → heartbeat →
// the PDF endpoint uses the staged image (no inline upload) → delete → heartbeat
// 404s.
func TestHandoutStaging(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake not portable to windows")
	}
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 770700, "stg")

	// fake typst that asserts the staged image landed in the render dir.
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-typst")
	body := "#!/bin/sh\nset -e\n[ -f pic.png ] || { echo 'staged image missing' >&2; exit 1; }\n" +
		"eval \"out=\\${$#}\"\nprintf '%%PDF-fake' > \"$out\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XY_TYPST_CMD", script)

	// stage an image
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("img", "pic.png")
	fw.Write(tinyPNG(t))
	mw.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/handouts/stage", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for _, ck := range c.jar {
		req.AddCookie(ck)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	mustStatus(t, resp, 200)
	var sr struct {
		Session string `json:"session"`
	}
	json.NewDecoder(resp.Body).Decode(&sr)
	resp.Body.Close()
	if sr.Session == "" {
		t.Fatal("no session id")
	}

	// heartbeat → 204
	resp = postFormCookied(c, ts.URL+"/api/handouts/heartbeat", url.Values{"session": {sr.Session}})
	mustStatus(t, resp, 204)
	resp.Body.Close()

	// PDF endpoint with the session (no inline image) → fake typst checks pic.png
	var pbuf bytes.Buffer
	pmw := multipart.NewWriter(&pbuf)
	pmw.WriteField("source", "for_question: 1\ncolumns: 3\n\nimage: pic.png\n")
	pmw.WriteField("session", sr.Session)
	pmw.Close()
	preq, _ := http.NewRequest("POST", ts.URL+"/api/handouts/pdf", &pbuf)
	preq.Header.Set("Content-Type", pmw.FormDataContentType())
	for _, ck := range c.jar {
		preq.AddCookie(ck)
	}
	presp, err := http.DefaultClient.Do(preq)
	if err != nil {
		t.Fatal(err)
	}
	mustStatus(t, presp, 200)
	presp.Body.Close()

	// delete the session
	dreq, _ := http.NewRequest("DELETE", ts.URL+"/api/handouts/stage?session="+sr.Session, nil)
	for _, ck := range c.jar {
		dreq.AddCookie(ck)
	}
	dresp, err := http.DefaultClient.Do(dreq)
	if err != nil {
		t.Fatal(err)
	}
	mustStatus(t, dresp, 204)
	dresp.Body.Close()

	// heartbeat now 404 (reaped) → client would re-stage
	resp = postFormCookied(c, ts.URL+"/api/handouts/heartbeat", url.Values{"session": {sr.Session}})
	mustStatus(t, resp, 404)
	resp.Body.Close()
}

func postFormCookied(c *apiClient, urlStr string, form url.Values) *http.Response {
	c.t.Helper()
	req, err := http.NewRequest("POST", urlStr, strings.NewReader(form.Encode()))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, ck := range c.jar {
		req.AddCookie(ck)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	return resp
}
