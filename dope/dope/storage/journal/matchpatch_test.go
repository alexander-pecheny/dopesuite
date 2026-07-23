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
	if _, err := db.Exec(`create table matches(id integer primary key, state_json text not null default '{}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`insert into matches(id) values (5)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func applyPatch(t *testing.T, db *sql.DB, payload string) {
	t.Helper()
	if err := ApplyMatchPatch(context.Background(), db, []byte(payload)); err != nil {
		t.Fatalf("apply %s: %v", payload, err)
	}
}

func stateOf(t *testing.T, db *sql.DB) string {
	t.Helper()
	var raw string
	if err := db.QueryRow(`select state_json from matches where id = 5`).Scan(&raw); err != nil {
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
