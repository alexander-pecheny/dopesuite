package tests

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dope/dope/domain/core"
	"dope/dope/domain/imports"
	"dope/dope/domain/overrides"
	rosterpkg "dope/dope/domain/roster"
	"dope/dope/platform/realtime"
	"dope/dope/platform/util"
	dopeserver "dope/dope/server"
)

// gameRosterTeam is one team of a game's roster tab as the endpoint sends it.
type gameRosterTeam struct {
	ParticipantID int64  `json:"participantID"`
	Name          string `json:"name"`
	Hand          bool   `json:"hand"`
	Players       []struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		Locked bool   `json:"locked"`
	} `json:"players"`
}

func (t gameRosterTeam) names() []string {
	out := make([]string, len(t.Players))
	for i, p := range t.Players {
		out[i] = p.Name
	}
	return out
}

func gameRosterTab(t *testing.T, srv *dopeserver.Server, festID, gameID int64, token string) map[int64]gameRosterTeam {
	t.Helper()
	resp := scopedAPIRequest(t, srv, http.MethodGet, fmt.Sprintf("/api/fest/%d/games/%d/roster?choices=1", festID, gameID), nil, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("roster: %d %s", resp.Code, resp.Body.String())
	}
	var body struct {
		Game    bool             `json:"game"`
		Teams   []gameRosterTeam `json:"teams"`
		Choices []any            `json:"choices"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Game || len(body.Choices) == 0 {
		t.Fatalf("roster tab is not the game's own: %s", resp.Body.String())
	}
	out := map[int64]gameRosterTeam{}
	for _, team := range body.Teams {
		out[team.ParticipantID] = team
	}
	return out
}

// seedTeamsWithPlayers gives each of n fest teams three players of its own.
func seedTeamsWithPlayers(t *testing.T, db *sql.DB, festID int64, n int) {
	t.Helper()
	seedFestTeams(t, db, festID, n)
	for team := 1; team <= n; team++ {
		for k := 1; k <= 3; k++ {
			res, err := db.Exec(`insert into fest_players(fest_id, first_name, last_name) values(?, ?, ?)`,
				festID, fmt.Sprintf("Игрок%d", k), fmt.Sprintf("Команды%d", team))
			if err != nil {
				t.Fatal(err)
			}
			playerID, _ := res.LastInsertId()
			if _, err := db.Exec(`
insert into fest_team_players(team_id, player_id, roster_order)
select id, ?, ? from fest_teams where fest_id = ? and number = ?`, playerID, k, festID, team); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// matchRosterOf reads the roster a бой's view gives a team.
func matchRosterOf(t *testing.T, srv *dopeserver.Server, festID, gameID int64, code string, participantID int64, token string) []string {
	t.Helper()
	resp := scopedAPIRequest(t, srv, http.MethodGet, fmt.Sprintf("/api/fest/%d/games/%d/matches/%s", festID, gameID, code), nil, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("match %s: %d %s", code, resp.Code, resp.Body.String())
	}
	var view struct {
		Participants []struct {
			ID     int64 `json:"id"`
			Roster []struct {
				Name string `json:"name"`
			} `json:"roster"`
		} `json:"participants"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	for _, p := range view.Participants {
		if p.ID == participantID {
			out := make([]string, len(p.Roster))
			for i, m := range p.Roster {
				out[i] = m.Name
			}
			return out
		}
	}
	t.Fatalf("match %s does not seat %d: %s", code, participantID, resp.Body.String())
	return nil
}

// A host changes a team's roster in one ЭС game on its Составы tab: the бои
// of that game seat the new roster, while the fest roster and another game
// keep the old one; a player who has played cannot be taken off; a seed
// re-import leaves the hand roster alone; and the fest roster comes back on
// request.
func TestGameRosterKeptByHand(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	seedTeamsWithPlayers(t, db, festID, 4)

	const dsl = "[init]\nseed: random\n\n[scheme]\nkind: single_elimination\nparticipants: 4\nmatch_size: 4\nwinning_places: 1\n"
	esID := createSchemeGame(t, db, festID, "es", "ЭС", dsl)
	otherID := createSchemeGame(t, db, festID, "es", "ЭС второй", dsl)
	for _, id := range []int64{esID, otherID} {
		if resp := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/games/%d/seed-import/run", festID, id), nil, token); resp.Code != http.StatusOK {
			t.Fatalf("seed-import: %d %s", resp.Code, resp.Body.String())
		}
	}
	var bout string
	if err := db.QueryRow(`select code from matches where game_id = ? order by position, id limit 1`, esID).Scan(&bout); err != nil {
		t.Fatal(err)
	}
	seats := matchSeatIDs(t, db, esID, bout)
	if len(seats) != 4 {
		t.Fatalf("бой seats %v", seats)
	}
	team := seats[0]
	before := gameRosterTab(t, srv, festID, esID, token)[team]
	if before.Hand || len(before.Players) != 3 {
		t.Fatalf("before any edit: %+v", before)
	}
	fest := before.names()

	put := func(players ...string) *http.Response {
		t.Helper()
		resp := scopedAPIRequest(t, srv, http.MethodPut, fmt.Sprintf("/api/fest/%d/games/%d/rosters/%d", festID, esID, team),
			map[string]any{"players": players}, token)
		return resp.Result()
	}
	// The first fest player stays, a suggestion's bracket is dropped, and a
	// new person joins.
	if resp := put(fest[0], "Новый Человек (из ниоткуда)"); resp.StatusCode != http.StatusOK {
		t.Fatalf("save = %d", resp.StatusCode)
	}
	want := []string{fest[0], "Новый Человек"}
	after := gameRosterTab(t, srv, festID, esID, token)[team]
	if !after.Hand || !slices.Equal(after.names(), want) {
		t.Fatalf("after save: %+v, want %v", after, want)
	}
	if got := matchRosterOf(t, srv, festID, esID, bout, team, token); !slices.Equal(got, want) {
		t.Fatalf("бой roster = %v, want %v", got, want)
	}
	// Nothing outside this game changes.
	if other := gameRosterTab(t, srv, festID, otherID, token)[team]; other.Hand || !slices.Equal(other.names(), fest) {
		t.Fatalf("other game's roster = %+v, want the fest's %v", other, fest)
	}
	var festPlayers int
	if err := db.QueryRow(`select count(*) from fest_players where fest_id = ?`, festID).Scan(&festPlayers); err != nil {
		t.Fatal(err)
	}
	if festPlayers != 12 {
		t.Fatalf("fest players = %d, want 12", festPlayers)
	}

	// The refusals: nobody at all, and one person twice.
	for _, players := range [][]string{{}, {"Новый Человек", "новый человек"}} {
		if resp := put(players...); resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("save %v = %d, want 400", players, resp.StatusCode)
		}
	}

	// The new person plays a theme; then they cannot be taken off, by an edit
	// or by giving the team back its fest roster.
	newID := after.Players[1].ID
	patchState(t, srv, festID, esID, bout, token, []map[string]any{
		{"path": []any{"participants", fmt.Sprint(team), "themes", 0, "players"}, "value": []int64{newID}},
	})
	if locked := gameRosterTab(t, srv, festID, esID, token)[team].Players[1]; !locked.Locked {
		t.Fatalf("a player who played is not locked: %+v", locked)
	}
	resp := scopedAPIRequest(t, srv, http.MethodPut, fmt.Sprintf("/api/fest/%d/games/%d/rosters/%d", festID, esID, team),
		map[string]any{"players": []string{fest[0]}}, token)
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "Новый Человек") {
		t.Fatalf("dropping a player who played = %d %s, want 400 naming them", resp.Code, resp.Body.String())
	}
	resp = scopedAPIRequest(t, srv, http.MethodDelete, fmt.Sprintf("/api/fest/%d/games/%d/rosters/%d", festID, esID, team), nil, token)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("reset dropping a player who played = %d %s, want 400", resp.Code, resp.Body.String())
	}
	// Adding somebody next to them is fine.
	if resp := put(fest[0], "Новый Человек", fest[2]); resp.StatusCode != http.StatusOK {
		t.Fatalf("save with an addition = %d", resp.StatusCode)
	}
	want = []string{fest[0], "Новый Человек", fest[2]}

	// A seed re-import copies the fest roster again, and leaves this alone.
	if resp := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/games/%d/seed-import/run", festID, esID), nil, token); resp.Code != http.StatusOK {
		t.Fatalf("seed re-import: %d %s", resp.Code, resp.Body.String())
	}
	if got := gameRosterTab(t, srv, festID, esID, token)[team]; !got.Hand || !slices.Equal(got.names(), want) {
		t.Fatalf("after seed re-import: %+v, want %v", got, want)
	}

	// A team outside the game, and a format whose rosters are not edited here.
	outsider := scopedAPIRequest(t, srv, http.MethodPut, fmt.Sprintf("/api/fest/%d/games/%d/rosters/%d", festID, esID, 999999),
		map[string]any{"players": []string{"Кто-то"}}, token)
	if outsider.Code != http.StatusBadRequest {
		t.Fatalf("a team outside the game = %d, want 400", outsider.Code)
	}

	// With the theme cleared the new person may go, and the fest roster comes
	// back.
	patchState(t, srv, festID, esID, bout, token, []map[string]any{
		{"path": []any{"participants", fmt.Sprint(team), "themes", 0, "players"}, "value": []int64{}},
	})
	if resp := scopedAPIRequest(t, srv, http.MethodDelete, fmt.Sprintf("/api/fest/%d/games/%d/rosters/%d", festID, esID, team), nil, token); resp.Code != http.StatusOK {
		t.Fatalf("reset = %d %s", resp.Code, resp.Body.String())
	}
	if got := gameRosterTab(t, srv, festID, esID, token)[team]; got.Hand || !slices.Equal(got.names(), fest) {
		t.Fatalf("after reset: %+v, want the fest's %v", got, fest)
	}
}

