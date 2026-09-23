package tests

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"dope/dope/domain/core"
	"dope/dope/domain/roster"
	"dope/dope/platform/realtime"
	dopeserver "dope/dope/server"
)

// TestBugMajorDemoFest builds a Bug Major III fest to look at, into the
// database DOPE_BUGMAJOR_DEMO names (a new file): thirty-six teams with their
// players, half of them students; an ОД of seven tours with three played; a
// Мелотрек; fourteen troikas per зачёт and a Тройка for each, the отбор
// written and the Swiss stage under way; and an Эрудит-секстет per зачёт
// seeded from the ОД, one a game in, the other at its semifinals. A host logs
// in as demo / demopass123. It is skipped without the variable.
//
//	DOPE_BUGMAJOR_DEMO=$PWD/.tmp/bm.db go test ./dope/server/tests -run BugMajorDemo
func TestBugMajorDemoFest(t *testing.T) {
	path := os.Getenv("DOPE_BUGMAJOR_DEMO")
	if path == "" {
		t.Skip("DOPE_BUGMAJOR_DEMO names the database to build")
	}
	db, err := dopeserver.OpenFestDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	createDefaultFestFixture(t, db, dopeserver.DefaultMatch())
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
		e.Assets = dopeserver.StaticFiles
	})
	hash, err := dopeserver.HashPassword("demopass123")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := db.Exec(`insert into users(username, password_hash, password_salt, is_system, created_at, updated_at) values('demo', ?, '', 0, ?, ?)`, hash, now, now)
	if err != nil {
		t.Fatal(err)
	}
	demoUser, _ := res.LastInsertId()
	festID := newFest(t, db, "bugmajor", "Bug Major III", demoUser)
	token := createTestSession(t, srv, demoUser)

	// Thirty-six teams of five; the first eighteen are students.
	const teams, students = 36, 18
	cities := []string{"Брест", "Минск", "Гомель", "Гродно", "Витебск", "Могилёв"}
	names := []string{"Бобры", "Зубры", "Совы", "Ежи", "Аисты", "Лоси", "Рыси", "Волки", "Барсуки", "Выдры", "Дятлы", "Кроты"}
	firsts := []string{"Иван", "Анна", "Олег", "Мария", "Павел", "Ольга", "Кирилл", "Дарья", "Егор", "Алина", "Максим", "Софья"}
	lasts := []string{"Петров", "Сидорова", "Кузнецов", "Ковалёва", "Новик", "Жук", "Мельник", "Шевчук", "Бондарь", "Лис", "Климович", "Гончар"}
	type person struct{ first, last string }
	var players [][]person
	for n := 1; n <= teams; n++ {
		name := fmt.Sprintf("%s-%d", names[(n-1)%len(names)], (n-1)/len(names)+1)
		res, err := db.Exec(`insert into fest_teams(fest_id, name, city, position, number) values(?, ?, ?, ?, ?)`,
			festID, name, cities[n%len(cities)], n, n)
		if err != nil {
			t.Fatal(err)
		}
		teamID, _ := res.LastInsertId()
		if n <= students {
			if _, err := db.Exec(`insert into fest_team_flags(team_id, position, short, full) values(?, 0, 'Студ', 'Студенческая команда')`, teamID); err != nil {
				t.Fatal(err)
			}
		}
		var team []person
		for k := 0; k < 5; k++ {
			p := person{firsts[(n+k)%len(firsts)], fmt.Sprintf("%s %d", lasts[(n*5+k)%len(lasts)], n)}
			res, err := db.Exec(`insert into fest_players(fest_id, first_name, last_name) values(?, ?, ?)`, festID, p.first, p.last)
			if err != nil {
				t.Fatal(err)
			}
			playerID, _ := res.LastInsertId()
			if _, err := db.Exec(`insert into fest_team_players(team_id, player_id, roster_order) values(?, ?, ?)`, teamID, playerID, k); err != nil {
				t.Fatal(err)
			}
			team = append(team, p)
		}
		players = append(players, team)
	}

	// ОД: seven tours of twelve, the first three played.
	odID := createGameThroughForm(t, srv, festID, token, map[string]string{"game_type": "od", "od_tours": "7", "od_questions": "12"})
	entries := make([][]int, 84)
	for q := 0; q < 36; q++ {
		entries[q] = []int{}
		for n := 1; n <= teams; n++ {
			if (q*13+n*7)%41 < 40-n {
				entries[q] = append(entries[q], n)
			}
		}
	}
	for q := 36; q < 84; q++ {
		entries[q] = []int{}
	}
	if resp := scopedAPIRequest(t, srv, http.MethodPatch, fmt.Sprintf("/api/fest/%d/games/%d/state", festID, odID),
		map[string]any{"ops": []map[string]any{{"path": []any{"entries"}, "value": entries}}}, token); resp.Code != http.StatusOK {
		t.Fatalf("od: %d %s", resp.Code, resp.Body.String())
	}
	var odCode string
	if err := db.QueryRow(`select code from games where id = ?`, odID).Scan(&odCode); err != nil {
		t.Fatal(err)
	}

	// Мелотрек: five темы of five вопросы, two points each, all teams played.
	melotrekID := createGameThroughForm(t, srv, festID, token, map[string]string{"game_type": "multi",
		"multi_games": "Вокруг транспорта: {0,1,2}x5\nЦвета: {0,1,2}x5\nГорода: {0,1,2}x5\nЖивотные: {0,1,2}x5\nЕда: {0,1,2}x5"})
	var cellOps []map[string]any
	for g := 0; g < 5; g++ {
		for p := 0; p < teams; p++ {
			for c := 0; c < 5; c++ {
				if v := (g*3 + p*5 + c*7) % 5; v > 0 && v <= 2 {
					cellOps = append(cellOps, map[string]any{"path": []any{"games", g, "cells", p, c}, "value": v})
				}
			}
		}
	}
	if resp := scopedAPIRequest(t, srv, http.MethodPatch, fmt.Sprintf("/api/fest/%d/games/%d/state", festID, melotrekID),
		map[string]any{"ops": cellOps}, token); resp.Code != http.StatusOK {
		t.Fatalf("мелотрек: %d %s", resp.Code, resp.Body.String())
	}
	if _, err := db.Exec(`update games set title = 'Мелотрек' where id = ?`, melotrekID); err != nil {
		t.Fatal(err)
	}

	// Troikas: fourteen per зачёт, three players of one team, every fourth
	// one mixed from two teams of its зачёт.
	makeTroikas := func(label string, from, count int) []int64 {
		var ids []int64
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < count; i++ {
			team := players[from+i]
			who := []string{team[0].first + " " + team[0].last, team[1].first + " " + team[1].last, team[2].first + " " + team[2].last}
			if i%4 == 3 {
				other := players[from+(i+1)%count]
				who[2] = other[3].first + " " + other[3].last
				who = append(who, other[4].first+" "+other[4].last)
			}
			id, err := roster.SaveAssembledTx(context.Background(), tx, festID, 0,
				roster.AssembledInput{Name: fmt.Sprintf("%s %d", label, i+1), Players: who})
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		return ids
	}
	studentTroikas := makeTroikas("Тройка С", 0, 14)
	adultTroikas := makeTroikas("Тройка В", students, 14)

	// Тройка: the отбор written; the students' Swiss stage two rounds in, the
	// adults' first round half played.
	playTroika := func(title string, entrants []int64, rounds, lastRoundBouts int, salt int) int64 {
		gameID := createSchemeGameFor(t, db, festID, "troika", title, readFile(t, "../../../scripts/bugmajor/troika.dsl"), entrants)
		seats := matchSeatIDs(t, db, gameID, "s1-m1")
		var ops []map[string]any
		for side := range seats {
			for theme := 0; theme < 9; theme++ {
				for q := 0; q < 3; q++ {
					if count := (side*5 + theme*7 + q*3 + salt) % 4; count > 0 {
						ops = append(ops, map[string]any{"path": []any{"sides", side, "counts", theme, q}, "value": count})
					}
				}
			}
		}
		patchState(t, srv, festID, gameID, "s1-m1", token, ops)
		finish(t, srv, festID, gameID, "s1-m1", token)
		seedOf := map[int64]int{}
		for i, id := range standingsOrder(t, db, gameID, "s1") {
			seedOf[id] = i + 1
		}
		for round := 1; round <= rounds; round++ {
			bouts := roundBouts(t, db, gameID, fmt.Sprintf("s2-r%d", round))
			if round == rounds && lastRoundBouts < len(bouts) {
				bouts = bouts[:lastRoundBouts]
			}
			for _, code := range bouts {
				playTroikaDemo(t, srv, db, festID, gameID, code, token, seedOf)
			}
		}
		return gameID
	}
	troikaS := playTroika("Тройка — студенты", studentTroikas, 2, 99, 0)
	troikaA := playTroika("Тройка — взрослые", adultTroikas, 1, 3, 1)

	// Эрудит-секстет: both seeded from the ОД by зачёт; the students' first
	// game played, the adults' group stage done and their semifinals seated.
	playES := func(title, file string, games int) int64 {
		dsl := strings.Replace(readFile(t, "../../../scripts/bugmajor/"+file), "seed: od\n", "seed: "+odCode+"\n", 1)
		gameID := createSchemeGame(t, db, festID, "es", title, dsl)
		if resp := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/games/%d/seed-import/run", festID, gameID), nil, token); resp.Code != http.StatusOK {
			t.Fatalf("%s seed: %d %s", title, resp.Code, resp.Body.String())
		}
		seeds := map[int64]int{}
		for _, row := range seedRanks(t, db, gameID) {
			seeds[row[0]] = int(row[1])
		}
		for g := 1; g <= games; g++ {
			for _, code := range roundBouts(t, db, gameID, fmt.Sprintf("s1-r%d", g)) {
				playESDemo(t, srv, db, festID, gameID, code, token, seeds)
			}
		}
		return gameID
	}
	esS := playES("Эрудит-секстет — студенты", "es-students.dsl", 1)
	esA := playES("Эрудит-секстет — взрослые", "es-adults.dsl", 2)
	t.Logf("fest %d: ОД %d, Мелотрек %d, Тройка %d/%d, ЭС %d/%d in %s", festID, odID, melotrekID, troikaS, troikaA, esS, esA, path)
}

