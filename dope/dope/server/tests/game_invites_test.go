package tests

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"dope/dope/platform/roles"

	"code.pecheny.me/pecheny/dopesuite/dopecore/session"
)

// An admin mints a link to one Game. A stranger who follows it becomes a host
// limited to that Game; following it again changes nothing; a host nobody
// limited keeps every Game; and a revoked link lets nobody else in.
func TestAHostInviteLinkMakesAHostOfOneGame(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, cityGame := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	adminID, adminToken := createAPITestSession(t, srv, "boss")
	addAPITestRole(t, srv, festID, adminID, roles.Admin)
	hostID, hostToken := createAPITestSession(t, srv, "everywhere")
	addAPITestRole(t, srv, festID, hostID, roles.Host)
	strangerID, strangerToken := createAPITestSession(t, srv, "minsk")
	_, lateToken := createAPITestSession(t, srv, "late")

	newGame := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/host/fest/%d/game/new", festID),
		strings.NewReader(url.Values{"game_type": {"od"}, "od_tours": {"1"}, "od_questions": {"3"}}.Encode()))
	newGame.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	newGame.AddCookie(&http.Cookie{Name: session.CookieName, Value: adminToken})
	srv.HostPageServer().HandleHostRouter(httptest.NewRecorder(), newGame)
	var otherGame int64
	db.QueryRow(`select max(id) from games where fest_id = ?`, festID).Scan(&otherGame)

	invites := fmt.Sprintf("/api/fest/%d/invites", festID)
	mint := fmt.Sprintf("/api/fest/%d/games/%d/invites", festID, cityGame)
	if resp := scopedAPIRequest(t, srv, http.MethodPost, mint, map[string]any{"label": "Минск"}, hostToken); resp.Code != http.StatusForbidden {
		t.Fatalf("a host minted a link: %d", resp.Code)
	}
	resp := scopedAPIRequest(t, srv, http.MethodPost, mint, map[string]any{"label": "Минск", "max_uses": 3}, adminToken)
	if resp.Code != http.StatusOK {
		t.Fatalf("mint: %d %s", resp.Code, resp.Body.String())
	}
	type link struct {
		ID     int64  `json:"id"`
		Code   string `json:"code"`
		URL    string `json:"url"`
		GameID int64  `json:"game_id"`
		State  string `json:"state"`
		Used   int64  `json:"used"`
	}
	links := decodeInto[[]link](t, resp)
	if len(links) != 1 || links[0].GameID != cityGame || !strings.HasSuffix(links[0].URL, "/join/"+links[0].Code) {
		t.Fatalf("links after minting: %+v", links)
	}
	code := links[0].Code
	var festSlug string
	db.QueryRow(`select slug from fests where id = ?`, festID).Scan(&festSlug)
	gamePage := fmt.Sprintf("/host/fest/%s/game/%d/", festSlug, cityGame)

	page := func(method, token string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "/join/"+code, nil)
		if token != "" {
			req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
		}
		rec := httptest.NewRecorder()
		srv.HostPageServer().HandleJoin(rec, req)
		srv.Settle()
		return rec
	}
	if got := page(http.MethodGet, ""); got.Code != http.StatusSeeOther || got.Header().Get("Location") != "/login?next=%2Fjoin%2F"+code {
		t.Fatalf("signed out: %d → %q", got.Code, got.Header().Get("Location"))
	}
	if got := page(http.MethodGet, strangerToken); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `action="/join/`+code+`"`) {
		t.Fatalf("the join page offers no button: %d %s", got.Code, got.Body.String())
	}
	if got := page(http.MethodPost, strangerToken); got.Code != http.StatusSeeOther ||
		got.Header().Get("Location") != gamePage {
		t.Fatalf("join: %d → %q", got.Code, got.Header().Get("Location"))
	}
	write := func(token string, gameID int64) int {
		t.Helper()
		return scopedAPIRequest(t, srv, http.MethodPatch, fmt.Sprintf("/api/fest/%d/games/%d/state", festID, gameID),
			map[string]any{"ops": []map[string]any{{"path": []any{"completed"}, "value": []bool{true}}}}, token).Code
	}
	if a, b := write(strangerToken, cityGame), write(strangerToken, otherGame); a != http.StatusOK || b != http.StatusForbidden {
		t.Fatalf("the joined host writes %d to their game and %d to the other; want 200, 403", a, b)
	}
	var role string
	db.QueryRow(`select role from fest_organizers where fest_id = ? and user_id = ?`, festID, strangerID).Scan(&role)
	if role != roles.Host {
		t.Fatalf("the joined role is %q", role)
	}
	if got := page(http.MethodGet, strangerToken); !strings.Contains(got.Body.String(), `href="`+gamePage+`"`) {
		t.Fatalf("a member's join page does not open the game: %s", got.Body.String())
	}

	// A host who ran every Game is a member already, and stays unlimited.
	join := func(token string) *httptest.ResponseRecorder {
		return scopedAPIRequest(t, srv, http.MethodPost, "/api/join/"+code, nil, token)
	}
	if resp := join(hostToken); resp.Code != http.StatusOK {
		t.Fatalf("the unlimited host joins: %d %s", resp.Code, resp.Body.String())
	}
	var limits int
	db.QueryRow(`select count(*) from fest_game_hosts where user_id = ?`, hostID).Scan(&limits)
	if limits != 0 {
		t.Fatalf("following a link limited a host who ran every Game (%d rows)", limits)
	}

	// The admin sees who came in, revokes the link, and nobody else gets in.
	links = decodeInto[[]link](t, scopedAPIRequest(t, srv, http.MethodGet, invites, nil, adminToken))
	if links[0].Used != 1 {
		t.Fatalf("used = %d, want 1", links[0].Used)
	}
	if resp := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("%s/%d/revoke", invites, links[0].ID), nil, adminToken); resp.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", resp.Code, resp.Body.String())
	}
	if resp := join(lateToken); resp.Code != http.StatusBadRequest {
		t.Fatalf("a revoked link let someone in: %d", resp.Code)
	}
	dash := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/host/fest/%d", festID), nil)
	dash.AddCookie(&http.Cookie{Name: session.CookieName, Value: adminToken})
	rec := httptest.NewRecorder()
	srv.HostPageServer().HandleHostRouter(rec, dash)
	if body := rec.Body.String(); !strings.Contains(body, `id="invites"`) || !strings.Contains(body, "minsk") {
		t.Fatalf("the dashboard does not list the link and who came in")
	}
}
