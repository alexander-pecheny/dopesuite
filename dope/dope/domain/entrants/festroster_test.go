package entrants_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"

	"dope/dope/domain/core"
	"dope/dope/domain/entrants"
	"dope/dope/domain/festops"
	"dope/dope/domain/gamebuild"
	"dope/dope/domain/roster"
	dopeserver "dope/dope/server"
	"dope/dope/storage/store"
)

const troikaScheme = "\n[scheme]\nkind: flat\nwritten: true\nthemes: 1\nletters: false\n"

// fest is a fest with two teams, Альфа in the Студ зачёт and Бета in none,
// and two Тройка Games: one takes the Студ troikas, the other the rest.
type fest struct {
	db                      *sql.DB
	id                      int64
	alpha, beta             int64
	studTroika, adultTroika int64
	studentGame, adultGame  int64
}

func newFest(t *testing.T) *fest {
	t.Helper()
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f := &fest{db: db}
	f.inTx(t, func(ctx context.Context, tx *sql.Tx) (err error) {
		userID, err := dopeserver.EnsureSystemUser(ctx, tx)
		if err != nil {
			return err
		}
		f.id, err = store.InsertReturningID(ctx, tx, `
insert into fests(slug, title, created_by, created_at, updated_at) values('fest', 'Фест', ?, '2026-10-06', '2026-10-06')`, userID)
		if err != nil {
			return err
		}
		if f.alpha, err = teamWithPlayers(ctx, tx, f.id, "Альфа", "А"); err != nil {
			return err
		}
		if f.beta, err = teamWithPlayers(ctx, tx, f.id, "Бета", "Б"); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `insert into fest_team_flags(team_id, position, short, full) values(?, 1, 'Студ', 'Студ')`, f.alpha)
		return err
	})
	f.studTroika = f.addTroika(t, "С1", "А Один", "А Два")
	f.adultTroika = f.addTroika(t, "В1", "Б Один", "Б Два")
	f.studentGame = f.createGame(t, "Тройка — студенты", "[init]\ndivision: Студ\n"+troikaScheme)
	f.adultGame = f.createGame(t, "Тройка — взрослые", "[init]\ndivision: -Студ\n"+troikaScheme)
	return f
}

func teamWithPlayers(ctx context.Context, tx *sql.Tx, festID int64, name, initial string) (int64, error) {
	teamID, err := store.InsertReturningID(ctx, tx, `insert into fest_teams(fest_id, name, city, position) values(?, ?, '', 1)`, festID, name)
	if err != nil {
		return 0, err
	}
	for i, last := range []string{"Один", "Два", "Три", "Четыре"} {
		playerID, err := store.InsertReturningID(ctx, tx, `insert into fest_players(fest_id, first_name, last_name) values(?, ?, ?)`, festID, initial, last)
		if err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `insert into fest_team_players(team_id, player_id, roster_order) values(?, ?, ?)`, teamID, playerID, i); err != nil {
			return 0, err
		}
	}
	return teamID, nil
}

