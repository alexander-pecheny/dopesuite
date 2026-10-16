package server

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/url"
	"testing"
)

func TestRenameLegacyFields(t *testing.T) {
	for in, want := range map[string]string{
		`{"description_enc":"d","rank":"m"}`:                          `{"description":"d","rank":"m"}`,
		`{"events":[{"payload_enc":"p","card_id":9007199254740993}]}`: `{"events":[{"card_id":9007199254740993,"payload":"p"}]}`,
		`{"name_enc":"old","name":"new"}`:                             `{"name":"new"}`,
		`{"title":"t"}`:                                               `{"title":"t"}`,
		`{"title_enc":`:                                               `{"title_enc":`,
	} {
		if got := string(renameLegacyFields([]byte(in))); got != want {
			t.Errorf("%s → %s, want %s", in, got, want)
		}
	}
}

// TestWritesAcceptLegacyNames: a write queued before v29, with the old field
// names, still lands.
func TestWritesAcceptLegacyNames(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 990303, "legacy")
	boardID := newBoard(t, c, "board")

	resp := c.do("POST", "/api/boards/"+boardID+"/lists", map[string]string{"title_enc": enc("список"), "rank": "m"})
	mustStatus(t, resp, 200)
	var l struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &l)
	resp = c.do("POST", "/api/lists/"+itoa(l.ID)+"/cards", map[string]string{"description_enc": enc("вопрос"), "alias_enc": enc("а"), "rank": "m"})
	mustStatus(t, resp, 200)
	var card struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &card)
	mustStatus(t, c.do("POST", "/api/cards/"+itoa(card.ID)+"/comments", map[string]string{"payload_enc": enc("коммент")}), 204)
	mustStatus(t, c.do("POST", "/api/boards/"+boardID+"/labels", map[string]string{"name_enc": enc("метка"), "color_enc": enc("#f00")}), 200)

	body := new(bytes.Buffer)
	mw := multipart.NewWriter(body)
	_ = mw.WriteField("meta", `{"filename_enc":"`+enc("f.png")+`","mime":"image/png"}`)
	fw, _ := mw.CreateFormFile("blob", "blob")
	_, _ = fw.Write([]byte("xy1bytes"))
	_ = mw.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/cards/"+itoa(card.ID)+"/attachments", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for _, ck := range c.jar {
		req.AddCookie(ck)
	}
	uresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	mustStatus(t, uresp, 200)
	var att struct {
		ID int64 `json:"id"`
	}
	c.decode(uresp, &att)
	mustStatus(t, c.do("DELETE", "/api/attachments/"+itoa(att.ID)+"?event_payload_enc="+url.QueryEscape(enc("f.png")), nil), 204)
	if got := scalarInt(t, srv.db, `select count(*) from timeline_events where type = 'attach_remove'`); got != 1 {
		t.Error("the delete's event_payload_enc was dropped")
	}

	resp = c.do("GET", "/api/boards/"+boardID, nil)
	mustStatus(t, resp, 200)
	var snap boardSnapshot
	c.decode(resp, &snap)
	if len(snap.Lists) != 1 || dec(snap.Lists[0].Title) != "список" {
		t.Errorf("lists = %+v", snap.Lists)
	}
	if len(snap.Cards) != 1 || dec(snap.Cards[0].Desc) != "вопрос" || snap.Cards[0].Alias == nil || dec(*snap.Cards[0].Alias) != "а" {
		t.Errorf("cards = %+v", snap.Cards)
	}
	if len(snap.Labels) != 1 || dec(snap.Labels[0].Name) != "метка" {
		t.Errorf("labels = %+v", snap.Labels)
	}
	if got := scalarStr(t, srv.db, `select cast(payload as text) from timeline_events where type = 'comment'`); got == "" {
		t.Error("the comment's payload was lost")
	}
	if got := scalarStr(t, srv.db, `select cast(filename as text) from attachments`); got == "" {
		t.Error("the attachment's filename was lost")
	}
}
