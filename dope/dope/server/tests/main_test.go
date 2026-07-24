package tests

import (
	"database/sql"
	"dope/dope/domain/core"
	"dope/dope/domain/games"
	"dope/dope/domain/imports"
	"dope/dope/domain/numbering"
	"dope/dope/domain/overrides"
	rosterpkg "dope/dope/domain/roster"
	"dope/dope/platform/realtime"
	"dope/dope/platform/util"
	dopeserver "dope/dope/server"
	"dope/dope/storage/store"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"pecheny.me/dopecore/authcred"
	"pecheny.me/dopecore/session"
)

func TestDefaultMatchScores(t *testing.T) {
	state := dopeserver.DefaultMatch()
	view := store.BuildView(state)

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
	state := store.MatchState{
		Teams: []store.TeamState{
			{
				Name: "A",
				Themes: []store.ThemeEntry{
					{Answers: [5]string{"right", "", "", "", ""}},
				},
				ShootoutThemes: []store.ThemeEntry{
					{Answers: [5]string{"wrong", "", "", "", "right"}},
				},
			},
		},
	}

	team := store.BuildView(state).Teams[0]
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
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.State = dopeserver.DefaultMatch()
		e.RT = realtime.NewManager()
	})

	if _, _, err := srv.ApplyUpdate(dopeserver.UpdateRequest{Action: dopeserver.ActionAddShootoutTheme}); err != nil {
		t.Fatalf("add shootout theme: %v", err)
	}
	for _, team := range srv.Eng().State.Teams {
		if len(team.ShootoutThemes) != 1 {
			t.Fatalf("%s shootout themes = %d, want 1", team.Name, len(team.ShootoutThemes))
		}
	}

	theme := 0
	answer := 4
	shootout := true
	mark := "right"
	view, _, err := srv.ApplyUpdate(dopeserver.UpdateRequest{
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

	if _, _, err := srv.ApplyUpdate(dopeserver.UpdateRequest{Action: dopeserver.ActionRemoveShootoutTheme}); err != nil {
		t.Fatalf("remove shootout theme: %v", err)
	}
	if len(srv.Eng().State.Teams[0].ShootoutThemes) != 0 {
		t.Fatalf("shootout themes after remove = %d, want 0", len(srv.Eng().State.Teams[0].ShootoutThemes))
	}
}

func TestManualStandingsAllowsSplitPlace(t *testing.T) {
	state := dopeserver.DefaultMatch()
	state.Teams[0].Place = 3.5
	state.Teams[1].Place = 2
	state.Teams[2].Place = 3.5
	state.Teams[3].Place = 1

	standings := store.BuildView(state).Standings
	want := []float64{1, 2, 3.5, 3.5}
	for i, place := range want {
		if standings[i].Place != place {
			t.Fatalf("standings[%d].Place = %v, want %v", i, standings[i].Place, place)
		}
	}
}

func TestFinishedMatchRejectsEditsButCanBeReopened(t *testing.T) {
	t.Chdir(t.TempDir())
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.State = dopeserver.DefaultMatch()
		e.RT = realtime.NewManager()
	})

	finished := true
	if _, _, err := srv.ApplyUpdate(dopeserver.UpdateRequest{Finished: &finished}); err != nil {
		t.Fatalf("finish match: %v", err)
	}

	place := 2.5
	if _, _, err := srv.ApplyUpdate(dopeserver.UpdateRequest{Team: 0, Place: &place}); err == nil {
		t.Fatal("place update while finished succeeded, want error")
	}

	finished = false
	if _, _, err := srv.ApplyUpdate(dopeserver.UpdateRequest{Finished: &finished}); err != nil {
		t.Fatalf("reopen match: %v", err)
	}
	if _, _, err := srv.ApplyUpdate(dopeserver.UpdateRequest{Team: 0, Place: &place}); err != nil {
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
		got := store.NormalizeMark(input)
		if got != want {
			t.Fatalf("normalizeMark(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSQLiteBootstrapAndMatchUpdate(t *testing.T) {
	t.Chdir(t.TempDir())
	db, err := dopeserver.OpenFestDB("test.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID := createDefaultFestFixture(t, db, dopeserver.DefaultMatch())
	gameID, err := dopeserver.DefaultGameID(t.Context(), db, festID)
	if err != nil {
		t.Fatalf("default game: %v", err)
	}

	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.FestID = festID
		e.ActiveGameID = gameID
		e.ActiveMatchCode = dopeserver.DefaultMatchCode
		e.RT = realtime.NewManager()
	})

	view, err := srv.LoadMatchViewLocked(festID, dopeserver.DefaultMatchCode)
	if err != nil {
		t.Fatalf("load match: %v", err)
	}
	if view.Code != dopeserver.DefaultMatchCode {
		t.Fatalf("code = %q, want %q", view.Code, dopeserver.DefaultMatchCode)
	}
	if view.Venue == nil || view.Venue.Number != 1 {
		t.Fatalf("venue = %#v, want number 1", view.Venue)
	}

	scope, err := srv.VerifyMatchInScope(t.Context(), dopeserver.FestScope{FestID: festID, GameID: gameID}, dopeserver.DefaultMatchCode)
	if err != nil {
		t.Fatalf("scope: %v", err)
	}
	view = editMark(t, srv, scope, 2, 0, 0, "right")
	if view.Teams[2].Total != 10 {
		t.Fatalf("updated total = %d, want 10", view.Teams[2].Total)
	}

	reloaded, err := srv.LoadMatchViewLocked(festID, dopeserver.DefaultMatchCode)
	if err != nil {
		t.Fatalf("reload match: %v", err)
	}
	if reloaded.Teams[2].Themes[0].Answers[0] != "right" {
		t.Fatalf("persisted mark = %q, want right", reloaded.Teams[2].Themes[0].Answers[0])
	}
}

func TestSQLiteVenuesAndRosterLimit(t *testing.T) {
	t.Chdir(t.TempDir())
	db, err := dopeserver.OpenFestDB("test.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID := createDefaultFestFixture(t, db, dopeserver.DefaultMatch())
	gameID, err := dopeserver.DefaultGameID(t.Context(), db, festID)
	if err != nil {
		t.Fatalf("default game: %v", err)
	}

	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.FestID = festID
		e.ActiveGameID = gameID
		e.ActiveMatchCode = dopeserver.DefaultMatchCode
		e.RT = realtime.NewManager()
	})

	venues, _, err := srv.UpdateVenue(t.Context(), festID, 1, "Рим")
	if err != nil {
		t.Fatalf("update venue: %v", err)
	}
	if len(venues) != 1 || venues[0].Title != "Рим" {
		t.Fatalf("venues = %#v, want renamed venue", venues)
	}
	view, err := srv.LoadMatchViewLocked(festID, dopeserver.DefaultMatchCode)
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
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	scheme := store.FestScheme{
		SchemaVersion:     2,
		Slug:              "multi-stage",
		Title:             "multi-stage",
		GameType:          "ek",
		RegularThemeCount: store.ThemeCount,
		Venues:            []store.SchemeVenue{{Number: 1, Title: "Main"}},
		Teams: []store.SchemeTeam{
			{Name: "Alpha", Basket: 1, Number: 1},
			{Name: "Beta", Basket: 1, Number: 2},
			{Name: "Gamma", Basket: 1, Number: 3},
			{Name: "Delta", Basket: 1, Number: 4},
		},
		Stages: []store.SchemeStage{
			{
				Code:      "r1",
				Title:     "Round 1",
				StageType: "matches",
				Position:  1,
				Matches: []store.SchemeMatch{
					{
						Code:             "A",
						Title:            "A",
						Venue:            1,
						ParticipantCount: 2,
						Slots: []store.SchemeSlot{
							{Seed: &store.SchemeSeedRef{Basket: 1, Number: 1}},
							{Seed: &store.SchemeSeedRef{Basket: 1, Number: 2}},
						},
					},
					{
						Code:             "B",
						Title:            "B",
						Venue:            1,
						ParticipantCount: 2,
						Slots: []store.SchemeSlot{
							{Seed: &store.SchemeSeedRef{Basket: 1, Number: 3}},
							{Seed: &store.SchemeSeedRef{Basket: 1, Number: 4}},
						},
					},
				},
			},
			{
				Code:      "final",
				Title:     "Final",
				StageType: "matches",
				Position:  2,
				Matches: []store.SchemeMatch{{
					Code:             "C",
					Title:            "C",
					Venue:            1,
					ParticipantCount: 2,
					Slots: []store.SchemeSlot{
						{FromMatch: &store.SchemeFromMatchRef{Match: "A", Place: 1}},
						{FromMatch: &store.SchemeFromMatchRef{Match: "B", Place: 1}},
					},
				}},
			},
		},
	}

	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.ActiveMatchCode = dopeserver.DefaultMatchCode
		e.RT = realtime.NewManager()
	})
	view, err := srv.ImportScheme(scheme)
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
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	festID, gameID, matchCode, err := dopeserver.LoadActiveContext(db)
	if err != nil {
		t.Fatalf("loadActiveContext: %v", err)
	}
	if festID != 0 || gameID != 0 || matchCode != "" {
		t.Fatalf("empty db produced (%d, %d, %q), want zero values", festID, gameID, matchCode)
	}
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
	})
	view, err := srv.LoadFestViewLocked(0, 0)
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
	if err := dopeserver.MigrateDB(db); err != nil {
		t.Fatalf("migrate legacy schema: %v", err)
	}
	legacyExists, err := dopeserver.SqliteTableExists(t.Context(), db, "tournaments")
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
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
	})
	scheme := store.FestScheme{
		SchemaVersion: 2,
		Slug:          "with-team-slot",
		Title:         "with team slot",
		Stages: []store.SchemeStage{{
			Code:      "stage1",
			Title:     "stage 1",
			StageType: "matches",
			Position:  1,
			Matches: []store.SchemeMatch{{
				Code:             "A",
				Title:            "A",
				ParticipantCount: 1,
				Slots: []store.SchemeSlot{{
					Team: &store.SchemeTeamRef{Name: "Inline"},
				}},
			}},
		}},
	}
	if _, err := srv.ImportScheme(scheme); err == nil {
		t.Fatal("expected error for slot.team, got nil")
	} else if !strings.Contains(err.Error(), "removed source") {
		t.Fatalf("error = %v, want mention of removed source", err)
	}
}

