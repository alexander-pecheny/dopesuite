package numbering

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestDisplayName(t *testing.T) {
	if got := DisplayName(Team{Name: "Ёжики", City: "Тверь"}); got != "Ёжики (Тверь)" {
		t.Fatalf("got %q", got)
	}
	if got := DisplayName(Team{Name: "Ёжики"}); got != "Ёжики" {
		t.Fatalf("got %q", got)
	}
}

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
create table fest_teams(id integer primary key, fest_id integer, name text, city text default '', position real, number integer, deleted integer default 0);
create table game_participants(game_id integer, participant_id integer, position integer, number integer default 0);
insert into fest_teams(fest_id, name, city, position, number) values
  (1, 'B', '', 2, 5), (1, 'A', 'X', 1, null), (1, 'gone', '', 0, null), (2, 'C', '', 1, 1);
update fest_teams set deleted = 1 where name = 'gone';`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestLoadFestTeamsOrdersActiveTeamsByPosition(t *testing.T) {
	db := openDB(t)
	teams, err := LoadFestTeams(context.Background(), db, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(teams) != 2 || teams[0].Name != "A" || teams[0].Number != 0 || teams[1].Number != 5 {
		t.Fatalf("teams %+v", teams)
	}
}

func TestNumberingGuards(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	all, total, err := AllNumbered(ctx, db, 1)
	if err != nil || all || total != 2 {
		t.Fatalf("fest 1: all=%v total=%d err=%v", all, total, err)
	}
	if all, total, _ := AllNumbered(ctx, db, 2); !all || total != 1 {
		t.Fatalf("fest 2: all=%v total=%d", all, total)
	}
	if all, total, _ := AllNumbered(ctx, db, 3); all || total != 0 {
		t.Fatalf("empty fest: all=%v total=%d", all, total)
	}
	for fest, want := range map[int64]bool{1: true, 2: false, 3: false} {
		if got, _ := HasUnnumbered(ctx, db, fest); got != want {
			t.Errorf("HasUnnumbered(%d) = %v", fest, got)
		}
	}
	// A Game with its own entrants answers for itself; one without falls back to the фест.
	if _, err := db.Exec(`insert into game_participants values (10, 1, 0, 1), (10, 2, 1, 2), (11, 1, 0, 0)`); err != nil {
		t.Fatal(err)
	}
	for game, want := range map[int64]bool{10: false, 11: true, 12: true} {
		if got, _ := GameHasUnnumbered(ctx, db, 1, game); got != want {
			t.Errorf("GameHasUnnumbered(fest 1, game %d) = %v", game, got)
		}
	}
	if got, _ := GameHasUnnumbered(ctx, db, 2, 12); got {
		t.Error("fest 2 fallback reported unnumbered")
	}
}
