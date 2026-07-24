package migrate

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"dope/dope/storage/store"
)

// Host place pins move out of match_results and into the state blob, one match
// at a time, and the pass is idempotent — the column it drains is also the
// column that tells it there is work left.
func TestRunPinBackfill(t *testing.T) {
	db, err := sql.Open("sqlite", "file:pins?mode=memory&cache=shared")
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
	mustExec(`create table matches(id integer primary key, fest_id integer, game_id integer, state_json text not null default '{}')`)
	mustExec(`create table fests(id integer primary key, revision integer not null default 0)`)
	mustExec(`create table journal(id integer primary key autoincrement, fest_id integer, game_id integer, seq integer,
	  ts text, actor_user_id integer, request_id text, op integer, payload blob, created_at text)`)
	mustExec(`create table match_results(match_id integer, team_id integer, place real, place_override real,
	  primary key(match_id, team_id))`)
	mustExec(`insert into fests(id, revision) values(1, 3)`)
	mustExec(`insert into matches(id, fest_id, game_id, state_json) values(1, 1, 1, '{}'), (2, 1, 1, '{}')`)
	mustExec(`insert into match_results(match_id, team_id, place, place_override) values
	  (1, 11, 2, 2), (1, 22, 1, null), (2, 33, 4, 4)`)

	if err := RunPinBackfill(db); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	var raw string
	if err := db.QueryRow(`select state_json from matches where id = 1`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	blob, err := store.ParseMatchBlob(raw)
	if err != nil {
		t.Fatalf("blob: %v", err)
	}
	if pin := blob.Pin(11); pin == nil || *pin != 2 {
		t.Fatalf("team 11 pin = %v, want 2 (blob %s)", pin, raw)
	}
	if blob.Pin(22) != nil {
		t.Fatalf("team 22 had no override and must stay unpinned (blob %s)", raw)
	}

	var remaining int
	if err := db.QueryRow(`select count(*) from match_results where place_override is not null`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("%d overrides left behind", remaining)
	}
	var journaled int
	if err := db.QueryRow(`select count(*) from journal`).Scan(&journaled); err != nil {
		t.Fatal(err)
	}
	if journaled != 2 {
		t.Fatalf("journaled %d patches, want one per converted match", journaled)
	}
	if err := RunPinBackfill(db); err != nil {
		t.Fatalf("second pass: %v", err)
	}
}
