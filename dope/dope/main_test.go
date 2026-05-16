package main

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
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