// A hand roster and a player override on the same team: the hand roster wins
// in that game, the overrides page says the override does nothing there, a
// rating re-import leaves the hand roster alone, and giving the team back its
// roster lets the override apply again.
func TestGameRosterBeatsOverrides(t *testing.T) {
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	festID, _, _ := createRosterPropagationFixture(t, db)
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
	})
	rating := []rosterpkg.FestRosterImportTeam{
		{RatingID: 101, Name: "Альфа", City: "Москва", Players: []rosterpkg.FestRosterImportPlayer{
			{RatingID: 1001, FirstName: "Мария", LastName: "Сидорова"},
		}},
		{RatingID: 102, Name: "Бета", City: "Москва", Players: []rosterpkg.FestRosterImportPlayer{
			{RatingID: 1002, FirstName: "Олег", LastName: "Петров"},
		}},
	}
	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 13533, rating, imports.RosterChoice{}); err != nil {
		t.Fatalf("import roster: %v", err)
	}
	now := util.UtcNow()
	res, err := db.Exec(`
insert into games(fest_id, code, title, game_type, position, scheme_json, state_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(?, 'ek', 'ЭК', 'ek', 3, '{}', '{}', 'pending', 'fest', 'fest', 1, ?, ?)`, festID, now, now)
	if err != nil {
		t.Fatal(err)
	}
	gameID, _ := res.LastInsertId()
	// The game names both teams as its entrants.
	var beta int64
	for i, name := range []string{"Альфа", "Бета"} {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		id, _, err := rosterpkg.EnsureSeedTeam(t.Context(), tx, festID, name, "Москва", nil)
		if err == nil {
			_, err = tx.Exec(`insert into game_participants(game_id, participant_id, position) values(?, ?, ?)`, gameID, id, i)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if name == "Бета" {
			beta = id
		}
	}
	rosterOf := func() rosterpkg.GameRosterTeam {
		t.Helper()
		teams, err := rosterpkg.LoadGameRosters(t.Context(), db, festID, gameID)
		if err != nil {
			t.Fatal(err)
		}
		for _, team := range teams {
			if team.ParticipantID == beta {
				return team
			}
		}
		t.Fatalf("no Бета among %+v", teams)
		return rosterpkg.GameRosterTeam{}
	}
	names := func(team rosterpkg.GameRosterTeam) []string {
		out := []string{}
		for _, p := range team.Players {
			out = append(out, p.Name)
		}
		return out
	}

	write := func(fn func(tx *sql.Tx) error) {
		t.Helper()
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if err := fn(tx); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	write(func(tx *sql.Tx) error {
		return rosterpkg.SaveGameRosterTx(t.Context(), tx, festID, gameID, beta, []string{"Олег Петров", "Анна Новая"})
	})

	// Сидорова moves to Бета in this game by an override: Бета's hand roster
	// does not take her.
	var sidorova, betaTeam int64
	if err := db.QueryRow(`select id from fest_players where fest_id = ? and rating_id = 1001`, festID).Scan(&sidorova); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`select id from fest_teams where fest_id = ? and rating_id = 102`, festID).Scan(&betaTeam); err != nil {
		t.Fatal(err)
	}
	if _, _, err := overrides.SavePlayerTeamOverride(srv.Eng(), t.Context(), festID, sidorova, betaTeam, []int64{gameID}); err != nil {
		t.Fatalf("save override: %v", err)
	}
	hand := []string{"Олег Петров", "Анна Новая"}
	if got := rosterOf(); !got.Hand || !slices.Equal(names(got), hand) {
		t.Fatalf("after the override: %+v, want the hand roster %v", got, hand)
	}
	rows, err := overrides.LoadHostPlayerOverrideRows(t.Context(), db, festID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Shadowed == "" {
		t.Fatalf("override rows = %+v, want one marked as not acting", rows)
	}

	// A rating re-import rebuilds the overrides' rosters and leaves this one.
	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 13533, rating, imports.RosterChoice{}); err != nil {
		t.Fatalf("re-import roster: %v", err)
	}
	if got := rosterOf(); !got.Hand || !slices.Equal(names(got), hand) {
		t.Fatalf("after the rating re-import: %+v, want %v", got, hand)
	}

	// Given its roster back, Бета plays with the override applied.
	write(func(tx *sql.Tx) error {
		return rosterpkg.ResetGameRosterTx(t.Context(), tx, festID, gameID, beta, func(ctx context.Context, tx *sql.Tx) error {
			return overrides.MaterializeGameRosterOverridesTx(ctx, tx, festID, gameID)
		})
	})
	got := rosterOf()
	if got.Hand || !slices.Contains(names(got), "Мария Сидорова") || slices.Contains(names(got), "Анна Новая") {
		t.Fatalf("after reset: %+v, want the fest roster with Сидорова", got)
	}
}

