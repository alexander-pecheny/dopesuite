package server

import (
	"testing"
)

// getBoardSnapshot fetches a fresh board snapshot for the client. Always
// decodes into a brand-new boardSnapshot — json.Unmarshal only adds/overwrites
// map keys present in the payload, it never clears stale ones from a reused
// destination, so a fresh struct is required to see a shrinking `unread` map.
func getBoardSnapshot(c *apiClient, boardID string) boardSnapshot {
	var snap boardSnapshot
	c.decode(c.do("GET", "/api/boards/"+boardID, nil), &snap)
	return snap
}

// TestReadMarkers exercises the unread-tracking flow (migrateV7 / card_reads):
// two users share a board+card, the second user's comment + desc_edit show up
// as unread for the first (never for their own author), the activity feed
// reflects the same, and marking the card read clears it from both the
// snapshot's `unread` map and (implicitly) future activity fetches.
func TestReadMarkers(t *testing.T) {
	ts, srv := newTestServer(t)

	// Two users, sharing a board owned by A.
	a := registerUser(t, srv, ts, 990001, "reader-a")
	b := registerUser(t, srv, ts, 990002, "reader-b")

	var meA, meB meResponse
	a.decode(a.do("GET", "/api/auth/me", nil), &meA)
	b.decode(b.do("GET", "/api/auth/me", nil), &meB)

	board := map[string]string{
		"name_enc":     enc("shared board"),
		"kdf_salt":     enc("salt"),
		"kdf_params":   `{"kdf":"scrypt","N":32768,"r":8,"p":1}`,
		"wrapped_key":  enc("wrapped"),
		"verify_token": enc("verify"),
	}
	resp := a.do("POST", "/api/boards", board)
	mustStatus(t, resp, 200)
	var createdBoard struct {
		ID int64 `json:"id"`
	}
	a.decode(resp, &createdBoard)
	boardID := itoa(createdBoard.ID)
	addBoardMember(t, srv, createdBoard.ID, meB.UserID)

	resp = a.do("POST", "/api/boards/"+boardID+"/lists", map[string]string{"title_enc": enc("To do"), "rank": "m"})
	mustStatus(t, resp, 200)
	var listC struct {
		ID int64 `json:"id"`
	}
	a.decode(resp, &listC)
	listID := itoa(listC.ID)

	resp = a.do("POST", "/api/lists/"+listID+"/cards", map[string]string{"description_enc": enc("q1"), "rank": "m"})
	mustStatus(t, resp, 200)
	var cardC struct {
		ID int64 `json:"id"`
	}
	a.decode(resp, &cardC)
	cardID := itoa(cardC.ID)

	// A's own snapshot right after creating the card: nothing unread yet.
	if snap := getBoardSnapshot(a, boardID); len(snap.Unread) != 0 {
		t.Fatalf("unread before B's edits = %v, want empty", snap.Unread)
	}

	// B comments and edits the description.
	resp = b.do("POST", "/api/cards/"+cardID+"/comments", map[string]string{"payload_enc": enc("nice question")})
	mustStatus(t, resp, 204)
	resp = b.do("PATCH", "/api/cards/"+cardID, map[string]string{
		"description_enc": enc("q1 edited"),
		"desc_event_enc":  enc(`{"before":"q1","after":"q1 edited"}`),
	})
	mustStatus(t, resp, 204)

	// A's snapshot now shows the card unread in both buckets.
	snap := getBoardSnapshot(a, boardID)
	u, ok := snap.Unread[cardID]
	if !ok {
		t.Fatalf("card %s missing from unread map: %v", cardID, snap.Unread)
	}
	if !u.Content || !u.Comments {
		t.Fatalf("unread = %+v, want both true", u)
	}

	// B never sees their own events as unread.
	if snapB := getBoardSnapshot(b, boardID); len(snapB.Unread) != 0 {
		t.Fatalf("author B sees own events as unread: %v", snapB.Unread)
	}

	// Activity feed (as A) returns both events, flagged unread.
	var activity []activityEventDTO
	a.decode(a.do("GET", "/api/boards/"+boardID+"/activity", nil), &activity)
	if len(activity) != 2 {
		t.Fatalf("activity len = %d, want 2: %+v", len(activity), activity)
	}
	var commentEvID, descEvID int64
	for _, ev := range activity {
		if !ev.Unread {
			t.Fatalf("activity event %+v not flagged unread", ev)
		}
		if ev.AuthorID != meB.UserID {
			t.Fatalf("activity event author = %d, want %d", ev.AuthorID, meB.UserID)
		}
		switch ev.Type {
		case "comment":
			commentEvID = ev.ID
		case "desc_edit":
			descEvID = ev.ID
		}
	}
	if commentEvID == 0 || descEvID == 0 {
		t.Fatalf("expected one comment + one desc_edit, got %+v", activity)
	}

	// A marks both buckets read via the max event id seen.
	resp = a.do("POST", "/api/cards/"+cardID+"/read", map[string]any{
		"content_read_id": descEvID,
		"comment_read_id": commentEvID,
	})
	mustStatus(t, resp, 204)

	if snap := getBoardSnapshot(a, boardID); len(snap.Unread) != 0 {
		t.Fatalf("card still unread after marking read: %v", snap.Unread)
	}

	// A new comment from B should surface as unread again (watermark, not a
	// one-shot dismissal).
	resp = b.do("POST", "/api/cards/"+cardID+"/comments", map[string]string{"payload_enc": enc("follow-up")})
	mustStatus(t, resp, 204)
	snap = getBoardSnapshot(a, boardID)
	u, ok = snap.Unread[cardID]
	if !ok || !u.Comments || u.Content {
		t.Fatalf("unread after follow-up comment = %+v, ok=%v, want comments-only", u, ok)
	}

	// read-all clears everything for A.
	resp = a.do("POST", "/api/boards/"+boardID+"/read-all", nil)
	mustStatus(t, resp, 204)
	if snap := getBoardSnapshot(a, boardID); len(snap.Unread) != 0 {
		t.Fatalf("unread after read-all = %v, want empty", snap.Unread)
	}
}
