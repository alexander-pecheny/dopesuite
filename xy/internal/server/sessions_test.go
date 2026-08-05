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

	// A label is just a label; what makes it a test's verdict is the ASSIGNMENT
	// carrying the session (ADR-0004).
	resp = c.do("POST", "/api/boards/"+board+"/labels", map[string]any{
		"name_enc": enc("взяли"), "color_enc": enc("#3aa657"),
	})
	mustStatus(t, resp, 200)
	var label struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &label)

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
	// The label survives its session: it is an ordinary board label, and only the
	// assignments scoped to the session go.
	if len(snap.Labels) != 1 {
		t.Errorf("labels after session delete = %d, want 1", len(snap.Labels))
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

// A comment is tagged with the test it came out of after the fact, from its own
// ⋯ menu: the tag moves between tests and clears with a 0, and it may not point
// at a test on another board.
func TestCommentSessionRetag(t *testing.T) {
	c, board, listID := boardWithList(t)

	resp := c.do("POST", "/api/lists/"+listID+"/cards", map[string]string{"description_enc": enc("q"), "rank": "m"})
	mustStatus(t, resp, 200)
	var card struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &card)
	cardID := itoa(card.ID)

	newSession := func(cl *apiClient, boardID string) int64 {
		t.Helper()
		r := cl.do("POST", "/api/boards/"+boardID+"/sessions", map[string]string{"meta_enc": enc("s")})
		mustStatus(t, r, 200)
		var s struct {
			ID int64 `json:"id"`
		}
		cl.decode(r, &s)
		return s.ID
	}
	first, second := newSession(c, board), newSession(c, board)

	resp = c.do("POST", "/api/cards/"+cardID+"/comments", map[string]string{"payload_enc": enc("формулировка споткнулась")})
	mustStatus(t, resp, 204)

	comment := func() timelineEventDTO {
		t.Helper()
		r := c.do("GET", "/api/cards/"+cardID+"/timeline", nil)
		mustStatus(t, r, 200)
		var tl []timelineEventDTO
		c.decode(r, &tl)
		if len(tl) != 1 {
			t.Fatalf("timeline len = %d, want 1", len(tl))
		}
		return tl[0]
	}
	ev := itoa(comment().ID)
	if got := comment().SessionID; got != nil {
		t.Fatalf("a fresh comment carries session %d, want none", *got)
	}

	// Tagging, then moving the tag to the other test.
	for _, want := range []int64{first, second} {
		resp = c.do("PATCH", "/api/comments/"+ev, map[string]any{"session_id": want})
		mustStatus(t, resp, 204)
		if got := comment().SessionID; got == nil || *got != want {
			t.Fatalf("session after retag = %v, want %d", got, want)
		}
	}

	resp = c.do("PATCH", "/api/comments/"+ev, map[string]any{"session_id": 0})
	mustStatus(t, resp, 204)
	if got := comment().SessionID; got != nil {
		t.Fatalf("session after clearing = %d, want none", *got)
	}

	// Another board's test is not a choice here, however it is asked for.
	resp = c.do("POST", "/api/boards", map[string]string{
		"name": "other", "kdf_salt": enc("s"),
		"kdf_params": `{"kdf":"scrypt","N":1,"r":1,"p":1}`, "wrapped_key": enc("w"), "verify_token": enc("v"),
	})
	mustStatus(t, resp, 200)
	var otherBoard struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &otherBoard)
	foreign := newSession(c, itoa(otherBoard.ID))
	resp = c.do("PATCH", "/api/comments/"+ev, map[string]any{"session_id": foreign})
	mustStatus(t, resp, 400)
}
