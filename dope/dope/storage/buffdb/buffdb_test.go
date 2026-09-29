package buffdb

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func fixture(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "buff.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
create table towns(id integer primary key, name text not null, country_id integer);
create table countries(id integer primary key, name text not null, iso text not null default '');

insert into countries values
  (21, 'Россия', 'RU'),
  (48, 'Швейцария', 'CH'),
  (60, 'Дания', 'DK');

insert into towns values
  (1971, 'Цюрих', 48),
  (2046, 'Базель', 48),
  (1972, 'Копенгаген', 60),
  (197, 'Москва', 21),
  (1044, 'Севастополь', null);
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
	t.Cleanup(func() { store.Close() })
	return store
}

func TestTownCountriesAnswersEveryTownItKnows(t *testing.T) {
	store := fixture(t)
	got := store.TownCountries(context.Background(), []string{"Цюрих", "базель", "Копенгаген", "Нарния", ""})
	want := map[string]string{"цюрих": "CH", "базель": "CH", "копенгаген": "DK"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for city, iso := range want {
		if got[city] != iso {
			t.Errorf("%s: got %q, want %q", city, got[city], iso)
		}
	}
}

// A town the rating site leaves without a country gets no flag rather than a
// wrong one, and it must not be mistaken for a town the mirror has never seen.
func TestTownWithoutACountry(t *testing.T) {
	store := fixture(t)
	if got := store.TownCountries(context.Background(), []string{"Севастополь"}); len(got) != 0 {
		t.Errorf("TownCountries: got %v, want nothing", got)
	}
	iso, known := store.TownCountry(context.Background(), 1044)
	if !known || iso != "" {
		t.Errorf("TownCountry(1044) = %q, %v; want \"\", true", iso, known)
	}
	if _, known := store.TownCountry(context.Background(), 99999); known {
		t.Error("TownCountry of a town the mirror has never seen: got known")
	}
}

func TestCountryISO(t *testing.T) {
	store := fixture(t)
	if got := store.CountryISO(context.Background(), 48); got != "CH" {
		t.Errorf("CountryISO(48) = %q, want CH", got)
	}
	if got := store.CountryISO(context.Background(), 404); got != "" {
		t.Errorf("CountryISO of an unknown country = %q, want empty", got)
	}
}

// Every method of a store with no mirror behind it answers nothing, because a
// dope without buff configured still has to serve its pages.
func TestDisabledStoreAnswersNothing(t *testing.T) {
	store := Disabled()
	if store.Enabled() {
		t.Fatal("Disabled().Enabled() is true")
	}
	if got := store.TownCountries(context.Background(), []string{"Цюрих"}); got != nil {
		t.Errorf("TownCountries: got %v, want nil", got)
	}
	if _, known := store.TownCountry(context.Background(), 1971); known {
		t.Error("TownCountry: got known")
	}
	if got := store.CountryISO(context.Background(), 48); got != "" {
		t.Errorf("CountryISO: got %q, want empty", got)
	}
	if err := store.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestOpenMissingFileIsDisabled(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "absent.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if store.Enabled() {
		t.Error("a missing mirror opened as enabled")
	}
}

func peopleFixture(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "buff.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
create table players(id integer primary key, name text, patronymic text, surname text);
create table player_games(player_id integer primary key, games integer not null default 0);
create table teams(id integer primary key, name text not null, name_fold text not null default '', town text not null default '', last_tournament_id integer, town_id integer);
create table seasons(id integer primary key, date_start text, date_end text);
create table team_seasons(team_id integer, season_id integer, player_id integer, date_added text, date_removed text, player_number integer, primary key (team_id, season_id, player_id));

insert into players values (1, 'Илья', 'Сергеевич', 'Кобзев'), (2, 'Илья', '', 'Кобзев'), (3, 'Анна', '', 'Кобзарева'), (4, 'Борис', '', 'Жук');
insert into player_games values (1, 12), (2, 300), (3, 40);
insert into teams values (10, 'Bikes for Peace', 'bikes for peace', 'Москва', 900, 1), (11, 'Мирные байки', 'мирные байки', 'Минск', 100, 2);
insert into seasons values (60, '2025-09-01T00:00:00+00:00', '2026-08-28T00:00:00+00:00'), (61, '2026-08-28T00:00:00+00:00', '2027-08-27T00:00:00+00:00');
insert into team_seasons values (10, 60, 4, '2025-09-05', null, 0), (10, 61, 1, '2026-08-28', null, 1), (10, 61, 3, '2026-08-28', '2026-09-10T00:00:00+00:00', 2), (10, 61, 2, '2026-08-28', null, 0);
`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// A host types a surname's start, or a surname and a name in either order,
// in any case; the most played namesake comes first.
func TestSearchPlayersFindsByTheStartOfTheNames(t *testing.T) {
	store := peopleFixture(t)
	got := store.SearchPlayers(t.Context(), "кобз", 10)
	if len(got) != 3 || got[0].ID != 2 || got[1].ID != 3 {
		t.Fatalf("кобз = %+v", got)
	}
	if got := store.SearchPlayers(t.Context(), "Ил Кобзев", 10); len(got) != 2 || got[0].ID != 2 {
		t.Fatalf("Ил Кобзев = %+v", got)
	}
	if got := store.SearchPlayers(t.Context(), "кобзев анна", 10); len(got) != 0 {
		t.Fatalf("кобзев анна = %+v", got)
	}
}

func TestSearchTeamsFindsAWordsStart(t *testing.T) {
	store := peopleFixture(t)
	if got := store.SearchTeams(t.Context(), "Peace", 10); len(got) != 1 || got[0].ID != 10 || got[0].Town != "Москва" {
		t.Fatalf("Peace = %+v", got)
	}
	if got := store.SearchTeams(t.Context(), "байк", 10); len(got) != 1 || got[0].ID != 11 {
		t.Fatalf("байк = %+v", got)
	}
}

// The base roster is the season running on the day, without the people who
// left it before then.
func TestBaseRosterIsTheSeasonOfTheDay(t *testing.T) {
	store := peopleFixture(t)
	day := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	got := store.BaseRoster(t.Context(), 10, day)
	if len(got) != 2 || got[0].ID != 2 || got[1].ID != 1 {
		t.Fatalf("base roster = %+v", got)
	}
	if got := store.BaseRoster(t.Context(), 11, day); len(got) != 0 {
		t.Fatalf("an unmirrored team = %+v", got)
	}
}