// A player who has something entered is locked on the roster in every team
// buzzer format: брейн records the name that buzzed, Хамса the id that sat a
// theme.
func TestGameRosterLocksPlayedPlayers(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	seedTeamsWithPlayers(t, db, festID, 2)
	now := util.UtcNow()
	for _, tc := range []struct {
		gameType string
		state    func(team, player int64) string
	}{
		{"brain", func(_, _ int64) string {
			return `{"teams":[{"rows":[{"player":"Игрок2 Команды1","mark":"right"}]},{"rows":[{"player":"","mark":""}]}]}`
		}},
		{"hamsa", func(team, player int64) string {
			return fmt.Sprintf(`{"participants":{"%d":{"themes":[{"player":%d,"answers":["right"]}]}}}`, team, player)
		}},
	} {
		t.Run(tc.gameType, func(t *testing.T) {
			res, err := db.Exec(`
insert into games(fest_id, code, title, game_type, position, scheme_json, state_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(?, ?, ?, ?, 5, '{}', '{}', 'active', 'fest', 'fest', 1, ?, ?)`, festID, tc.gameType, tc.gameType, tc.gameType, now, now)
			if err != nil {
				t.Fatal(err)
			}
			gameID, _ := res.LastInsertId()
			res, err = db.Exec(`insert into stages(fest_id, game_id, code, title, stage_type, position) values(?, ?, 's1', 's1', 'group', 0)`, festID, gameID)
			if err != nil {
				t.Fatal(err)
			}
			stageID, _ := res.LastInsertId()
			var teams [2]int64
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			for i := range teams {
				n := int64(i + 1)
				var name string
				if err := tx.QueryRow(`select name from fest_teams where fest_id = ? and number = ?`, festID, n).Scan(&name); err != nil {
					t.Fatal(err)
				}
				players := []rosterpkg.SeedRosterPlayer{}
				for k := 1; k <= 3; k++ {
					players = append(players, rosterpkg.SeedRosterPlayer{FirstName: fmt.Sprintf("Игрок%d", k), LastName: fmt.Sprintf("Команды%d", n)})
				}
				if teams[i], _, err = imports.EnsureSeedTeamByNumber(t.Context(), tx, festID, n, name, "", players); err != nil {
					t.Fatal(err)
				}
			}
			var played int64
			if err := tx.QueryRow(`select id from players where fest_id = ? and first_name = 'Игрок2' and last_name = 'Команды1'`, festID).Scan(&played); err != nil {
				t.Fatal(err)
			}
			res, err = tx.Exec(`
insert into matches(fest_id, game_id, stage_id, code, title, position, participant_count, state_json) values(?, ?, ?, 'm1', 'm1', 0, 2, ?)`,
				festID, gameID, stageID, tc.state(teams[0], played))
			if err != nil {
				t.Fatal(err)
			}
			matchID, _ := res.LastInsertId()
			for i, team := range teams {
				if _, err := tx.Exec(`insert into match_slots(match_id, slot_index, source_type, participant_id) values(?, ?, 'seed', ?)`, matchID, i, team); err != nil {
					t.Fatal(err)
				}
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}

			rosters, err := rosterpkg.LoadGameRosters(t.Context(), db, festID, gameID)
			if err != nil {
				t.Fatal(err)
			}
			if len(rosters) != 2 || rosters[0].ParticipantID != teams[0] {
				t.Fatalf("rosters = %+v", rosters)
			}
			for _, p := range rosters[0].Players {
				if p.Locked != (p.Name == "Игрок2 Команды1") {
					t.Fatalf("%s locked = %v", p.Name, p.Locked)
				}
			}
			for _, p := range rosters[1].Players {
				if p.Locked {
					t.Fatalf("the other team's %s is locked", p.Name)
				}
			}
			tx, err = db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			err = rosterpkg.SaveGameRosterTx(t.Context(), tx, festID, gameID, teams[0], []string{"Игрок1 Команды1"})
			if err == nil || !strings.Contains(err.Error(), "Игрок2 Команды1") {
				t.Fatalf("dropping the player who played: %v", err)
			}
		})
	}
}
