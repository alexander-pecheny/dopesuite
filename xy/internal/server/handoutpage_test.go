package server

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHandoutPage follows a tour's page setup like TestCommentFirst follows its
// flag: stored checked and compact, taken by a group from its first list, and
// handed back to every member when the group is dissolved.
func TestHandoutPage(t *testing.T) {
	c, boardID, listID1 := boardWithList(t)
	resp := c.do("POST", "/api/boards/"+boardID+"/lists", map[string]string{"title": enc("L2"), "rank": "n"})
	mustStatus(t, resp, 200)
	var l2 struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &l2)

	a5 := `{"paperwidth":148,"paperheight":210,"font":"PT Serif"}`
	mustStatus(t, c.do("PATCH", "/api/lists/"+listID1, map[string]string{"handout_page": a5}), 204)
	for _, bad := range []string{`{"paperwidth":5}`, `{"font":"/etc/x"}`, `{"unknown":1}`, `not json`} {
		mustStatus(t, c.do("PATCH", "/api/lists/"+listID1, map[string]string{"handout_page": bad}), http.StatusBadRequest)
	}
	resp = c.do("POST", "/api/boards/"+boardID+"/list-groups", map[string]any{"name": enc("Тур"), "list_ids": []int64{mustAtoi(t, listID1), l2.ID}})
	mustStatus(t, resp, 200)
	var grp struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &grp)
	if g := getSnap(t, c, boardID).Groups[0]; g.HandoutPage == nil || *g.HandoutPage != a5 {
		t.Fatalf("the group took %v, not its first list's setup", g.HandoutPage)
	}
	mustStatus(t, c.do("DELETE", "/api/list-groups/"+itoa(grp.ID), nil), 204)
	for _, l := range getSnap(t, c, boardID).Lists {
		if l.HandoutPage == nil || *l.HandoutPage != a5 {
			t.Fatalf("list %d has %v after the dissolve", l.ID, l.HandoutPage)
		}
	}
	mustStatus(t, c.do("PATCH", "/api/lists/"+listID1, map[string]string{"handout_page": "{}"}), 204)
	for _, l := range getSnap(t, c, boardID).Lists {
		if itoa(l.ID) == listID1 && l.HandoutPage != nil {
			t.Fatalf("the defaults were stored as %q, not cleared", *l.HandoutPage)
		}
	}
}

func TestHandoutPageArgs(t *testing.T) {
	p, err := parseHandoutPage(`{"paperwidth":148,"margin_left":5,"no_auto_resize_images":true,"tikz_mm":0}`)
	if err != nil {
		t.Fatal(err)
	}
	a := p.args()
	if a.PaperWidth != 148 || a.PaperHeight != 297 || a.MarginLeft != 5 || !a.Resize.Off || a.TikzMM == nil || *a.TikzMM != 0 {
		t.Fatalf("args = %+v", a)
	}
}

func postHandoutForm(t *testing.T, ts *httptest.Server, c *apiClient, path string, fields map[string]string) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("source", "for_question: 1\ncolumns: 3\n\nРаз\n")
	for k, v := range fields {
		_ = mw.WriteField(k, v)
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
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// TestHandoutRendersTakeThePage: the page setup a render is sent is the one
// typst lays out, and a setup or a team count out of bounds is refused.
func TestHandoutRendersTakeThePage(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 770501, "page")
	fake := stubTypst(t, srv)

	mustStatus(t, postHandoutForm(t, ts, c, "/api/handouts/pdf", map[string]string{"page": `{"paperwidth":148,"paperheight":210}`}), 200)
	if src := fake.source(); !strings.Contains(src, "148mm") || !strings.Contains(src, "210mm") {
		t.Fatalf("typst was not given the A5 page:\n%s", src)
	}
	mustStatus(t, postHandoutForm(t, ts, c, "/api/handouts/pdf", map[string]string{"page": `{"paperwidth":1}`}), http.StatusBadRequest)
	for _, teams := range []string{"", "0", "abc", "100000"} {
		mustStatus(t, postHandoutForm(t, ts, c, "/api/handouts/pack", map[string]string{"teams": teams}), http.StatusBadRequest)
	}
}
