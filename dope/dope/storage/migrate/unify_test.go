package migrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	_ "modernc.org/sqlite"

	"dope/dope/storage/journal"
	"dope/dope/storage/store"
)

// A checkpoint's themes/answers row dumps fold into its matches rows'
// state_json; the legacy keys disappear so restores reproduce the unified
// shape.
func TestFoldCheckpointThemes(t *testing.T) {
	cp := &journal.GameCheckpoint{Tables: map[string][]map[string]any{
		"matches": {{"id": int64(1), "code": "A", "state_json": "{}"}},
		"themes": {
			{"id": int64(10), "match_id": int64(1), "team_id": int64(7), "kind": "regular", "theme_index": int64(0), "player_id": int64(55)},
		},
		"answers": {
			{"id": int64(1), "theme_id": int64(10), "answer_index": int64(2), "mark": "right"},
		},
	}}
	if !foldCheckpointThemes(cp) {
		t.Fatal("legacy rows not detected")
	}
	if cp.Tables["themes"] != nil || cp.Tables["answers"] != nil {
		t.Fatal("legacy keys not removed")
	}
	raw, _ := cp.Tables["matches"][0]["state_json"].(string)
	blob, err := store.ParseMatchBlob(raw)
	if err != nil {
		t.Fatalf("folded blob: %v", err)
	}
	team := blob.Teams["7"]
	if team == nil || team.Themes[0].Player != 55 || team.Themes[0].Answers[2] != "right" {
		t.Fatalf("folded blob = %s", raw)
	}
}

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

// The journal legitimately mixes row ops (JSON {"t","r"} payloads) with event
// records whose payloads may be non-JSON (OpEvGeneric length-prefixes the event
// type). The conversion's scans must skip event records, not die in
// json_extract, and still rewrite the row-op records around them — in the hot
// journal AND inside zstd cold segments (both the archiver's JSON encoding and
// the audit converter's varint encoding).
func TestRunUnifyConversionToleratesForeignJournalPayloads(t *testing.T) {
	db, err := sql.Open("sqlite", "file:foreign?mode=memory&cache=shared")
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
	mustExec(`create table games(id integer primary key, fest_id integer, title text, state_json text, status text, game_type text)`)
	mustExec(`create table stages(id integer primary key, fest_id integer, game_id integer, code text, title text, stage_type text, kind text, position integer, status text, config_json text)`)
	mustExec(`create table matches(id integer primary key, fest_id integer, game_id integer, stage_id integer, code text, title text, position integer, participant_count integer, status text, revision integer, state_json text)`)
	mustExec(`create table themes(id integer primary key, match_id integer, team_id integer, kind text, theme_index integer, player_id integer)`)
	mustExec(`create table answers(id integer primary key, theme_id integer, answer_index integer, mark text)`)
	mustExec(`create table reseed_entries(stage_id integer, rank integer, team_id integer, metrics_json text)`)
	mustExec(`create table stage_standings(stage_id integer, rank integer, participant_id integer, metrics_json text)`)
	mustExec(`create table journal(id integer primary key, fest_id integer, game_id integer, seq integer, ts text, actor_user_id integer, request_id text, op integer, payload blob, created_at text)`)
	mustExec(`create table journal_checkpoint(game_id integer, seq integer, state_blob blob)`)
	mustExec(`create table fests(id integer primary key, revision integer)`)

	mustExec(`insert into games(id, fest_id, title, state_json, status, game_type) values (1, 1, 'flat', '{"x":1}', 'active', 'ksi')`)
	generic := append([]byte{0x14}, []byte(`game:screen-settings{"bg":"#ffffff"}`)...)
	mustExec(`insert into journal(id, game_id, seq, op, payload) values (1, 1, 1, 127, ?)`, generic)
	mustExec(`insert into journal(id, game_id, seq, op, payload) values (2, 1, 2, 2, ?)`,
		`{"t":"games","r":{"id":1,"state_json":"{\"x\":1}"}}`)

	if err := journal.CreateTables(db); err != nil {
		t.Fatal(err)
	}
	dict, err := journal.LoadWritableDict(db)
	if err != nil {
		t.Fatal(err)
	}
	varintArgs := journal.EncodeRowArgs(journal.RowArgs{TableID: dict.Intern("reseed_entries"), Cols: []journal.ColVal{
		{NameID: dict.Intern("stage_id"), Val: int64(5)},
		{NameID: dict.Intern("team_id"), Val: int64(9)},
	}})
	if err := dict.Persist(db); err != nil {
		t.Fatal(err)
	}
	segRecords := []journal.Record{
		{Seq: 1, Op: journal.Op(2), Args: []byte(`{"t":"reseed_entries","r":{"stage_id":5,"rank":1,"team_id":9}}`)},
		{Seq: 2, Op: journal.Op(1), Args: varintArgs},
		{Seq: 3, Op: journal.Op(2), Args: []byte(`{"t":"games","r":{"id":1,"state_json":"{\"y\":2}"}}`)},
		{Seq: 4, Op: journal.Op(127), Args: append([]byte(nil), generic...)},
	}
	mustExec(`insert into journal_segment(fest_id, seq_start, seq_end, dsl_version, n_records, blob, created_at)
values (1, 1, 4, 1, 4, ?, 'now')`, journal.Compress(journal.EncodeSegment(segRecords)))

	if err := RunUnifyConversion(db); err != nil {
		t.Fatalf("conversion: %v", err)
	}

	var payload string
	if err := db.QueryRow(`select payload from journal where id = 2`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if payload == `{"t":"games","r":{"id":1,"state_json":"{\"x\":1}"}}` {
		t.Fatalf("games state record not redirected onto the match: %s", payload)
	}
	var kept []byte
	if err := db.QueryRow(`select payload from journal where id = 1`).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if string(kept) != string(generic) {
		t.Fatalf("event payload rewritten: %x", kept)
	}

	var blob []byte
	if err := db.QueryRow(`select blob from journal_segment`).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	raw, err := journal.Decompress(blob)
	if err != nil {
		t.Fatal(err)
	}
	recs, err := journal.DecodeSegment(raw)
	if err != nil {
		t.Fatal(err)
	}
	names, err := journal.LoadDict(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(recs[0].Args); got != `{"r":{"participant_id":9,"rank":1,"stage_id":5},"t":"stage_standings"}` {
		t.Fatalf("segment JSON reseed record = %s", got)
	}
	args, err := journal.DecodeRowArgs(recs[1].Args)
	if err != nil {
		t.Fatal(err)
	}
	cols := map[string]any{}
	for _, c := range args.Cols {
		cols[names[c.NameID]] = c.Val
	}
	if names[args.TableID] != "stage_standings" || cols["participant_id"] != int64(9) || cols["team_id"] != nil {
		t.Fatalf("segment varint reseed record: table=%s cols=%v", names[args.TableID], cols)
	}
	var matchID int64
	if err := db.QueryRow(`select id from matches where game_id = 1 and code = 'main'`).Scan(&matchID); err != nil {
		t.Fatal(err)
	}
	var seg3 struct {
		Table string         `json:"t"`
		Row   map[string]any `json:"r"`
	}
	if err := json.Unmarshal(recs[2].Args, &seg3); err != nil {
		t.Fatal(err)
	}
	if seg3.Table != "matches" || seg3.Row["id"] != float64(matchID) || seg3.Row["state_json"] != `{"y":2}` {
		t.Fatalf("segment game-state record = %s", recs[2].Args)
	}
	if string(recs[3].Args) != string(generic) {
		t.Fatalf("segment event record rewritten: %x", recs[3].Args)
	}
}
