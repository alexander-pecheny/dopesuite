package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDefaultMatchScores(t *testing.T) {
	state := defaultMatch()
	view := buildView(state)

	wantTotals := map[string]int{
		"ВШЭстером":       120,
		"Тина Терияки":    130,
		"Вина России":     0,
		"Злая щитоспинка": 130,
	}
	wantPlaces := map[string]float64{
		"Злая щитоспинка": 1,
		"Тина Терияки":    2,
		"ВШЭстером":       3,
		"Вина России":     4,
	}

	for _, team := range view.Teams {
		if team.Total != wantTotals[team.Name] {
			t.Fatalf("%s total = %d, want %d", team.Name, team.Total, wantTotals[team.Name])
		}
		if team.Place != wantPlaces[team.Name] {
			t.Fatalf("%s place = %v, want %v", team.Name, team.Place, wantPlaces[team.Name])
		}
		if len(team.ShootoutThemes) != 0 {
			t.Fatalf("%s shootout themes = %d, want 0", team.Name, len(team.ShootoutThemes))
		}
		if team.Tiebreak != 0 || team.ShootoutTotal != 0 {
			t.Fatalf("%s shootout total = %d/%d, want 0", team.Name, team.Tiebreak, team.ShootoutTotal)
		}
	}
}

func TestShootoutScoresDoNotAffectBattleStats(t *testing.T) {
	state := MatchState{
		Teams: []TeamState{
			{
				Name: "A",
				Themes: []ThemeEntry{
					{Answers: [5]string{"right", "", "", "", ""}},
				},
				ShootoutThemes: []ThemeEntry{
					{Answers: [5]string{"wrong", "", "", "", "right"}},
				},
			},
		},
	}

	team := buildView(state).Teams[0]
	if team.Total != 10 {
		t.Fatalf("total = %d, want 10", team.Total)
	}
	if team.Plus != 10 {
		t.Fatalf("plus = %d, want 10", team.Plus)
	}
	if team.CorrectCounts[0] != 1 || team.CorrectCounts[4] != 0 {
		t.Fatalf("correct counts = %v, want only the battle 10 counted", team.CorrectCounts)
	}
	if team.ShootoutTotal != 40 || team.Tiebreak != 40 {
		t.Fatalf("shootout total = %d/%d, want 40", team.ShootoutTotal, team.Tiebreak)
	}
	if team.ShootoutThemes[0].Score != 40 {
		t.Fatalf("shootout theme score = %d, want 40", team.ShootoutThemes[0].Score)
	}
}

func TestShootoutThemeActions(t *testing.T) {
	t.Chdir(t.TempDir())
	srv := &server{
		state:       defaultMatch(),
		subscribers: make(map[chan event]struct{}),
	}

	if _, _, err := srv.applyUpdate(updateRequest{Action: actionAddShootoutTheme}); err != nil {
		t.Fatalf("add shootout theme: %v", err)
	}
	for _, team := range srv.state.Teams {
		if len(team.ShootoutThemes) != 1 {
			t.Fatalf("%s shootout themes = %d, want 1", team.Name, len(team.ShootoutThemes))
		}
	}

	theme := 0
	answer := 4
	shootout := true
	mark := "right"
	view, _, err := srv.applyUpdate(updateRequest{
		Team:     0,
		Theme:    &theme,
		Shootout: &shootout,
		Answer:   &answer,
		Mark:     &mark,
	})
	if err != nil {
		t.Fatalf("mark shootout answer: %v", err)
	}
	if view.Teams[0].ShootoutTotal != 50 {
		t.Fatalf("shootout total = %d, want 50", view.Teams[0].ShootoutTotal)
	}

	if _, _, err := srv.applyUpdate(updateRequest{Action: actionRemoveShootoutTheme}); err != nil {
		t.Fatalf("remove shootout theme: %v", err)
	}
	if len(srv.state.Teams[0].ShootoutThemes) != 0 {
		t.Fatalf("shootout themes after remove = %d, want 0", len(srv.state.Teams[0].ShootoutThemes))
	}
}

func TestManualStandingsAllowsSplitPlace(t *testing.T) {
	state := defaultMatch()
	state.Teams[0].Place = 3.5
	state.Teams[1].Place = 2
	state.Teams[2].Place = 3.5
	state.Teams[3].Place = 1

	standings := buildView(state).Standings
	want := []float64{1, 2, 3.5, 3.5}
	for i, place := range want {
		if standings[i].Place != place {
			t.Fatalf("standings[%d].Place = %v, want %v", i, standings[i].Place, place)
		}
	}
}

func TestFinishedMatchRejectsEditsButCanBeReopened(t *testing.T) {
	t.Chdir(t.TempDir())
	srv := &server{
		state:       defaultMatch(),
		subscribers: make(map[chan event]struct{}),
	}

	finished := true
	if _, _, err := srv.applyUpdate(updateRequest{Finished: &finished}); err != nil {
		t.Fatalf("finish match: %v", err)
	}

	place := 2.5
	if _, _, err := srv.applyUpdate(updateRequest{Team: 0, Place: &place}); err == nil {
		t.Fatal("place update while finished succeeded, want error")
	}

	finished = false
	if _, _, err := srv.applyUpdate(updateRequest{Finished: &finished}); err != nil {
		t.Fatalf("reopen match: %v", err)
	}
	if _, _, err := srv.applyUpdate(updateRequest{Team: 0, Place: &place}); err != nil {
		t.Fatalf("place update after reopen: %v", err)
	}
}

