package server

import (
	"testing"
)

// mentionBoard is the three-member fixture the mention/reaction tests share:
// A owns the board, B and C are editors, one card.
func mentionBoard(t *testing.T) (a, b, c *apiClient, meA, meB, meC meResponse, boardID, cardID string) {
	t.Helper()
	ts, srv := newTestServer(t)
	a = registerUser(t, srv, ts, 880001, "mention-a")
	b = registerUser(t, srv, ts, 880002, "mention-b")
	c = registerUser(t, srv, ts, 880003, "mention-c")
	a.decode(a.do("GET", "/api/auth/me", nil), &meA)
	b.decode(b.do("GET", "/api/auth/me", nil), &meB)
	c.decode(c.do("GET", "/api/auth/me", nil), &meC)

	resp := a.do("POST", "/api/boards", map[string]string{
		"name":         "mention board",
		"kdf_salt":     enc("salt"),
		"kdf_params":   `{"kdf":"scrypt","N":32768,"r":8,"p":1}`,
		"wrapped_key":  enc("wrapped"),
		"verify_token": enc("verify"),
	})
	mustStatus(t, resp, 200)
	var createdBoard struct {
		ID int64 `json:"id"`
	}
	a.decode(resp, &createdBoard)
	boardID = itoa(createdBoard.ID)
	addBoardMember(t, srv, createdBoard.ID, meB.UserID)
	addBoardMember(t, srv, createdBoard.ID, meC.UserID)

	resp = a.do("POST", "/api/boards/"+boardID+"/lists", map[string]string{"title_enc": enc("l"), "rank": "m"})
	mustStatus(t, resp, 200)
	var listC struct {
		ID int64 `json:"id"`
	}
	a.decode(resp, &listC)
	resp = a.do("POST", "/api/lists/"+itoa(listC.ID)+"/cards", map[string]string{"description_enc": enc("q"), "rank": "m"})
	mustStatus(t, resp, 200)
	var cardC struct {
		ID int64 `json:"id"`
	}
	a.decode(resp, &cardC)
	cardID = itoa(cardC.ID)
	return
}

// TestMentions: a comment naming a member red-flags exactly that member, a
// reply red-flags the parent's author with no @ at all, editing the comment
// re-resolves, and reading clears red together with the comment bucket.
func TestMentions(t *testing.T) {
	a, b, ccl, meA, _, _, boardID, cardID := mentionBoard(t)

	// B mentions A. A goes red, C goes plain blue.
	resp := b.do("POST", "/api/cards/"+cardID+"/comments", map[string]any{
		"payload_enc": enc("@mention-a смотри"),
		"mentions":    []int64{meA.UserID},
	})
	mustStatus(t, resp, 204)

	u := getBoardSnapshot(a, boardID).Unread[cardID]
	if !u.Comments || !u.Mentions {
		t.Fatalf("mentioned A unread = %+v, want comments+mentions", u)
	}
	u = getBoardSnapshot(ccl, boardID).Unread[cardID]
	if !u.Comments || u.Mentions {
		t.Fatalf("bystander C unread = %+v, want comments only", u)
	}

	// The board list rollup carries the red flag too.
	var boardsA []boardSummary
	a.decode(a.do("GET", "/api/boards", nil), &boardsA)
	if len(boardsA) != 1 || !boardsA[0].Unread || !boardsA[0].UnreadMentions {
		t.Fatalf("A board rollup = %+v, want unread+mentions", boardsA)
	}
	var boardsC []boardSummary
	ccl.decode(ccl.do("GET", "/api/boards", nil), &boardsC)
	if len(boardsC) != 1 || !boardsC[0].Unread || boardsC[0].UnreadMentions {
		t.Fatalf("C board rollup = %+v, want unread without mentions", boardsC)
	}

	// The activity feed marks the row as mentioning A.
	var actA []activityEventDTO
	a.decode(a.do("GET", "/api/boards/"+boardID+"/activity", nil), &actA)
	if len(actA) != 1 || !actA[0].Mention || !actA[0].Unread {
		t.Fatalf("A activity = %+v, want one unread mention row", actA)
	}
	commentID := actA[0].ID

	// Reading the card clears red and blue together.
	resp = a.do("POST", "/api/cards/"+cardID+"/read", map[string]any{"comment_read_id": commentID})
	mustStatus(t, resp, 204)
	if snap := getBoardSnapshot(a, boardID); len(snap.Unread) != 0 {
		t.Fatalf("A unread after read = %v, want empty", snap.Unread)
	}

	// B edits the comment and the mention goes away: red must not come back,
	// but C (never having read) still sees plain blue.
	resp = b.do("PATCH", "/api/comments/"+itoa(commentID), map[string]any{
		"payload_enc": enc("без упоминаний"),
		"mentions":    []int64{},
	})
	mustStatus(t, resp, 204)
	if u := getBoardSnapshot(a, boardID).Unread[cardID]; u.Mentions {
		t.Fatalf("A red after mention removed = %+v", u)
	}

	// A root comment by A; B replies without any @. The reply alone turns A red.
	resp = a.do("POST", "/api/cards/"+cardID+"/comments", map[string]string{"payload_enc": enc("мой вопрос")})
	mustStatus(t, resp, 204)
	var actB []activityEventDTO
	b.decode(b.do("GET", "/api/boards/"+boardID+"/activity", nil), &actB)
	rootID := actB[0].ID
	resp = b.do("POST", "/api/cards/"+cardID+"/comments", map[string]any{
		"payload_enc": enc("отвечаю"),
		"reply_to_id": rootID,
	})
	mustStatus(t, resp, 204)
	if u := getBoardSnapshot(a, boardID).Unread[cardID]; !u.Mentions {
		t.Fatalf("A unread after reply = %+v, want mentions (implicit)", u)
	}
	if u := getBoardSnapshot(ccl, boardID).Unread[cardID]; u.Mentions {
		t.Fatalf("C unread after reply to A = %+v, want no mentions", u)
	}

	// A stranger in the mention list is dropped, not bounced — the comment
	// itself must land (an offline-queued op may outlive the roster it saw).
	resp = b.do("POST", "/api/cards/"+cardID+"/comments", map[string]any{
		"payload_enc": enc("@кто-то"),
		"mentions":    []int64{999999},
	})
	mustStatus(t, resp, 204)
	var actC []activityEventDTO
	ccl.decode(ccl.do("GET", "/api/boards/"+boardID+"/activity", nil), &actC)
	for _, ev := range actC {
		if ev.Mention {
			t.Fatalf("stranger mention leaked into activity: %+v", ev)
		}
	}
}

