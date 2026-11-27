package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"dope/dope/platform/roles"
	"dope/dope/storage/store"
)

// A fest's rooms and a game's venue map (ADR-0037): the fest lists its rooms;
// a game's venues are the numbers its scheme deals, and its map puts each in
// a room with its hosts, by the room's title through the API as an agent
// would; a bout reads the room and the hosts, and may name hosts of its own;
// the rooms page lists each room's bouts; a room a map uses is not deleted.
func TestRoomsAndAGamesVenueMap(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	db := srv.Eng().DB
	res, err := db.Exec(`insert into fests(title, revision, created_at, updated_at) values('Воронёнок', 1, '2026-11-27', '2026-11-27')`)
	if err != nil {
		t.Fatal(err)
	}
	festID, _ := res.LastInsertId()
	seedFestTeams(t, db, festID, 4)
	gameID := createSchemeGame(t, db, festID, "brain", "Брейн", "[defaults]\nquestions: 5\nvenues: 2\n\n[scheme]\nkind: roundrobin\ngroups: 2\ngroup_size: 2\n")
	id, token := createAPITestSession(t, srv, "host")
	addAPITestRole(t, srv, festID, id, roles.Admin)
	api := func(method, path string, body any) (int, string) {
		t.Helper()
		resp := scopedAPIRequest(t, srv, method, fmt.Sprintf("/api/fest/%d%s", festID, path), body, token)
		return resp.Code, resp.Body.String()
	}
	game := fmt.Sprintf("/games/%d", gameID)

	if code, body := api(http.MethodGet, "/rooms", nil); code != http.StatusOK || !strings.Contains(body, `"rooms":[]`) || !strings.Contains(body, `"bouts":[]`) {
		t.Fatalf("an empty fest's rooms: %d %s", code, body)
	}
	for _, title := range []string{"Б-414", "Б-209"} {
		if code, body := api(http.MethodPost, "/rooms", map[string]any{"title": title}); code != http.StatusOK {
			t.Fatalf("add %s: %d %s", title, code, body)
		}
	}
	if code, _ := api(http.MethodPost, "/rooms", map[string]any{"title": " б-414 "}); code != http.StatusBadRequest {
		t.Fatalf("a second Б-414 answered %d", code)
	}

	venues := func() []store.VenueView {
		t.Helper()
		code, body := api(http.MethodGet, game+"/venues", nil)
		var out []store.VenueView
		if code != http.StatusOK || json.Unmarshal([]byte(body), &out) != nil {
			t.Fatalf("venues: %d %s", code, body)
		}
		return out
	}
	if got := venues(); len(got) != 2 || got[0].Number != 1 || got[1].Number != 2 || got[0].Title != "" {
		t.Fatalf("the game's venues before its map = %+v", got)
	}
	if code, body := api(http.MethodPut, game+"/venues", map[string]any{"venues": []map[string]any{
		{"number": 1, "room": "б-414", "hosts": "Даша Жукова"},
		{"number": 2, "room": "Б-209"},
	}}); code != http.StatusOK {
		t.Fatalf("map: %d %s", code, body)
	}
	if got := venues(); got[0].Title != "Б-414" || got[0].Hosts != "Даша Жукова" || got[1].Title != "Б-209" || got[0].Bouts == 0 {
		t.Fatalf("the game's venues after its map = %+v", got)
	}
	for _, bad := range []map[string]any{{"number": 1, "room": "Б-999"}, {"number": 3, "room": "Б-414"}} {
		if code, _ := api(http.MethodPut, game+"/venues", map[string]any{"venues": []map[string]any{bad}}); code != http.StatusBadRequest {
			t.Fatalf("map line %v answered %d", bad, code)
		}
	}

	var boutCode string
	if err := db.QueryRow(`select code from matches where game_id = ? and venue = 1 order by position limit 1`, gameID).Scan(&boutCode); err != nil {
		t.Fatal(err)
	}
	bout := func() store.VenueView {
		t.Helper()
		code, body := api(http.MethodGet, game+"/matches/"+boutCode, nil)
		var view struct {
			Venue *store.VenueView `json:"venue"`
		}
		if code != http.StatusOK || json.Unmarshal([]byte(body), &view) != nil || view.Venue == nil {
			t.Fatalf("bout: %d %s", code, body)
		}
		return *view.Venue
	}
	if got := bout(); got.Title != "Б-414" || got.Hosts != "Даша Жукова" {
		t.Fatalf("the bout's venue = %+v", got)
	}
	// The final in the same room, run by the head judge.
	if code, body := api(http.MethodPost, game+"/matches/"+boutCode+"/venue", map[string]any{"number": 1, "hosts": "Дрозд"}); code != http.StatusOK {
		t.Fatalf("bout hosts: %d %s", code, body)
	}
	if got := bout(); got.Hosts != "Дрозд" || got.OwnHosts != "Дрозд" || got.Title != "Б-414" {
		t.Fatalf("the bout's venue with its own hosts = %+v", got)
	}
	if code, _ := api(http.MethodPost, game+"/matches/"+boutCode+"/venue", map[string]any{"number": 5}); code == http.StatusOK {
		t.Fatal("a bout moved to a venue its game does not have")
	}

	code, body := api(http.MethodGet, "/rooms", nil)
	var page struct {
		Rooms []struct {
			ID    int64    `json:"id"`
			Title string   `json:"title"`
			Games []string `json:"games"`
		} `json:"rooms"`
		Bouts []struct {
			Room  int64  `json:"room"`
			Code  string `json:"code"`
			Hosts string `json:"hosts"`
		} `json:"bouts"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(body), &page) != nil || len(page.Rooms) != 2 {
		t.Fatalf("rooms page: %d %s", code, body)
	}
	first := page.Rooms[0]
	if first.Title != "Б-414" || len(first.Games) != 1 || first.Games[0] != "Брейн" {
		t.Fatalf("first room = %+v", first)
	}
	var judged bool
	for _, b := range page.Bouts {
		judged = judged || (b.Room == first.ID && b.Code == boutCode && b.Hosts == "Дрозд")
	}
	if !judged {
		t.Fatalf("the schedule lacks the judged bout in Б-414: %s", body)
	}

	room := fmt.Sprintf("/rooms/%d", first.ID)
	if code, body := api(http.MethodDelete, room, nil); code != http.StatusBadRequest || !strings.Contains(body, "Брейн") {
		t.Fatalf("deleting a room in use: %d %s", code, body)
	}
	if code, body := api(http.MethodPut, room, map[string]any{"title": "Б-415"}); code != http.StatusOK {
		t.Fatalf("rename: %d %s", code, body)
	}
	if got := bout(); got.Title != "Б-415" {
		t.Fatalf("the bout after the rename = %+v", got)
	}
	if code, body := api(http.MethodPut, game+"/venues/1", map[string]any{"room": ""}); code != http.StatusOK {
		t.Fatalf("unmap: %d %s", code, body)
	}
	if code, body := api(http.MethodDelete, room, nil); code != http.StatusOK {
		t.Fatalf("deleting a room no map uses: %d %s", code, body)
	}
}
