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
	t.Parallel()
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
	t.Parallel()
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

// v60 turns the player overrides into hand rosters. In a КСИ, an override that
// moved Иван from Альфа to Бета leaves both teams kept by hand, Бета with him
// and Альфа without. In an ЭК whose overrides were copied into its own rosters,
// the two teams whose copies differ from what they brought are kept by hand,
// and Гамма, whose copy is what it brought, reads that again; and the
// overrides table is gone.
func TestV60OverridesBecomeHandRosters(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := dopeserver.OpenFestDB(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
create table game_player_team_overrides(
  fest_id integer not null, game_id integer not null, player_id integer not null,
  source_team_id integer not null, override_team_id integer not null,
  created_at text not null, updated_at text not null, primary key(fest_id, game_id, player_id));
insert into fests(id, slug, title, created_at, updated_at) values(1, 'f', 'f', '', '');
insert into fest_teams(id, fest_id, name, position, number) values(11, 1, 'Альфа', 1, 1), (12, 1, 'Бета', 2, 2), (13, 1, 'Гамма', 3, 3);
insert into fest_players(id, fest_id, first_name, last_name) values(21, 1, 'Иван', 'Иванов'), (22, 1, 'Пётр', 'Петров'), (23, 1, 'Анна', 'Ли Сун');
insert into fest_team_players(team_id, player_id, roster_order) values(11, 21, 0), (11, 22, 1), (12, 23, 0);
insert into participants(id, fest_id, name, fest_team_id, number) values(31, 1, 'Альфа', 11, 1), (32, 1, 'Бета', 12, 2), (33, 1, 'Гамма', 13, 3);
insert into players(id, fest_id, first_name, last_name) values(41, 1, 'Иван', 'Иванов'), (42, 1, 'Пётр', 'Петров'), (43, 1, 'Анна', 'Ли Сун'), (44, 1, 'Ева', 'Ким');
insert into participant_players(participant_id, player_id, roster_order) values(31, 41, 0), (31, 42, 1), (32, 43, 0), (33, 44, 0);
insert into games(id, fest_id, code, title, game_type, position, scheme_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(7, 1, 'ksi', 'КСИ', 'ksi', 1, '{}', 'active', 'fest', 'fest', 1, '', ''),
      (8, 1, 'ek', 'ЭК', 'ek', 2, '{}', 'active', 'fest', 'game', 1, '', '');
insert into game_participants(game_id, participant_id, position) values(7, 31, 0), (7, 32, 1), (8, 31, 0), (8, 32, 1), (8, 33, 2);
insert into game_team_players(game_id, participant_id, player_id, roster_order) values(8, 31, 42, 0), (8, 32, 43, 0), (8, 32, 41, 1), (8, 33, 44, 0);
insert into game_player_team_overrides values(1, 7, 21, 11, 12, '', ''), (1, 8, 21, 11, 12, '', '');
delete from schema_versions where version = 60;`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	db, err = dopeserver.OpenFestDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rosters := func(gameID int64) string {
		t.Helper()
		var got string
		if err := db.QueryRow(`
select coalesce(group_concat(participant_id || ':' || name, ' '), '') from (
  select g.participant_id, p.first_name || ' ' || p.last_name as name from game_team_players g join players p on p.id = g.player_id
  where g.game_id = ? and g.hand = 1 order by g.participant_id, g.roster_order)`, gameID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got, want := rosters(7), "31:Пётр Петров 32:Анна Ли Сун 32:Иван Иванов"; got != want {
		t.Errorf("КСИ hand rosters = %q, want %q", got, want)
	}
	if got, want := rosters(8), "31:Пётр Петров 32:Анна Ли Сун 32:Иван Иванов"; got != want {
		t.Errorf("ЭК hand rosters = %q, want %q", got, want)
	}
	var left, source string
	if err := db.QueryRow(`select count(*) from game_team_players where hand = 0`).Scan(&left); err != nil || left != "0" {
		t.Errorf("copied rows left = %s (%v), want 0", left, err)
	}
	if err := db.QueryRow(`select roster_source from games where id = 8`).Scan(&source); err != nil || source != "fest" {
		t.Errorf("ЭК roster_source = %q (%v), want fest", source, err)
	}
	var tables int
	if err := db.QueryRow(`select count(*) from sqlite_master where name = 'game_player_team_overrides'`).Scan(&tables); err != nil || tables != 0 {
		t.Errorf("overrides table still there (%d, %v)", tables, err)
	}
}