// playESDemo seats each team's first three players on the first тема and
// lets the better seed take more.
func playESDemo(t *testing.T, srv *dopeserver.Server, db *sql.DB, festID, gameID int64, code, token string, seeds map[int64]int) {
	t.Helper()
	playES(t, srv, festID, gameID, code, token, seeds)
}

// playTroikaDemo seats each side's three first players and lets the better
// seed win the first тема.
func playTroikaDemo(t *testing.T, srv *dopeserver.Server, db *sql.DB, festID, gameID int64, code, token string, seedOf map[int64]int) {
	t.Helper()
	ids := matchSeatIDs(t, db, gameID, code)
	var ops []map[string]any
	for side, id := range ids {
		rows, err := db.Query(`select player_id from participant_players where participant_id = ? order by roster_order limit 3`, id)
		if err != nil {
			t.Fatal(err)
		}
		var order []int64
		for rows.Next() {
			var p int64
			if err := rows.Scan(&p); err != nil {
				t.Fatal(err)
			}
			order = append(order, p)
		}
		rows.Close()
		for len(order) < 3 {
			order = append(order, 0)
		}
		for theme := 0; theme < 6; theme++ {
			ops = append(ops, map[string]any{"path": []any{"sides", side, "themes", theme, "order"}, "value": order})
		}
	}
	patchState(t, srv, festID, gameID, code, token, ops)
	playBout(t, srv, festID, gameID, code, token, ids, seedOf)
}
