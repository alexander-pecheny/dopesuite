package tests

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"testing"

	"dope/dope/storage/buffdb"
)

var initPayload = regexp.MustCompile(`(?s)id="__GAME_INIT__"[^>]*>(.*?)</script>`)

// buffMirror is a stand-in for buff's nightly mirror: the two tables dope reads
// out of it, holding one Swiss town and one the rating site leaves without a
// country.
func buffMirror(t *testing.T) *buffdb.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "buff.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
create table towns(id integer primary key, name text not null, country_id integer);
create table countries(id integer primary key, name text not null, iso text not null default '');
insert into countries values (48, 'Швейцария', 'CH');
insert into towns values (2046, 'Базель', 48), (1044, 'Севастополь', null);
`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	store, err := buffdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// The page is served the country of every city on the roster: the one the
// import already resolved comes off the team's own row, and the one it did not
// is looked up in buff by name. A city neither knows is simply absent, and the
// page falls back to the list it carries itself.
func TestGamePageCarriesTheCountryOfEachCity(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	srv.Eng().Buff = buffMirror(t)

	for i, team := range []struct{ name, city, country string }{
		{"Эрликон", "Цюрих", "CH"},
		{"Циклопы-скоморохи", "Базель", ""},
		{"Гадюкинцы", "Гадюкино", ""},
	} {
		if _, err := db.Exec(`
insert into fest_teams(fest_id, name, city, country, position, number) values(?, ?, ?, ?, ?, ?)`,
			festID, team.name, team.city, team.country, i+1, i+1); err != nil {
			t.Fatalf("insert team %d: %v", i, err)
		}
	}
	gameID := createSchemeGame(t, db, festID, "od", "od", "")

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/fest/%d/game/%d", festID, gameID), nil)
	rec := httptest.NewRecorder()
	srv.HandleFestRouter(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("game page: %d", rec.Code)
	}
	match := initPayload.FindStringSubmatch(rec.Body.String())
	if match == nil {
		t.Fatal("no init payload on the game page")
	}
	var payload struct {
		CityCountry map[string]string `json:"cityCountry"`
	}
	if err := json.Unmarshal([]byte(match[1]), &payload); err != nil {
		t.Fatalf("init payload: %v", err)
	}
	want := map[string]string{"цюрих": "CH", "базель": "CH"}
	if len(payload.CityCountry) != len(want) {
		t.Fatalf("cityCountry = %v, want %v", payload.CityCountry, want)
	}
	for city, iso := range want {
		if payload.CityCountry[city] != iso {
			t.Errorf("%s = %q, want %q", city, payload.CityCountry[city], iso)
		}
	}
}

// Without a mirror the page still renders; it just carries no countries.
func TestGamePageWithoutAMirror(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	seedFestTeams(t, db, festID, 2)
	gameID := createSchemeGame(t, db, festID, "od", "od", "")

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/fest/%d/game/%d", festID, gameID), nil)
	rec := httptest.NewRecorder()
	srv.HandleFestRouter(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("game page: %d", rec.Code)
	}
	if match := initPayload.FindStringSubmatch(rec.Body.String()); match == nil {
		t.Fatal("no init payload on the game page")
	} else if json.Valid([]byte(match[1])) == false {
		t.Fatal("init payload is not JSON")
	}
}
