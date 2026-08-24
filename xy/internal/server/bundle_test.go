package server

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"testing"
)

// TestBoardBundleEndpoints drives the Bundle's server half end to end: the
// whole-board timeline and attachment reads the export uses, and the
// board-level timeline import that recreates history — threading, session
// events, authors matched by username — on a fresh board.
func TestBoardBundleEndpoints(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 771001, "bundle-user")

	mkBoard := func(name string) string {
		resp := c.do("POST", "/api/boards", map[string]string{
			"name": name, "kdf_salt": enc("s"), "kdf_params": "{}", "wrapped_key": enc("w"), "verify_token": enc("v"),
		})
		mustStatus(t, resp, 200)
		var b struct {
			ID int64 `json:"id"`
		}
		c.decode(resp, &b)
		return itoa(b.ID)
	}
	board := mkBoard("source")

	resp := c.do("POST", "/api/boards/"+board+"/lists", map[string]string{"title_enc": enc("l"), "rank": "m"})
	mustStatus(t, resp, 200)
	var list struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &list)
	resp = c.do("POST", "/api/lists/"+itoa(list.ID)+"/cards", map[string]string{"description_enc": enc("d"), "rank": "m"})
	mustStatus(t, resp, 200)
	var card struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &card)
	resp = c.do("POST", "/api/boards/"+board+"/sessions", map[string]string{"meta_enc": enc("meta")})
	mustStatus(t, resp, 200)
	var session struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &session)

	// One event of each flavor: a card comment, a session comment, a reaction on
	// the comment, and a deleted comment that must stay behind.
	resp = c.do("POST", "/api/cards/"+itoa(card.ID)+"/comments", map[string]string{"payload_enc": enc("норм")})
	mustStatus(t, resp, 204)
	resp = c.do("POST", "/api/sessions/"+itoa(session.ID)+"/comments", map[string]string{"payload_enc": enc("про тест")})
	mustStatus(t, resp, 204)
	resp = c.do("GET", "/api/cards/"+itoa(card.ID)+"/timeline", nil)
	mustStatus(t, resp, 200)
	var cardEvents []timelineEventDTO
	c.decode(resp, &cardEvents)
	if len(cardEvents) != 1 {
		t.Fatalf("card timeline: %d events, want 1", len(cardEvents))
	}
	commentID := cardEvents[0].ID
	resp = c.do("POST", "/api/cards/"+itoa(card.ID)+"/reactions", map[string]any{"payload_enc": enc("👍"), "target_id": commentID})
	mustStatus(t, resp, 200)
	resp = c.do("POST", "/api/cards/"+itoa(card.ID)+"/comments", map[string]string{"payload_enc": enc("удалю")})
	mustStatus(t, resp, 204)
	resp = c.do("GET", "/api/cards/"+itoa(card.ID)+"/timeline", nil)
	mustStatus(t, resp, 200)
	c.decode(resp, &cardEvents)
	var deletedID int64
	for _, e := range cardEvents {
		if e.Type == "comment" && dec(e.PayloadEnc) == "удалю" {
			deletedID = e.ID
		}
	}
	resp = c.do("DELETE", "/api/comments/"+itoa(deletedID), nil)
	mustStatus(t, resp, 204)

	// ---- whole-board timeline read ----
	resp = c.do("GET", "/api/boards/"+board+"/timeline", nil)
	mustStatus(t, resp, 200)
	var all []bundleEventDTO
	c.decode(resp, &all)
	if len(all) != 3 {
		t.Fatalf("board timeline: %d events, want 3 (deleted one excluded)", len(all))
	}
	byType := map[string]bundleEventDTO{}
	for _, e := range all {
		byType[e.Type+payloadKey(e)] = e
		if e.AuthorUsername == nil || *e.AuthorUsername != "bundle-user" {
			t.Fatalf("event %d: author username %v, want bundle-user", e.ID, e.AuthorUsername)
		}
	}
	if e := byType["comment:про тест"]; e.SessionID == nil || e.CardID != nil {
		t.Fatalf("session comment misfiled: %+v", e)
	}
	if e := byType["reaction:👍"]; e.ReplyToID == nil || *e.ReplyToID != commentID {
		t.Fatalf("reaction target lost: %+v", e)
	}

	// ---- whole-board attachment read ----
	body := new(bytes.Buffer)
	mw := multipart.NewWriter(body)
	_ = mw.WriteField("meta", `{"filename_enc":"`+enc("р.png")+`","mime":"image/png","lossless":true}`)
	fw, _ := mw.CreateFormFile("blob", "blob")
	fw.Write([]byte("xy1cipherbytes"))
	mw.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/cards/"+itoa(card.ID)+"/attachments", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for _, ck := range c.jar {
		req.AddCookie(ck)
	}
	upResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	mustStatus(t, upResp, 200)

	resp = c.do("GET", "/api/boards/"+board+"/attachments", nil)
	mustStatus(t, resp, 200)
	var atts []boardAttachmentDTO
	c.decode(resp, &atts)
	if len(atts) != 1 || atts[0].CardID != card.ID || dec(atts[0].FilenameEnc) != "р.png" {
		t.Fatalf("board attachments: %+v", atts)
	}
	// An upload with no event_payload_enc (the import's shape) adds no
	// attach_add event — imported history must not double up.
	resp = c.do("GET", "/api/boards/"+board+"/timeline", nil)
	mustStatus(t, resp, 200)
	c.decode(resp, &all)
	if len(all) != 3 {
		t.Fatalf("attachment upload without event grew the timeline to %d", len(all))
	}

	// ---- board-level timeline import onto a fresh board ----
	dst := mkBoard("target")
	resp = c.do("POST", "/api/boards/"+dst+"/lists", map[string]string{"title_enc": enc("l"), "rank": "m"})
	mustStatus(t, resp, 200)
	c.decode(resp, &list)
	resp = c.do("POST", "/api/lists/"+itoa(list.ID)+"/cards", map[string]string{"description_enc": enc("d"), "rank": "m"})
	mustStatus(t, resp, 200)
	c.decode(resp, &card)
	resp = c.do("POST", "/api/boards/"+dst+"/sessions", map[string]string{"meta_enc": enc("meta")})
	mustStatus(t, resp, 200)
	c.decode(resp, &session)

	batch1 := map[string]any{"events": []map[string]any{
		{"src_id": 10, "card_id": card.ID, "type": "comment", "author_username": "bundle-user",
			"created_at": "2026-01-01T10:00:00Z", "payload_enc": enc("родитель")},
		{"src_id": 11, "card_id": card.ID, "type": "comment", "author_username": "стёртый-с-той-стороны",
			"reply_to_src_id": 10, "created_at": "2026-01-01T11:00:00Z", "payload_enc": enc("ответ")},
		{"src_id": 12, "session_id": session.ID, "type": "comment",
			"created_at": "2026-01-01T12:00:00Z", "payload_enc": enc("про тест")},
		{"src_id": 13, "card_id": card.ID, "type": "desc_edit", "author_username": "bundle-user",
			"created_at": "2026-01-02T10:00:00Z", "edited_at": "2026-01-02T11:00:00Z",
			"is_excerpt": false, "payload_enc": enc(`{"before":"a","after":"b"}`)},
	}}
	resp = c.do("POST", "/api/boards/"+dst+"/timeline/import", batch1)
	mustStatus(t, resp, 200)
	var mapped struct {
		IDs map[string]int64 `json:"ids"`
	}
	c.decode(resp, &mapped)
	if len(mapped.IDs) != 4 {
		t.Fatalf("id map has %d entries, want 4", len(mapped.IDs))
	}
	// A later batch threads a reaction onto batch 1's comment via the map. The
	// second reaction's comment is gone (tombstoned on the source): it must be
	// dropped, not reparented onto the card.
	batch2 := map[string]any{"events": []map[string]any{
		{"src_id": 14, "card_id": card.ID, "type": "reaction", "author_username": "bundle-user",
			"reply_to_id": mapped.IDs["10"], "created_at": "2026-01-03T10:00:00Z", "payload_enc": enc("👍")},
		{"src_id": 15, "card_id": card.ID, "type": "reaction", "author_username": "bundle-user",
			"reply_to_src_id": 99999, "created_at": "2026-01-03T11:00:00Z", "payload_enc": enc("💀")},
	}}
	resp = c.do("POST", "/api/boards/"+dst+"/timeline/import", batch2)
	mustStatus(t, resp, 200)

	resp = c.do("GET", "/api/boards/"+dst+"/timeline", nil)
	mustStatus(t, resp, 200)
	// A fresh slice: json.Unmarshal into the reused one would keep stale fields
	// on elements whose author_username is omitted.
	var imported []bundleEventDTO
	c.decode(resp, &imported)
	if len(imported) != 5 {
		t.Fatalf("imported timeline: %d events, want 5", len(imported))
	}
	got := map[string]bundleEventDTO{}
	for _, e := range imported {
		got[dec(e.PayloadEnc)] = e
	}
	if e := got["ответ"]; e.ReplyToID == nil || *e.ReplyToID != mapped.IDs["10"] {
		t.Fatalf("reply not threaded: %+v", e)
	}
	if e := got["ответ"]; e.AuthorUsername != nil {
		t.Fatalf("unknown author should import as nil, got %v", *e.AuthorUsername)
	}
	if e := got["родитель"]; e.AuthorUsername == nil || *e.AuthorUsername != "bundle-user" || e.CreatedAt != "2026-01-01T10:00:00Z" {
		t.Fatalf("author/timestamp not preserved: %+v", e)
	}
	if e := got["про тест"]; e.SessionID == nil {
		t.Fatalf("session event lost its session: %+v", e)
	}
	if e := got[`{"before":"a","after":"b"}`]; e.EditedAt == nil || *e.EditedAt != "2026-01-02T11:00:00Z" {
		t.Fatalf("edited_at not preserved: %+v", e)
	}
	if e := got["👍"]; e.ReplyToID == nil || *e.ReplyToID != mapped.IDs["10"] {
		t.Fatalf("cross-batch reply_to_id lost: %+v", e)
	}

	// Rejections: a bad type, and an event pointing at another board's card.
	resp = c.do("POST", "/api/boards/"+dst+"/timeline/import", map[string]any{"events": []map[string]any{
		{"src_id": 1, "card_id": card.ID, "type": "hack", "created_at": "2026-01-01T00:00:00Z", "payload_enc": enc("x")},
	}})
	mustStatus(t, resp, 400)
	resp = c.do("POST", "/api/boards/"+board+"/timeline/import", map[string]any{"events": []map[string]any{
		{"src_id": 1, "card_id": card.ID, "type": "comment", "created_at": "2026-01-01T00:00:00Z", "payload_enc": enc("x")},
	}})
	mustStatus(t, resp, 400)
}

func payloadKey(e bundleEventDTO) string { return ":" + dec(e.PayloadEnc) }
