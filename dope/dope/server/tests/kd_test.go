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
	t.Parallel()
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

	patch := map[string]any{"ops": []map[string]any{
		{"path": []any{"players", "1"}, "value": map[string]any{"name": "Joker One", "team": "Участник 1"}},
		{"path": []any{"players", "4"}, "value": map[string]any{"name": "Mover"}},
		{"path": []any{"players", "5"}, "value": map[string]any{"name": "Other"}},
		{"path": []any{"players", "2"}, "value": map[string]any{"name": "Joker Two"}},
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
	if kept, _ := after["players"].(map[string]any); len(kept) != 4 {
		t.Fatalf("players after clear = %v", after["players"])
	}
	if got := strings.Join(results(), "; "); got != " Joker One 0;  Joker Two 0;  Mover 0;  Other 0" {
		t.Fatalf("standings after clear = %s", got)
	}
	if got := fests(); strings.Contains(got, "Стол") {
		t.Fatalf("a table renamed a fest team: %s", got)
	}
}

// kdCup is a friendship cup of two tours of two questions at three tables on
// a fest of three numbered teams: patch sends one state PATCH, get reads a
// path under the game, and exec runs SQL with the game's id.
type kdCup struct {
	patch func(ops ...map[string]any) *httptest.ResponseRecorder
	get   func(path string) *httptest.ResponseRecorder
	exec  func(query string)
}

func newKDCup(t *testing.T) kdCup {
	t.Helper()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	seedFestTeams(t, db, festID, 3)
	gameID := createGameThroughForm(t, srv, festID, token, map[string]string{"game_type": "kd", "kd_tours": "2", "kd_questions": "2", "kd_tables": "3"})
	base := fmt.Sprintf("/api/fest/%d/games/%d", festID, gameID)
	return kdCup{
		patch: func(ops ...map[string]any) *httptest.ResponseRecorder {
			t.Helper()
			return scopedAPIRequest(t, srv, http.MethodPatch, base+"/state", map[string]any{"ops": ops}, token)
		},
		get: func(path string) *httptest.ResponseRecorder {
			t.Helper()
			return scopedAPIRequest(t, srv, http.MethodGet, base+path, nil, token)
		},
		exec: func(query string) {
			t.Helper()
			if _, err := db.Exec(query, gameID); err != nil {
				t.Fatal(err)
			}
		},
	}
}

func kdSet(card string, value any) map[string]any {
	return map[string]any{"path": []any{"players", card}, "value": value}
}

// kdStandings reads the personal standings as "card name" in rank order.
func (c kdCup) standings(t *testing.T) string {
	t.Helper()
	resp := c.get("/results")
	if resp.Code != http.StatusOK {
		t.Fatalf("results: %d %s", resp.Code, resp.Body.String())
	}
	var out struct {
		Players []struct {
			Card int    `json:"card"`
			Name string `json:"name"`
		} `json:"players"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range out.Players {
		got = append(got, fmt.Sprintf("%d %s", p.Card, p.Name))
	}
	return strings.Join(got, ", ")
}

// A friendship cup's tables are nobody on the fest roster, so a fest team
// without a number must not stop the registration desk or the answers.
func TestFriendshipCupIgnoresTheFestNumbering(t *testing.T) {
	t.Parallel()
	cup := newKDCup(t)
	cup.exec(`insert into fest_teams(fest_id, name, city, position, number)
select fest_id, 'Без номера', '', 99, null from games where id = ?`)
	if resp := cup.patch(kdSet("1", map[string]any{"name": "Joker"})); resp.Code != http.StatusOK {
		t.Fatalf("register with an unnumbered fest team = %d %s", resp.Code, resp.Body.String())
	}
	if resp := cup.patch(map[string]any{"path": []any{"entries", 0}, "value": []int{1}}); resp.Code != http.StatusOK {
		t.Fatalf("answer with an unnumbered fest team = %d %s", resp.Code, resp.Body.String())
	}
}

// Two hosts registering at once each patch their own card, so neither
// overwrites the other. A card already held is refused to the second host,
// and an entry that is no player is refused with a line the host can read.
func TestFriendshipCupRegistrationsAreSeparatePatches(t *testing.T) {
	t.Parallel()
	cup := newKDCup(t)
	for _, op := range []map[string]any{kdSet("4", map[string]any{"name": "Anna"}), kdSet("5", map[string]any{"name": "Boris", "team": "Участник 2"})} {
		if resp := cup.patch(op); resp.Code != http.StatusOK {
			t.Fatalf("register: %d %s", resp.Code, resp.Body.String())
		}
	}
	if got := cup.standings(t); got != "4 Anna, 5 Boris" {
		t.Fatalf("players = %s", got)
	}

	for _, tc := range []struct {
		name string
		op   map[string]any
		want string
	}{
		{"a card somebody holds", kdSet("4", map[string]any{"name": "Clara"}), "Anna"},
		{"card zero", kdSet("0", map[string]any{"name": "Clara"}), "целым числом"},
		{"a card that is no number", kdSet("x", map[string]any{"name": "Clara"}), "целым числом"},
		{"a card with a leading zero", kdSet("07", map[string]any{"name": "Clara"}), "целым числом"},
		{"a card past n²", kdSet("10", map[string]any{"name": "Clara"}), "не больше 9"},
		{"no name", kdSet("6", map[string]any{"name": "  "}), "имя"},
		{"a list with a string card", map[string]any{"path": []any{"players"}, "value": []any{map[string]any{"card": "3", "name": "Clara"}}}, "целым числом"},
		{"a list with one card twice", map[string]any{"path": []any{"players"}, "value": []any{map[string]any{"card": 3, "name": "Clara"}, map[string]any{"card": 3, "name": "Dora"}}}, "дважды"},
	} {
		resp := cup.patch(tc.op)
		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), tc.want) {
			t.Errorf("%s: %d %s, want 400 naming %q", tc.name, resp.Code, resp.Body.String(), tc.want)
		}
	}
	if got := cup.standings(t); got != "4 Anna, 5 Boris" {
		t.Fatalf("players after the refusals = %s", got)
	}

	// Taking a player off writes null under his card, and the card is free.
	if resp := cup.patch(kdSet("4", nil)); resp.Code != http.StatusOK {
		t.Fatalf("unregister: %d %s", resp.Code, resp.Body.String())
	}
	if resp := cup.patch(kdSet("4", map[string]any{"name": "Clara"})); resp.Code != http.StatusOK {
		t.Fatalf("register on a freed card: %d %s", resp.Code, resp.Body.String())
	}
	if got := cup.standings(t); got != "4 Clara, 5 Boris" {
		t.Fatalf("players = %s", got)
	}
}

// A document stored with bad entries (the old list shape, before edits were
// checked) still answers its standings. The bad entries are left out, as the
// page leaves them out, and answers still go in.
func TestFriendshipCupStandingsSkipBadEntries(t *testing.T) {
	t.Parallel()
	cup := newKDCup(t)
	cup.exec(`update matches set state_json = json_set(state_json, '$.players', json('[{"card":"2","name":"String"},{"card":0,"name":"Zero"},{"card":3,"name":""},{"card":4,"name":"Good"},{"card":4,"name":"Twice"},{"card":1,"name":"Joker"}]')) where game_id = ?`)
	if got := cup.standings(t); got != "1 Joker, 4 Good" {
		t.Fatalf("ranked %s, want Joker and Good only", got)
	}
	if resp := cup.patch(map[string]any{"path": []any{"entries", 0}, "value": []int{1}}); resp.Code != http.StatusOK {
		t.Fatalf("an answer on a document with bad players = %d %s", resp.Code, resp.Body.String())
	}
}

// Two players share a table at most once only while the tours are no more
// than the tables.
func TestFriendshipCupRefusesMoreToursThanTables(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	token := createTestSession(t, srv, systemUserID(t, srv.Eng().DB))
	form := url.Values{"game_type": {"kd"}, "kd_tours": {"4"}, "kd_questions": {"2"}, "kd_tables": {"3"}}
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/host/fest/%d/game/new", festID), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
	resp := httptest.NewRecorder()
	srv.HostPageServer().HandleHostRouter(resp, req)
	if resp.Code == http.StatusSeeOther || !strings.Contains(resp.Body.String(), "не меньше, чем туров") {
		t.Fatalf("4 tours at 3 tables: %d, want the refusal", resp.Code)
	}
}