func TestImportSeedSlotsResolveViaAssignments(t *testing.T) {
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
	})
	scheme := store.FestScheme{
		SchemaVersion: 2,
		Slug:          "symbolic",
		Title:         "symbolic",
		GameType:      "ek",
		Stages: []store.SchemeStage{{
			Code:      "r1",
			Title:     "r1",
			StageType: "matches",
			Position:  1,
			Matches: []store.SchemeMatch{{
				Code:             "A",
				Title:            "A",
				ParticipantCount: 2,
				Slots: []store.SchemeSlot{
					{Seed: &store.SchemeSeedRef{Basket: 1, Number: 1}},
					{Seed: &store.SchemeSeedRef{Basket: 1, Number: 2}},
				},
			}},
		}},
		Teams: []store.SchemeTeam{
			{Name: "Alpha", Basket: 1, Number: 1},
			{Name: "Beta", Basket: 1, Number: 2},
		},
	}
	view, err := srv.ImportScheme(scheme)
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
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
	})
	scheme := store.FestScheme{
		SchemaVersion: 2,
		Slug:          "minimal",
		Title:         "minimal",
		Stages: []store.SchemeStage{{
			Code:      "r1",
			Title:     "r1",
			StageType: "matches",
			Position:  1,
			Matches: []store.SchemeMatch{{
				Code: "A", Title: "A", ParticipantCount: 0,
				Slots: []store.SchemeSlot{{Placeholder: "TBD"}},
			}},
		}},
	}
	if _, err := srv.ImportScheme(scheme); err != nil {
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

func TestImportFestRosterPropagatesToChGKAndKSI(t *testing.T) {
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID, chgkGameID, ksiGameID := createRosterPropagationFixture(t, db)

	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
	})
	result, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 13533, []rosterpkg.FestRosterImportTeam{
		{
			RatingID: 101,
			Name:     "Первая",
			City:     "Москва",
			Players: []rosterpkg.FestRosterImportPlayer{
				{RatingID: 1002, FirstName: "Борис", LastName: "Второй"},
				{RatingID: 1001, FirstName: "Анна", LastName: "Первая"},
			},
		},
		{
			RatingID: 102,
			Name:     "Вторая",
			City:     "Казань",
			Players: []rosterpkg.FestRosterImportPlayer{
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
	if err := db.QueryRow(`select scheme_json, coalesce((select m.state_json from matches m where m.game_id = games.id and m.code = 'main'), '{}') from games where id = ?`, chgkGameID).Scan(&schemeJSON, &stateJSON); err != nil {
		t.Fatalf("load chgk json: %v", err)
	}
	var scheme struct {
		NTeams int                      `json:"nTeams"`
		Teams  []rosterpkg.ChgkTeamJSON `json:"teams"`
	}
	if err := json.Unmarshal([]byte(schemeJSON), &scheme); err != nil {
		t.Fatalf("decode scheme: %v", err)
	}
	if scheme.NTeams != 2 || len(scheme.Teams) != 2 || scheme.Teams[0].Name != "Вторая" || scheme.Teams[1].Name != "Первая" {
		t.Fatalf("scheme teams = %#v, want alphabetically sorted imported teams", scheme)
	}
	var state struct {
		Teams   []rosterpkg.ChgkTeamJSON `json:"teams"`
		Entries [][]int                  `json:"entries"`
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

	if err := db.QueryRow(`select scheme_json, coalesce((select m.state_json from matches m where m.game_id = games.id and m.code = 'main'), '{}') from games where id = ?`, ksiGameID).Scan(&schemeJSON, &stateJSON); err != nil {
		t.Fatalf("load ksi json: %v", err)
	}
	var ksiScheme struct {
		GameType     string                 `json:"gameType"`
		Participants []games.KSIParticipant `json:"participants"`
		Themes       int                    `json:"themes"`
	}
	if err := json.Unmarshal([]byte(schemeJSON), &ksiScheme); err != nil {
		t.Fatalf("decode ksi scheme: %v", err)
	}
	if ksiScheme.GameType != "ksi" || len(ksiScheme.Participants) != 2 || ksiScheme.Participants[0].Name != "Вторая" || ksiScheme.Participants[1].Name != "Первая" || ksiScheme.Themes != games.KSIThemeCount {
		t.Fatalf("ksi scheme = %#v, want alphabetically sorted imported participants", ksiScheme)
	}
	var ksiState struct {
		Participants []games.KSIParticipant `json:"participants"`
		Themes       []struct {
			Answers [][]string `json:"answers"`
		} `json:"themes"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &ksiState); err != nil {
		t.Fatalf("decode ksi state: %v", err)
	}
	if len(ksiState.Participants) != 2 || ksiState.Participants[0].Name != "Вторая" || ksiState.Participants[1].Name != "Первая" {
		t.Fatalf("ksi state participants = %#v, want alphabetically sorted imported teams", ksiState.Participants)
	}
	if ksiState.Participants[0].Number == 0 || ksiState.Participants[1].Number == 0 {
		t.Fatalf("ksi participants must carry numbers: %#v", ksiState.Participants)
	}
	if len(ksiState.Themes) != games.KSIThemeCount || len(ksiState.Themes[0].Answers) != 2 || len(ksiState.Themes[0].Answers[0]) != 5 {
		t.Fatalf("ksi answers shape = %#v, want %dx2x5", ksiState.Themes, games.KSIThemeCount)
	}
}

func TestImportFestRosterNoOpWhenUnchanged(t *testing.T) {
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

	roster := []rosterpkg.FestRosterImportTeam{
		{
			RatingID: 101, Name: "Первая", City: "Москва",
			Players: []rosterpkg.FestRosterImportPlayer{
				{RatingID: 1002, FirstName: "Борис", LastName: "Второй"},
				{RatingID: 1001, FirstName: "Анна", LastName: "Первая"},
			},
		},
		{
			RatingID: 102, Name: "Вторая", City: "Казань",
			Players: []rosterpkg.FestRosterImportPlayer{
				{RatingID: 1003, FirstName: "Вера", LastName: "Третья"},
			},
		},
	}

	// First import does the real work and bumps the fest revision.
	first, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 13533, roster)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if first.Unchanged {
		t.Fatalf("first import should not be a no-op: %#v", first)
	}
	var revAfterFirst int64
	if err := db.QueryRow(`select revision from fests where id = ?`, festID).Scan(&revAfterFirst); err != nil {
		t.Fatalf("revision after first: %v", err)
	}

	// Re-importing the identical roster must short-circuit: Unchanged, accurate
	// counts, and crucially NO revision bump (proving no write tx ran).
	second, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 13533, roster)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if !second.Unchanged {
		t.Fatalf("identical re-import should be a no-op, got %#v", second)
	}
	if second.TeamCount != 2 || second.PlayerCount != 3 {
		t.Fatalf("no-op counts = %d teams / %d players, want 2/3", second.TeamCount, second.PlayerCount)
	}
	if second.ODGameCount != 0 || second.KSIGameCount != 0 {
		t.Fatalf("no-op must report 0 games rewritten, got od=%d ksi=%d", second.ODGameCount, second.KSIGameCount)
	}
	var revAfterSecond int64
	if err := db.QueryRow(`select revision from fests where id = ?`, festID).Scan(&revAfterSecond); err != nil {
		t.Fatalf("revision after second: %v", err)
	}
	if revAfterSecond != revAfterFirst {
		t.Fatalf("no-op re-import bumped revision %d -> %d; should not write at all", revAfterFirst, revAfterSecond)
	}

	// A real change (added player) must fall through to the full rebuild again.
	roster[1].Players = append(roster[1].Players, rosterpkg.FestRosterImportPlayer{RatingID: 1004, FirstName: "Глеб", LastName: "Четвёртый"})
	third, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 13533, roster)
	if err != nil {
		t.Fatalf("third import: %v", err)
	}
	if third.Unchanged {
		t.Fatalf("roster with an added player must not be treated as unchanged: %#v", third)
	}
	if third.PlayerCount != 4 {
		t.Fatalf("after adding a player, PlayerCount = %d, want 4", third.PlayerCount)
	}
}

func TestImportFestRosterIncrementalKeepsPlayerIDsStable(t *testing.T) {
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

	playerID := func(rating int64) (int64, bool) {
		var id int64
		err := db.QueryRow(`select id from fest_players where fest_id = ? and rating_id = ?`, festID, rating).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false
		}
		if err != nil {
			t.Fatalf("player id lookup rating=%d: %v", rating, err)
		}
		return id, true
	}
	teamPlayers := func(rating int64) []string {
		names, err := store.CollectRows(t.Context(), db, `
select p.first_name from fest_team_players ftp
join fest_teams tt on tt.id = ftp.team_id
join fest_players p on p.id = ftp.player_id
where tt.fest_id = ? and tt.rating_id = ? and tt.deleted = 0
order by ftp.roster_order`, []any{festID, rating}, func(rows *sql.Rows) (string, error) {
			var n string
			return n, rows.Scan(&n)
		})
		if err != nil {
			t.Fatalf("team players rating=%d: %v", rating, err)
		}
		return names
	}

	roster := []rosterpkg.FestRosterImportTeam{
		{RatingID: 101, Name: "Первая", City: "Москва", Players: []rosterpkg.FestRosterImportPlayer{
			{RatingID: 1002, FirstName: "Борис", LastName: "Второй"},
			{RatingID: 1001, FirstName: "Анна", LastName: "Первая"},
		}},
		{RatingID: 102, Name: "Вторая", City: "Казань", Players: []rosterpkg.FestRosterImportPlayer{
			{RatingID: 1003, FirstName: "Вера", LastName: "Третья"},
		}},
	}
	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 13533, roster); err != nil {
		t.Fatalf("initial import: %v", err)
	}
	id1001, _ := playerID(1001)
	id1002, _ := playerID(1002)
	id1003, _ := playerID(1003)
	team101ID, _ := festTeamID(t, db, festID, 101)

	// Add a player to team 102; everyone else's row id must be untouched.
	roster[1].Players = append(roster[1].Players, rosterpkg.FestRosterImportPlayer{RatingID: 1004, FirstName: "Глеб", LastName: "Четвёртый"})
	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 13533, roster); err != nil {
		t.Fatalf("add-player import: %v", err)
	}
	if got, _ := playerID(1001); got != id1001 {
		t.Fatalf("player 1001 id changed %d -> %d on add (must stay stable)", id1001, got)
	}
	if got, _ := playerID(1003); got != id1003 {
		t.Fatalf("player 1003 id changed %d -> %d on add (must stay stable)", id1003, got)
	}
	if _, ok := playerID(1004); !ok {
		t.Fatalf("added player 1004 not inserted")
	}
	if names := teamPlayers(102); len(names) != 2 {
		t.Fatalf("team 102 players after add = %v, want 2", names)
	}

	// Remove a player from team 101; their fest_players row must be gone, the
	// other kept stable, the team row id unchanged.
	roster[0].Players = roster[0].Players[:1] // drop "Анна" (1001) — leaves "Борис" (1002)
	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 13533, roster); err != nil {
		t.Fatalf("remove-player import: %v", err)
	}
	if _, ok := playerID(1001); ok {
		t.Fatalf("removed player 1001 should be deleted from fest_players")
	}
	if got, _ := playerID(1002); got != id1002 {
		t.Fatalf("player 1002 id changed %d -> %d on remove (must stay stable)", id1002, got)
	}
	if names := teamPlayers(101); len(names) != 1 || names[0] != "Борис" {
		t.Fatalf("team 101 players after remove = %v, want [Борис]", names)
	}

	// Rename team 101; same fest_teams row id, new name.
	roster[0].Name = "Первая-2"
	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 13533, roster); err != nil {
		t.Fatalf("rename import: %v", err)
	}
	if got, _ := festTeamID(t, db, festID, 101); got != team101ID {
		t.Fatalf("team 101 row id changed %d -> %d on rename (must stay stable)", team101ID, got)
	}
	var name101 string
	if err := db.QueryRow(`select name from fest_teams where id = ?`, team101ID).Scan(&name101); err != nil {
		t.Fatalf("name lookup: %v", err)
	}
	if name101 != "Первая-2" {
		t.Fatalf("team 101 name = %q, want renamed", name101)
	}

	// Drop team 102 entirely: soft-deleted, roster links cleared.
	roster = roster[:1]
	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 13533, roster); err != nil {
		t.Fatalf("drop-team import: %v", err)
	}
	var deleted102, links102 int
	if err := db.QueryRow(`select deleted from fest_teams where fest_id = ? and rating_id = 102`, festID).Scan(&deleted102); err != nil {
		t.Fatalf("deleted lookup: %v", err)
	}
	if deleted102 != 1 {
		t.Fatalf("team 102 deleted = %d, want soft-deleted (1)", deleted102)
	}
	if err := db.QueryRow(`select count(*) from fest_team_players ftp join fest_teams tt on tt.id = ftp.team_id where tt.fest_id = ? and tt.rating_id = 102`, festID).Scan(&links102); err != nil {
		t.Fatalf("links lookup: %v", err)
	}
	if links102 != 0 {
		t.Fatalf("soft-deleted team 102 still has %d roster links, want 0", links102)
	}
}

func festTeamID(t *testing.T, db *sql.DB, festID, rating int64) (int64, bool) {
	t.Helper()
	var id int64
	err := db.QueryRow(`select id from fest_teams where fest_id = ? and rating_id = ?`, festID, rating).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false
	}
	if err != nil {
		t.Fatalf("team id lookup rating=%d: %v", rating, err)
	}
	return id, true
}

func TestImportFestRosterPreservesPlayerTeamOverrides(t *testing.T) {
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID, _, ksiGameID := createRosterPropagationFixture(t, db)
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
	})
	roster := []rosterpkg.FestRosterImportTeam{
		{
			RatingID: 101,
			Name:     "Первая",
			City:     "Москва",
			Players: []rosterpkg.FestRosterImportPlayer{
				{RatingID: 1001, FirstName: "Анна", LastName: "Первая"},
			},
		},
		{
			RatingID: 102,
			Name:     "Вторая",
			City:     "Казань",
			Players: []rosterpkg.FestRosterImportPlayer{
				{RatingID: 1002, FirstName: "Борис", LastName: "Второй"},
			},
		},
	}
	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 13533, roster); err != nil {
		t.Fatalf("initial import roster: %v", err)
	}

	var playerID, overrideTeamID int64
	if err := db.QueryRow(`select id from fest_players where fest_id = ? and rating_id = 1001`, festID).Scan(&playerID); err != nil {
		t.Fatalf("load player: %v", err)
	}
	if err := db.QueryRow(`select id from fest_teams where fest_id = ? and rating_id = 102`, festID).Scan(&overrideTeamID); err != nil {
		t.Fatalf("load override team: %v", err)
	}
	if _, _, err := overrides.SavePlayerTeamOverride(srv, t.Context(), festID, playerID, overrideTeamID, []int64{ksiGameID}); err != nil {
		t.Fatalf("save override: %v", err)
	}

	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 13533, roster); err != nil {
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
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID, _, ksiGameID := createRosterPropagationFixture(t, db)
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
	})
	roster := []rosterpkg.FestRosterImportTeam{
		{
			RatingID: 101,
			Name:     "Команда добра и позитива",
			City:     "Москва",
			Players: []rosterpkg.FestRosterImportPlayer{
				{RatingID: 1001, FirstName: "Василиса", LastName: "Павлейчук"},
			},
		},
		{RatingID: 102, Name: "Bikes for Peace", City: "Москва"},
	}
	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 13533, roster); err != nil {
		t.Fatalf("import roster: %v", err)
	}
	if _, err := db.Exec(`update games set title = 'КСИ' where id = ?`, ksiGameID); err != nil {
		t.Fatalf("rename ksi game: %v", err)
	}
	now := util.UtcNow()
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

	options, err := overrides.LoadHostPlayerOverrideGameOptions(t.Context(), db, festID)
	if err != nil {
		t.Fatalf("load game options: %v", err)
	}
	if len(options) != 2 || options[0].Label != "КСИ" || options[1].Label != "ЭК" {
		t.Fatalf("game option labels = %#v, want КСИ/ЭК", options)
	}
	rows, err := overrides.LoadHostPlayerOverrideRows(t.Context(), db, festID)
	if err != nil {
		t.Fatalf("load override rows: %v", err)
	}
	if len(rows) != 1 || rows[0].Player != "Василиса Павлейчук" || rows[0].SourceTeam != "Команда добра и позитива" || rows[0].OverrideTeam != "Bikes for Peace" || rows[0].Games != "КСИ, ЭК" {
		t.Fatalf("override rows = %#v, want one grouped row", rows)
	}
	if !rows[0].HasGame(ksiGameID) || !rows[0].HasGame(ekGameID) {
		t.Fatalf("row game ids = %#v, want both games", rows[0].GameIDs)
	}

	if _, _, err := overrides.ReplacePlayerTeamOverride(srv, t.Context(), festID, playerID, sourceTeamID, overrideTeamID, []int64{ekGameID}); err != nil {
		t.Fatalf("replace override games: %v", err)
	}
	rows, err = overrides.LoadHostPlayerOverrideRows(t.Context(), db, festID)
	if err != nil {
		t.Fatalf("reload override rows: %v", err)
	}
	if len(rows) != 1 || rows[0].Games != "ЭК" || rows[0].HasGame(ksiGameID) || !rows[0].HasGame(ekGameID) {
		t.Fatalf("rows after replace = %#v, want only ЭК", rows)
	}

	if _, _, err := overrides.ReplacePlayerTeamOverride(srv, t.Context(), festID, playerID, sourceTeamID, overrideTeamID, nil); err != nil {
		t.Fatalf("delete override games: %v", err)
	}
	rows, err = overrides.LoadHostPlayerOverrideRows(t.Context(), db, festID)
	if err != nil {
		t.Fatalf("reload deleted rows: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows after delete = %#v, want none", rows)
	}
}

func TestFestNumbersFlow(t *testing.T) {
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID, chgkGameID, _ := createRosterPropagationFixture(t, db)
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
	})
	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 999, []rosterpkg.FestRosterImportTeam{
		{RatingID: 11, Name: "Алёша", City: "А"},
		{RatingID: 12, Name: "Боря", City: "Б"},
		{RatingID: 13, Name: "Витя", City: "В"},
	}); err != nil {
		t.Fatalf("import roster: %v", err)
	}

	// After roster import every team is numbered (1..N alphabetically).
	allSet, total, err := numbering.AllNumbered(t.Context(), db, festID)
	if err != nil {
		t.Fatalf("allNumbered: %v", err)
	}
	if total != 3 || !allSet {
		t.Fatalf("after import: total=%d allSet=%v, want 3/true", total, allSet)
	}

	// Auto-assign numbers by alphabet (idempotent here — import already did this).
	teams, err := numbering.LoadFestTeams(t.Context(), db, festID)
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
	if err := srv.PageServer().SaveFestNumbers(t.Context(), festID, assignments); err != nil {
		t.Fatalf("save numbers: %v", err)
	}

	allSet, _, err = numbering.AllNumbered(t.Context(), db, festID)
	if err != nil {
		t.Fatalf("allNumbered post-save: %v", err)
	}
	if !allSet {
		t.Fatalf("after save: allSet should be true")
	}

	// Verify OD state.teams now carries the assigned numbers.
	var stateJSON string
	if err := db.QueryRow(`select coalesce((select m.state_json from matches m where m.game_id = games.id and m.code = 'main'), '{}') from games where id = ?`, chgkGameID).Scan(&stateJSON); err != nil {
		t.Fatalf("load od state: %v", err)
	}
	var state struct {
		Teams []rosterpkg.ChgkTeamJSON `json:"teams"`
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
	if err := srv.PageServer().SaveFestNumbers(t.Context(), festID, nil); err != nil {
		t.Fatalf("clear numbers: %v", err)
	}
	allSet, _, err = numbering.AllNumbered(t.Context(), db, festID)
	if err != nil {
		t.Fatalf("allNumbered post-clear: %v", err)
	}
	if allSet {
		t.Fatalf("after clear: allSet should be false")
	}
	if err := db.QueryRow(`select coalesce((select m.state_json from matches m where m.game_id = games.id and m.code = 'main'), '{}') from games where id = ?`, chgkGameID).Scan(&stateJSON); err != nil {
		t.Fatalf("load od state after clear: %v", err)
	}
	state = struct {
		Teams []rosterpkg.ChgkTeamJSON `json:"teams"`
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
	festID, chgkGameID, _ := createRosterPropagationFixture(t, srv.Eng().DB)
	organizerID, token := createAPITestSession(t, srv, "numbers-host")
	addAPITestOrganizer(t, srv, festID, organizerID)

	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 1, []rosterpkg.FestRosterImportTeam{
		{RatingID: 11, Name: "Алёша"},
		{RatingID: 12, Name: "Боря"},
	}); err != nil {
		t.Fatalf("import roster: %v", err)
	}

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
		resp := httptest.NewRecorder()
		srv.HostPageServer().HandleHostRouter(resp, req)
		return resp
	}
	post := func(path string, form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
		resp := httptest.NewRecorder()
		srv.HostPageServer().HandleHostRouter(resp, req)
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

	teams, err := numbering.LoadFestTeams(t.Context(), srv.Eng().DB, festID)
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
	byNum := func() (lo, hi numbering.Team) {
		teams, err := numbering.LoadFestTeams(t.Context(), srv.Eng().DB, festID)
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
	if err := srv.Eng().DB.QueryRow(`select number from fest_teams where id = ?`, low.ID).Scan(&num1); err != nil {
		t.Fatalf("load num1: %v", err)
	}
	if err := srv.Eng().DB.QueryRow(`select number from fest_teams where id = ?`, high.ID).Scan(&num2); err != nil {
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
	if err := srv.Eng().DB.QueryRow(`select number from fest_teams where id = ?`, low.ID).Scan(&num1); err != nil {
		t.Fatalf("load num after reserve: %v", err)
	}
	if num1 != 101 {
		t.Fatalf("after reserve: low.number=%d, want 101", num1)
	}

	// Verify state.teams in the chgk game contains the reassigned numbers.
	var stateJSON string
	if err := srv.Eng().DB.QueryRow(`select coalesce((select m.state_json from matches m where m.game_id = games.id and m.code = 'main'), '{}') from games where id = ?`, chgkGameID).Scan(&stateJSON); err != nil {
		t.Fatalf("load chgk state: %v", err)
	}
	var state struct {
		Teams []rosterpkg.ChgkTeamJSON `json:"teams"`
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
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID, chgkGameID, _ := createRosterPropagationFixture(t, db)
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
	})
	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 1, []rosterpkg.FestRosterImportTeam{
		{RatingID: 11, Name: "Алёша"},
		{RatingID: 12, Name: "Боря"},
		{RatingID: 13, Name: "Витя"},
	}); err != nil {
		t.Fatalf("import roster: %v", err)
	}
	teams, err := numbering.LoadFestTeams(t.Context(), db, festID)
	if err != nil {
		t.Fatalf("load teams: %v", err)
	}
	if len(teams) != 3 {
		t.Fatalf("teams=%d, want 3", len(teams))
	}
	// Initial 1..3 alphabetical numbering.
	initial := map[int64]int{teams[0].ID: 1, teams[1].ID: 2, teams[2].ID: 3}
	if err := srv.PageServer().SaveFestNumbers(t.Context(), festID, initial); err != nil {
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
update matches
set state_json = json_set(state_json, '$.entries', json(?), '$.shootoutRounds', json(?))
where game_id = ? and code = 'main'`, string(entriesJSON), string(shootoutRoundsJSON), chgkGameID); err != nil {
		t.Fatalf("seed entries: %v", err)
	}

	// Reassign: team[0] gets reserve 101 (was 1), team[1] gets 1 (was 2), team[2] keeps 3.
	reassign := map[int64]int{teams[0].ID: 101, teams[1].ID: 1, teams[2].ID: 3}
	if err := srv.PageServer().SaveFestNumbers(t.Context(), festID, reassign); err != nil {
		t.Fatalf("reassign: %v", err)
	}

	var stateJSON string
	if err := db.QueryRow(`select coalesce((select m.state_json from matches m where m.game_id = games.id and m.code = 'main'), '{}') from games where id = ?`, chgkGameID).Scan(&stateJSON); err != nil {
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

// TestFestNumbersPropagateToKSI guards the bug where a number reassignment
// flowed into OD states but not KSI: KSI participants kept their stale numbers
// (e.g. the 1..N auto-assigned at import) while OD showed the corrected ones.
// The reassignment must update KSI participant numbers and carry each team's
// answers along by name (since the number itself changed).
func TestFestNumbersPropagateToKSI(t *testing.T) {
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID, _, ksiGameID := createRosterPropagationFixture(t, db)
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
	})
	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 1, []rosterpkg.FestRosterImportTeam{
		{RatingID: 11, Name: "Алёша"},
		{RatingID: 12, Name: "Боря"},
		{RatingID: 13, Name: "Витя"},
	}); err != nil {
		t.Fatalf("import roster: %v", err)
	}

	// Mark a right answer for Боря (alphabetical row 1) before renumbering.
	if _, err := db.Exec(`
update matches
set state_json = json_set(state_json, '$.themes[0].answers', json(?))
where game_id = ? and code = 'main'`, `[["",""],["right",""],["",""]]`, ksiGameID); err != nil {
		t.Fatalf("seed answers: %v", err)
	}

	teams, err := numbering.LoadFestTeams(t.Context(), db, festID)
	if err != nil {
		t.Fatalf("load teams: %v", err)
	}
	byName := map[string]int64{}
	for _, team := range teams {
		byName[team.Name] = team.ID
	}
	// Reassign to numbers far from the 1..3 import default so a stale KSI state
	// is unmistakable.
	reassign := map[int64]int{byName["Алёша"]: 201, byName["Боря"]: 202, byName["Витя"]: 203}
	if err := srv.PageServer().SaveFestNumbers(t.Context(), festID, reassign); err != nil {
		t.Fatalf("reassign: %v", err)
	}

	var stateJSON string
	if err := db.QueryRow(`select coalesce((select m.state_json from matches m where m.game_id = games.id and m.code = 'main'), '{}') from games where id = ?`, ksiGameID).Scan(&stateJSON); err != nil {
		t.Fatalf("load ksi state: %v", err)
	}
	var got struct {
		Participants []games.KSIParticipant `json:"participants"`
		Themes       []struct {
			Answers [][]string `json:"answers"`
		} `json:"themes"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &got); err != nil {
		t.Fatalf("decode ksi state: %v", err)
	}
	wantNum := map[string]int{"Алёша": 201, "Боря": 202, "Витя": 203}
	for _, p := range got.Participants {
		if wantNum[p.Name] != p.Number {
			t.Fatalf("participant %q number=%d, want %d (participants=%+v)", p.Name, p.Number, wantNum[p.Name], got.Participants)
		}
	}
	// Боря's right answer follows him by name across the renumber.
	boryRow := -1
	for i, p := range got.Participants {
		if p.Name == "Боря" {
			boryRow = i
		}
	}
	if boryRow < 0 || len(got.Themes) == 0 || got.Themes[0].Answers[boryRow][0] != "right" {
		t.Fatalf("Боря answer not preserved: row=%d themes=%+v", boryRow, got.Themes)
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

// TestFestNumbersStableAcrossResync verifies that re-importing a roster keeps
// previously assigned numbers — a team that leaves the roster takes its slot
// out of circulation (no automatic renumbering) so already-printed answer
// sheets still point at the right team.
func TestFestNumbersStableAcrossResync(t *testing.T) {
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

	// Initial import: 5 teams "А".."Д" with rating IDs 11..15.
	initial := []rosterpkg.FestRosterImportTeam{
		{RatingID: 11, Name: "Алёша", City: "А"},
		{RatingID: 12, Name: "Боря", City: "Б"},
		{RatingID: 13, Name: "Витя", City: "В"},
		{RatingID: 14, Name: "Гена", City: "Г"},
		{RatingID: 15, Name: "Дима", City: "Д"},
	}
	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 999, initial); err != nil {
		t.Fatalf("initial import: %v", err)
	}
	teams, err := numbering.LoadFestTeams(t.Context(), db, festID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(teams) != 5 {
		t.Fatalf("teams = %d, want 5", len(teams))
	}
	// Assign numbers 1..5 alphabetically (which matches numbering.LoadFestTeams ordering).
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
	if err := srv.PageServer().SaveFestNumbers(t.Context(), festID, assignments); err != nil {
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

	// Case 1: team Гена (rating 14, number 4) leaves the rating rosterpkg.
	// Expected: remaining teams keep 1, 2, 3, 5 — no renumbering.
	without14 := []rosterpkg.FestRosterImportTeam{
		{RatingID: 11, Name: "Алёша", City: "А"},
		{RatingID: 12, Name: "Боря", City: "Б"},
		{RatingID: 13, Name: "Витя", City: "В"},
		{RatingID: 15, Name: "Дима", City: "Д"},
	}
	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 999, without14); err != nil {
		t.Fatalf("resync without 14: %v", err)
	}
	checkNumbers("after Гена leaves", map[int64]int64{11: 1, 12: 2, 13: 3, 15: 5})

	// Case 2: still without Гена, but two new teams join.
	// Expected: existing keep 1, 2, 3, 5; newcomers receive 6 and 7 (strictly
	// greater than the largest number ever assigned, never filling the 4-gap).
	withNewcomers := []rosterpkg.FestRosterImportTeam{
		{RatingID: 11, Name: "Алёша", City: "А"},
		{RatingID: 12, Name: "Боря", City: "Б"},
		{RatingID: 13, Name: "Витя", City: "В"},
		{RatingID: 15, Name: "Дима", City: "Д"},
		{RatingID: 16, Name: "Егор", City: "Е"},
		{RatingID: 17, Name: "Жора", City: "Ж"},
	}
	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 999, withNewcomers); err != nil {
		t.Fatalf("resync with newcomers: %v", err)
	}
	checkNumbers("after newcomers join", map[int64]int64{11: 1, 12: 2, 13: 3, 15: 5, 16: 6, 17: 7})

	// Case 3: Гена returns. Expected: her old number 4 comes back because the
	// soft-deleted row preserved it.
	withGenaBack := []rosterpkg.FestRosterImportTeam{
		{RatingID: 11, Name: "Алёша", City: "А"},
		{RatingID: 12, Name: "Боря", City: "Б"},
		{RatingID: 13, Name: "Витя", City: "В"},
		{RatingID: 14, Name: "Гена", City: "Г"},
		{RatingID: 15, Name: "Дима", City: "Д"},
		{RatingID: 16, Name: "Егор", City: "Е"},
		{RatingID: 17, Name: "Жора", City: "Ж"},
	}
	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 999, withGenaBack); err != nil {
		t.Fatalf("resync with Гена back: %v", err)
	}
	checkNumbers("after Гена returns", map[int64]int64{11: 1, 12: 2, 13: 3, 14: 4, 15: 5, 16: 6, 17: 7})

	// Case 4: fresh "assign numbers" (auto) must wipe everything and renumber
	// 1..N alphabetically over the current active roster, including discarding
	// any leftover soft-deleted rows so their archived numbers don't reserve
	// slots.
	autoResp := httptest.NewRecorder()
	autoReq := httptest.NewRequest(http.MethodPost, "/host/fest/"+itoa(festID)+"/numbers/auto", nil)
	srv.PageServer().HandleHostAutoFestNumbers(autoResp, autoReq, festID)
	if autoResp.Code != http.StatusOK {
		t.Fatalf("auto status %d", autoResp.Code)
	}
	checkNumbers("after auto-assign", map[int64]int64{11: 1, 12: 2, 13: 3, 14: 4, 15: 5, 16: 6, 17: 7})
}

// TestFestNumbersFreshImport ensures that a first-ever import now numbers every
// team (team number is the universal identity, so every active team must have
// one), deterministically by alphabetical order, and that a later import keeps
// existing numbers and continues past the largest one for new teams.
func TestFestNumbersFreshImport(t *testing.T) {
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

	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 999, []rosterpkg.FestRosterImportTeam{
		{RatingID: 11, Name: "Алёша"},
		{RatingID: 12, Name: "Боря"},
	}); err != nil {
		t.Fatalf("first import: %v", err)
	}
	allSet, total, err := numbering.AllNumbered(t.Context(), db, festID)
	if err != nil {
		t.Fatalf("allNumbered: %v", err)
	}
	if total != 2 || !allSet {
		t.Fatalf("after fresh import: total=%d allSet=%v, want 2/true", total, allSet)
	}
	checkFestTeamNumber(t, db, festID, 11, 1)
	checkFestTeamNumber(t, db, festID, 12, 2)

	// A second import keeps the existing numbers and assigns the new team the
	// next number past the largest one seen.
	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 999, []rosterpkg.FestRosterImportTeam{
		{RatingID: 11, Name: "Алёша"},
		{RatingID: 12, Name: "Боря"},
		{RatingID: 13, Name: "Витя"},
	}); err != nil {
		t.Fatalf("second import: %v", err)
	}
	allSet, total, err = numbering.AllNumbered(t.Context(), db, festID)
	if err != nil {
		t.Fatalf("allNumbered: %v", err)
	}
	if total != 3 || !allSet {
		t.Fatalf("after second import: total=%d allSet=%v, want 3/true", total, allSet)
	}
	checkFestTeamNumber(t, db, festID, 11, 1)
	checkFestTeamNumber(t, db, festID, 12, 2)
	checkFestTeamNumber(t, db, festID, 13, 3)
}

// checkFestTeamNumber asserts the active fest_team with the given rating_id has
// the expected number.
func checkFestTeamNumber(t *testing.T, db *sql.DB, festID, ratingID, want int64) {
	t.Helper()
	var got sql.NullInt64
	if err := db.QueryRow(`select number from fest_teams where fest_id = ? and rating_id = ? and deleted = 0`, festID, ratingID).Scan(&got); err != nil {
		t.Fatalf("number for rating_id %d: %v", ratingID, err)
	}
	if !got.Valid || got.Int64 != want {
		t.Fatalf("rating_id %d number = %v, want %d", ratingID, got, want)
	}
}

// TestBackfillFestTeamNumbers covers the v13 migration helper: every active
// unnumbered team gets a fresh number past the largest ever seen (soft-deleted
// rows counted), in (position, id) order, leaving already-numbered teams alone.
func TestBackfillFestTeamNumbers(t *testing.T) {
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID, _, _ := createRosterPropagationFixture(t, db)
	ctx := t.Context()
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	ins := `insert into fest_teams(fest_id, rating_id, name, city, position, number, deleted) values(?, ?, ?, '', ?, ?, ?)`
	mustExec(ins, festID, 101, "A", 1, 5, 0)   // active, already numbered
	mustExec(ins, festID, 102, "B", 2, nil, 0) // active, unnumbered
	mustExec(ins, festID, 103, "C", 3, nil, 0) // active, unnumbered
	mustExec(ins, festID, 104, "D", 4, 9, 1)   // soft-deleted, number 9 (counts toward maxSeen)

	if err := dopeserver.BackfillFestTeamNumbers(db); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	// maxSeen = 9 (soft-deleted included), so the two unnumbered teams continue at
	// 10 and 11 in position order; the already-numbered team is untouched.
	checkFestTeamNumber(t, db, festID, 101, 5)
	checkFestTeamNumber(t, db, festID, 102, 10)
	checkFestTeamNumber(t, db, festID, 103, 11)

	allSet, total, err := numbering.AllNumbered(ctx, db, festID)
	if err != nil {
		t.Fatalf("allNumbered: %v", err)
	}
	if !allSet || total != 3 {
		t.Fatalf("after backfill: total=%d allSet=%v, want 3/true", total, allSet)
	}

	var collisions int
	if err := db.QueryRow(`select count(*) - count(distinct number) from fest_teams where fest_id = ? and deleted = 0 and number is not null`, festID).Scan(&collisions); err != nil {
		t.Fatalf("collision check: %v", err)
	}
	if collisions != 0 {
		t.Fatalf("active teams share a number (%d collisions)", collisions)
	}
}

func TestAuthCodeHelpers(t *testing.T) {
	a, err := dopeserver.NewInviteCode()
	if err != nil {
		t.Fatalf("invite code: %v", err)
	}
	b, err := dopeserver.NewInviteCode()
	if err != nil {
		t.Fatalf("invite code: %v", err)
	}
	if a == b || a == "" {
		t.Fatalf("invite codes collide or empty: %q vs %q", a, b)
	}
	tok, err := dopeserver.NewSessionToken()
	if err != nil {
		t.Fatalf("session token: %v", err)
	}
	if authcred.HashSessionToken(tok) == tok {
		t.Fatal("session hash should differ from token")
	}
	if authcred.HashSessionToken(tok) != authcred.HashSessionToken(tok) {
		t.Fatal("session hash should be deterministic")
	}
}

func TestVersionAssetRefs(t *testing.T) {
	s := dopeserver.NewTestServer(func(e *core.Engine) {
		e.AssetETags = map[string]string{
			"/static/host.js":    `"abc123"`,
			"/static/styles.css": `"def456"`,
		}
	})
	in := []byte(`<link rel="stylesheet" href="/static/styles.css">` +
		`<link rel="preload" href="/static/fonts/x.woff2">` +
		`<script defer src="/static/host.js"></script>` +
		`<script defer src="/static/unknown.js"></script>` +
		`<script defer src="/static/host.js?v=stale"></script>`)
	out := string(s.VersionAssetRefs(in))
	if !strings.Contains(out, `href="/static/styles.css?v=def456"`) {
		t.Fatalf("css not versioned: %s", out)
	}
	if !strings.Contains(out, `src="/static/host.js?v=abc123"`) {
		t.Fatalf("js not versioned: %s", out)
	}
	if strings.Contains(out, "woff2?v=") {
		t.Fatalf("font (non js/css) must be untouched: %s", out)
	}
	if strings.Contains(out, "unknown.js?v=") {
		t.Fatalf("asset with no known etag must be untouched: %s", out)
	}
	// Already-versioned URL must not be double-stamped.
	if strings.Contains(out, "v=stale?v=") || strings.Contains(out, "host.js?v=abc123?") {
		t.Fatalf("already-versioned URL was double-stamped: %s", out)
	}
	if !strings.Contains(out, `src="/static/host.js?v=stale"`) {
		t.Fatalf("already-versioned URL should be left as-is: %s", out)
	}
	// Disk mode (no etags) is a no-op.
	bare := dopeserver.NewTestServer(nil)
	if got := string(bare.VersionAssetRefs(in)); got != string(in) {
		t.Fatalf("disk-mode versionAssetRefs should be a no-op")
	}
}

func TestServeStaticPageVersionsAndNoCache(t *testing.T) {
	html := `<!doctype html><link rel="stylesheet" href="/static/styles.css">` +
		`<script defer src="/static/login.js"></script>`
	src := fstest.MapFS{"static/login.html": &fstest.MapFile{Data: []byte(html)}}
	s := dopeserver.NewTestServer(func(e *core.Engine) {
		e.AssetETags = map[string]string{
			"/static/login.js":   `"j1"`,
			"/static/styles.css": `"c1"`,
		}
	})
	rec := httptest.NewRecorder()
	s.ServeStaticPage(src, "static/login.html").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `src="/static/login.js?v=j1"`) || !strings.Contains(body, `href="/static/styles.css?v=c1"`) {
		t.Fatalf("assets not versioned: %s", body)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", cc)
	}
}