func (f *fest) inTx(t *testing.T, fn func(ctx context.Context, tx *sql.Tx) error) {
	t.Helper()
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := fn(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func (f *fest) addTroika(t *testing.T, name string, players ...string) int64 {
	t.Helper()
	var id int64
	f.inTx(t, func(ctx context.Context, tx *sql.Tx) error {
		written, err := entrants.AddTroikasTx(ctx, tx, f.id, []roster.AssembledInput{{Name: name, Players: players}})
		if err == nil {
			id = written.Added[0]
		}
		return err
	})
	return id
}

func (f *fest) createGame(t *testing.T, label, dsl string) int64 {
	t.Helper()
	var id int64
	f.inTx(t, func(ctx context.Context, tx *sql.Tx) (err error) {
		id, err = gamebuild.Create(ctx, tx, gamebuild.Spec{FestID: f.id, Type: "troika", Label: label, DSL: dsl})
		return err
	})
	return id
}

func (f *fest) entrantsOf(t *testing.T, q store.Queryer, gameID int64) []int64 {
	t.Helper()
	ids, err := store.CollectRows(t.Context(), q, `
select participant_id from game_participants where game_id = ? order by position`, []any{gameID},
		func(rows *sql.Rows) (int64, error) {
			var id int64
			return id, rows.Scan(&id)
		})
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

// names says the write's broadcast names both Тройка Games.
func (f *fest) names(t *testing.T, written core.FestWrite) {
	t.Helper()
	views := written.Broadcast.Views
	if !slices.Contains(views, f.studentGame) || !slices.Contains(views, f.adultGame) {
		t.Fatalf("broadcast views = %v, want both Troika Games %d and %d", views, f.studentGame, f.adultGame)
	}
}

// Flags typed by hand move each team's troika to the Game of its new зачёт
// inside the write's own transaction, and the write names both Games for the
// broadcast.
func TestTeamFlagsMoveATroikaBetweenGames(t *testing.T) {
	t.Parallel()
	f := newFest(t)
	if got := f.entrantsOf(t, f.db, f.studentGame); !slices.Equal(got, []int64{f.studTroika}) {
		t.Fatalf("student game = %v, want %v", got, f.studTroika)
	}
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	written, err := entrants.SaveTeamFlagsTx(t.Context(), tx, f.id, map[int64][]roster.FestRosterFlag{f.alpha: nil, f.beta: {{Short: "Студ", Full: "Студ"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.entrantsOf(t, tx, f.studentGame); !slices.Equal(got, []int64{f.adultTroika}) {
		t.Fatalf("student game inside the write = %v, want %v", got, f.adultTroika)
	}
	if got := f.entrantsOf(t, tx, f.adultGame); !slices.Equal(got, []int64{f.studTroika}) {
		t.Fatalf("adult game inside the write = %v, want %v", got, f.studTroika)
	}
	if written.Event != "fest:team-flags" {
		t.Fatalf("event = %q", written.Event)
	}
	f.names(t, written)
}

// A troika added, or moved to another зачёт, re-seats the Games that follow
// its division, and the write names them with how each followed.
func TestTroikaWritesReseatTheFollowingGames(t *testing.T) {
	t.Parallel()
	f := newFest(t)
	var added entrants.TroikaWrite
	f.inTx(t, func(ctx context.Context, tx *sql.Tx) (err error) {
		added, err = entrants.AddTroikasTx(ctx, tx, f.id, []roster.AssembledInput{{Name: "С2", Players: []string{"А Три", "А Четыре"}}})
		return err
	})
	second := added.Added[0]
	if got := f.entrantsOf(t, f.db, f.studentGame); !slices.Equal(got, []int64{f.studTroika, second}) {
		t.Fatalf("student game after an add = %v", got)
	}
	f.names(t, added.Write)
	if len(added.Games) != 2 || !added.Games[0].Current || !added.Games[1].Current {
		t.Fatalf("followed games = %+v, want both current", added.Games)
	}

	none := ""
	var saved entrants.TroikaWrite
	f.inTx(t, func(ctx context.Context, tx *sql.Tx) (err error) {
		saved, err = entrants.SaveTroikaTx(ctx, tx, f.id, second, roster.AssembledInput{Name: "С2", Players: []string{"А Три", "А Четыре"},
			Placement: &roster.AssembledPlacement{HeadTeamID: f.alpha, Division: &none}})
		return err
	})
	if got := f.entrantsOf(t, f.db, f.adultGame); !slices.Equal(got, []int64{f.adultTroika, second}) {
		t.Fatalf("adult game after a move = %v", got)
	}
	f.names(t, saved.Write)

	var deleted entrants.TroikaWrite
	f.inTx(t, func(ctx context.Context, tx *sql.Tx) (err error) {
		deleted, err = entrants.DeleteTroikaTx(ctx, tx, f.id, second)
		return err
	})
	if got := f.entrantsOf(t, f.db, f.adultGame); !slices.Equal(got, []int64{f.adultTroika}) {
		t.Fatalf("adult game after a delete = %v", got)
	}
	f.names(t, deleted.Write)
}

// A Game's scheme changed to take another division re-seats it, and the
// settings write names the Troika Games for the broadcast.
func TestSchemeChangeNamesTheTroikaGames(t *testing.T) {
	t.Parallel()
	f := newFest(t)
	var written core.FestWrite
	f.inTx(t, func(ctx context.Context, tx *sql.Tx) (err error) {
		written, err = festops.UpdateSettingsTx(ctx, tx, f.id, f.adultGame, festops.Settings{Title: "Тройка — все студенты", SchemeDSL: "[init]\ndivision: Студ\n" + troikaScheme})
		return err
	})
	if got := f.entrantsOf(t, f.db, f.adultGame); !slices.Equal(got, []int64{f.studTroika}) {
		t.Fatalf("game after the scheme change = %v, want %v", got, f.studTroika)
	}
	f.names(t, written)
}
