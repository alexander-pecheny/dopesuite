package server

import (
	"bytes"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// inviteBoard is the fixture the invite-link tests share: A owns a board, B and
// C are strangers with accounts.
func inviteBoard(t *testing.T) (srv *server, ts *httptest.Server, a, b, c *apiClient, boardID string) {
	t.Helper()
	ts, srv = newTestServer(t)
	a = registerUser(t, srv, ts, 990001, "invite-a")
	b = registerUser(t, srv, ts, 990002, "invite-b")
	c = registerUser(t, srv, ts, 990003, "invite-c")
	resp := a.do("POST", "/api/boards", map[string]string{
		"name":         "invite board",
		"kdf_salt":     enc("salt"),
		"kdf_params":   `{"kdf":"scrypt","N":32768,"r":8,"p":1}`,
		"wrapped_key":  enc("wrapped"),
		"verify_token": enc("verify"),
	})
	mustStatus(t, resp, 200)
	var created struct {
		ID int64 `json:"id"`
	}
	a.decode(resp, &created)
	return srv, ts, a, b, c, itoa(created.ID)
}

func mintInviteLink(t *testing.T, a *apiClient, boardID string, body map[string]any) boardInviteDTO {
	t.Helper()
	resp := a.do("POST", "/api/boards/"+boardID+"/invites", body)
	mustStatus(t, resp, 200)
	var inv boardInviteDTO
	a.decode(resp, &inv)
	return inv
}

// TestInviteLinkJoin: a plain link admits a stranger as an editor, and the
// owner's list then names who came in through it.
func TestInviteLinkJoin(t *testing.T) {
	_, _, a, b, _, boardID := inviteBoard(t)

	inv := mintInviteLink(t, a, boardID, map[string]any{"label": "тестерам"})
	if inv.Code == "" {
		t.Fatal("minted invite has no code")
	}

	// B sees the board's name before joining, and nothing else.
	var peek boardInvitePeekDTO
	b.decode(b.do("GET", "/api/board-invites/code/"+inv.Code, nil), &peek)
	if peek.BoardName != "invite board" || peek.State != "active" {
		t.Fatalf("peek = %+v, want the board name and state active", peek)
	}

	mustStatus(t, b.do("POST", "/api/board-invites/code/"+inv.Code+"/join", nil), 200)

	var members []struct {
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	b.decode(b.do("GET", "/api/boards/"+boardID+"/members", nil), &members)
	if len(members) != 2 {
		t.Fatalf("members = %+v, want owner + the joiner", members)
	}

	var links []boardInviteDTO
	a.decode(a.do("GET", "/api/boards/"+boardID+"/invites", nil), &links)
	if len(links) != 1 {
		t.Fatalf("links = %+v, want one", links)
	}
	if links[0].Label != "тестерам" || links[0].Used != 1 || links[0].Left != nil {
		t.Fatalf("link = %+v, want label, one use and no cap", links[0])
	}
	if len(links[0].Joined) != 1 || links[0].Joined[0].Username != "invite-b" {
		t.Fatalf("joined = %+v, want invite-b", links[0].Joined)
	}
}

// TestInviteLinkLimits: a one-seat link admits one person and refuses the next,
// an expired link refuses everyone, and revoking stops a link that still had
// seats — while the row and its history stay in the owner's list.
func TestInviteLinkLimits(t *testing.T) {
	srv, _, a, b, c, boardID := inviteBoard(t)

	oneSeat := mintInviteLink(t, a, boardID, map[string]any{"max_uses": 1})
	mustStatus(t, b.do("POST", "/api/board-invites/code/"+oneSeat.Code+"/join", nil), 200)

	var peek boardInvitePeekDTO
	c.decode(c.do("GET", "/api/board-invites/code/"+oneSeat.Code, nil), &peek)
	if peek.State != "exhausted" {
		t.Fatalf("peek after the seat went = %q, want exhausted", peek.State)
	}
	mustStatus(t, c.do("POST", "/api/board-invites/code/"+oneSeat.Code+"/join", nil), 400)

	// B follows it again: already a member, no second use spent.
	mustStatus(t, b.do("POST", "/api/board-invites/code/"+oneSeat.Code+"/join", nil), 200)

	// An expiry that has passed refuses everyone. Backdating the row is the only
	// way to reach it without waiting an hour.
	timed := mintInviteLink(t, a, boardID, map[string]any{"ttl_hours": 1})
	if _, err := srv.db.Exec(`update board_invites set expires_at = ? where id = ?`,
		rfc3339(time.Now().Add(-time.Minute)), timed.ID); err != nil {
		t.Fatal(err)
	}
	c.decode(c.do("GET", "/api/board-invites/code/"+timed.Code, nil), &peek)
	if peek.State != "expired" {
		t.Fatalf("peek on a lapsed link = %q, want expired", peek.State)
	}
	mustStatus(t, c.do("POST", "/api/board-invites/code/"+timed.Code+"/join", nil), 400)

	// A link with seats left still dies when revoked.
	open := mintInviteLink(t, a, boardID, nil)
	mustStatus(t, a.do("POST", "/api/board-invites/"+itoa(open.ID)+"/revoke", nil), 204)
	mustStatus(t, c.do("POST", "/api/board-invites/code/"+open.Code+"/join", nil), 400)

	var links []boardInviteDTO
	a.decode(a.do("GET", "/api/boards/"+boardID+"/invites", nil), &links)
	byID := map[int64]boardInviteDTO{}
	for _, l := range links {
		byID[l.ID] = l
	}
	if got := byID[oneSeat.ID]; got.State != "exhausted" || got.Used != 1 || *got.Left != 0 {
		t.Fatalf("one-seat link = %+v, want exhausted with 1 used and 0 left", got)
	}
	if got := byID[open.ID]; got.State != "revoked" {
		t.Fatalf("revoked link = %+v, want state revoked", got)
	}
	if got := byID[oneSeat.ID]; len(got.Joined) != 1 {
		t.Fatalf("revoked link kept %d joiners, want the history to survive", len(got.Joined))
	}
}

// TestInviteLinkApproval: a link that needs approval queues the joiner instead
// of admitting them, a pending request spends no seat, approving admits, and a
// decline is final for that link.
func TestInviteLinkApproval(t *testing.T) {
	_, _, a, b, c, boardID := inviteBoard(t)
	var meB, meC meResponse
	b.decode(b.do("GET", "/api/auth/me", nil), &meB)
	c.decode(c.do("GET", "/api/auth/me", nil), &meC)

	inv := mintInviteLink(t, a, boardID, map[string]any{"max_uses": 1, "requires_approval": true})

	var joined joinInviteResponse
	b.decode(b.do("POST", "/api/board-invites/code/"+inv.Code+"/join", nil), &joined)
	if joined.State != "pending" {
		t.Fatalf("join state = %q, want pending", joined.State)
	}
	// Still a stranger: the board is not readable while the request waits.
	mustStatus(t, b.do("GET", "/api/boards/"+boardID+"/members", nil), 403)

	// The queue may grow past the cap, because only an approval spends a seat.
	c.decode(c.do("POST", "/api/board-invites/code/"+inv.Code+"/join", nil), &joined)
	if joined.State != "pending" {
		t.Fatalf("second join state = %q, want pending", joined.State)
	}
	var links []boardInviteDTO
	a.decode(a.do("GET", "/api/boards/"+boardID+"/invites", nil), &links)
	if len(links) != 1 || links[0].Used != 0 || *links[0].Left != 1 || len(links[0].Pending) != 2 {
		t.Fatalf("link with two waiting = %+v, want 0 used, 1 left, 2 pending", links[0])
	}

	mustStatus(t, a.do("POST", "/api/boards/"+boardID+"/join-requests/"+itoa(meB.UserID),
		map[string]string{"decision": "approve"}), 204)
	mustStatus(t, b.do("GET", "/api/boards/"+boardID+"/members", nil), 200)

	// The seat is gone, so C cannot be approved into it.
	mustStatus(t, a.do("POST", "/api/boards/"+boardID+"/join-requests/"+itoa(meC.UserID),
		map[string]string{"decision": "approve"}), 400)

	mustStatus(t, a.do("POST", "/api/boards/"+boardID+"/join-requests/"+itoa(meC.UserID),
		map[string]string{"decision": "decline"}), 204)
	var peek boardInvitePeekDTO
	c.decode(c.do("GET", "/api/board-invites/code/"+inv.Code, nil), &peek)
	if peek.State != "declined" {
		t.Fatalf("declined peek = %q, want declined", peek.State)
	}
	// Asking again through the same link is refused, not re-queued.
	mustStatus(t, c.do("POST", "/api/board-invites/code/"+inv.Code+"/join", nil), 400)

	a.decode(a.do("GET", "/api/boards/"+boardID+"/invites", nil), &links)
	if links[0].Used != 1 || *links[0].Left != 0 || len(links[0].Pending) != 0 || len(links[0].Joined) != 1 {
		t.Fatalf("settled link = %+v, want 1 used, 0 left, nobody waiting", links[0])
	}
}

// TestInviteLinkOwnerOnly: an editor may hold a board but not its links.
func TestInviteLinkOwnerOnly(t *testing.T) {
	srv, _, a, b, _, boardID := inviteBoard(t)
	var meB meResponse
	b.decode(b.do("GET", "/api/auth/me", nil), &meB)
	bid, _ := strconv.ParseInt(boardID, 10, 64)
	addBoardMember(t, srv, bid, meB.UserID)

	inv := mintInviteLink(t, a, boardID, nil)
	mustStatus(t, b.do("GET", "/api/boards/"+boardID+"/invites", nil), 403)
	mustStatus(t, b.do("POST", "/api/boards/"+boardID+"/invites", map[string]any{}), 403)
	mustStatus(t, b.do("POST", "/api/board-invites/"+itoa(inv.ID)+"/revoke", nil), 403)
	mustStatus(t, b.do("DELETE", "/api/board-invites/"+itoa(inv.ID), nil), 403)
}

// TestInviteLinkDelete: deleting a link takes its history with it and leaves
// the member it admitted in place.
func TestInviteLinkDelete(t *testing.T) {
	_, _, a, b, _, boardID := inviteBoard(t)
	inv := mintInviteLink(t, a, boardID, nil)
	mustStatus(t, b.do("POST", "/api/board-invites/code/"+inv.Code+"/join", nil), 200)
	mustStatus(t, a.do("DELETE", "/api/board-invites/"+itoa(inv.ID), nil), 204)

	var links []boardInviteDTO
	a.decode(a.do("GET", "/api/boards/"+boardID+"/invites", nil), &links)
	if len(links) != 0 {
		t.Fatalf("links after delete = %+v, want none", links)
	}
	mustStatus(t, b.do("GET", "/api/boards/"+boardID+"/members", nil), 200)
	mustStatus(t, b.do("GET", "/api/board-invites/code/"+inv.Code, nil), 404)
}

// TestLoginNextOnlyJoinPaths: a logged-out invitee is sent to /login and must
// land back on the link. Only a /join/ path is honoured — `next` is attacker
// input, and anything else is an open redirect.
func TestLoginNextOnlyJoinPaths(t *testing.T) {
	ts, _ := newTestServer(t)
	c := &apiClient{t: t, base: ts.URL}
	body := func(path string) string {
		resp := c.do("GET", path, nil)
		mustStatus(t, resp, 200)
		defer resp.Body.Close()
		buf := new(bytes.Buffer)
		buf.ReadFrom(resp.Body)
		return buf.String()
	}
	if got := body("/login?next=/join/ABC123"); !strings.Contains(got, `data-login-redirect="/join/ABC123"`) {
		t.Fatalf("login page did not carry the join destination")
	}
	for _, bad := range []string{"https://evil.example", "//evil.example", "/admin", "/join/../admin", "/join/x?y=z"} {
		if got := body("/login?next=" + url.QueryEscape(bad)); !strings.Contains(got, `data-login-redirect="/"`) {
			t.Fatalf("next=%q was honoured, want the default destination", bad)
		}
	}
}

// TestInviteLinkOnePersonOneRequest: waiting is board-wide. Two links must not
// let one person queue twice — approving both would spend two seats, and a
// decide would pick between their rows arbitrarily.
func TestInviteLinkOnePersonOneRequest(t *testing.T) {
	_, _, a, b, _, boardID := inviteBoard(t)
	var meB meResponse
	b.decode(b.do("GET", "/api/auth/me", nil), &meB)

	first := mintInviteLink(t, a, boardID, map[string]any{"requires_approval": true})
	second := mintInviteLink(t, a, boardID, map[string]any{"requires_approval": true})
	mustStatus(t, b.do("POST", "/api/board-invites/code/"+first.Code+"/join", nil), 200)

	var peek boardInvitePeekDTO
	b.decode(b.do("GET", "/api/board-invites/code/"+second.Code, nil), &peek)
	if peek.State != "pending" {
		t.Fatalf("second link peek = %q, want pending — the request belongs to the board", peek.State)
	}
	var joined joinInviteResponse
	b.decode(b.do("POST", "/api/board-invites/code/"+second.Code+"/join", nil), &joined)
	if joined.State != "pending" {
		t.Fatalf("second join = %q, want the existing request", joined.State)
	}

	var links []boardInviteDTO
	a.decode(a.do("GET", "/api/boards/"+boardID+"/invites", nil), &links)
	waiting := 0
	for _, l := range links {
		waiting += len(l.Pending)
	}
	if waiting != 1 {
		t.Fatalf("owner sees %d requests, want one person once", waiting)
	}

	mustStatus(t, a.do("POST", "/api/boards/"+boardID+"/join-requests/"+itoa(meB.UserID),
		map[string]string{"decision": "approve"}), 204)
	a.decode(a.do("GET", "/api/boards/"+boardID+"/invites", nil), &links)
	used := 0
	for _, l := range links {
		used += int(l.Used)
	}
	if used != 1 {
		t.Fatalf("approving spent %d seats, want one", used)
	}
}

// TestInviteLinkRemovedMemberStaysOut: a link that already admitted someone the
// owner has since removed does not quietly let them back in.
func TestInviteLinkRemovedMemberStaysOut(t *testing.T) {
	_, _, a, b, _, boardID := inviteBoard(t)
	var meB meResponse
	b.decode(b.do("GET", "/api/auth/me", nil), &meB)

	inv := mintInviteLink(t, a, boardID, nil)
	mustStatus(t, b.do("POST", "/api/board-invites/code/"+inv.Code+"/join", nil), 200)
	mustStatus(t, a.do("DELETE", "/api/boards/"+boardID+"/members/"+itoa(meB.UserID), nil), 204)

	var peek boardInvitePeekDTO
	b.decode(b.do("GET", "/api/board-invites/code/"+inv.Code, nil), &peek)
	if peek.State != "spent" {
		t.Fatalf("peek after removal = %q, want spent", peek.State)
	}
	mustStatus(t, b.do("POST", "/api/board-invites/code/"+inv.Code+"/join", nil), 400)
}

// TestInviteLinkDeletedBoard: deleting a board kills its links, which otherwise
// resolve off board_invites alone and never see boards.deleted_at.
func TestInviteLinkDeletedBoard(t *testing.T) {
	_, _, a, b, _, boardID := inviteBoard(t)
	inv := mintInviteLink(t, a, boardID, nil)
	mustStatus(t, a.do("DELETE", "/api/boards/"+boardID, nil), 204)

	mustStatus(t, b.do("GET", "/api/board-invites/code/"+inv.Code, nil), 404)
	mustStatus(t, b.do("POST", "/api/board-invites/code/"+inv.Code+"/join", nil), 404)
}

// TestInviteLinkMintLimits: the two caps a link's own settings must respect.
func TestInviteLinkMintLimits(t *testing.T) {
	_, _, a, _, _, boardID := inviteBoard(t)
	mustStatus(t, a.do("POST", "/api/boards/"+boardID+"/invites", map[string]any{"ttl_hours": 1 << 40}), 400)
	mustStatus(t, a.do("POST", "/api/boards/"+boardID+"/invites", map[string]any{"max_uses": -1}), 400)
	mustStatus(t, a.do("POST", "/api/boards/"+boardID+"/invites", map[string]any{"label": strings.Repeat("я", 101)}), 400)
}
