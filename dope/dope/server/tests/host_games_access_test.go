package tests

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"dope/dope/platform/roles"

	"pecheny.me/dopecore/session"
)

// An admin limits a host to some of the fest's Games: the host writes to
// those, is refused on the rest (and watches them as a viewer), and cannot
// change the limits. A host no admin limited runs every Game, as before.
func TestAdminLimitsAHostToSomeGames(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	if _, err := db.Exec(`update fests set is_public = 0 where id = ?`, festID); err != nil {
		t.Fatal(err)
	}
	adminID, adminToken := createAPITestSession(t, srv, "boss")
	addAPITestRole(t, srv, festID, adminID, roles.Admin)
	hostID, hostToken := createAPITestSession(t, srv, "troika_host")
	addAPITestRole(t, srv, festID, hostID, roles.Host)

	hostPost := func(token, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
		resp := httptest.NewRecorder()
		srv.HostPageServer().HandleHostRouter(resp, req)
		return resp
	}
	get := func(token, path string, handler func(http.ResponseWriter, *http.Request)) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if token != "" {
			req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
		}
		resp := httptest.NewRecorder()
		handler(resp, req)
		return resp
	}
	var games []int64
	for i := 0; i < 2; i++ {
		if resp := hostPost(adminToken, fmt.Sprintf("/host/fest/%d/game/new", festID),
			url.Values{"game_type": {"od"}, "od_tours": {"1"}, "od_questions": {"3"}}); resp.Code != http.StatusSeeOther {
			t.Fatalf("create od: %d %s", resp.Code, resp.Body.String())
		}
		var id int64
		db.QueryRow(`select max(id) from games where fest_id = ?`, festID).Scan(&id)
		games = append(games, id)
	}
	troika, other := games[0], games[1]
	write := func(token string, gameID int64) int {
		t.Helper()
		return scopedAPIRequest(t, srv, http.MethodPatch, fmt.Sprintf("/api/fest/%d/games/%d/state", festID, gameID),
			map[string]any{"ops": []map[string]any{{"path": []any{"completed"}, "value": []bool{true}}}}, token).Code
	}

	// Not limited: every Game, as before.
	if a, b := write(hostToken, troika), write(hostToken, other); a != http.StatusOK || b != http.StatusOK {
		t.Fatalf("unlimited host: %d, %d; want 200, 200", a, b)
	}

	// The host may not limit anyone, themselves included.
	uid := fmt.Sprint(hostID)
	limit := url.Values{"role_" + uid: {"host"}, "games_present_" + uid: {"1"}, "games_" + uid: {fmt.Sprint(troika)}}
	hostPost(hostToken, fmt.Sprintf("/host/fest/%d/access", festID), limit)
	var rows int
	db.QueryRow(`select count(*) from fest_game_hosts where fest_id = ?`, festID).Scan(&rows)
	if rows != 0 {
		t.Fatalf("a host limited hosts (%d rows)", rows)
	}

	// The admin limits them to the first Game.
	if resp := hostPost(adminToken, fmt.Sprintf("/host/fest/%d/access", festID), limit); resp.Code >= 400 {
		t.Fatalf("admin access save: %d %s", resp.Code, resp.Body.String())
	}
	if a, b := write(hostToken, troika), write(hostToken, other); a != http.StatusOK || b != http.StatusForbidden {
		t.Fatalf("limited host: %d, %d; want 200, 403", a, b)
	}
	// Admins are never limited.
	if code := write(adminToken, other); code != http.StatusOK {
		t.Fatalf("admin on the other game: %d", code)
	}
	// The other Game's host page sends the host to its viewer page, which a
	// private fest now opens to its organizers — and still not to strangers.
	page := get(hostToken, fmt.Sprintf("/host/fest/%d/game/%d/", festID, other), srv.HostPageServer().HandleHostRouter)
	if page.Code != http.StatusSeeOther || !strings.HasPrefix(page.Header().Get("Location"), fmt.Sprintf("/fest/%d/game/%d", festID, other)) {
		t.Fatalf("host page of the other game: %d → %q", page.Code, page.Header().Get("Location"))
	}
	if page := get(hostToken, fmt.Sprintf("/host/fest/%d/game/%d/", festID, troika), srv.HostPageServer().HandleHostRouter); page.Code != http.StatusOK {
		t.Fatalf("host page of their own game: %d", page.Code)
	}
	viewer := get(hostToken, fmt.Sprintf("/fest/%d/game/%d/", festID, other), srv.HandleFestRouter)
	if viewer.Code != http.StatusOK {
		t.Fatalf("viewer page for the limited host: %d", viewer.Code)
	}
	if cc := viewer.Header().Get("Cache-Control"); strings.Contains(cc, "public") {
		t.Fatalf("a private fest's viewer page is cacheable by anyone: %q", cc)
	}
	if stranger := get("", fmt.Sprintf("/fest/%d/game/%d/", festID, other), srv.HandleFestRouter); stranger.Code == http.StatusOK {
		t.Fatalf("a stranger opened a private fest's game page")
	}

	// Unticking every box: every Game again.
	if resp := hostPost(adminToken, fmt.Sprintf("/host/fest/%d/access", festID),
		url.Values{"role_" + uid: {"host"}, "games_present_" + uid: {"1"}}); resp.Code >= 400 {
		t.Fatalf("admin clears limits: %d", resp.Code)
	}
	if code := write(hostToken, other); code != http.StatusOK {
		t.Fatalf("host after the limits were cleared: %d", code)
	}
	// Removing the host from the fest drops their limits with them.
	hostPost(adminToken, fmt.Sprintf("/host/fest/%d/access", festID), limit)
	hostPost(adminToken, fmt.Sprintf("/host/fest/%d/access", festID), url.Values{"delete_" + uid: {"1"}})
	db.QueryRow(`select count(*) from fest_game_hosts where user_id = ?`, hostID).Scan(&rows)
	if rows != 0 {
		t.Fatalf("%d limits outlived the host's access", rows)
	}
}
