package buffdb

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func fixture(t *testing.T) *Store { return fixtureAt(t, filepath.Join(t.TempDir(), "buff.db")) }

func fixtureAt(t *testing.T, path string) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
create table players(id integer primary key, name text, patronymic text, surname text);
create table tournaments(id integer primary key, name text, date_start text, date_end text, tournament_type text, questions_by_tour text, editors text, difficulty_forecast real);
create table tournament_requests(tournament_id integer primary key, teams integer, venues integer);
create table tournament_results(id integer, team_id integer, team_current_name text, team_current_town text);
create table seasons(id integer primary key, date_start text, date_end text);
create table team_seasons(team_id integer, season_id integer, player_id integer, date_added text, date_removed text, player_number integer);

create table player_games(player_id integer primary key, games integer not null default 0);

insert into players values
  (24850, 'Александр', 'Павлович', 'Печеный'),
  (25121, 'Дмитрий', 'Владимирович', 'Плотников'),
  (25122, 'Дмитрий', 'Сергеевич', 'Плотников'),
  (25123, 'Данила', 'Игоревич', 'Плотников'),
  (3438, 'Игорь', 'Владимирович', 'Биткин'),
  (999, '100%', '_под', 'Проце_нт');

insert into player_games values (24850, 500), (25121, 412), (25122, 7), (25123, 900);

insert into tournaments values
  (10233, 'Синхрон августа', '2026-08-29T07:00:00+00:00', '2026-09-05T10:00:00+03:00', 'Синхрон', '12,12,12', '[24850, 3438]', 3.5),
  (10234, 'Асинхрон августа', '2026-08-01T00:00:00+00:00', '2026-09-30T00:00:00+00:00', 'Асинхрон', '12,12', null, null),
  (10235, 'Обычный турнир', '2026-08-28T00:00:00+00:00', '2026-09-03T00:00:00+00:00', 'Обычный', '12', '', null),
  (10100, 'Старый синхрон', '2025-01-01T00:00:00+00:00', '2025-01-07T00:00:00+00:00', 'Синхрон', '12', null, null);

insert into tournament_requests values (10233, 86, 12);

insert into tournament_results values
  (10100, 62868, 'Гей Гериллья', 'сборная'),
  (10233, 62868, 'Gay Guerrilla', 'сборная'),
  (10233, 52916, 'Неловко', 'сборная');

insert into seasons values
  (60, '2025-08-28T00:00:00+00:00', '2026-08-26T00:00:00+00:00'),
  (61, '2026-08-27T00:00:00+00:00', '2027-08-26T00:00:00+00:00');
