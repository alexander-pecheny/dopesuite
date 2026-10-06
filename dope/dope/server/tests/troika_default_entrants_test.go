package tests

import (
	"context"
	"database/sql"
	"net/url"
	"slices"
	"testing"

	"dope/dope/domain/gamebuild"
)

// A Тройка created with no troika ticked and no seed declared seats every
// troika of the fest, in the order of applications. It used to fall back to
// what «none ticked» means for a team game — the fest's teams — and a Тройка
// never seats those: its Составы showed «Участник 1…8» with nobody in them.
func TestTroikaWithNoneTickedSeatsTheFestTroikas(t *testing.T) {
	t.Parallel()
	srv, festID, _, createGame := troikaSeedFest(t)
	db := srv.Eng().DB
	dsl := "[scheme]\nkind: roundrobin\ngroup_size: 4\nthemes: 6\nmetric: total\npoints: [1, 0.5, 0]\n"
	gameID := createGame(url.Values{"game_type": {"troika"}, "troika_dsl": {dsl}})

	rows, err := db.Query(`
select p.name, p.assembled from game_participants gp join participants p on p.id = gp.participant_id
where gp.game_id = ? and p.fest_id = ? order by gp.position`, gameID, festID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		var assembled bool
		if err := rows.Scan(&name, &assembled); err != nil {
			t.Fatal(err)
		}
		if !assembled {
			t.Fatalf("the game seats %s, a fest team", name)
		}
		names = append(names, name)
	}
	want := []string{"Ромашка", "Лютик", "Василёк", "По коням"}
	if len(names) != len(want) {
		t.Fatalf("seated %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("seated %v, want %v (the order of applications)", names, want)
		}
	}
}

// A Тройка Game with nothing recorded — no Entrant written for it — is
// cleared and recompiled with its default troikas, never the fest's teams
// (27ee06d9). Both paths are run on a Game whose game_participants rows were
// taken away, and its seats emptied before the recompile, so each path has to
// find the troikas itself.
func TestTroikaWithNothingRecordedClearsAndRecompilesWithItsTroikas(t *testing.T) {
	t.Parallel()
	srv, festID, _, createGame := troikaSeedFest(t)
	db := srv.Eng().DB
	dsl := "[scheme]\nkind: roundrobin\ngroup_size: 4\nthemes: 6\nmetric: total\npoints: [1, 0.5, 0]\n"
	gameID := createGame(url.Values{"game_type": {"troika"}, "troika_dsl": {dsl}})
	want := []string{"Василёк", "Лютик", "По коням", "Ромашка"}

	forget := func(emptySeats bool) {
		t.Helper()
		if _, err := db.Exec(`delete from game_participants where game_id = ?`, gameID); err != nil {
			t.Fatal(err)
		}
		if !emptySeats {
			return
		}
		if _, err := db.Exec(`
update match_slots set participant_id = null
where match_id in (select id from matches where game_id = ?)`, gameID); err != nil {
			t.Fatal(err)
		}
	}
	// Both record the Entrants they seat, so the tab shows who the bouts seat.
	run := func(name string, write func(tx *sql.Tx) error) {
		t.Helper()
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if err := write(tx); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if got := troikaGameNames(t, db, gameID, `
select p.name, p.assembled from game_participants gp join participants p on p.id = gp.participant_id
where gp.game_id = ?`); !slices.Equal(got, want) {
			t.Errorf("after %s the Game's Entrants are %v, want the troikas %v", name, got, want)
		}
		if got := troikaGameNames(t, db, gameID, `
select distinct p.name, p.assembled from match_slots s
join matches m on m.id = s.match_id join participants p on p.id = s.participant_id
where m.game_id = ?`); !slices.Equal(got, want) {
			t.Errorf("after %s the bouts seat %v, want the troikas %v", name, got, want)
		}
	}

	forget(false)
	run("clear", func(tx *sql.Tx) error {
		_, err := gamebuild.Clear(context.Background(), tx, festID, gameID)
		return err
	})
	forget(true)
	run("recompile", func(tx *sql.Tx) error {
		return gamebuild.Recompile(context.Background(), tx, festID, gameID, dsl)
	})
}

// troikaGameNames reads (name, assembled) rows, fails on a fest team, and
// returns the names sorted.
func troikaGameNames(t *testing.T, db *sql.DB, gameID int64, query string) []string {
	t.Helper()
	rows, err := db.Query(query, gameID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		var assembled bool
		if err := rows.Scan(&name, &assembled); err != nil {
			t.Fatal(err)
		}
		if !assembled {
			t.Fatalf("the Game holds %s, a fest team", name)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	slices.Sort(names)
	return names
}