func TestNormalizeMark(t *testing.T) {
	cases := map[string]string{
		"q":     "right",
		"Й":     "right",
		"1":     "right",
		"w":     "wrong",
		"Ц":     "wrong",
		"-1":    "wrong",
		"−":     "wrong",
		"−1":    "wrong",
		"empty": "",
	}

	for input, want := range cases {
		got := normalizeMark(input)
		if got != want {
			t.Fatalf("normalizeMark(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSQLiteBootstrapAndMatchUpdate(t *testing.T) {
	t.Chdir(t.TempDir())
	db, err := openFestDB("test.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID := createDefaultFestFixture(t, db, defaultMatch())
	gameID, err := defaultGameID(t.Context(), db, festID)
	if err != nil {
		t.Fatalf("default game: %v", err)
	}

	srv := &server{
		db:              db,
		festID:          festID,
		activeGameID:    gameID,
		activeMatchCode: defaultMatchCode,
		subscribers:     make(map[chan event]struct{}),
	}

	view, err := srv.loadMatchViewLocked(festID, defaultMatchCode)
	if err != nil {
		t.Fatalf("load match: %v", err)
	}
	if view.Code != defaultMatchCode {
		t.Fatalf("code = %q, want %q", view.Code, defaultMatchCode)
	}
	if view.Venue == nil || view.Venue.Number != 1 {
		t.Fatalf("venue = %#v, want number 1", view.Venue)
	}

	theme := 0
	answer := 0
	mark := "right"
	view, _, err = srv.applyMatchUpdate(festID, defaultMatchCode, updateRequest{
		Team:   2,
		Theme:  &theme,
		Answer: &answer,
		Mark:   &mark,
	})
	if err != nil {
		t.Fatalf("update answer: %v", err)
	}
	if view.Teams[2].Total != 10 {
		t.Fatalf("updated total = %d, want 10", view.Teams[2].Total)
	}

	reloaded, err := srv.loadMatchViewLocked(festID, defaultMatchCode)
	if err != nil {
		t.Fatalf("reload match: %v", err)
	}
	if reloaded.Teams[2].Themes[0].Answers[0] != "right" {
		t.Fatalf("persisted mark = %q, want right", reloaded.Teams[2].Themes[0].Answers[0])
	}
}

func TestSQLiteVenuesAndRosterLimit(t *testing.T) {
	t.Chdir(t.TempDir())
	db, err := openFestDB("test.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID := createDefaultFestFixture(t, db, defaultMatch())
	gameID, err := defaultGameID(t.Context(), db, festID)
	if err != nil {
		t.Fatalf("default game: %v", err)
	}

	srv := &server{
		db:              db,
		festID:          festID,
		activeGameID:    gameID,
		activeMatchCode: defaultMatchCode,
		subscribers:     make(map[chan event]struct{}),
	}

	venues, _, err := srv.updateVenue(festID, 1, "Рим")
	if err != nil {
		t.Fatalf("update venue: %v", err)
	}
	if len(venues) != 1 || venues[0].Title != "Рим" {
		t.Fatalf("venues = %#v, want renamed venue", venues)
	}
	view, err := srv.loadMatchViewLocked(festID, defaultMatchCode)
	if err != nil {
		t.Fatalf("load match: %v", err)
	}
	if view.Venue == nil || view.Venue.Title != "Рим" {
		t.Fatalf("match venue = %#v, want Рим", view.Venue)
	}

	var teamID int64
	if err := db.QueryRow(`select id from teams where fest_id = ? order by id limit 1`, festID).Scan(&teamID); err != nil {
		t.Fatalf("team id: %v", err)
	}
	for i := 0; i < 3; i++ {
		playerID, err := insertTestPlayer(db, festID)
		if err != nil {
			t.Fatalf("insert player %d: %v", i, err)
		}
		_, err = db.Exec(`insert into team_players(team_id, player_id, roster_order) values(?, ?, ?)`, teamID, playerID, 90+i)
		if i < 2 && err != nil {
			t.Fatalf("insert extra roster player %d: %v", i, err)
		}
		if i == 2 && err == nil {
			t.Fatal("insert 10th roster player succeeded, want trigger error")
		}
	}
}

func insertTestPlayer(db *sql.DB, festID int64) (int64, error) {
	result, err := db.Exec(`insert into players(fest_id, first_name, last_name) values(?, 'Тест', 'Игрок')`, festID)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func TestImportMultiStageScheme(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	scheme := festScheme{
		SchemaVersion:     2,
		Slug:              "multi-stage",
		Title:             "multi-stage",
		GameType:          "ek",
		RegularThemeCount: themeCount,
		Venues:            []schemeVenue{{Number: 1, Title: "Main"}},
		Teams: []schemeTeam{
			{Name: "Alpha", Basket: 1, Number: 1},
			{Name: "Beta", Basket: 1, Number: 2},
			{Name: "Gamma", Basket: 1, Number: 3},
			{Name: "Delta", Basket: 1, Number: 4},
		},
		Stages: []schemeStage{
			{
				Code:      "r1",
				Title:     "Round 1",
				StageType: "matches",
				Position:  1,
				Matches: []schemeMatch{
					{
						Code:             "A",
						Title:            "A",
						Venue:            1,
						ParticipantCount: 2,
						Slots: []schemeSlot{
							{Seed: &schemeSeedRef{Basket: 1, Number: 1}},
							{Seed: &schemeSeedRef{Basket: 1, Number: 2}},
						},
					},
					{
						Code:             "B",
						Title:            "B",
						Venue:            1,
						ParticipantCount: 2,
						Slots: []schemeSlot{
							{Seed: &schemeSeedRef{Basket: 1, Number: 3}},
							{Seed: &schemeSeedRef{Basket: 1, Number: 4}},
						},
					},
				},
			},
			{
				Code:      "final",
				Title:     "Final",
				StageType: "matches",
				Position:  2,
				Matches: []schemeMatch{{
					Code:             "C",
					Title:            "C",
					Venue:            1,
					ParticipantCount: 2,
					Slots: []schemeSlot{
						{FromMatch: &schemeFromMatchRef{Match: "A", Place: 1}},
						{FromMatch: &schemeFromMatchRef{Match: "B", Place: 1}},
					},
				}},
			},
		},
	}

	srv := &server{
		db:              db,
		activeMatchCode: defaultMatchCode,
		subscribers:     make(map[chan event]struct{}),
	}
	view, err := srv.importScheme(scheme)
	if err != nil {
		t.Fatalf("import scheme: %v", err)
	}
	if view.Slug != "multi-stage" {
		t.Fatalf("slug = %q, want multi-stage", view.Slug)
	}
	if len(view.Stages) != 2 {
		t.Fatalf("stages = %d, want 2", len(view.Stages))
	}
	if len(view.Stages[0].Matches) != 2 || len(view.Stages[1].Matches) != 1 {
		t.Fatalf("matches = %d/%d, want 2/1", len(view.Stages[0].Matches), len(view.Stages[1].Matches))
	}
	if view.Stages[0].Matches[0].Teams[0].Name != "Alpha" {
		t.Fatalf("first team = %q, want Alpha", view.Stages[0].Matches[0].Teams[0].Name)
	}
	final := view.Stages[1].Matches[0]
	if final.Code != "C" || final.Teams[0].SourceType != "from_match" || final.Teams[1].SourceType != "from_match" {
		t.Fatalf("final = %#v, want match C with fromMatch slots", final)
	}
}

func TestEmptyDatabaseHasNoFest(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	festID, gameID, matchCode, err := loadActiveContext(db)
	if err != nil {
		t.Fatalf("loadActiveContext: %v", err)
	}
	if festID != 0 || gameID != 0 || matchCode != "" {
		t.Fatalf("empty db produced (%d, %d, %q), want zero values", festID, gameID, matchCode)
	}
	srv := &server{db: db, subscribers: make(map[chan event]struct{})}
	view, err := srv.loadFestViewLocked(0, 0)
	if err != nil {
		t.Fatalf("loadFestViewLocked: %v", err)
	}
	if view.Slug != "" || len(view.Stages) != 0 {
		t.Fatalf("empty view = %#v, want zero", view)
	}
}

func TestLegacyFestSchemaMigration(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`
create table schemes(
  id integer primary key,
  slug text not null unique,
  title text not null,
  version integer not null,
  schema_json text not null,
  created_at text not null
);
create table tournaments(
  id integer primary key,
  slug text not null unique,
  title text not null,
  description text not null default '',
  rating_id integer,
  created_by integer,
  revision integer not null default 1,
  created_at text not null,
  updated_at text not null
);
create table games(
  id integer primary key,
  tournament_id integer not null references tournaments(id) on delete cascade,
  code text not null,
  title text not null,
  game_type text not null,
  position integer not null,
  scheme_id integer references schemes(id),
  scheme_json text not null default '{}',
  state_json text not null default '{}',
  status text not null default 'pending',
  team_list_source text not null default 'tournament' check (team_list_source in ('tournament','game')),
  roster_source text not null default 'tournament' check (roster_source in ('tournament','game')),
  revision integer not null default 1,
  created_at text not null,
  updated_at text not null,
  unique(tournament_id, code)
);
insert into tournaments(id, slug, title, created_at, updated_at) values(7, 'legacy', 'Legacy', 'now', 'now');
insert into games(id, tournament_id, code, title, game_type, position, scheme_json, state_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(11, 7, 'main', 'Main', 'ek', 1, '{}', '{"ok":true}', 'active', 'tournament', 'tournament', 3, 'now', 'now');
`); err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}
	if err := migrateDB(db); err != nil {
		t.Fatalf("migrate legacy schema: %v", err)
	}
	legacyExists, err := sqliteTableExists(t.Context(), db, "tournaments")
	if err != nil {
		t.Fatalf("check legacy table: %v", err)
	}
	if legacyExists {
		t.Fatal("legacy tournaments table still exists")
	}
	var teamListSource, rosterSource, stateJSON string
	if err := db.QueryRow(`select team_list_source, roster_source, state_json from games where fest_id = 7 and id = 11`).Scan(&teamListSource, &rosterSource, &stateJSON); err != nil {
		t.Fatalf("query migrated game: %v", err)
	}
	if teamListSource != "fest" || rosterSource != "fest" || stateJSON != `{"ok":true}` {
		t.Fatalf("migrated game = (%q, %q, %q), want fest/fest with preserved state", teamListSource, rosterSource, stateJSON)
	}
	if _, err := db.Exec(`
insert into games(fest_id, code, title, game_type, position, scheme_json, state_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(7, 'next', 'Next', 'ek', 2, '{}', '{}', 'pending', 'fest', 'fest', 1, 'now', 'now')`); err != nil {
		t.Fatalf("insert with fest source after migration: %v", err)
	}
}

func TestImportRejectsTeamSlot(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	srv := &server{db: db, subscribers: make(map[chan event]struct{})}
	scheme := festScheme{
		SchemaVersion: 2,
		Slug:          "with-team-slot",
		Title:         "with team slot",
		Stages: []schemeStage{{
			Code:      "stage1",
			Title:     "stage 1",
			StageType: "matches",
			Position:  1,
			Matches: []schemeMatch{{
				Code:             "A",
				Title:            "A",
				ParticipantCount: 1,
				Slots: []schemeSlot{{
					Team: &schemeTeamRef{Name: "Inline"},
				}},
			}},
		}},
	}
	if _, err := srv.importScheme(scheme); err == nil {
		t.Fatal("expected error for slot.team, got nil")
	} else if !strings.Contains(err.Error(), "removed source") {
		t.Fatalf("error = %v, want mention of removed source", err)
	}
}

func TestImportSeedSlotsResolveViaAssignments(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	srv := &server{db: db, subscribers: make(map[chan event]struct{})}
	scheme := festScheme{
		SchemaVersion: 2,
		Slug:          "symbolic",
		Title:         "symbolic",
		GameType:      "ek",
		Stages: []schemeStage{{
			Code:      "r1",
			Title:     "r1",
			StageType: "matches",
			Position:  1,
			Matches: []schemeMatch{{
				Code:             "A",
				Title:            "A",
				ParticipantCount: 2,
				Slots: []schemeSlot{
					{Seed: &schemeSeedRef{Basket: 1, Number: 1}},
					{Seed: &schemeSeedRef{Basket: 1, Number: 2}},
				},
			}},
		}},
		Teams: []schemeTeam{
			{Name: "Alpha", Basket: 1, Number: 1},
			{Name: "Beta", Basket: 1, Number: 2},
		},
	}
	view, err := srv.importScheme(scheme)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	match := view.Stages[0].Matches[0]
	if match.Teams[0].Name != "Alpha" || match.Teams[1].Name != "Beta" {
		t.Fatalf("slot teams = %q/%q, want Alpha/Beta", match.Teams[0].Name, match.Teams[1].Name)
	}
	for _, team := range match.Teams {
		if team.SourceType != "seed" {
			t.Fatalf("source type = %q, want seed", team.SourceType)
		}
	}

	var assignments int
	if err := db.QueryRow(`select count(*) from game_assignments`).Scan(&assignments); err != nil {
		t.Fatalf("count assignments: %v", err)
	}
	if assignments != 2 {
		t.Fatalf("game_assignments rows = %d, want 2", assignments)
	}

	var sourceTeamRows int
	if err := db.QueryRow(`select count(*) from match_slots where source_type = 'team'`).Scan(&sourceTeamRows); err != nil {
		t.Fatalf("count team-source slots: %v", err)
	}
	if sourceTeamRows != 0 {
		t.Fatalf("legacy team-source slots = %d, want 0", sourceTeamRows)
	}
}

func TestSystemUserIsCreatedOnImport(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	srv := &server{db: db, subscribers: make(map[chan event]struct{})}
	scheme := festScheme{
		SchemaVersion: 2,
		Slug:          "minimal",
		Title:         "minimal",
		Stages: []schemeStage{{
			Code:      "r1",
			Title:     "r1",
			StageType: "matches",
			Position:  1,
			Matches: []schemeMatch{{
				Code: "A", Title: "A", ParticipantCount: 0,
				Slots: []schemeSlot{{Placeholder: "TBD"}},
			}},
		}},
	}
	if _, err := srv.importScheme(scheme); err != nil {
		t.Fatalf("import: %v", err)
	}
	var systemUsers int
	if err := db.QueryRow(`select count(*) from users where is_system = 1`).Scan(&systemUsers); err != nil {
		t.Fatalf("count system users: %v", err)
	}
	if systemUsers != 1 {
		t.Fatalf("system users = %d, want 1", systemUsers)
	}
	var organizers int
	if err := db.QueryRow(`select count(*) from fest_organizers`).Scan(&organizers); err != nil {
		t.Fatalf("count organizers: %v", err)
	}
	if organizers != 1 {
		t.Fatalf("fest_organizers = %d, want 1", organizers)
	}
	var games int
	if err := db.QueryRow(`select count(*) from games`).Scan(&games); err != nil {
		t.Fatalf("count games: %v", err)
	}
	if games != 1 {
		t.Fatalf("games = %d, want 1", games)
	}
}

func TestRatingResultsToFestRoster(t *testing.T) {
	raw := `[
		{
			"team":{"id":20,"name":"Beta","town":{"name":"Town B"}},
			"current":{"name":"Beta Current"},
			"position":18.5,
			"teamMembers":[{"player":{"id":200,"name":"Иван","patronymic":"Иванович","surname":"Петров"}}]
		},
		{
			"team":{"id":10,"town":{"name":"Town A"}},
			"current":{"name":"Alpha"},
			"teamMembers":[{"player":{"id":100,"name":"Анна","surname":"Сидорова"}}]
		}
	]`
	var results []ratingFestResult
	if err := json.Unmarshal([]byte(raw), &results); err != nil {
		t.Fatalf("decode rating json: %v", err)
	}

	teams, err := ratingResultsToFestRoster(results)
	if err != nil {
		t.Fatalf("normalize rating results: %v", err)
	}
	if len(teams) != 2 {
		t.Fatalf("teams = %d, want 2", len(teams))
	}
	if teams[0].Name != "Alpha" || teams[0].City != "Town A" {
		t.Fatalf("first team = %#v, want Alpha/Town A", teams[0])
	}
	if teams[1].Name != "Beta Current" {
		t.Fatalf("second team name = %q, want Beta Current", teams[1].Name)
	}
	if got := joinPlayerName(teams[1].Players[0].FirstName, teams[1].Players[0].LastName); got != "Иван Петров" {
		t.Fatalf("player name = %q, want name and surname only", got)
	}
}

func TestImportFestRosterPropagatesToChGKAndKSI(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID, chgkGameID, ksiGameID := createRosterPropagationFixture(t, db)

	srv := &server{db: db, subscribers: make(map[chan event]struct{})}
	result, err := srv.importFestRoster(t.Context(), festID, 13533, []festRosterImportTeam{
		{
			RatingID: 101,
			Name:     "Первая",
			City:     "Москва",
			Players: []festRosterImportPlayer{
				{RatingID: 1002, FirstName: "Борис", LastName: "Второй"},
				{RatingID: 1001, FirstName: "Анна", LastName: "Первая"},
			},
		},
		{
			RatingID: 102,
			Name:     "Вторая",
			City:     "Казань",
			Players: []festRosterImportPlayer{
				{RatingID: 1003, FirstName: "Вера", LastName: "Третья"},
			},
		},
	})
	if err != nil {
		t.Fatalf("import roster: %v", err)
	}
	if result.TeamCount != 2 || result.PlayerCount != 3 || result.ODGameCount != 1 || result.KSIGameCount != 1 {
		t.Fatalf("result = %#v, want 2 teams / 3 players / 1 od game / 1 ksi game", result)
	}

	var teamsCount, playersCount, ekTeamsCount int
	if err := db.QueryRow(`select count(*) from fest_teams where fest_id = ?`, festID).Scan(&teamsCount); err != nil {
		t.Fatalf("count fest teams: %v", err)
	}
	if err := db.QueryRow(`select count(*) from fest_players where fest_id = ?`, festID).Scan(&playersCount); err != nil {
		t.Fatalf("count fest players: %v", err)
	}
	if err := db.QueryRow(`select count(*) from teams where fest_id = ?`, festID).Scan(&ekTeamsCount); err != nil {
		t.Fatalf("count existing game teams: %v", err)
	}
	if teamsCount != 2 || playersCount != 3 {
		t.Fatalf("roster counts = %d/%d, want 2/3", teamsCount, playersCount)
	}
	if ekTeamsCount != 4 {
		t.Fatalf("game teams count = %d, want existing EK teams preserved", ekTeamsCount)
	}
	var firstTeam, firstPlayer string
	if err := db.QueryRow(`
select tt.name, p.first_name || ' ' || p.last_name
from fest_team_players ttp
join fest_teams tt on tt.id = ttp.team_id
join fest_players p on p.id = ttp.player_id
where tt.fest_id = ?
order by tt.position, ttp.roster_order
limit 1`, festID).Scan(&firstTeam, &firstPlayer); err != nil {
		t.Fatalf("load first imported roster row: %v", err)
	}
	if firstTeam != "Вторая" || firstPlayer != "Вера Третья" {
		t.Fatalf("first imported roster row = %q / %q, want alphabetically first team/player", firstTeam, firstPlayer)
	}

	var schemeJSON, stateJSON string
	if err := db.QueryRow(`select scheme_json, state_json from games where id = ?`, chgkGameID).Scan(&schemeJSON, &stateJSON); err != nil {
		t.Fatalf("load chgk json: %v", err)
	}
	var scheme struct {
		NTeams int            `json:"nTeams"`
		Teams  []chgkTeamJSON `json:"teams"`
	}
	if err := json.Unmarshal([]byte(schemeJSON), &scheme); err != nil {
		t.Fatalf("decode scheme: %v", err)
	}
	if scheme.NTeams != 2 || len(scheme.Teams) != 2 || scheme.Teams[0].Name != "Вторая" || scheme.Teams[1].Name != "Первая" {
		t.Fatalf("scheme teams = %#v, want alphabetically sorted imported teams", scheme)
	}
	var state struct {
		Teams   []chgkTeamJSON `json:"teams"`
		Entries [][]int        `json:"entries"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if len(state.Teams) != 2 || state.Teams[0].Name != "Вторая" || state.Teams[1].Name != "Первая" {
		t.Fatalf("state teams = %#v, want alphabetically sorted imported teams", state.Teams)
	}
	if len(state.Entries) == 0 || len(state.Entries[0]) != 2 {
		t.Fatalf("state entries first row len = %d, want 2", len(state.Entries[0]))
	}

	if err := db.QueryRow(`select scheme_json, state_json from games where id = ?`, ksiGameID).Scan(&schemeJSON, &stateJSON); err != nil {
		t.Fatalf("load ksi json: %v", err)
	}
	var ksiScheme struct {
		GameType     string   `json:"gameType"`
		Participants []string `json:"participants"`
		Themes       int      `json:"themes"`
	}
	if err := json.Unmarshal([]byte(schemeJSON), &ksiScheme); err != nil {
		t.Fatalf("decode ksi scheme: %v", err)
	}
	if ksiScheme.GameType != "ksi" || len(ksiScheme.Participants) != 2 || ksiScheme.Participants[0] != "Вторая" || ksiScheme.Participants[1] != "Первая" || ksiScheme.Themes != ksiThemeCount {
		t.Fatalf("ksi scheme = %#v, want alphabetically sorted imported participants", ksiScheme)
	}
	var ksiState struct {
		Participants []string `json:"participants"`
		Themes       []struct {
			Answers [][]string `json:"answers"`
		} `json:"themes"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &ksiState); err != nil {
		t.Fatalf("decode ksi state: %v", err)
	}
	if len(ksiState.Participants) != 2 || ksiState.Participants[0] != "Вторая" || ksiState.Participants[1] != "Первая" {
		t.Fatalf("ksi state participants = %#v, want alphabetically sorted imported teams", ksiState.Participants)
	}
	if len(ksiState.Themes) != ksiThemeCount || len(ksiState.Themes[0].Answers) != 2 || len(ksiState.Themes[0].Answers[0]) != 5 {
		t.Fatalf("ksi answers shape = %#v, want %dx2x5", ksiState.Themes, ksiThemeCount)
	}
}

func TestImportFestRosterPreservesPlayerTeamOverrides(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID, _, ksiGameID := createRosterPropagationFixture(t, db)
	srv := &server{db: db, subscribers: make(map[chan event]struct{})}
	roster := []festRosterImportTeam{
		{
			RatingID: 101,
			Name:     "Первая",
			City:     "Москва",
			Players: []festRosterImportPlayer{
				{RatingID: 1001, FirstName: "Анна", LastName: "Первая"},
			},
		},
		{
			RatingID: 102,
			Name:     "Вторая",
			City:     "Казань",
			Players: []festRosterImportPlayer{
				{RatingID: 1002, FirstName: "Борис", LastName: "Второй"},
			},
		},
	}
	if _, err := srv.importFestRoster(t.Context(), festID, 13533, roster); err != nil {
		t.Fatalf("initial import roster: %v", err)
	}

	var playerID, overrideTeamID int64
	if err := db.QueryRow(`select id from fest_players where fest_id = ? and rating_id = 1001`, festID).Scan(&playerID); err != nil {
		t.Fatalf("load player: %v", err)
	}
	if err := db.QueryRow(`select id from fest_teams where fest_id = ? and rating_id = 102`, festID).Scan(&overrideTeamID); err != nil {
		t.Fatalf("load override team: %v", err)
	}
	if _, _, err := srv.savePlayerTeamOverride(t.Context(), festID, playerID, overrideTeamID, []int64{ksiGameID}); err != nil {
		t.Fatalf("save override: %v", err)
	}

	if _, err := srv.importFestRoster(t.Context(), festID, 13533, roster); err != nil {
		t.Fatalf("second import roster: %v", err)
	}

	var count int
	var restoredPlayerRating, restoredTargetRating int64
	if err := db.QueryRow(`
select count(*), coalesce(max(p.rating_id), 0), coalesce(max(target.rating_id), 0)
from game_player_team_overrides o
join fest_players p on p.id = o.player_id
join fest_teams target on target.id = o.override_team_id
where o.fest_id = ? and o.game_id = ?`, festID, ksiGameID).Scan(&count, &restoredPlayerRating, &restoredTargetRating); err != nil {
		t.Fatalf("load restored override: %v", err)
	}
	if count != 1 || restoredPlayerRating != 1001 || restoredTargetRating != 102 {
		t.Fatalf("restored override = count %d player %d target %d, want 1 / 1001 / 102", count, restoredPlayerRating, restoredTargetRating)
	}
}

func TestHostPlayerOverrideRowsGroupGames(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID, _, ksiGameID := createRosterPropagationFixture(t, db)
	srv := &server{db: db, subscribers: make(map[chan event]struct{})}
	roster := []festRosterImportTeam{
		{
			RatingID: 101,
			Name:     "Команда добра и позитива",
			City:     "Москва",
			Players: []festRosterImportPlayer{
				{RatingID: 1001, FirstName: "Василиса", LastName: "Павлейчук"},
			},
		},
		{RatingID: 102, Name: "Bikes for Peace", City: "Москва"},
	}
	if _, err := srv.importFestRoster(t.Context(), festID, 13533, roster); err != nil {
		t.Fatalf("import roster: %v", err)
	}
	if _, err := db.Exec(`update games set title = 'КСИ' where id = ?`, ksiGameID); err != nil {
		t.Fatalf("rename ksi game: %v", err)
	}
	now := utcNow()
	res, err := db.Exec(`
insert into games(fest_id, code, title, game_type, position, scheme_json, state_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(?, 'ek', 'ЭК', 'ek', 3, '{}', '{}', 'pending', 'fest', 'fest', 1, ?, ?)`,
		festID, now, now)
	if err != nil {
		t.Fatalf("insert ek game: %v", err)
	}
	ekGameID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("ek game id: %v", err)
	}

	var playerID, sourceTeamID, overrideTeamID int64
	if err := db.QueryRow(`
select p.id, tt.id
from fest_team_players ftp
join fest_players p on p.id = ftp.player_id
join fest_teams tt on tt.id = ftp.team_id
where p.fest_id = ? and p.rating_id = 1001`, festID).Scan(&playerID, &sourceTeamID); err != nil {
		t.Fatalf("load player/source: %v", err)
	}
	if err := db.QueryRow(`select id from fest_teams where fest_id = ? and rating_id = 102`, festID).Scan(&overrideTeamID); err != nil {
		t.Fatalf("load target: %v", err)
	}
	for _, gameID := range []int64{ksiGameID, ekGameID} {
		if _, err := db.Exec(`
insert into game_player_team_overrides(fest_id, game_id, player_id, source_team_id, override_team_id, created_at, updated_at)
values(?, ?, ?, ?, ?, ?, ?)`,
			festID, gameID, playerID, sourceTeamID, overrideTeamID, now, now); err != nil {
			t.Fatalf("insert override for game %d: %v", gameID, err)
		}
	}

	options, err := loadHostPlayerOverrideGameOptions(t.Context(), db, festID)
	if err != nil {
		t.Fatalf("load game options: %v", err)
	}
	if len(options) != 2 || options[0].Label != "КСИ" || options[1].Label != "ЭК" {
		t.Fatalf("game option labels = %#v, want КСИ/ЭК", options)
	}
	rows, err := loadHostPlayerOverrideRows(t.Context(), db, festID)
	if err != nil {
		t.Fatalf("load override rows: %v", err)
	}
	if len(rows) != 1 || rows[0].Player != "Василиса Павлейчук" || rows[0].SourceTeam != "Команда добра и позитива" || rows[0].OverrideTeam != "Bikes for Peace" || rows[0].Games != "КСИ, ЭК" {
		t.Fatalf("override rows = %#v, want one grouped row", rows)
	}
	if !rows[0].HasGame(ksiGameID) || !rows[0].HasGame(ekGameID) {
		t.Fatalf("row game ids = %#v, want both games", rows[0].GameIDs)
	}

	if _, _, err := srv.replacePlayerTeamOverride(t.Context(), festID, playerID, sourceTeamID, overrideTeamID, []int64{ekGameID}); err != nil {
		t.Fatalf("replace override games: %v", err)
	}
	rows, err = loadHostPlayerOverrideRows(t.Context(), db, festID)
	if err != nil {
		t.Fatalf("reload override rows: %v", err)
	}
	if len(rows) != 1 || rows[0].Games != "ЭК" || rows[0].HasGame(ksiGameID) || !rows[0].HasGame(ekGameID) {
		t.Fatalf("rows after replace = %#v, want only ЭК", rows)
	}

	if _, _, err := srv.replacePlayerTeamOverride(t.Context(), festID, playerID, sourceTeamID, overrideTeamID, nil); err != nil {
		t.Fatalf("delete override games: %v", err)
	}
	rows, err = loadHostPlayerOverrideRows(t.Context(), db, festID)
	if err != nil {
		t.Fatalf("reload deleted rows: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows after delete = %#v, want none", rows)
	}
}

func TestFestNumbersFlow(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID, chgkGameID, _ := createRosterPropagationFixture(t, db)
	srv := &server{db: db, subscribers: make(map[chan event]struct{})}
	if _, err := srv.importFestRoster(t.Context(), festID, 999, []festRosterImportTeam{
		{RatingID: 11, Name: "Алёша", City: "А"},
		{RatingID: 12, Name: "Боря", City: "Б"},
		{RatingID: 13, Name: "Витя", City: "В"},
	}); err != nil {
		t.Fatalf("import roster: %v", err)
	}

	// After roster import, no numbers are assigned.
	allSet, total, err := festTeamsAllNumbered(t.Context(), db, festID)
	if err != nil {
		t.Fatalf("allNumbered: %v", err)
	}
	if total != 3 || allSet {
		t.Fatalf("after import: total=%d allSet=%v, want 3/false", total, allSet)
	}

	// Auto-assign numbers by alphabet.
	teams, err := loadFestTeamsForNumbering(t.Context(), db, festID)
	if err != nil {
		t.Fatalf("load teams: %v", err)
	}
	if len(teams) != 3 {
		t.Fatalf("loaded teams = %d, want 3", len(teams))
	}
	assignments := make(map[int64]int, len(teams))
	for i, team := range teams {
		assignments[team.ID] = i + 1
	}
	if err := srv.saveFestNumbers(t.Context(), festID, assignments); err != nil {
		t.Fatalf("save numbers: %v", err)
	}

	allSet, _, err = festTeamsAllNumbered(t.Context(), db, festID)
	if err != nil {
		t.Fatalf("allNumbered post-save: %v", err)
	}
	if !allSet {
		t.Fatalf("after save: allSet should be true")
	}

	// Verify OD state.teams now carries the assigned numbers.
	var stateJSON string
	if err := db.QueryRow(`select state_json from games where id = ?`, chgkGameID).Scan(&stateJSON); err != nil {
		t.Fatalf("load od state: %v", err)
	}
	var state struct {
		Teams []chgkTeamJSON `json:"teams"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if len(state.Teams) != 3 {
		t.Fatalf("state teams = %d, want 3", len(state.Teams))
	}
	numbers := make([]int64, len(state.Teams))
	for i, team := range state.Teams {
		numbers[i] = team.Number
	}
	seen := map[int64]bool{}
	for _, n := range numbers {
		if n < 1 || n > 3 || seen[n] {
			t.Fatalf("numbers in state = %v, want unique 1..3", numbers)
		}
		seen[n] = true
	}

	// Rejecting duplicate number is the responsibility of the handler; saveFestNumbers
	// dedupes by map key, so we just verify clearing wipes numbers.
	if err := srv.saveFestNumbers(t.Context(), festID, nil); err != nil {
		t.Fatalf("clear numbers: %v", err)
	}
	allSet, _, err = festTeamsAllNumbered(t.Context(), db, festID)
	if err != nil {
		t.Fatalf("allNumbered post-clear: %v", err)
	}
	if allSet {
		t.Fatalf("after clear: allSet should be false")
	}
	if err := db.QueryRow(`select state_json from games where id = ?`, chgkGameID).Scan(&stateJSON); err != nil {
		t.Fatalf("load od state after clear: %v", err)
	}
	state = struct {
		Teams []chgkTeamJSON `json:"teams"`
	}{}
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		t.Fatalf("decode state after clear: %v", err)
	}
	for _, team := range state.Teams {
		if team.Number != 0 {
			t.Fatalf("after clear team %q has number %d, want 0", team.Name, team.Number)
		}
	}
}

func TestHostFestNumbersPage(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, chgkGameID, _ := createRosterPropagationFixture(t, srv.db)
	organizerID, token := createAPITestSession(t, srv, "numbers-host")
	addAPITestOrganizer(t, srv, festID, organizerID)

	if _, err := srv.importFestRoster(t.Context(), festID, 1, []festRosterImportTeam{
		{RatingID: 11, Name: "Алёша"},
		{RatingID: 12, Name: "Боря"},
	}); err != nil {
		t.Fatalf("import roster: %v", err)
	}

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		resp := httptest.NewRecorder()
		srv.handleHostRouter(resp, req)
		return resp
	}
	post := func(path string, form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		resp := httptest.NewRecorder()
		srv.handleHostRouter(resp, req)
		return resp
	}

	page := get("/host/fest/" + itoa(festID) + "/numbers")
	if page.Code != http.StatusOK {
		t.Fatalf("GET numbers status = %d body=%s", page.Code, page.Body.String())
	}
	body := page.Body.String()
	if !strings.Contains(body, "Номера команд") || !strings.Contains(body, "Проставить автоматически") {
		t.Fatalf("numbers page missing expected text: %s", body)
	}

	auto := post("/host/fest/"+itoa(festID)+"/numbers/auto", url.Values{})
	if auto.Code != http.StatusOK {
		t.Fatalf("auto status = %d body=%s", auto.Code, auto.Body.String())
	}

	teams, err := loadFestTeamsForNumbering(t.Context(), srv.db, festID)
	if err != nil {
		t.Fatalf("load teams: %v", err)
	}
	if len(teams) != 2 {
		t.Fatalf("teams = %d, want 2", len(teams))
	}
	for _, team := range teams {
		if team.Number == 0 {
			t.Fatalf("team %q has no number after auto", team.Name)
		}
	}

	// Sort to know which team currently has number 1 / 2.
	byNum := func() (lo, hi festNumberingTeam) {
		teams, err := loadFestTeamsForNumbering(t.Context(), srv.db, festID)
		if err != nil {
			t.Fatalf("load teams: %v", err)
		}
		for _, team := range teams {
			if team.Number == 1 {
				lo = team
			}
			if team.Number == 2 {
				hi = team
			}
		}
		return
	}
	low, high := byNum()

	// Manual reassignment via N-row form: swap numbers.
	swap := url.Values{}
	swap.Set("num_1", "2")
	swap.Set(fmt.Sprintf("team_id_%d", 1), itoa(low.ID))
	swap.Set("num_2", "1")
	swap.Set(fmt.Sprintf("team_id_%d", 2), itoa(high.ID))
	manual := post("/host/fest/"+itoa(festID)+"/numbers", swap)
	if manual.Code != http.StatusOK {
		t.Fatalf("manual save status = %d body=%s", manual.Code, manual.Body.String())
	}
	var num1, num2 int
	if err := srv.db.QueryRow(`select number from fest_teams where id = ?`, low.ID).Scan(&num1); err != nil {
		t.Fatalf("load num1: %v", err)
	}
	if err := srv.db.QueryRow(`select number from fest_teams where id = ?`, high.ID).Scan(&num2); err != nil {
		t.Fatalf("load num2: %v", err)
	}
	if num1 != 2 || num2 != 1 {
		t.Fatalf("after swap: low=%d high=%d, want 2/1", num1, num2)
	}

	// Duplicate number should produce an error page rather than overwriting state.
	dup := url.Values{}
	dup.Set("num_1", "5")
	dup.Set("team_id_1", itoa(low.ID))
	dup.Set("num_2", "5")
	dup.Set("team_id_2", itoa(high.ID))
	dupResp := post("/host/fest/"+itoa(festID)+"/numbers", dup)
	if dupResp.Code != http.StatusOK {
		t.Fatalf("dup save status = %d body=%s", dupResp.Code, dupResp.Body.String())
	}
	if !strings.Contains(dupResp.Body.String(), "указан сразу") {
		t.Fatalf("dup response missing conflict message: %s", dupResp.Body.String())
	}

	// Reserve number > N is allowed by renaming a row's number.
	reserve := url.Values{}
	reserve.Set("num_1", "101")
	reserve.Set("team_id_1", itoa(low.ID))
	reserve.Set("num_2", "1")
	reserve.Set("team_id_2", itoa(high.ID))
	if resp := post("/host/fest/"+itoa(festID)+"/numbers", reserve); resp.Code != http.StatusOK {
		t.Fatalf("reserve save status = %d body=%s", resp.Code, resp.Body.String())
	}
	if err := srv.db.QueryRow(`select number from fest_teams where id = ?`, low.ID).Scan(&num1); err != nil {
		t.Fatalf("load num after reserve: %v", err)
	}
	if num1 != 101 {
		t.Fatalf("after reserve: low.number=%d, want 101", num1)
	}

	// Verify state.teams in the chgk game contains the reassigned numbers.
	var stateJSON string
	if err := srv.db.QueryRow(`select state_json from games where id = ?`, chgkGameID).Scan(&stateJSON); err != nil {
		t.Fatalf("load chgk state: %v", err)
	}
	var state struct {
		Teams []chgkTeamJSON `json:"teams"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		t.Fatalf("decode chgk state: %v", err)
	}
	if len(state.Teams) != 2 {
		t.Fatalf("state teams = %d, want 2", len(state.Teams))
	}
	got := map[string]int64{}
	for _, team := range state.Teams {
		got[team.Name] = team.Number
	}
	if got["Алёша"] != 101 || got["Боря"] != 1 {
		t.Fatalf("state numbers = %#v, want Алёша→101, Боря→1", got)
	}
}

func TestFestNumbersRemapEntries(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID, chgkGameID, _ := createRosterPropagationFixture(t, db)
	srv := &server{db: db, subscribers: make(map[chan event]struct{})}
	if _, err := srv.importFestRoster(t.Context(), festID, 1, []festRosterImportTeam{
		{RatingID: 11, Name: "Алёша"},
		{RatingID: 12, Name: "Боря"},
		{RatingID: 13, Name: "Витя"},
	}); err != nil {
		t.Fatalf("import roster: %v", err)
	}
	teams, err := loadFestTeamsForNumbering(t.Context(), db, festID)
	if err != nil {
		t.Fatalf("load teams: %v", err)
	}
	if len(teams) != 3 {
		t.Fatalf("teams=%d, want 3", len(teams))
	}
	// Initial 1..3 alphabetical numbering.
	initial := map[int64]int{teams[0].ID: 1, teams[1].ID: 2, teams[2].ID: 3}
	if err := srv.saveFestNumbers(t.Context(), festID, initial); err != nil {
		t.Fatalf("initial numbers: %v", err)
	}

	// Pre-fill some entries by number.
	entries := [][]int{{1, 2, 0}, {3, 1, 0}, {2, 0, 0}}
	entriesJSON, _ := json.Marshal(entries)
	shootoutRounds := []map[string]any{{
		"teams":   []int{1, 2},
		"entries": [][]int{{1, 2}, {2, 1}},
		"answers": [][]string{{"right", ""}, {"wrong", "right"}},
	}}
	shootoutRoundsJSON, _ := json.Marshal(shootoutRounds)
	if _, err := db.Exec(`
update games
set state_json = json_set(state_json, '$.entries', json(?), '$.shootoutRounds', json(?))
where id = ?`, string(entriesJSON), string(shootoutRoundsJSON), chgkGameID); err != nil {
		t.Fatalf("seed entries: %v", err)
	}

	// Reassign: team[0] gets reserve 101 (was 1), team[1] gets 1 (was 2), team[2] keeps 3.
	reassign := map[int64]int{teams[0].ID: 101, teams[1].ID: 1, teams[2].ID: 3}
	if err := srv.saveFestNumbers(t.Context(), festID, reassign); err != nil {
		t.Fatalf("reassign: %v", err)
	}

	var stateJSON string
	if err := db.QueryRow(`select state_json from games where id = ?`, chgkGameID).Scan(&stateJSON); err != nil {
		t.Fatalf("load state: %v", err)
	}
	var got struct {
		Entries        [][]int `json:"entries"`
		ShootoutRounds []struct {
			Teams   []int      `json:"teams"`
			Entries [][]int    `json:"entries"`
			Answers [][]string `json:"answers"`
		} `json:"shootoutRounds"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &got); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	// Expected mapping: 1 -> 101, 2 -> 1, 3 stays.
	want := [][]int{{101, 1, 0}, {3, 101, 0}, {1, 0, 0}}
	for q := range want {
		for slot, value := range want[q] {
			if got.Entries[q][slot] != value {
				t.Fatalf("entries[%d][%d]=%d, want %d (entries=%v)", q, slot, got.Entries[q][slot], value, got.Entries)
			}
		}
	}
	if len(got.ShootoutRounds) != 1 {
		t.Fatalf("shootout rounds = %#v, want one round", got.ShootoutRounds)
	}
	if fmt.Sprint(got.ShootoutRounds[0].Teams) != "[101 1]" {
		t.Fatalf("shootout teams = %#v, want [101 1]", got.ShootoutRounds[0].Teams)
	}
	if fmt.Sprint(got.ShootoutRounds[0].Entries) != "[[101 1] [1 101]]" {
		t.Fatalf("shootout entries = %#v, want remapped entries", got.ShootoutRounds[0].Entries)
	}
	if fmt.Sprint(got.ShootoutRounds[0].Answers) != "[[right ] [wrong right]]" {
		t.Fatalf("shootout answers = %#v, want answers preserved", got.ShootoutRounds[0].Answers)
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

// TestFestNumbersStableAcrossResync verifies that re-importing a roster keeps
// previously assigned numbers — a team that leaves the roster takes its slot
// out of circulation (no automatic renumbering) so already-printed answer
// sheets still point at the right team.
func TestFestNumbersStableAcrossResync(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID, _, _ := createRosterPropagationFixture(t, db)
	srv := &server{db: db, subscribers: make(map[chan event]struct{})}

	// Initial import: 5 teams "А".."Д" with rating IDs 11..15.
	initial := []festRosterImportTeam{
		{RatingID: 11, Name: "Алёша", City: "А"},
		{RatingID: 12, Name: "Боря", City: "Б"},
		{RatingID: 13, Name: "Витя", City: "В"},
		{RatingID: 14, Name: "Гена", City: "Г"},
		{RatingID: 15, Name: "Дима", City: "Д"},
	}
	if _, err := srv.importFestRoster(t.Context(), festID, 999, initial); err != nil {
		t.Fatalf("initial import: %v", err)
	}
	teams, err := loadFestTeamsForNumbering(t.Context(), db, festID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(teams) != 5 {
		t.Fatalf("teams = %d, want 5", len(teams))
	}
	// Assign numbers 1..5 alphabetically (which matches loadFestTeamsForNumbering ordering).
	assignments := map[int64]int{}
	byRating := map[int64]int64{}
	for i, team := range teams {
		assignments[team.ID] = i + 1
		// Stash team id → rating id via separate query.
		var ratingID int64
		if err := db.QueryRow(`select coalesce(rating_id, 0) from fest_teams where id = ?`, team.ID).Scan(&ratingID); err != nil {
			t.Fatalf("rating id: %v", err)
		}
		byRating[ratingID] = team.ID
	}
	if err := srv.saveFestNumbers(t.Context(), festID, assignments); err != nil {
		t.Fatalf("save: %v", err)
	}

	checkNumbers := func(label string, want map[int64]int64) {
		t.Helper()
		rows, err := db.Query(`select coalesce(rating_id, 0), coalesce(number, 0) from fest_teams where fest_id = ? and deleted = 0`, festID)
		if err != nil {
			t.Fatalf("%s: query: %v", label, err)
		}
		defer rows.Close()
		got := map[int64]int64{}
		for rows.Next() {
			var rid, num int64
			if err := rows.Scan(&rid, &num); err != nil {
				t.Fatalf("%s: scan: %v", label, err)
			}
			got[rid] = num
		}
		if len(got) != len(want) {
			t.Fatalf("%s: got %d active teams, want %d (got=%v)", label, len(got), len(want), got)
		}
		for rid, num := range want {
			if got[rid] != num {
				t.Fatalf("%s: rating_id %d → %d, want %d (got=%v)", label, rid, got[rid], num, got)
			}
		}
	}

	checkNumbers("after initial assign", map[int64]int64{11: 1, 12: 2, 13: 3, 14: 4, 15: 5})

	// Case 1: team Гена (rating 14, number 4) leaves the rating roster.
	// Expected: remaining teams keep 1, 2, 3, 5 — no renumbering.
	without14 := []festRosterImportTeam{
		{RatingID: 11, Name: "Алёша", City: "А"},
		{RatingID: 12, Name: "Боря", City: "Б"},
		{RatingID: 13, Name: "Витя", City: "В"},
		{RatingID: 15, Name: "Дима", City: "Д"},
	}
	if _, err := srv.importFestRoster(t.Context(), festID, 999, without14); err != nil {
		t.Fatalf("resync without 14: %v", err)
	}
	checkNumbers("after Гена leaves", map[int64]int64{11: 1, 12: 2, 13: 3, 15: 5})

	// Case 2: still without Гена, but two new teams join.
	// Expected: existing keep 1, 2, 3, 5; newcomers receive 6 and 7 (strictly
	// greater than the largest number ever assigned, never filling the 4-gap).
	withNewcomers := []festRosterImportTeam{
		{RatingID: 11, Name: "Алёша", City: "А"},
		{RatingID: 12, Name: "Боря", City: "Б"},
		{RatingID: 13, Name: "Витя", City: "В"},
		{RatingID: 15, Name: "Дима", City: "Д"},
		{RatingID: 16, Name: "Егор", City: "Е"},
		{RatingID: 17, Name: "Жора", City: "Ж"},
	}
	if _, err := srv.importFestRoster(t.Context(), festID, 999, withNewcomers); err != nil {
		t.Fatalf("resync with newcomers: %v", err)
	}
	checkNumbers("after newcomers join", map[int64]int64{11: 1, 12: 2, 13: 3, 15: 5, 16: 6, 17: 7})

	// Case 3: Гена returns. Expected: her old number 4 comes back because the
	// soft-deleted row preserved it.
	withGenaBack := []festRosterImportTeam{
		{RatingID: 11, Name: "Алёша", City: "А"},
		{RatingID: 12, Name: "Боря", City: "Б"},
		{RatingID: 13, Name: "Витя", City: "В"},
		{RatingID: 14, Name: "Гена", City: "Г"},
		{RatingID: 15, Name: "Дима", City: "Д"},
		{RatingID: 16, Name: "Егор", City: "Е"},
		{RatingID: 17, Name: "Жора", City: "Ж"},
	}
	if _, err := srv.importFestRoster(t.Context(), festID, 999, withGenaBack); err != nil {
		t.Fatalf("resync with Гена back: %v", err)
	}
	checkNumbers("after Гена returns", map[int64]int64{11: 1, 12: 2, 13: 3, 14: 4, 15: 5, 16: 6, 17: 7})

	// Case 4: fresh "assign numbers" (auto) must wipe everything and renumber
	// 1..N alphabetically over the current active roster, including discarding
	// any leftover soft-deleted rows so their archived numbers don't reserve
	// slots.
	autoResp := httptest.NewRecorder()
	autoReq := httptest.NewRequest(http.MethodPost, "/host/fest/"+itoa(festID)+"/numbers/auto", nil)
	srv.handleHostAutoFestNumbers(autoResp, autoReq, festID)
	if autoResp.Code != http.StatusOK {
		t.Fatalf("auto status %d", autoResp.Code)
	}
	checkNumbers("after auto-assign", map[int64]int64{11: 1, 12: 2, 13: 3, 14: 4, 15: 5, 16: 6, 17: 7})
}

// TestFestNumbersFreshImport ensures that an import into a fest that has never
// had any numbers leaves teams unnumbered — auto-assignment is an explicit
// host action.
func TestFestNumbersFreshImport(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID, _, _ := createRosterPropagationFixture(t, db)
	srv := &server{db: db, subscribers: make(map[chan event]struct{})}

	if _, err := srv.importFestRoster(t.Context(), festID, 999, []festRosterImportTeam{
		{RatingID: 11, Name: "Алёша"},
		{RatingID: 12, Name: "Боря"},
	}); err != nil {
		t.Fatalf("first import: %v", err)
	}
	allSet, total, err := festTeamsAllNumbered(t.Context(), db, festID)
	if err != nil {
		t.Fatalf("allNumbered: %v", err)
	}
	if total != 2 || allSet {
		t.Fatalf("after fresh import: total=%d allSet=%v, want 2/false", total, allSet)
	}

	// A second import while nothing has ever been numbered must still leave
	// teams unnumbered — there's no prior numbering context to extend.
	if _, err := srv.importFestRoster(t.Context(), festID, 999, []festRosterImportTeam{
		{RatingID: 11, Name: "Алёша"},
		{RatingID: 12, Name: "Боря"},
		{RatingID: 13, Name: "Витя"},
	}); err != nil {
		t.Fatalf("second import: %v", err)
	}
	allSet, total, err = festTeamsAllNumbered(t.Context(), db, festID)
	if err != nil {
		t.Fatalf("allNumbered: %v", err)
	}
	if total != 3 || allSet {
		t.Fatalf("after second import: total=%d allSet=%v, want 3/false", total, allSet)
	}
}

func TestAuthCodeHelpers(t *testing.T) {
	a, err := newInviteCode()
	if err != nil {
		t.Fatalf("invite code: %v", err)
	}
	b, err := newInviteCode()
	if err != nil {
		t.Fatalf("invite code: %v", err)
	}
	if a == b || a == "" {
		t.Fatalf("invite codes collide or empty: %q vs %q", a, b)
	}
	tok, err := newSessionToken()
	if err != nil {
		t.Fatalf("session token: %v", err)
	}
	if hashSessionToken(tok) == tok {
		t.Fatal("session hash should differ from token")
	}
	if hashSessionToken(tok) != hashSessionToken(tok) {
		t.Fatal("session hash should be deterministic")
	}
}
