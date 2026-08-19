package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	dopeserver "dope/dope/server"

	"pecheny.me/dopecore/session"
)

// A flat game is a Structure like every other: its one Block ranks into
// stage_standings as the document changes, and the fest view carries the
// table — ОД by взятые and rating, a КСИ team that declined ranked last.
func TestFlatGameRanksAsItIsPlayed(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	seedFestTeams(t, db, festID, 4)
	odID := createGameThroughForm(t, srv, festID, token, map[string]string{"game_type": "od", "od_tours": "1", "od_questions": "3"})

	names := func(gameID int64) []string {
		resp := scopedAPIRequest(t, srv, http.MethodGet, fmt.Sprintf("/api/fest/%d/games/%d", festID, gameID), nil, token)
		if resp.Code != http.StatusOK {
			t.Fatalf("view: %d %s", resp.Code, resp.Body.String())
		}
		var view struct {
			Stages []struct {
				Kind      string `json:"kind"`
				Standings []struct {
					Name string `json:"name"`
				} `json:"standings"`
			} `json:"stages"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &view); err != nil {
			t.Fatal(err)
		}
		if len(view.Stages) != 1 || view.Stages[0].Kind != "flat" {
			t.Fatalf("stages = %+v, want one flat Block", view.Stages)
		}
		out := []string{}
		for _, row := range view.Stages[0].Standings {
			out = append(out, row.Name)
		}
		return out
	}
	// Участник 4 takes three, 2 takes two, 1 takes one, 3 none.
	if resp := scopedAPIRequest(t, srv, http.MethodPatch, fmt.Sprintf("/api/fest/%d/games/%d/state", festID, odID),
		map[string]any{"ops": []map[string]any{
			{"path": []any{"entries"}, "value": [][]int{{4, 2, 1}, {4, 2}, {4}}},
			{"path": []any{"completed"}, "value": []bool{true, true, true}},
		}}, token); resp.Code != http.StatusOK {
		t.Fatalf("patch od: %d %s", resp.Code, resp.Body.String())
	}
	if got := fmt.Sprint(names(odID)); got != "[Участник 4 Участник 2 Участник 1 Участник 3]" {
		t.Errorf("ОД table = %s", got)
	}

	ksiID := createGameThroughForm(t, srv, festID, token, map[string]string{"game_type": "ksi", "ksi_themes": "1"})
	// Team 2 answers the 50; team 1 declines to play on.
	if resp := scopedAPIRequest(t, srv, http.MethodPatch, fmt.Sprintf("/api/fest/%d/games/%d/state", festID, ksiID),
		map[string]any{"ops": []map[string]any{
			{"path": []any{"themes", 0, "answers", 1, 4}, "value": "right"},
			{"path": []any{"declined", "n1"}, "value": true},
		}}, token); resp.Code != http.StatusOK {
		t.Fatalf("patch ksi: %d %s", resp.Code, resp.Body.String())
	}
	got := names(ksiID)
	if len(got) != 4 || got[0] != "Участник 2" || got[3] != "Участник 1" {
		t.Errorf("КСИ table = %v, want Участник 2 first and the declined Участник 1 last", got)
	}
}

// createGameThroughForm posts the host's «новая игра» form and returns the
// game it made — the newest game of the fest.
func createGameThroughForm(t *testing.T, srv *dopeserver.Server, festID int64, token string, fields map[string]string) int64 {
	t.Helper()
	form := url.Values{}
	for k, v := range fields {
		form.Set(k, v)
	}
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/host/fest/%d/game/new", festID), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
	resp := httptest.NewRecorder()
	srv.HostPageServer().HandleHostRouter(resp, req)
	if resp.Code != http.StatusSeeOther {
		t.Fatalf("create game: %d %s", resp.Code, resp.Body.String())
	}
	var id int64
	if err := srv.Eng().DB.QueryRow(`select id from games where fest_id = ? order by id desc limit 1`, festID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// A seed source is a Game's table: [init] sorting re-orders it by any Metric
// the source's Protocol declares, and a Game of several tables is refused.
func TestSeedSourceIsAGamesTable(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	seedFestTeams(t, db, festID, 4)
	ksiID := createGameThroughForm(t, srv, festID, token, map[string]string{"game_type": "ksi", "ksi_themes": "1"})
	// Team 1: 50 right → 50/50. Team 2: 50 right, 20 and 10 wrong → 20/50.
	// Team 3: 40 right → 40/40. By total: 1, 3, 2, 4; by plus: 1, 2, 3, 4.
	if resp := scopedAPIRequest(t, srv, http.MethodPatch, fmt.Sprintf("/api/fest/%d/games/%d/state", festID, ksiID),
		map[string]any{"ops": []map[string]any{
			{"path": []any{"themes", 0, "answers", 0, 4}, "value": "right"},
			{"path": []any{"themes", 0, "answers", 1, 4}, "value": "right"},
			{"path": []any{"themes", 0, "answers", 1, 1}, "value": "wrong"},
			{"path": []any{"themes", 0, "answers", 1, 0}, "value": "wrong"},
			{"path": []any{"themes", 0, "answers", 2, 3}, "value": "right"},
		}}, token); resp.Code != http.StatusOK {
		t.Fatalf("patch ksi: %d %s", resp.Code, resp.Body.String())
	}
	var ksiCode string
	if err := db.QueryRow(`select code from games where id = ?`, ksiID).Scan(&ksiCode); err != nil {
		t.Fatal(err)
	}
	seededNames := func(dsl string) ([]string, *httptest.ResponseRecorder) {
		brainID := createSchemeGame(t, db, festID, "brain", "Брейн", dsl)
		resp := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/games/%d/seed-import/run", festID, brainID), nil, token)
		if resp.Code != http.StatusOK {
			return nil, resp
		}
		var view struct {
			Rows []struct {
				Name string `json:"name"`
			} `json:"rows"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &view); err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, row := range view.Rows {
			out = append(out, row.Name)
		}
		return out, resp
	}
	byTable, _ := seededNames(fmt.Sprintf("[init]\nseed: %s\n\n[scheme]\nkind: roundrobin\ngroup_size: 4\nmatch_size: 2\n", ksiCode))
	if got := fmt.Sprint(byTable); got != "[Участник 1 Участник 3 Участник 2 Участник 4]" {
		t.Errorf("seeded by the КСИ table: %s", got)
	}
	byPlus, _ := seededNames(fmt.Sprintf("[init]\nseed: %s\nsorting: [plus]\n\n[scheme]\nkind: roundrobin\ngroup_size: 4\nmatch_size: 2\n", ksiCode))
	if got := fmt.Sprint(byPlus); got != "[Участник 1 Участник 2 Участник 3 Участник 4]" {
		t.Errorf("seeded by plus: %s", got)
	}
	twoTables := createSchemeGame(t, db, festID, "brain", "Пары", "[scheme]\nkind: roundrobin\ngroup_size: 2\ngroups: 2\nmatch_size: 2\n")
	var pairsCode string
	if err := db.QueryRow(`select code from games where id = ?`, twoTables).Scan(&pairsCode); err != nil {
		t.Fatal(err)
	}
	if _, resp := seededNames(fmt.Sprintf("[init]\nseed: %s\n\n[scheme]\nkind: roundrobin\ngroup_size: 4\nmatch_size: 2\n", pairsCode)); resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "таблиц") {
		t.Errorf("a two-table source: %d %s, want a refusal naming the tables", resp.Code, resp.Body.String())
	}
}