-- 62868 declared a roster for the season under way; 70001 has not yet, and
-- 70002 has declared an empty one in both (player 0 is the mirror's sentinel).
insert into team_seasons values
  (62868, 61, 24850, '2026-08-27', null, 0), (62868, 61, 25121, '2026-08-27', null, 0),
  (70001, 61, 0, null, null, null),
  (70001, 60, 3438, '2025-08-28', null, 0), (70001, 60, 25122, '2025-08-28', null, 0),
  (70002, 61, 0, null, null, null), (70002, 60, 0, null, null, null);
`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func at(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func day(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestPlayersRankByGames(t *testing.T) {
	s := fixture(t)
	got := s.Players(context.Background(), "П", 10)
	if len(got) != 5 {
		t.Fatalf("players: %+v", got)
	}
	// Most games first; the one the mirror knows nothing about goes last.
	if got[0].ID != 25123 || got[1].ID != 24850 || got[2].ID != 25121 || got[3].ID != 25122 {
		t.Fatalf("order %+v", got)
	}
	if got[1].FullName() != "Печеный Александр Павлович" || got[1].Games != 500 {
		t.Fatalf("player %+v", got[1])
	}
	if len(s.Players(context.Background(), "", 10)) != 0 {
		t.Fatal("an empty query must suggest nothing")
	}
}

// "Plotnikov D" is a surname and a first name, not a surname nobody has.
func TestPlayersMatchNameWords(t *testing.T) {
	s := fixture(t)
	got := s.Players(context.Background(), "Плотников Д", 10)
	if len(got) != 3 {
		t.Fatalf("two words: %+v", got)
	}
	if got[0].ID != 25123 || got[1].ID != 25121 || got[2].ID != 25122 {
		t.Fatalf("order %+v", got)
	}
	if got := s.Players(context.Background(), " Плотников   Дмитрий  Влад ", 10); len(got) != 1 || got[0].ID != 25121 {
		t.Fatalf("three words: %+v", got)
	}
	if got := s.Players(context.Background(), "Плотников Я", 10); len(got) != 0 {
		t.Fatalf("no such first name: %+v", got)
	}
}

// SQLite's LIKE folds case for ASCII only, so a Cyrillic query has to be tried
// the way the mirror spells it as well as the way it was typed.
func TestSuggestsIgnoreCase(t *testing.T) {
	s := fixture(t)
	want := s.Players(context.Background(), "Пече", 10)
	if len(want) != 1 || want[0].ID != 24850 {
		t.Fatalf("as spelled: %+v", want)
	}
	for _, typed := range []string{"пече", "ПЕЧЕ", "ПеЧе", "печеный александр"} {
		if got := s.Players(context.Background(), typed, 10); len(got) != 1 || got[0].ID != 24850 {
			t.Errorf("%q: %+v", typed, got)
		}
	}
	if got := s.Teams(context.Background(), "гей", 10); len(got) != 1 || got[0].ID != 62868 {
		t.Errorf("teams: %+v", got)
	}
}

// A wildcard typed by hand is a literal, not a pattern.
func TestPlayersEscapeWildcards(t *testing.T) {
	s := fixture(t)
	if got := s.Players(context.Background(), "Проце_", 10); len(got) != 1 || got[0].ID != 999 {
		t.Fatalf("underscore: %+v", got)
	}
	if got := s.Players(context.Background(), "Проце_нт 100%", 10); len(got) != 1 {
		t.Fatalf("percent: %+v", got)
	}
	if got := s.Players(context.Background(), "Проце%", 10); len(got) != 0 {
		t.Fatalf("a literal %% must match nothing: %+v", got)
	}
}

// A mirror from before player_games still suggests, just unranked.
func TestPlayersWithoutTheGamesTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "buff.db")
	s := fixtureAt(t, path)
	writable, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writable.Exec(`drop table player_games`); err != nil {
		t.Fatal(err)
	}
	_ = writable.Close()
	got := s.Players(context.Background(), "Плотников", 10)
	if len(got) != 3 || got[0].Name != "Данила" || got[0].Games != 0 {
		t.Fatalf("alphabetical fallback: %+v", got)
	}
}

func TestTeamNameIsTheLatest(t *testing.T) {
	s := fixture(t)
	if name := s.TeamName(context.Background(), 62868); name != "Gay Guerrilla" {
		t.Fatalf("team name %q", name)
	}
	if name := s.TeamName(context.Background(), 999); name != "" {
		t.Fatalf("unknown team %q", name)
	}
	teams := s.Teams(context.Background(), "Не", 10)
	if len(teams) != 1 || teams[0].ID != 52916 {
		t.Fatalf("teams %+v", teams)
	}
}

func TestBaseRoster(t *testing.T) {
	s := fixture(t)
	roster, ok := s.BaseRoster(context.Background(), 62868, day(t, "2026-09-02"))
	if !ok || !roster[24850] || !roster[25121] || roster[3438] {
		t.Fatalf("roster %v ok=%v", roster, ok)
	}
	if _, ok := s.BaseRoster(context.Background(), 62868, day(t, "2025-09-02")); ok {
		t.Fatal("a season with no rows must read as unknown")
	}
	if _, ok := s.BaseRoster(context.Background(), 52916, day(t, "2026-09-02")); ok {
		t.Fatal("a team with no rows must read as unknown")
	}
}

// Most teams declare nothing for weeks after a season turns over, so until they
// do their last declared roster is the answer.
func TestBaseRosterFallsBackToTheLastSeasonDeclared(t *testing.T) {
	s := fixture(t)
	roster, ok := s.BaseRoster(context.Background(), 70001, day(t, "2026-09-02"))
	if !ok || !roster[3438] || !roster[25122] || roster[0] {
		t.Fatalf("previous season's roster %v ok=%v", roster, ok)
	}
	if _, ok := s.BaseRoster(context.Background(), 70002, day(t, "2026-09-02")); ok {
		t.Fatal("sentinel rows in every season are no roster at all")
	}
	// A season the team did declare is not overtaken by an older one.
	roster, ok = s.BaseRoster(context.Background(), 70001, day(t, "2026-08-01"))
	if !ok || !roster[3438] {
		t.Fatalf("season 60 roster %v ok=%v", roster, ok)
	}
}

func TestPlayableTournamentsPutSynchronsFirst(t *testing.T) {
	s := fixture(t)
	got := s.PlayableTournaments(context.Background(), day(t, "2026-09-02"))
	if len(got) != 2 || got[0].ID != 10233 || got[1].ID != 10234 {
		t.Fatalf("playable %+v", got)
	}
	// The window closes Saturday 10:00 Moscow time: an evening Slot that day
	// is past it, a morning one is not.
	if got := s.PlayableTournaments(context.Background(), at(t, "2026-09-05 18:00")); len(got) != 1 || got[0].ID != 10234 {
		t.Fatalf("after the window %+v", got)
	}
	if got := s.PlayableTournaments(context.Background(), at(t, "2026-09-05 09:59")); len(got) != 2 {
		t.Fatalf("inside the window %+v", got)
	}
	if got := s.SearchTournaments(context.Background(), "августа", at(t, "2026-09-05 18:00"), 10); len(got) != 1 || got[0].ID != 10234 {
		t.Fatalf("search after the window %+v", got)
	}
	if comp := TourComposition(got[0].QuestionsByTour); len(comp) != 3 || comp[0] != 12 {
		t.Fatalf("tour composition %v", comp)
	}
}

// The tournament field is one box: an id typed into it names that tournament
// even when the date asked about would have filtered it out.
func TestSearchTournamentsAnswersAnID(t *testing.T) {
	s := fixture(t)
	got := s.SearchTournaments(context.Background(), "10233", at(t, "2026-09-05 18:00"), 10)
	if len(got) != 1 || got[0].ID != 10233 {
		t.Fatalf("search by id %+v", got)
	}
}

func TestSearchAndLoadTournament(t *testing.T) {
	s := fixture(t)
	// Three: «Синхрон августа», «Аsync tournament августа» and «Старый sync tournament» — the
	// lowercase query finds the capitalised name too.
	if got := s.SearchTournaments(context.Background(), "синхрон", time.Time{}, 10); len(got) != 3 {
		t.Fatalf("search %+v", got)
	}
	tournament, ok := s.Tournament(context.Background(), 10233)
	if !ok || tournament.Name != "Синхрон августа" || tournament.QuestionsByTour != "12,12,12" {
		t.Fatalf("tournament %+v ok=%v", tournament, ok)
	}
}

// The picker's card is one row of the mirror: the editors named rather than
// listed as ids, the forecast, and buff's own count of the teams asked for. A
// tournament the mirror knows nothing of the sort about still answers.
// A captain who knows their team's number types it; one who does not types the
// name, in whatever case they please. Both answer, and the number first.
func TestTeamsAnswerByNameAndByID(t *testing.T) {
	s := fixture(t)
	byName := s.Teams(context.Background(), "гей", 10)
	if len(byName) != 1 || byName[0].ID != 62868 {
		t.Fatalf("by name %+v", byName)
	}
	byID := s.Teams(context.Background(), "52916", 10)
	if len(byID) != 1 || byID[0].Name != "Неловко" {
		t.Fatalf("by id %+v", byID)
	}
}

func TestTournamentsCarryWhatThePickerShows(t *testing.T) {
	s := fixture(t)
	got := s.PlayableTournaments(context.Background(), day(t, "2026-09-02"))
	if len(got) != 2 {
		t.Fatalf("playable %+v", got)
	}
	if got[0].Editors != "Александр Печеный, Игорь Биткин" || got[0].Difficulty != 3.5 || got[0].Teams != 86 {
		t.Fatalf("card %+v", got[0])
	}
	if got[1].Editors != "" || got[1].Difficulty != 0 || got[1].Teams != 0 {
		t.Fatalf("a tournament with nothing said about it %+v", got[1])
	}
}

// A mirror that is not there, or one without the tables, answers empty rather
// than failing the page (ADR-0020).
func TestDisabledAndMissingTablesFailSoft(t *testing.T) {
	off, err := Open("")
	if err != nil || off.Enabled() {
		t.Fatalf("empty path: %v %v", err, off.Enabled())
	}
	if off.Players(context.Background(), "П", 10) != nil || off.TeamName(context.Background(), 1) != "" {
		t.Fatal("a disabled store must answer empty")
	}
	if _, ok := off.BaseRoster(context.Background(), 1, time.Now()); ok {
		t.Fatal("a disabled store knows no rosters")
	}
	missing, err := Open(filepath.Join(t.TempDir(), "nope.db"))
	if err != nil || missing.Enabled() {
		t.Fatalf("missing file: %v", err)
	}

	path := filepath.Join(t.TempDir(), "empty.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`create table other(x integer)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	bare, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer bare.Close()
	if bare.Players(context.Background(), "П", 10) != nil {
		t.Fatal("a mirror without the tables must answer empty")
	}
	if got := bare.PlayableTournaments(context.Background(), time.Now()); got != nil {
		t.Fatalf("playable %+v", got)
	}
}
