package tests

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"dope/dope/domain/roster"

	corei18n "pecheny.me/dopecore/i18nstrings"
)

// Troikas are assembled out of fest players: they are Participants the rating
// roster does not have, a troika of two or more players of one team counts
// for that team, and a Тройка Game seats them like any other entrant.
func TestTroikasAreAssembledFromFestPlayers(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	festTeamWithPlayers(t, db, festID, "Альфа", [][2]string{{"Иван", "Петров"}, {"Анна", "Сидорова"}, {"Олег", "Кузнецов"}})

	inputs, err := roster.ParseAssembledLines("Бобры: Иван Петров (Альфа), Анна Сидорова, Ия Ли\n\nЕжи: Олег Кузнецов, Ян Ким\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 2 || inputs[0].Name != "Бобры" || len(inputs[0].Players) != 3 {
		t.Fatalf("parsed %+v", inputs)
	}
	withTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		for _, in := range inputs {
			if _, err := roster.SaveAssembledTx(ctx, tx, festID, 0, in); err != nil {
				return err
			}
		}
		return nil
	})
	troikas, err := roster.LoadAssembled(t.Context(), db, festID)
	if err != nil {
		t.Fatal(err)
	}
	if len(troikas) != 2 {
		t.Fatalf("troikas = %+v", troikas)
	}
	bobry, ezhi := troikas[0], troikas[1]
	if bobry.Name != "Бобры" || strings.Join(bobry.Players, ", ") != "Иван Петров, Анна Сидорова, Ия Ли" || bobry.Team != "Альфа" {
		t.Fatalf("Бобры = %+v", bobry)
	}
	if ezhi.Team != "" {
		t.Fatalf("Ежи have one Альфа player and count for nobody, got %q", ezhi.Team)
	}

	// The rules a host is told about.
	for _, bad := range []roster.AssembledInput{
		{Name: "Один", Players: []string{"Ия Ли"}},
		{Name: "Пятеро", Players: []string{"А Б", "В Г", "Д Е", "Ж З", "И К"}},
		{Name: "Бобры", Players: []string{"А Б", "В Г"}},
		{Name: "Дубль", Players: []string{"Ия Ли", "Ия  Ли"}},
	} {
		err := saveErr(t, db, festID, 0, bad)
		if _, user := corei18n.AsUser(err); !user {
			t.Errorf("%s: err = %v, want a message for the host", bad.Name, err)
		}
	}

	// A substitution: Ежи take a third player.
	withTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		_, err := roster.SaveAssembledTx(ctx, tx, festID, ezhi.ID, roster.AssembledInput{Name: "Ежи", Players: []string{"Олег Кузнецов", "Ян Ким", "Иван Петров"}})
		return err
	})

	// A Тройка Game seats them, and a seated troika cannot be deleted.
	gameID := createSchemeGameFor(t, db, festID, "troika", "Тройка",
		"[scheme]\nkind: single_elimination\nparticipants: 2\nthemes: 1\n", []int64{bobry.ID, ezhi.ID})
	if got := len(gameEntrants(t, db, gameID)); got != 2 {
		t.Fatalf("entrants = %d", got)
	}
	var roster3 int
	if err := db.QueryRow(`select count(*) from participant_players where participant_id = ?`, ezhi.ID).Scan(&roster3); err != nil || roster3 != 3 {
		t.Fatalf("Ежи roster = %d, %v", roster3, err)
	}
	err = func() error {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		return roster.DeleteAssembledTx(t.Context(), tx, festID, bobry.ID)
	}()
	if _, user := corei18n.AsUser(err); !user {
		t.Fatalf("deleting a seated troika: %v", err)
	}
}

func festTeamWithPlayers(t *testing.T, db *sql.DB, festID int64, name string, players [][2]string) int64 {
	t.Helper()
	res, err := db.Exec(`insert into fest_teams(fest_id, name, city, position) values(?, ?, '', 1)`, festID, name)
	if err != nil {
		t.Fatal(err)
	}
	teamID, _ := res.LastInsertId()
	for i, p := range players {
		res, err := db.Exec(`insert into fest_players(fest_id, first_name, last_name) values(?, ?, ?)`, festID, p[0], p[1])
		if err != nil {
			t.Fatal(err)
		}
		playerID, _ := res.LastInsertId()
		if _, err := db.Exec(`insert into fest_team_players(team_id, player_id, roster_order) values(?, ?, ?)`, teamID, playerID, i); err != nil {
			t.Fatal(err)
		}
	}
	return teamID
}

func withTx(t *testing.T, db *sql.DB, fn func(ctx context.Context, tx *sql.Tx) error) {
	t.Helper()
	tx, err := db.Begin()
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

func saveErr(t *testing.T, db *sql.DB, festID, id int64, in roster.AssembledInput) error {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	_, err = roster.SaveAssembledTx(t.Context(), tx, festID, id, in)
	return err
}
