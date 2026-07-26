package server

import (
	"testing"
)

// A session's whole life over the API: created, labelled, commented on, and
// tombstoned — taking its labels and its own notes with it.
func TestSessionFlow(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 9001, "sessowner")

	resp := c.do("POST", "/api/boards", map[string]string{
		"name":         "board",
		"kdf_salt":     enc("salt"),
		"kdf_params":   `{"kdf":"scrypt","N":32768,"r":8,"p":1}`,
		"wrapped_key":  enc("wrapped"),
		"verify_token": enc("verify"),
	})
	mustStatus(t, resp, 200)
	var created struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &created)
	board := itoa(created.ID)

	resp = c.do("POST", "/api/boards/"+board+"/sessions", map[string]string{"meta_enc": enc("session-meta")})
	mustStatus(t, resp, 200)
	var session struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &session)
	if session.ID == 0 {
		t.Fatal("no session id")
	}
	sid := itoa(session.ID)

	// A test label needs both a session and a mark; a null colour means "inherit
	// the board's mark template", which is a legal state, not a missing field.
	resp = c.do("POST", "/api/boards/"+board+"/labels", map[string]any{
		"name_enc": enc("20 июля · Алиев и др."), "color_enc": "", "kind": "test",
		"session_id": session.ID, "mark": "taken",
	})
	mustStatus(t, resp, 200)
	var label struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &label)

	resp = c.do("POST", "/api/boards/"+board+"/labels", map[string]any{
		"name_enc": enc("x"), "color_enc": enc("#fff"), "kind": "test",
	})
	mustStatus(t, resp, 400) // test label with no session

	// A note about the test itself: no question attached.
	resp = c.do("POST", "/api/sessions/"+sid+"/comments", map[string]string{"payload_enc": enc("шли дольше обычного")})
	mustStatus(t, resp, 204)

	resp = c.do("GET", "/api/sessions/"+sid+"/timeline", nil)
	mustStatus(t, resp, 200)
	var events []timelineEventDTO
	c.decode(resp, &events)
	if len(events) != 1 {
		t.Fatalf("session timeline has %d events, want 1", len(events))
	}
	if events[0].CardID != nil {
		t.Error("session note should carry no card")
	}

	// The snapshot is what the client actually reads.
	resp = c.do("GET", "/api/boards/"+board, nil)
	mustStatus(t, resp, 200)
	var snap boardSnapshot
	c.decode(resp, &snap)
	if len(snap.Sessions) != 1 || snap.Sessions[0].MetaEnc != enc("session-meta") {
		t.Fatalf("snapshot sessions = %+v", snap.Sessions)
	}
	if len(snap.Labels) != 1 {
		t.Fatalf("snapshot labels = %d, want 1", len(snap.Labels))
	}
	if got := snap.Labels[0]; got.Mark != "taken" || got.SessionID == nil || *got.SessionID != session.ID {
		t.Errorf("label = %+v, want mark taken bound to the session", got)
	}
	if snap.Labels[0].ColorEnc != "" {
		t.Errorf("colour = %q, want empty (inherit)", snap.Labels[0].ColorEnc)
	}

	resp = c.do("POST", "/api/boards/"+board+"/mark-template", map[string]string{"mark_template_enc": enc("tmpl")})
	mustStatus(t, resp, 204)

	resp = c.do("PATCH", "/api/sessions/"+sid, map[string]string{"meta_enc": enc("edited")})
	mustStatus(t, resp, 204)

	// Deleting the session takes its labels and its notes with it: a test label
	// has no life of its own.
	resp = c.do("DELETE", "/api/sessions/"+sid, nil)
	mustStatus(t, resp, 204)

	resp = c.do("GET", "/api/boards/"+board, nil)
	mustStatus(t, resp, 200)
	c.decode(resp, &snap)
	if len(snap.Sessions) != 0 {
		t.Errorf("sessions after delete = %d, want 0", len(snap.Sessions))
	}
	if len(snap.Labels) != 0 {
		t.Errorf("labels after session delete = %d, want 0", len(snap.Labels))
	}
	if snap.MarkTemplateEnc != enc("tmpl") {
		t.Errorf("mark template = %q", snap.MarkTemplateEnc)
	}
}

// The first-run modal's two answers, and the announce-set seed.
func TestProfileDefaults(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 9002, "prefs")

	tz := "Europe/Moscow"
	author := "Иванов И."
	resp := c.do("POST", "/api/auth/profile-defaults", map[string]any{
		"timezone": tz, "default_author": author, "onboarded": true,
	})
	mustStatus(t, resp, 204)

	resp = c.do("POST", "/api/auth/announce-cities", map[string]any{
		"announce_cities": []map[string]string{{"zone": "Europe/Berlin", "name": "Берлин"}},
	})
	mustStatus(t, resp, 204)

	resp = c.do("POST", "/api/boards", map[string]string{
		"name": "b", "kdf_salt": enc("s"), "kdf_params": "{}",
		"wrapped_key": enc("w"), "verify_token": enc("v"),
	})
	mustStatus(t, resp, 200)
	var created struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &created)

	resp = c.do("GET", "/api/boards/"+itoa(created.ID), nil)
	mustStatus(t, resp, 200)
	var snap boardSnapshot
	c.decode(resp, &snap)
	if snap.Timezone != tz {
		t.Errorf("timezone = %q, want %q", snap.Timezone, tz)
	}
	if snap.DefaultAuthor != author {
		t.Errorf("default author = %q, want %q", snap.DefaultAuthor, author)
	}
	if len(snap.AnnounceCities) == 0 {
		t.Error("announce cities missing from the snapshot")
	}
}
