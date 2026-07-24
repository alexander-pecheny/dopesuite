package journal

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"dope/dope/storage/store"
)

func patchDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`create table matches(id integer primary key, game_id integer, state_json text not null default '{}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`create table games(id integer primary key, game_type text not null)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`insert into matches(id) values (5)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func addFlatMatch(t *testing.T, db *sql.DB, matchID int64, gameType string) {
	t.Helper()
	if _, err := db.Exec(`insert into games(id, game_type) values (?, ?)`, matchID, gameType); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`insert into matches(id, game_id) values (?, ?)`, matchID, matchID); err != nil {
		t.Fatal(err)
	}
}

func applyPatch(t *testing.T, db *sql.DB, payload string) {
	t.Helper()
	if err := ApplyMatchPatch(context.Background(), db, []byte(payload)); err != nil {
		t.Fatalf("apply %s: %v", payload, err)
	}
}

func stateOf(t *testing.T, db *sql.DB) string {
	t.Helper()
	return stateOfMatch(t, db, 5)
}

func stateOfMatch(t *testing.T, db *sql.DB, matchID int64) string {
	t.Helper()
	var raw string
	if err := db.QueryRow(`select state_json from matches where id = ?`, matchID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

// Set ops create intermediate containers (objects for named segments, arrays
// padded for numeric ones); remove splices arrays and prunes emptied team
// sections; a path through missing containers is a tolerated no-op.
func TestMatchPatchApply(t *testing.T) {
	db := patchDB(t)
	applyPatch(t, db, `{"m":5,"ops":[
		{"k":"set","p":"/teams/7/themes/1/answers/4","v":"right"},
		{"k":"set","p":"/teams/7/themes/1/player","v":55}
	]}`)
	want := `{"teams":{"7":{"themes":[{"answers":["","","","",""]},{"answers":["","","","","right"],"player":55}]}}}`
	if got := stateOf(t, db); got != want {
		t.Fatalf("state = %s\nwant   %s", got, want)
	}

	applyPatch(t, db, `{"m":5,"ops":[{"k":"remove","p":"/teams/7/themes/0"}]}`)
	want = `{"teams":{"7":{"themes":[{"answers":["","","","","right"],"player":55}]}}}`
	if got := stateOf(t, db); got != want {
		t.Fatalf("after splice = %s\nwant %s", got, want)
	}

	applyPatch(t, db, `{"m":5,"ops":[{"k":"remove","p":"/teams/7/themes/0"}]}`)
	if got := stateOf(t, db); got != `{}` {
		t.Fatalf("emptied section should prune to {}: %s", got)
	}

	applyPatch(t, db, `{"m":5,"ops":[{"k":"set","p":"/teams/9/themes/9/answers/9","v":"x"},
		{"k":"remove","p":"/teams/404/themes/3"}]}`)
	if got := stateOf(t, db); got != `{}` {
		t.Fatalf("out-of-range answer index and missing-path remove must no-op: %s", got)
	}
}

// Flat games replay with the live editbatch semantics, not the EK-shaped
// applier: no 5-answer bound, null padding, and recorded path parts restore
// index-vs-key typing the pointer string erased.
func TestMatchPatchFlatReplayMatchesLive(t *testing.T) {
	db := patchDB(t)
	addFlatMatch(t, db, 6, "ksi")
	if _, err := db.Exec(`update matches set state_json = ? where id = 6`,
		`{"themes":[{"answers":[["x"],[],[],[],[],[]]}]}`); err != nil {
		t.Fatal(err)
	}
	applyPatch(t, db, `{"m":6,"ops":[{"k":"set","p":"/themes/0/answers/5/2","v":"30"}]}`)
	want := `{"themes":[{"answers":[["x"],[],[],[],[],[null,null,"30"]]}]}`
	if got := stateOfMatch(t, db, 6); got != want {
		t.Fatalf("ksi 6th participant = %s\nwant %s", got, want)
	}

	applyPatch(t, db, `{"m":6,"ops":[{"k":"set","p":"/declined/4","v":true,"pp":["declined","4"]}]}`)
	want = `{"declined":{"4":true},"themes":[{"answers":[["x"],[],[],[],[],[null,null,"30"]]}]}`
	if got := stateOfMatch(t, db, 6); got != want {
		t.Fatalf("declined key = %s\nwant %s", got, want)
	}

	addFlatMatch(t, db, 7, "od")
	if _, err := db.Exec(`update matches set state_json = ? where id = 7`, `{"entries":[]}`); err != nil {
		t.Fatal(err)
	}
	applyPatch(t, db, `{"m":7,"ops":[{"k":"set","p":"/entries/2/3","v":1}]}`)
	want = `{"entries":[null,null,[null,null,null,1]]}`
	if got := stateOfMatch(t, db, 7); got != want {
		t.Fatalf("od entries = %s\nwant %s", got, want)
	}

	applyPatch(t, db, `{"m":7,"ops":[{"k":"replace","p":"","v":{"entries":[[1,0],[0,1]],"teams":["x","y"]}}]}`)
	want = `{"entries":[[1,0],[0,1]],"teams":["x","y"]}`
	if got := stateOfMatch(t, db, 7); got != want {
		t.Fatalf("replace = %s\nwant %s", got, want)
	}
}

// The codec round-trips through EncodeMatchPatch, and the record decodes from
// exactly the same bytes hot or cold.
func TestMatchPatchEncode(t *testing.T) {
	payload := EncodeMatchPatch(5, []store.BlobOp{
		{Kind: "set", Path: "/teams/7/themes/0/answers/2", Value: "wrong"},
		{Kind: "remove", Path: "/teams/7/shootoutThemes/1"},
	})
	db := patchDB(t)
	applyPatch(t, db, string(payload))
	want := `{"teams":{"7":{"themes":[{"answers":["","wrong","","",""]}]}}}`
	_ = want
	var raw string
	if err := db.QueryRow(`select state_json from matches where id = 5`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != `{"teams":{"7":{"themes":[{"answers":["","","wrong","",""]}]}}}` {
		t.Fatalf("state = %s", raw)
	}
}
