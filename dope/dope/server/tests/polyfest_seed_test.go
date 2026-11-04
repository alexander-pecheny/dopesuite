package tests

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Polyfest's КИнСбФ seeds on the ОД's first game day, whatever has been played
// since: `seed: od-1[tours 1]` ranks the ОД's table after tour 1 alone, kept
// to the youth/fest teams (division: -Студ).
func TestPolyfestKinsbfSeedsOnTheOdsFirstDay(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	seedFestTeams(t, db, festID, 20)
	for n := 1; n <= 4; n++ {
		if _, err := db.Exec(`
insert into fest_team_flags(team_id, position, short, full)
select id, 0, 'Студ', 'Студенческая команда' from fest_teams where fest_id = ? and number = ?`, festID, n); err != nil {
			t.Fatal(err)
		}
	}
	odID := createGameThroughForm(t, srv, festID, token, map[string]string{"game_type": "od", "od_tours": "2", "od_questions": "2"})
	var odCode string
	if err := db.QueryRow(`select code from games where id = ?`, odID).Scan(&odCode); err != nil {
		t.Fatal(err)
	}
	// Team 20 takes the one question of tour 1 anybody takes; team 5 takes
	// both of tour 2 and wins the ОД. Team 1 is a student team and leads
	// everything, which the youth seed must not see.
	if resp := scopedAPIRequest(t, srv, http.MethodPatch, fmt.Sprintf("/api/fest/%d/games/%d/state", festID, odID),
		map[string]any{"ops": []map[string]any{
			{"path": []any{"entries"}, "value": [][]int{{1, 20}, {1}, {1, 5}, {1, 5}}},
			{"path": []any{"completed"}, "value": []bool{true, true, true, true}},
		}}, token); resp.Code != http.StatusOK {
		t.Fatalf("od state: %d %s", resp.Code, resp.Body.String())
	}
	youth := readFile(t, "../../../scripts/polyfest/kinsbf-youth.dsl")
	numberOf := teamNumbers(t, db, festID)
	firstSeed := func(seed string) int {
		t.Helper()
		dsl := strings.Replace(youth, "seed: od-1[tours 1..3]", "seed: "+seed, 1)
		gameID := createSchemeGame(t, db, festID, "brain", "КИнСбФ "+seed, dsl)
		resp := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/games/%d/seed-import/run", festID, gameID), nil, token)
		if resp.Code != http.StatusOK {
			t.Fatalf("%s: seed-import %d %s", seed, resp.Code, resp.Body.String())
		}
		view := decodeJSON[esSeedView](t, resp)
		if len(view.Rows) != 16 {
			t.Fatalf("%s: imported %d teams, want the 16 youth/fest teams", seed, len(view.Rows))
		}
		return numberOf[view.Rows[0].TeamID]
	}
	if got := firstSeed(odCode); got != 5 {
		t.Fatalf("the whole ОД seeds team %d first, want 5", got)
	}
	if got := firstSeed(odCode + "[tours 1]"); got != 20 {
		t.Fatalf("the ОД's tour 1 seeds team %d first, want 20", got)
	}
	// The Участники tab offers the table over those tours beside the whole of
	// it and preselects it, and taking the list from it keeps to them.
	gameID := lastGameID(t, db, festID)
	type picker struct {
		Sources []struct {
			Game  string `json:"game"`
			Tours []int  `json:"tours"`
		} `json:"sources"`
		Preselect struct {
			Game  string `json:"game"`
			Tours []int  `json:"tours"`
		} `json:"preselect"`
		Rows []struct {
			TeamID int64 `json:"teamID"`
		} `json:"rows"`
	}
	entrants := fmt.Sprintf("/api/fest/%d/games/%d/entrants", festID, gameID)
	view := decodeJSON[picker](t, scopedAPIRequest(t, srv, http.MethodGet, entrants, nil, token))
	offered := 0
	for _, source := range view.Sources {
		if source.Game == odCode && fmt.Sprint(source.Tours) == "[1]" {
			offered++
		}
	}
	if offered != 1 || fmt.Sprint(view.Preselect.Tours) != "[1]" {
		t.Fatalf("the picker offers %+v, preselects %+v; want the ОД over tour 1", view.Sources, view.Preselect)
	}
	resp := scopedAPIRequest(t, srv, http.MethodPost, entrants+"/import",
		map[string]any{"kind": "game", "game": odCode, "division": "-Студ", "tours": []int{1}}, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("import from the picker: %d %s", resp.Code, resp.Body.String())
	}
	if got := numberOf[decodeJSON[picker](t, resp).Rows[0].TeamID]; got != 20 {
		t.Fatalf("the picker's ОД over tour 1 seeds team %d first, want 20", got)
	}
}

func lastGameID(t *testing.T, db *sql.DB, festID int64) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`select max(id) from games where fest_id = ?`, festID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// A seed that counts some tours names an ОД's: another Game has none, and
// the brackets take nothing but tours.
func TestASeedCountsToursOfAnOdAlone(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	seedFestTeams(t, db, festID, 4)
	ksiID := createGameThroughForm(t, srv, festID, token, map[string]string{"game_type": "ksi", "ksi_themes": "2"})
	var ksiCode string
	if err := db.QueryRow(`select code from games where id = ?`, ksiID).Scan(&ksiCode); err != nil {
		t.Fatal(err)
	}
	dsl := func(seed string) string {
		return "[init]\nseed: " + seed + "\n\n[scheme]\nkind: roundrobin\ngroups: 1\ngroup_size: 4\nquestions: 5\npoints: [3, 1, 0]\n"
	}
	for seed, want := range map[string]string{
		ksiCode + "[tours 1]":   "туры есть только у ОД",
		ksiCode + "[1]":         "после кода игры в скобках",
		"random[tours 1]":       "после кода игры в скобках",
		ksiCode + "[tours два]": "после кода игры в скобках",
	} {
		resp := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/games", festID),
			map[string]any{"game_type": "brain", "dsl": dsl(seed)}, token)
		if resp.Code == http.StatusOK || !strings.Contains(resp.Body.String(), want) {
			t.Errorf("seed: %s → %d %s, want a refusal saying %q", seed, resp.Code, resp.Body.String(), want)
		}
	}
}
