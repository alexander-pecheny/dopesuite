package gamebuild_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"dope/dope/domain/festview"
	"dope/dope/domain/gamebuild"
	dopeserver "dope/dope/server"
	"dope/dope/storage/store"
)

func newFest(t *testing.T, teams int) (*sql.DB, int64) {
	t.Helper()
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	userID, err := dopeserver.EnsureSystemUser(context.Background(), tx)
	if err != nil {
		t.Fatal(err)
	}
	festID, err := store.InsertReturningID(context.Background(), tx, `
insert into fests(slug, title, created_by, created_at, updated_at) values('fest', 'Фест', ?, '2026-08-18', '2026-08-18')`, userID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= teams; i++ {
		if _, err := tx.Exec(`insert into fest_teams(fest_id, name, position, number) values(?, ?, ?, ?)`,
			festID, fmt.Sprintf("Команда %d", i), i, i); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`insert into participants(fest_id, roster, name, number) values(?, 'team', ?, ?)`,
			festID, fmt.Sprintf("Команда %d", i), i); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return db, festID
}

func inTx(t *testing.T, db *sql.DB, fn func(tx *sql.Tx) error) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// A pasted scheme is the ADR-0006 escape hatch, and its Blocks rank and its
// бои carry a буква like a compiled one's: the importer used to write stages
// without kind and matches without letter, so a pasted группа never ranked.
func TestMaterialiseWritesKindAndLetter(t *testing.T) {
	db, festID := newFest(t, 4)
	scheme := store.FestScheme{
		SchemaVersion: 2, Slug: "pasted", Title: "Вставленная", GameType: "ek",
		Venues: []store.SchemeVenue{{Number: 1, Title: "Стол 1"}},
		Stages: []store.SchemeStage{{
			Code: "s1-g1", Title: "Группа 1", StageType: "matches", Kind: "rr",
			Matches: []store.SchemeMatch{
				{Code: "s1-g1-m1", Title: "Бой 1", Venue: 1, Slots: []store.SchemeSlot{{Seed: &store.SchemeSeedRef{Basket: 1, Position: 1}}, {Seed: &store.SchemeSeedRef{Basket: 1, Position: 2}}}},
				{Code: "s1-g1-m2", Title: "Бой 2", Venue: 1, Slots: []store.SchemeSlot{{Seed: &store.SchemeSeedRef{Basket: 1, Position: 3}}, {Seed: &store.SchemeSeedRef{Basket: 1, Position: 4}}}},
			},
		}},
	}
	var gameID int64
	inTx(t, db, func(tx *sql.Tx) (err error) {
		gameID, err = gamebuild.Materialise(context.Background(), tx, festID, scheme)
		return err
	})
	view, err := festview.Load(context.Background(), db, festID, gameID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Stages) != 1 || view.Stages[0].Kind != "rr" {
		t.Fatalf("stages = %+v, want one of kind rr", view.Stages)
	}
	letters := []string{}
	for _, m := range view.Stages[0].Matches {
		letters = append(letters, m.Letter)
	}
	if fmt.Sprint(letters) != "[A B]" {
		t.Errorf("letters = %v, want [A B]", letters)
	}
	if view.Stages[0].Matches[0].Venue == nil || view.Stages[0].Matches[0].Venue.Number != 1 {
		t.Errorf("бой lost its стол: %+v", view.Stages[0].Matches[0].Venue)
	}
}

// «Очистить игру» on a flat game rewrites its one бой with the pristine
// document, roster and all — the game stays playable, not a stump.
func TestClearFlatGameKeepsItPlayable(t *testing.T) {
	db, festID := newFest(t, 3)
	var gameID int64
	inTx(t, db, func(tx *sql.Tx) (err error) {
		gameID, err = gamebuild.Create(context.Background(), tx, gamebuild.Spec{FestID: festID, Type: "od", ODTours: 2, ODQuestions: 12})
		return err
	})
	inTx(t, db, func(tx *sql.Tx) error {
		_, err := tx.Exec(`update matches set state_json = '{"played": true}' where game_id = ?`, gameID)
		return err
	})
	var first string
	inTx(t, db, func(tx *sql.Tx) (err error) {
		first, err = gamebuild.Clear(context.Background(), tx, festID, gameID)
		return err
	})
	if first != "main" {
		t.Errorf("first бой = %q, want main", first)
	}
	doc, err := store.LoadGameDoc(context.Background(), db, festID, gameID)
	if err != nil || !doc.MatchID.Valid {
		t.Fatalf("the flat match is gone: %v", err)
	}
	state := doc.State
	if strings.Contains(state, "played") || !strings.Contains(state, "Команда 1") {
		t.Errorf("state after clear = %s; want pristine with the roster", state)
	}
	view, err := festview.Load(context.Background(), db, festID, gameID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Stages) != 1 || len(view.Stages[0].Matches) != 1 {
		t.Errorf("structure after clear = %+v, want one stage of one бой", view.Stages)
	}
}

// A Game that named its entrants keeps them through a clear: the brackets
// are rebuilt for those three, not for the whole фест.
func TestClearKeepsAGamesEntrants(t *testing.T) {
	db, festID := newFest(t, 5)
	var chosen []int64
	rows, err := db.Query(`select id from participants where fest_id = ? order by number limit 3`, festID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		chosen = append(chosen, id)
	}
	rows.Close()
	var gameID int64
	inTx(t, db, func(tx *sql.Tx) (err error) {
		gameID, err = gamebuild.Create(context.Background(), tx, gamebuild.Spec{FestID: festID, Type: "brain", Label: "Брейн",
			DSL: "[scheme]\nkind: roundrobin\ngroup_size: 3\nmatch_size: 2\n", Entrants: chosen})
		return err
	})
	inTx(t, db, func(tx *sql.Tx) (err error) {
		_, err = gamebuild.Clear(context.Background(), tx, festID, gameID)
		return err
	})
	var entrants, seated int
	if err := db.QueryRow(`select count(*) from game_participants where game_id = ?`, gameID).Scan(&entrants); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`select count(distinct participant_id) from match_slots s join matches m on m.id = s.match_id where m.game_id = ? and s.participant_id is not null`, gameID).Scan(&seated); err != nil {
		t.Fatal(err)
	}
	if entrants != 3 || seated != 3 {
		t.Errorf("after clear: %d entrants, %d seated; want 3 and 3", entrants, seated)
	}
}
