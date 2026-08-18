package tests

import (
	"path/filepath"
	"testing"

	dopeserver "dope/dope/server"
)

// A game imported before gamebuild wrote the importer's rows has stages with
// no Kind and бои with no буква; opening the DB once more reads the Kinds back
// from the scheme and deals the буквы, and never touches a game that has them.
func TestV25BackfillRepairsImportedGames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := dopeserver.OpenFestDB(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
insert into fests(id, slug, title, created_at, updated_at) values(1, 'f', 'f', '', '');
insert into games(id, fest_id, code, title, game_type, position, scheme_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(7, 1, 'default', 'Old', 'ek', 1, '{"stages":[{"code":"s1-g1","kind":"rr","stage_type":"matches"}]}', 'pending', 'fest', 'fest', 1, '', '');
insert into stages(id, fest_id, game_id, code, title, stage_type, kind, position, status, config_json) values(70, 1, 7, 's1-g1', '', 'matches', 'matches', 1, 'pending', '{}');
insert into matches(fest_id, game_id, stage_id, code, title, position, round, wave, participant_count, status, revision)
values(1, 7, 70, 'm1', 'Бой 1', 1, 1, 1, 2, 'pending', 1), (1, 7, 70, 'm2', 'Бой 2', 2, 1, 1, 2, 'pending', 1), (1, 7, 70, 'w', 'Письменный отбор', 3, 1, 1, 2, 'pending', 1);
delete from schema_versions where version = 25;`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	db, err = dopeserver.OpenFestDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var kind string
	if err := db.QueryRow(`select kind from stages where id = 70`).Scan(&kind); err != nil || kind != "rr" {
		t.Errorf("kind = %q (%v), want rr", kind, err)
	}
	var letters string
	if err := db.QueryRow(`select group_concat(coalesce(nullif(letter, ''), '-'), ' ') from (select letter from matches where game_id = 7 order by position)`).Scan(&letters); err != nil || letters != "A B -" {
		t.Errorf("letters = %q (%v), want \"A B -\"", letters, err)
	}
}

// A flat game from before v26 has a 'main' Block of kind «matches», no
// seats and no table; opening the DB once more makes it the flat Kind,
// seats the document's teams and ranks them.
func TestV26BackfillSeatsFlatGames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := dopeserver.OpenFestDB(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
insert into fests(id, slug, title, created_at, updated_at) values(1, 'f', 'f', '', '');
insert into games(id, fest_id, code, title, game_type, position, scheme_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(8, 1, 'od-1', 'ОД', 'od', 1, '{"tourComp":[2]}', 'active', 'fest', 'fest', 1, '', '');
insert into stages(id, fest_id, game_id, code, title, stage_type, kind, position, status, config_json) values(80, 1, 8, 'main', '', 'matches', 'matches', 1, 'active', '{}');
insert into matches(fest_id, game_id, stage_id, code, title, position, round, wave, participant_count, status, revision, state_json)
values(1, 8, 80, 'main', 'ОД', 1, 1, 1, 0, 'active', 0, '{"teams":[{"number":1,"name":"Раз"},{"number":2,"name":"Два"}],"entries":[[2],[2,1]],"completed":[true,true]}');
delete from schema_versions where version = 26;`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	db, err = dopeserver.OpenFestDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var kind string
	if err := db.QueryRow(`select kind from stages where id = 80`).Scan(&kind); err != nil || kind != "flat" {
		t.Errorf("kind = %q (%v), want flat", kind, err)
	}
	var table string
	if err := db.QueryRow(`
select group_concat(p.name, ' ') from (select participant_id from stage_standings where stage_id = 80 order by rank) st
join participants p on p.id = st.participant_id`).Scan(&table); err != nil || table != "Два Раз" {
		t.Errorf("table = %q (%v), want \"Два Раз\"", table, err)
	}
}
