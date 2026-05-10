package main

import (
	"database/sql"
	"encoding/json"
	"os"
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
	db, err := openTournamentDB("test.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	tournamentID, err := bootstrapDefaultTournament(db, defaultMatch())
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	gameID, err := defaultGameID(t.Context(), db, tournamentID)
	if err != nil {
		t.Fatalf("default game: %v", err)
	}

	srv := &server{
		db:              db,
		tournamentID:    tournamentID,
		activeGameID:    gameID,
		activeMatchCode: defaultMatchCode,
		subscribers:     make(map[chan event]struct{}),
	}

	view, err := srv.loadMatchViewLocked(tournamentID, defaultMatchCode)
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
	view, _, err = srv.applyMatchUpdate(tournamentID, defaultMatchCode, updateRequest{
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

	reloaded, err := srv.loadMatchViewLocked(tournamentID, defaultMatchCode)
	if err != nil {
		t.Fatalf("reload match: %v", err)
	}
	if reloaded.Teams[2].Themes[0].Answers[0] != "right" {
		t.Fatalf("persisted mark = %q, want right", reloaded.Teams[2].Themes[0].Answers[0])
	}
}

func TestSQLiteVenuesAndRosterLimit(t *testing.T) {
	t.Chdir(t.TempDir())
	db, err := openTournamentDB("test.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	tournamentID, err := bootstrapDefaultTournament(db, defaultMatch())
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	gameID, err := defaultGameID(t.Context(), db, tournamentID)
	if err != nil {
		t.Fatalf("default game: %v", err)
	}

	srv := &server{
		db:              db,
		tournamentID:    tournamentID,
		activeGameID:    gameID,
		activeMatchCode: defaultMatchCode,
		subscribers:     make(map[chan event]struct{}),
	}

	venues, _, err := srv.updateVenue(tournamentID, 1, "Рим")
	if err != nil {
		t.Fatalf("update venue: %v", err)
	}
	if len(venues) != 1 || venues[0].Title != "Рим" {
		t.Fatalf("venues = %#v, want renamed venue", venues)
	}
	view, err := srv.loadMatchViewLocked(tournamentID, defaultMatchCode)
	if err != nil {
		t.Fatalf("load match: %v", err)
	}
	if view.Venue == nil || view.Venue.Title != "Рим" {
		t.Fatalf("match venue = %#v, want Рим", view.Venue)
	}

	var teamID int64
	if err := db.QueryRow(`select id from teams where tournament_id = ? order by id limit 1`, tournamentID).Scan(&teamID); err != nil {
		t.Fatalf("team id: %v", err)
	}
	for i := 0; i < 3; i++ {
		playerID, err := insertTestPlayer(db, tournamentID)
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

func insertTestPlayer(db *sql.DB, tournamentID int64) (int64, error) {
	result, err := db.Exec(`insert into players(tournament_id, first_name, last_name) values(?, 'Тест', 'Игрок')`, tournamentID)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func TestImportStudchrScheme(t *testing.T) {
	db, err := openTournamentDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	data, err := os.ReadFile("static/schemes/studchr-ek-2026.json")
	if err != nil {
		t.Fatalf("read scheme: %v", err)
	}
	var scheme tournamentScheme
	if err := json.Unmarshal(data, &scheme); err != nil {
		t.Fatalf("decode scheme: %v", err)
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
	if view.Slug != "studchr-ek-2026" {
		t.Fatalf("slug = %q, want studchr-ek-2026", view.Slug)
	}
	if len(view.Stages) != 6 {
		t.Fatalf("stages = %d, want 6", len(view.Stages))
	}
	if len(view.Stages[0].Matches) != 6 || len(view.Stages[1].Matches) != 6 {
		t.Fatalf("1/16 runs = %d/%d, want 6/6", len(view.Stages[0].Matches), len(view.Stages[1].Matches))
	}
	if view.Stages[0].Matches[0].Teams[0].Name != "ВШЭстером" {
		t.Fatalf("first team = %q, want ВШЭстером", view.Stages[0].Matches[0].Teams[0].Name)
	}
	if view.Stages[1].Matches[0].Code != "G" || view.Stages[1].Matches[0].Teams[0].Name != "Дахусим" {
		t.Fatalf("second run starts with %#v, want G / Дахусим", view.Stages[1].Matches[0])
	}
	final := view.Stages[len(view.Stages)-1]
	if len(final.Matches) != 1 || final.Matches[0].Code != "Y" {
		t.Fatalf("final = %#v, want match Y", final.Matches)
	}
}

func TestEmptyDatabaseHasNoTournament(t *testing.T) {
	db, err := openTournamentDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	tournamentID, gameID, matchCode, err := loadActiveContext(db)
	if err != nil {
		t.Fatalf("loadActiveContext: %v", err)
	}
	if tournamentID != 0 || gameID != 0 || matchCode != "" {
		t.Fatalf("empty db produced (%d, %d, %q), want zero values", tournamentID, gameID, matchCode)
	}
	srv := &server{db: db, subscribers: make(map[chan event]struct{})}
	view, err := srv.loadTournamentViewLocked(0, 0)
	if err != nil {
		t.Fatalf("loadTournamentViewLocked: %v", err)
	}
	if view.Slug != "" || len(view.Stages) != 0 {
		t.Fatalf("empty view = %#v, want zero", view)
	}
}

func TestImportRejectsTeamSlot(t *testing.T) {
	db, err := openTournamentDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	srv := &server{db: db, subscribers: make(map[chan event]struct{})}
	scheme := tournamentScheme{
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
	db, err := openTournamentDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	srv := &server{db: db, subscribers: make(map[chan event]struct{})}
	scheme := tournamentScheme{
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
	db, err := openTournamentDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	srv := &server{db: db, subscribers: make(map[chan event]struct{})}
	scheme := tournamentScheme{
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
	if err := db.QueryRow(`select count(*) from tournament_organizers`).Scan(&organizers); err != nil {
		t.Fatalf("count organizers: %v", err)
	}
	if organizers != 1 {
		t.Fatalf("tournament_organizers = %d, want 1", organizers)
	}
	var games int
	if err := db.QueryRow(`select count(*) from games`).Scan(&games); err != nil {
		t.Fatalf("count games: %v", err)
	}
	if games != 1 {
		t.Fatalf("games = %d, want 1", games)
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
