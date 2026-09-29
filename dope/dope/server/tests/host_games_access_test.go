package tests

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"dope/dope/platform/roles"
	"dope/dope/storage/store"

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

// The access API reads and sets a host's Games, as the dashboard does: a
// change carrying games limits the host to them (by id, code or slug), games
// alone need no role, an empty list lifts the limit, and only a host takes one.
func TestAccessAPISetsAHostsGames(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, gameID := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	adminID, adminToken := createAPITestSession(t, srv, "boss")
	addAPITestRole(t, srv, festID, adminID, roles.Admin)
	hostID, hostToken := createAPITestSession(t, srv, "troika_host")
	addAPITestRole(t, srv, festID, hostID, roles.Host)
	var code string
	if err := db.QueryRow(`select code from games where id = ?`, gameID).Scan(&code); err != nil {
		t.Fatal(err)
	}
	access := fmt.Sprintf("/api/fest/%d/access", festID)
	gamesOf := func(resp *httptest.ResponseRecorder) []int64 {
		t.Helper()
		if resp.Code != http.StatusOK {
			t.Fatalf("access: %d %s", resp.Code, resp.Body.String())
		}
		for _, m := range decodeInto[[]struct {
			Nickname string  `json:"nickname"`
			Games    []int64 `json:"games"`
		}](t, resp) {
			if m.Nickname == "troika_host" {
				return m.Games
			}
		}
		t.Fatal("the host is not listed")
		return nil
	}

	set := func(token string, games []string) *httptest.ResponseRecorder {
		return scopedAPIRequest(t, srv, http.MethodPost, access,
			map[string]any{"changes": []map[string]any{{"user": "troika_host", "games": games}}}, token)
	}
	if got := gamesOf(set(adminToken, []string{code})); len(got) != 1 || got[0] != gameID {
		t.Fatalf("limited to %v, want [%d]", got, gameID)
	}
	if got := gamesOf(scopedAPIRequest(t, srv, http.MethodGet, access, nil, adminToken)); len(got) != 1 {
		t.Fatalf("GET shows %v", got)
	}
	if resp := set(hostToken, []string{}); resp.Code == http.StatusOK {
		t.Fatalf("a host lifted their own limit: %d", resp.Code)
	}
	if resp := set(adminToken, []string{"no-such-game"}); resp.Code != http.StatusBadRequest {
		t.Fatalf("an unknown game: %d %s", resp.Code, resp.Body.String())
	}
	resp := scopedAPIRequest(t, srv, http.MethodPost, access,
		map[string]any{"changes": []map[string]any{{"user": "boss", "games": []string{code}}}}, adminToken)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("limiting an admin: %d %s", resp.Code, resp.Body.String())
	}
	if got := gamesOf(set(adminToken, []string{})); len(got) != 0 {
		t.Fatalf("after lifting: %v", got)
	}
}

// A scheme import deletes the fest's Games and makes them anew. A host limited
// to one keeps the limit on the Game that comes back under the same slug,
// rather than silently running every Game.
func TestSchemeImportKeepsAHostsGames(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	adminID, adminToken := createAPITestSession(t, srv, "boss")
	addAPITestRole(t, srv, festID, adminID, roles.Admin)
	hostID, _ := createAPITestSession(t, srv, "troika_host")
	addAPITestRole(t, srv, festID, hostID, roles.Host)
	scheme := store.FestScheme{SchemaVersion: 2, Slug: "keep-me", Title: "Keep me", GameType: "ek",
		Stages: []store.SchemeStage{{Code: "r1", Title: "r1", StageType: "matches", Position: 1,
			Matches: []store.SchemeMatch{{Code: "A", Title: "A", ParticipantCount: 1,
				Slots: []store.SchemeSlot{{Seed: &store.SchemeSeedRef{Basket: 1, Number: 1}}}}}}}}
	importScheme := func() int64 {
		t.Helper()
		resp := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/scheme-import", festID), scheme, adminToken)
		if resp.Code != http.StatusOK {
			t.Fatalf("import: %d %s", resp.Code, resp.Body.String())
		}
		var id int64
		if err := db.QueryRow(`select id from games where fest_id = ?`, festID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	first := importScheme()
	resp := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/access", festID),
		map[string]any{"changes": []map[string]any{{"user": "troika_host", "games": []string{fmt.Sprint(first)}}}}, adminToken)
	if resp.Code != http.StatusOK {
		t.Fatalf("limit: %d %s", resp.Code, resp.Body.String())
	}
	// The wipe deletes the Game and its limit rows with it (the id may come
	// back, SQLite reuses it); only the carry-over puts the limit back.
	second := importScheme()
	var limited int64
	if err := db.QueryRow(`select game_id from fest_game_hosts where fest_id = ? and user_id = ?`, festID, hostID).Scan(&limited); err != nil {
		t.Fatalf("the limit is gone after the import: %v", err)
	}
	if limited != second {
		t.Fatalf("limited to %d, want the re-made game %d", limited, second)
	}
}
