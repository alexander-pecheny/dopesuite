package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"dope/dope/domain/roster"
)

// A bout game follows a source for who its teams are, never for who sits
// where (ADR-0038). Following a ЧГК turns its hand rosters into overrides,
// its roster tab stops taking edits, a rename in the ЧГК reaches its бои
// without reseating anybody, a player override changes a team's roster, and
// a player who has played stays when the override that brought them goes.
func TestABoutGameFollowsASourceForWhoItsTeamsAre(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	seedTeamsWithPlayers(t, db, festID, 4)
	api := func(method, path string, body any) (int, string) {
		t.Helper()
		resp := scopedAPIRequest(t, srv, method, fmt.Sprintf("/api/fest/%d%s", festID, path), body, token)
		return resp.Code, resp.Body.String()
	}
	code, body := api(http.MethodPost, "/games", map[string]any{"game_type": "od", "od_tours": 1, "od_questions": 3})
	var od struct {
		ID int64 `json:"id"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(body), &od) != nil {
		t.Fatalf("create od: %d %s", code, body)
	}
	const dsl = "[init]\nseed: random\n\n[scheme]\nkind: single_elimination\nparticipants: 4\nmatch_size: 4\nwinning_places: 1\n"
	es := createSchemeGame(t, db, festID, "es", "ЭС", dsl)
	if code, body := api(http.MethodPost, fmt.Sprintf("/games/%d/seed-import/run", es), nil); code != http.StatusOK {
		t.Fatalf("seed-import: %d %s", code, body)
	}
	var bout string
	if err := db.QueryRow(`select code from matches where game_id = ? order by position, id limit 1`, es).Scan(&bout); err != nil {
		t.Fatal(err)
	}
	seats := matchSeatIDs(t, db, es, bout)
	team := seats[0]
	fest := gameRosterTab(t, srv, festID, es, token)[team].names()

	// A hand roster made before the game follows anything.
	if code, body := api(http.MethodPut, fmt.Sprintf("/games/%d/rosters/%d", es, team), map[string]any{"players": []string{fest[0], "Гость Гостев"}}); code != http.StatusOK {
		t.Fatalf("hand roster: %d %s", code, body)
	}
	want := []string{fest[0], "Гость Гостев"}

	type tab struct {
		From      int64             `json:"from"`
		Bout      bool              `json:"bout"`
		Teams     []roster.OwnTeam  `json:"teams"`
		Overrides []roster.Override `json:"overrides"`
	}
	teams := func(method, path string, body any) tab {
		t.Helper()
		code, out := api(method, fmt.Sprintf("/games/%d/teams%s", es, path), body)
		var got tab
		if code != http.StatusOK || json.Unmarshal([]byte(out), &got) != nil {
			t.Fatalf("%s teams%s: %d %s", method, path, code, out)
		}
		return got
	}
	got := teams(http.MethodPut, "", map[string]any{"from": od.ID})
	if !got.Bout || got.From != od.ID || len(got.Overrides) != 3 {
		t.Fatalf("following the ЧГК: %+v", got)
	}
	if after := gameRosterTab(t, srv, festID, es, token)[team]; !slices.Equal(after.names(), want) {
		t.Fatalf("the hand roster did not survive as overrides: %v, want %v", after.names(), want)
	}
	if code, _ := api(http.MethodPut, fmt.Sprintf("/games/%d/rosters/%d", es, team), map[string]any{"players": []string{fest[0]}}); code == http.StatusOK {
		t.Fatal("the roster tab took an edit of a game that follows a source")
	}

	// The ЧГК renames the team: the бой shows the new name, and nobody moves.
	var key, name string
	for _, row := range got.Teams {
		var participant int64
		if err := db.QueryRow(`select id from participants where name = ? and fest_id = ?`, row.Name, festID).Scan(&participant); err == nil && participant == team {
			key, name = row.Key, row.Name
		}
	}
	if key == "" {
		t.Fatalf("team %d is not on the tab: %+v", team, got.Teams)
	}
	if code, body := api(http.MethodPost, fmt.Sprintf("/games/%d/teams/overrides", od.ID), roster.Override{Kind: roster.OverrideRename, Team: key, Name: name + " (ЧГК)"}); code != http.StatusOK {
		t.Fatalf("rename in the ЧГК: %d %s", code, body)
	}
	if now := matchSeatIDs(t, db, es, bout); !slices.Equal(now, seats) {
		t.Fatalf("the бой reseated: %v, was %v", now, seats)
	}
	var shown string
	if err := db.QueryRow(`select coalesce(gp.name, p.name) from participants p left join game_team_names gp on gp.participant_id = p.id and gp.game_id = ? where p.id = ?`, es, team).Scan(&shown); err != nil || shown != name+" (ЧГК)" {
		t.Fatalf("the ЭС names the team %q (%v)", shown, err)
	}

	// The guest plays a theme; taking back the override that brought them
	// leaves them on the roster.
	guest := gameRosterTab(t, srv, festID, es, token)[team].Players[1]
	patchState(t, srv, festID, es, bout, token, []map[string]any{
		{"path": []any{"participants", fmt.Sprint(team), "themes", 0, "players"}, "value": []int64{guest.ID}},
	})
	var added int64
	for _, o := range teams(http.MethodGet, "", nil).Overrides {
		if o.Kind == roster.OverrideAddPlayer {
			added = o.ID
		}
	}
	teams(http.MethodDelete, fmt.Sprintf("/overrides/%d", added), nil)
	if names := gameRosterTab(t, srv, festID, es, token)[team].names(); !slices.Contains(names, "Гость Гостев") {
		t.Fatalf("a player who played left the roster: %v", names)
	}
}