// TestReactions: a reaction to a comment blue-dots the comment bucket, one to
// the card itself the content bucket; un-reacting hard-deletes and the dot dies
// with it.
func TestReactions(t *testing.T) {
	a, b, _, _, _, _, boardID, cardID := mentionBoard(t)

	// A comments; B reacts to that comment.
	resp := a.do("POST", "/api/cards/"+cardID+"/comments", map[string]string{"payload_enc": enc("вопрос")})
	mustStatus(t, resp, 204)
	var actB []activityEventDTO
	b.decode(b.do("GET", "/api/boards/"+boardID+"/activity", nil), &actB)
	commentID := actB[0].ID

	resp = b.do("POST", "/api/cards/"+cardID+"/reactions", map[string]any{
		"payload_enc": enc("👍"),
		"target_id":   commentID,
	})
	mustStatus(t, resp, 200)
	var made struct {
		ID int64 `json:"id"`
	}
	b.decode(resp, &made)

	u := getBoardSnapshot(a, boardID).Unread[cardID]
	if !u.Comments || u.Content || u.Mentions {
		t.Fatalf("A unread after comment reaction = %+v, want comments only", u)
	}

	// The reaction reaches A's activity feed and the card timeline.
	var actA []activityEventDTO
	a.decode(a.do("GET", "/api/boards/"+boardID+"/activity", nil), &actA)
	if len(actA) != 1 || actA[0].Type != "reaction" {
		t.Fatalf("A activity = %+v, want the reaction", actA)
	}

	// B un-reacts: hard delete, the dot goes with it.
	resp = b.do("DELETE", "/api/reactions/"+itoa(made.ID), nil)
	mustStatus(t, resp, 204)
	if snap := getBoardSnapshot(a, boardID); len(snap.Unread) != 0 {
		t.Fatalf("A unread after un-react = %v, want empty", snap.Unread)
	}

	// Only the author may remove a reaction.
	resp = b.do("POST", "/api/cards/"+cardID+"/reactions", map[string]any{
		"payload_enc": enc("🔥"),
		"target_id":   commentID,
	})
	mustStatus(t, resp, 200)
	b.decode(resp, &made)
	resp = a.do("DELETE", "/api/reactions/"+itoa(made.ID), nil)
	mustStatus(t, resp, 403)

	// A card-level reaction (no target) lands in the content bucket.
	resp = b.do("POST", "/api/cards/"+cardID+"/reactions", map[string]any{"payload_enc": enc("❤️")})
	mustStatus(t, resp, 200)
	u = getBoardSnapshot(a, boardID).Unread[cardID]
	if !u.Content {
		t.Fatalf("A unread after card reaction = %+v, want content", u)
	}

	// Reacting to a description edit is not a thing.
	resp = a.do("PATCH", "/api/cards/"+cardID, map[string]string{
		"description_enc": enc("q2"),
		"desc_event_enc":  enc(`{"before":"q","after":"q2"}`),
	})
	mustStatus(t, resp, 204)
	var actB2 []activityEventDTO
	b.decode(b.do("GET", "/api/boards/"+boardID+"/activity", nil), &actB2)
	var descID int64
	for _, ev := range actB2 {
		if ev.Type == "desc_edit" {
			descID = ev.ID
		}
	}
	resp = b.do("POST", "/api/cards/"+cardID+"/reactions", map[string]any{
		"payload_enc": enc("👍"),
		"target_id":   descID,
	})
	mustStatus(t, resp, 404)
}
