package buffdb

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
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
