package migrate

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"dope/dope/storage/store"
)

// A legacy EK match (relational themes/answers rows) converts into the
// team-id-keyed state blob: marks land under the right team/theme/answer,
// theme players carry over as ids, shootout themes keep their kind, and
// already-converted matches are left alone (idempotent).
func TestConvertEKMatchBlobs(t *testing.T) {
	db, err := sql.Open("sqlite", "file:convert?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`create table matches(id integer primary key, state_json text not null default '{}')`)
	mustExec(`create table themes(id integer primary key, match_id integer, team_id integer, kind text, theme_index integer, player_id integer)`)
	mustExec(`create table answers(id integer primary key, theme_id integer, answer_index integer, mark text)`)

	mustExec(`insert into matches(id) values (1), (2)`)
	mustExec(`insert into themes(id, match_id, team_id, kind, theme_index, player_id) values
		(10, 1, 7, 'regular', 0, 55),
		(11, 1, 7, 'shootout', 0, null),
		(12, 1, 8, 'regular', 3, null)`)
	mustExec(`insert into answers(theme_id, answer_index, mark) values
		(10, 0, 'right'), (10, 2, 'wrong'), (11, 4, '+'), (12, 1, 'Q')`)
	mustExec(`update matches set state_json = '{"teams":{"9":{}}}' where id = 2`)

	if err := ConvertEKMatchBlobs(context.Background(), db); err != nil {
		t.Fatalf("convert: %v", err)
	}

	var raw string
	if err := db.QueryRow(`select state_json from matches where id = 1`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	blob, err := store.ParseMatchBlob(raw)
	if err != nil {
		t.Fatalf("parse converted blob: %v", err)
	}
	team7 := blob.Teams["7"]
	if team7 == nil || len(team7.Themes) != 1 || len(team7.ShootoutThemes) != 1 {
		t.Fatalf("team 7 = %+v", team7)
	}
	if team7.Themes[0].Player != 55 || team7.Themes[0].Answers[0] != "right" || team7.Themes[0].Answers[2] != "wrong" {
		t.Fatalf("team 7 theme 0 = %+v", team7.Themes[0])
	}
	if team7.ShootoutThemes[0].Answers[4] != "right" {
		t.Fatalf("team 7 shootout = %+v", team7.ShootoutThemes[0])
	}
	team8 := blob.Teams["8"]
	if team8 == nil || len(team8.Themes) != 4 || team8.Themes[3].Answers[1] != "right" {
		t.Fatalf("team 8 = %+v", team8)
	}

	// Match 2 had no legacy rows and an existing blob — untouched.
	if err := db.QueryRow(`select state_json from matches where id = 2`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != `{"teams":{"9":{}}}` {
		t.Fatalf("match 2 blob rewritten: %s", raw)
	}
}
