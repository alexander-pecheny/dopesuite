package tests

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	"dope/dope/domain/entrants"
	"dope/dope/domain/roster"
)

// A Тройка created for a зачёт with no troika in it waits with empty seats and
// no Entrant list. When the host then takes the division: line out of its
// scheme, the recompile seats the fest's troikas and records them, as Clear
// does. It used to build bouts for every troika, seat nobody and record no
// list, so the entrants tab stayed empty for good.
//
// The Game takes no division after that, so it no longer follows the fest's
// troikas: one added later is not seated until the host adds it on the
// entrants tab. Only a Game with a division follows (entrants/follow.go).
func TestRecompileOfAnUndividedTroikaRecordsItsEntrants(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	festTeamWithPlayers(t, db, festID, "Бета", [][2]string{{"Б", "Один"}, {"Б", "Два"}, {"Б", "Три"}, {"Б", "Четыре"}, {"Б", "Пять"}, {"Б", "Шесть"}})
	add := func(name string, players ...string) int64 {
		var id int64
		withTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
			written, err := entrants.AddTroikasTx(ctx, tx, festID, []roster.AssembledInput{{Name: name, Players: players}})
			if err == nil {
				id = written.Added[0]
			}
			return err
		})
		return id
	}
	const scheme = "[scheme]\nkind: flat\nwritten: true\nthemes: 1\nletters: false\n"
	school := createSchemeGameFor(t, db, festID, "troika", "Тройка — школьники", "[init]\ndivision: Школ\n\n"+scheme, nil)
	b1 := add("В1", "Б Один", "Б Два")
	b2 := add("В2", "Б Три", "Б Четыре")
	if got := gameEntrants(t, db, school); len(got) != 0 {
		t.Fatalf("the school game took %v, troikas of another зачёт", got)
	}

	withTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		_, err := entrants.RecompileTx(ctx, tx, festID, school, scheme)
		return err
	})
	if got := gameEntrants(t, db, school); !slices.Equal(got, []int64{b1, b2}) {
		t.Fatalf("entrants after the recompile = %v, want %v", got, []int64{b1, b2})
	}
	if got := matchSeatIDs(t, db, school, "s1-m1"); !slices.Equal(got, []int64{b1, b2}) {
		t.Fatalf("отбор seats after the recompile = %v, want %v", got, []int64{b1, b2})
	}

	add("В3", "Б Пять", "Б Шесть")
	if got := gameEntrants(t, db, school); !slices.Equal(got, []int64{b1, b2}) {
		t.Fatalf("an undivided game took a later troika: entrants = %v", got)
	}
}

// A Тройка from before Games recorded their Entrants has its seats but no
// list. Two troikas joined the fest since. A recompile used to grow its
// отбор to five rows, two of them empty, while the tab still showed three. It
// now keeps the three where they sit, seats the two after them and records
// all five.
func TestRecompileOfALegacyTroikaSeatsAndRecordsTheLateTroikas(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	festTeamWithPlayers(t, db, festID, "Бета", [][2]string{{"Б", "Один"}, {"Б", "Два"}, {"Б", "Три"}, {"Б", "Четыре"}, {"Б", "Пять"}, {"Б", "Шесть"}})
	festTeamWithPlayers(t, db, festID, "Гамма", [][2]string{{"Г", "Один"}, {"Г", "Два"}, {"Г", "Три"}, {"Г", "Четыре"}})
	var ids []int64
	add := func(name string, players ...string) {
		withTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
			written, err := entrants.AddTroikasTx(ctx, tx, festID, []roster.AssembledInput{{Name: name, Players: players}})
			if err == nil {
				ids = append(ids, written.Added[0])
			}
			return err
		})
	}
	add("В1", "Б Один", "Б Два")
	add("В2", "Б Три", "Б Четыре")
	add("В3", "Б Пять", "Б Шесть")
	const scheme = "[scheme]\nkind: flat\nwritten: true\nthemes: 1\nletters: false\n"
	game := createSchemeGameFor(t, db, festID, "troika", "Тройка", scheme, nil)
	if _, err := db.Exec(`delete from game_participants where game_id = ?`, game); err != nil {
		t.Fatal(err)
	}
	add("В4", "Г Один", "Г Два")
	add("В5", "Г Три", "Г Четыре")

	withTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		_, err := entrants.RecompileTx(ctx, tx, festID, game, scheme+"\n")
		return err
	})
	if got := matchSeatIDs(t, db, game, "s1-m1"); !slices.Equal(got, ids) {
		t.Fatalf("отбор seats after the recompile = %v, want %v", got, ids)
	}
	if got := gameEntrants(t, db, game); !slices.Equal(got, ids) {
		t.Fatalf("entrants after the recompile = %v, want %v", got, ids)
	}
}
