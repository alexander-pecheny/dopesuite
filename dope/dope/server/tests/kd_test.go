package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"pecheny.me/dopecore/session"
)

// A Кубок Дружбы (ADR-0026) seats its tables, not the fest's teams: a table
// numbered 1 must not rename fest team 1, a roster change must not replace the
// tables, a player scores the tables his route card sends him to, and a clear
// keeps the players the registration desk entered.
func TestFriendshipCupScoresPlayersByTheirRouteCards(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	seedFestTeams(t, db, festID, 3)

	// A table count that is no prime is refused.
	form := url.Values{"game_type": {"kd"}, "kd_tours": {"2"}, "kd_questions": {"2"}, "kd_tables": {"4"}}
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/host/fest/%d/game/new", festID), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
	resp := httptest.NewRecorder()
	srv.HostPageServer().HandleHostRouter(resp, req)
	if resp.Code == http.StatusSeeOther || !strings.Contains(resp.Body.String(), "простым") {
		t.Fatalf("4 tables: %d, want the prime refusal", resp.Code)
	}

	gameID := createGameThroughForm(t, srv, festID, token, map[string]string{"game_type": "kd", "kd_tours": "2", "kd_questions": "2", "kd_tables": "3"})
	var gameType string
	if err := db.QueryRow(`select game_type from games where id = ?`, gameID).Scan(&gameType); err != nil || gameType != "kd" {
		t.Fatalf("game type %q, %v", gameType, err)
	}
	fests := func() string {
		t.Helper()
		rows, err := db.Query(`select name from participants where fest_id = ? and assembled = 0 order by number`, festID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var names []string
		for rows.Next() {
			var name string
			rows.Scan(&name)
			names = append(names, name)
		}
		return strings.Join(names, ",")
	}
	if got := fests(); strings.Contains(got, "Стол") {
		t.Fatalf("a table renamed a fest team: %s", got)
	}
	state := func() map[string]any {
		t.Helper()
		resp := scopedAPIRequest(t, srv, http.MethodGet, fmt.Sprintf("/api/fest/%d/games/%d/state", festID, gameID), nil, token)
		if resp.Code != http.StatusOK {
			t.Fatalf("state: %d %s", resp.Code, resp.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if inner, ok := out["state"].(map[string]any); ok {
			return inner
		}
		return out
	}
	tables := func() string {
		t.Helper()
		var names []string
		for _, team := range state()["teams"].([]any) {
			names = append(names, team.(map[string]any)["name"].(string))
		}
		return strings.Join(names, ",")
	}
	if got := tables(); got != "Стол 1,Стол 2,Стол 3" {
		t.Fatalf("tables = %s", got)
	}

	// The tables stay fixed: a PATCH may not rename them.
	if resp := scopedAPIRequest(t, srv, http.MethodPatch, fmt.Sprintf("/api/fest/%d/games/%d/state", festID, gameID),
		map[string]any{"ops": []map[string]any{{"path": []any{"teams", 0, "name"}, "value": "X"}}}, token); resp.Code == http.StatusOK {
		t.Fatal("a table was renamed through the state")
	}

	players := []map[string]any{{"card": 1, "name": "Joker One", "team": "Участник 1"}, {"card": 4, "name": "Mover"}, {"card": 5, "name": "Other"}, {"card": 2, "name": "Joker Two"}}
	patch := map[string]any{"ops": []map[string]any{
		{"path": []any{"players"}, "value": players},
		{"path": []any{"entries"}, "value": [][]int{{1, 2, 0}, {1, 0, 0}, {1, 2, 0}, {1, 0, 0}}},
		{"path": []any{"completed"}, "value": []bool{true, true, true, true}},
	}}
	if resp := scopedAPIRequest(t, srv, http.MethodPatch, fmt.Sprintf("/api/fest/%d/games/%d/state", festID, gameID), patch, token); resp.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", resp.Code, resp.Body.String())
	}
	results := func() []string {
		t.Helper()
		resp := scopedAPIRequest(t, srv, http.MethodGet, fmt.Sprintf("/api/fest/%d/games/%d/results", festID, gameID), nil, token)
		if resp.Code != http.StatusOK {
			t.Fatalf("results: %d %s", resp.Code, resp.Body.String())
		}
		var out struct {
			Players []struct {
				Place string `json:"place"`
				Name  string `json:"name"`
				Total int    `json:"total"`
			} `json:"players"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		var rows []string
		for _, p := range out.Players {
			rows = append(rows, fmt.Sprintf("%s %s %d", p.Place, p.Name, p.Total))
		}
		return rows
	}
	if got, want := strings.Join(results(), "; "), "1 Joker One 4; 2 Mover 3; 3 Joker Two 2; 4 Other 1"; got != want {
		t.Fatalf("personal standings = %s, want %s", got, want)
	}

	// A roster change reaches every flat game but this one.
	if resp := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/teams", festID),
		map[string]any{"name": "Новая", "city": "", "players": []map[string]any{{"rating_id": 0, "first_name": "Иван", "last_name": "Новый"}}}, token); resp.Code >= 400 {
		t.Fatalf("add team: %d %s", resp.Code, resp.Body.String())
	}
	if got := tables(); got != "Стол 1,Стол 2,Стол 3" {
		t.Fatalf("tables after a roster change = %s", got)
	}

	// Clearing wipes the answers and keeps the players.
	if resp := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/games/%d/clear", festID, gameID), nil, token); resp.Code != http.StatusOK {
		t.Fatalf("clear: %d %s", resp.Code, resp.Body.String())
	}
	after := state()
	if kept, _ := after["players"].([]any); len(kept) != 4 {
		t.Fatalf("players after clear = %v", after["players"])
	}
	if got := strings.Join(results(), "; "); got != " Joker One 0;  Joker Two 0;  Mover 0;  Other 0" {
		t.Fatalf("standings after clear = %s", got)
	}
	if got := fests(); strings.Contains(got, "Стол") {
		t.Fatalf("a table renamed a fest team: %s", got)
	}
}
