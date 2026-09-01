package server

import "testing"

// TestBoardMeta: /meta hands a member the two plaintext facts the passphrase
// overlay needs before any key exists — their role and the board's name — and
// tells a stranger nothing.
func TestBoardMeta(t *testing.T) {
	ts, srv := newTestServer(t)
	owner := registerUser(t, srv, ts, 991001, "metaowner")
	editor := registerUser(t, srv, ts, 991002, "metaeditor")
	stranger := registerUser(t, srv, ts, 991003, "metastranger")
	bid := newBoard(t, owner, "Студчем 2026")
	addBoardMember(t, srv, mustAtoi(t, bid), meUserID(t, editor))

	var meta boardMetaResponse
	resp := owner.do("GET", "/api/boards/"+bid+"/meta", nil)
	mustStatus(t, resp, 200)
	owner.decode(resp, &meta)
	if meta.Role != "owner" || meta.Name != "Студчем 2026" {
		t.Fatalf("owner meta = %+v, want owner + the plaintext name", meta)
	}

	resp = editor.do("GET", "/api/boards/"+bid+"/meta", nil)
	mustStatus(t, resp, 200)
	editor.decode(resp, &meta)
	if meta.Role != "editor" {
		t.Fatalf("editor meta = %+v, want role editor", meta)
	}

	mustStatus(t, stranger.do("GET", "/api/boards/"+bid+"/meta", nil), 403)
}

// TestLeaveBoard: a member ends their own Membership without the passphrase; the
// owner has none to end and must delete the board instead.
func TestLeaveBoard(t *testing.T) {
	ts, srv := newTestServer(t)
	owner := registerUser(t, srv, ts, 991101, "leaveowner")
	editor := registerUser(t, srv, ts, 991102, "leaveeditor")
	bid := newBoard(t, owner, "покидаемая")
	addBoardMember(t, srv, mustAtoi(t, bid), meUserID(t, editor))

	var boards []boardSummary
	editor.decode(editor.do("GET", "/api/boards", nil), &boards)
	if len(boards) != 1 {
		t.Fatalf("editor sees %d boards before leaving, want 1", len(boards))
	}

	mustStatus(t, editor.do("DELETE", "/api/boards/"+bid+"/membership", nil), 204)

	editor.decode(editor.do("GET", "/api/boards", nil), &boards)
	if len(boards) != 0 {
		t.Fatalf("editor still sees %+v after leaving", boards)
	}
	mustStatus(t, editor.do("GET", "/api/boards/"+bid, nil), 403)

	// The board itself is untouched: the owner keeps it, alone on the roster.
	var members []struct {
		Username string `json:"username"`
	}
	resp := owner.do("GET", "/api/boards/"+bid+"/members", nil)
	mustStatus(t, resp, 200)
	owner.decode(resp, &members)
	if len(members) != 1 {
		t.Fatalf("roster = %+v, want the owner alone", members)
	}

	mustStatus(t, owner.do("DELETE", "/api/boards/"+bid+"/membership", nil), 403)
	mustStatus(t, owner.do("GET", "/api/boards/"+bid, nil), 200)
}
