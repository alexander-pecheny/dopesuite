package server

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"testing"
)

// TestAttachmentsAndPlayerMap exercises the attachment upload/download/delete
// round-trip and the encrypted player-map endpoints.
func TestAttachmentsAndPlayerMap(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 556001, "att-user")

	// board + list + card
	resp := c.do("POST", "/api/boards", map[string]string{
		"name_enc": enc("b"), "kdf_salt": enc("s"), "kdf_params": "{}", "wrapped_key": enc("w"), "verify_token": enc("v"),
	})
	mustStatus(t, resp, 200)
	var b struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &b)
	resp = c.do("POST", "/api/boards/"+itoa(b.ID)+"/lists", map[string]string{"title_enc": enc("l"), "rank": "m"})
	mustStatus(t, resp, 200)
	var l struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &l)
	resp = c.do("POST", "/api/lists/"+itoa(l.ID)+"/cards", map[string]string{"description_enc": enc("d"), "rank": "m"})
	mustStatus(t, resp, 200)
	var card struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &card)

	// upload an attachment (ciphertext is just opaque bytes here)
	cipher := []byte("xy1\x01ENCRYPTED-IMAGE-BYTES")
	body := new(bytes.Buffer)
	mw := multipart.NewWriter(body)
	_ = mw.WriteField("meta", `{"filename_enc":"`+enc("photo.webp")+`","mime":"image/webp","lossless":false,"event_payload_enc":"`+enc(`{"file":"photo.webp"}`)+`"}`)
	fw, _ := mw.CreateFormFile("blob", "blob")
	fw.Write(cipher)
	mw.Close()

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
		ID   int64 `json:"id"`
		Size int64 `json:"size"`
	}
	c.decode(uresp, &att)
	if att.Size != int64(len(cipher)) {
		t.Fatalf("size = %d, want %d", att.Size, len(cipher))
	}

	// it appears in the list with the cleartext mime metadata
	resp = c.do("GET", "/api/cards/"+itoa(card.ID)+"/attachments", nil)
	mustStatus(t, resp, 200)
	var list []attachmentDTO
	c.decode(resp, &list)
	if len(list) != 1 || list[0].Mime != "image/webp" {
		t.Fatalf("attachments = %+v", list)
	}

	// download returns the exact ciphertext
	dresp := c.do("GET", "/api/attachments/"+itoa(att.ID), nil)
	mustStatus(t, dresp, 200)
	got, _ := io.ReadAll(dresp.Body)
	dresp.Body.Close()
	if !bytes.Equal(got, cipher) {
		t.Fatalf("download mismatch: %q", got)
	}

	// the upload also appended an attach_add timeline event
	resp = c.do("GET", "/api/cards/"+itoa(card.ID)+"/timeline", nil)
	mustStatus(t, resp, 200)
	var tl []timelineEventDTO
	c.decode(resp, &tl)
	if len(tl) != 1 || tl[0].Type != "attach_add" {
		t.Fatalf("timeline = %+v", tl)
	}

	// delete it
	resp = c.do("DELETE", "/api/attachments/"+itoa(att.ID), nil)
	mustStatus(t, resp, 204)
	dresp = c.do("GET", "/api/attachments/"+itoa(att.ID), nil)
	mustStatus(t, dresp, 404)

	// player map round-trips
	resp = c.do("PUT", "/api/boards/"+itoa(b.ID)+"/player-map", map[string]string{"payload_enc": enc(`{"123":"Иванов"}`)})
	mustStatus(t, resp, 204)
	resp = c.do("GET", "/api/boards/"+itoa(b.ID)+"/player-map", nil)
	mustStatus(t, resp, 200)
	var pm struct {
		PayloadEnc string `json:"payload_enc"`
	}
	c.decode(resp, &pm)
	if dec(pm.PayloadEnc) != `{"123":"Иванов"}` {
		t.Fatalf("player map = %q", dec(pm.PayloadEnc))
	}
}
