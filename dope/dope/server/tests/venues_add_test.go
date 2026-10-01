package tests

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"dope/dope/platform/roles"
	"dope/dope/storage/store"
)

// schemeVenueOf is the venue number the game's scheme puts each bout at, by
// the bout's code.
func schemeVenueOf(t *testing.T, db *sql.DB, gameID int64) map[string]int {
	t.Helper()
	var raw string
	if err := db.QueryRow(`select scheme_json from games where id = ?`, gameID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var scheme store.FestScheme
	if err := json.Unmarshal([]byte(raw), &scheme); err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, stage := range scheme.Stages {
		for _, match := range stage.Matches {
			out[match.Code] = match.Venue
		}
	}
	return out
}

// boutVenueNumbers is the fest venue number each of a game's bouts sits at,
// 0 for none.
func boutVenueNumbers(t *testing.T, db *sql.DB, gameID int64) map[string]int {
	t.Helper()
	rows, err := db.Query(`
select m.code, coalesce(v.number, 0) from matches m
left join venues v on v.id = m.venue_id
where m.game_id = ?`, gameID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var code string
		var n int
		if err := rows.Scan(&code, &n); err != nil {
			t.Fatal(err)
		}
		out[code] = n
	}
	return out
}

// A scheme that only counts its venues writes no venue rows, so its bouts
// sit at no venue. Adding venue 2 from the venues tab seats the bouts the
// scheme puts at table 2 there, and leaves the rest alone.
func TestAddVenueLinksTheSchemesBouts(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	if _, err := db.Exec(`update matches set venue_id = null where fest_id = ?`, festID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`delete from venues where fest_id = ?`, festID); err != nil {
		t.Fatal(err)
	}
	seedFestTeams(t, db, festID, 8)
	gameID := createSchemeGame(t, db, festID, "brain", "Столы",
		"[defaults]\nquestions: 3\nvenues: 3\n\n[scheme]\nkind: roundrobin\ngroup_size: 4\ngroups: 2\n")
	planned := schemeVenueOf(t, db, gameID)
	atTwo := 0
	for _, n := range planned {
		if n == 2 {
			atTwo++
		}
	}
	if atTwo == 0 || atTwo == len(planned) {
		t.Fatalf("scheme venues = %v, want some bouts at table 2 and some elsewhere", planned)
	}
	for code, n := range boutVenueNumbers(t, db, gameID) {
		if n != 0 {
			t.Fatalf("bout %s at venue %d before any venue exists", code, n)
		}
	}

	hostID, hostToken := createAPITestSession(t, srv, "venue-host")
	addAPITestRole(t, srv, festID, hostID, roles.Host)
	path := fmt.Sprintf("/api/fest/%d/venues", festID)
	resp := scopedAPIRequest(t, srv, http.MethodPost, path, map[string]any{"number": 2, "title": "Актовый зал"}, hostToken)
	if resp.Code != http.StatusOK {
		t.Fatalf("add venue 2 status = %d, body %s", resp.Code, resp.Body.String())
	}
	var venues []store.VenueView
	if err := json.Unmarshal(resp.Body.Bytes(), &venues); err != nil {
		t.Fatal(err)
	}
	if len(venues) != 1 || venues[0].Number != 2 || venues[0].Title != "Актовый зал" {
		t.Fatalf("venues = %#v, want only 2 «Актовый зал»", venues)
	}
	for code, n := range boutVenueNumbers(t, db, gameID) {
		want := 0
		if planned[code] == 2 {
			want = 2
		}
		if n != want {
			t.Fatalf("bout %s (scheme table %d) at venue %d, want %d", code, planned[code], n, want)
		}
	}

	// Without a number, a venue takes the next free one.
	resp = scopedAPIRequest(t, srv, http.MethodPost, path, map[string]any{"title": "Фойе"}, hostToken)
	if resp.Code != http.StatusOK {
		t.Fatalf("add venue status = %d, body %s", resp.Code, resp.Body.String())
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &venues); err != nil {
		t.Fatal(err)
	}
	if len(venues) != 2 || venues[1].Number != 3 || venues[1].Title != "Фойе" {
		t.Fatalf("venues = %#v, want «Фойе» at 3", venues)
	}

	// A taken number is refused.
	resp = scopedAPIRequest(t, srv, http.MethodPost, path, map[string]any{"number": 2, "title": "Ещё"}, hostToken)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("add taken venue status = %d, want 400", resp.Code)
	}

	// A venue a bout plays at cannot be deleted; an unused one can.
	resp = scopedAPIRequest(t, srv, http.MethodDelete, path+"/2", nil, hostToken)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("delete used venue status = %d, want 400", resp.Code)
	}
	if _, err := db.Exec(`update matches set venue_id = null where game_id = ? and venue_id = (select id from venues where fest_id = ? and number = 3)`, gameID, festID); err != nil {
		t.Fatal(err)
	}
	resp = scopedAPIRequest(t, srv, http.MethodDelete, path+"/3", nil, hostToken)
	if resp.Code != http.StatusOK {
		t.Fatalf("delete unused venue status = %d, body %s", resp.Code, resp.Body.String())
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &venues); err != nil {
		t.Fatal(err)
	}
	if len(venues) != 1 || venues[0].Number != 2 {
		t.Fatalf("venues after delete = %#v, want only 2", venues)
	}

	// Someone with no role on the fest reads the venues but cannot add one.
	_, viewerToken := createAPITestSession(t, srv, "venue-viewer")
	resp = scopedAPIRequest(t, srv, http.MethodPost, path, map[string]any{"title": "Кухня"}, viewerToken)
	if resp.Code != http.StatusForbidden && resp.Code != http.StatusUnauthorized {
		t.Fatalf("viewer add venue status = %d, want refused", resp.Code)
	}
	resp = scopedAPIRequest(t, srv, http.MethodPost, path, map[string]any{"title": "Кухня"}, "")
	if resp.Code != http.StatusForbidden && resp.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous add venue status = %d, want refused", resp.Code)
	}
}
