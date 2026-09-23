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
// database DOPE_BUGMAJOR_DEMO names (a new file): twenty teams with their
// players and зачёты, an ОД of three tours, fourteen student troikas and their
// Тройка with the отбор written and the Swiss stage two rounds in, and the
// student Эрудит-секстет seeded from the ОД with its first game played. A host
// logs in as demo / demopass123. It is skipped without the variable.
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

	// Twenty teams of five; the first fourteen are students.
	cities := []string{"Брест", "Минск", "Гомель", "Гродно"}
	firsts := []string{"Иван", "Анна", "Олег", "Мария", "Павел", "Ольга", "Кирилл", "Дарья", "Егор", "Алина"}
	lasts := []string{"Петров", "Сидорова", "Кузнецов", "Ковалёва", "Новик", "Жук", "Мельник", "Шевчук", "Бондарь", "Лис"}
	type person struct{ first, last string }
	var players [][]person
	for n := 1; n <= 20; n++ {
		res, err := db.Exec(`insert into fest_teams(fest_id, name, city, position, number) values(?, ?, ?, ?, ?)`,
			festID, fmt.Sprintf("Команда %d", n), cities[n%len(cities)], n, n)
		if err != nil {
			t.Fatal(err)
		}
		teamID, _ := res.LastInsertId()
		if n <= 14 {
			if _, err := db.Exec(`insert into fest_team_flags(team_id, position, short, full) values(?, 0, 'Студ', 'Студенческая команда')`, teamID); err != nil {
				t.Fatal(err)
			}
		}
		var team []person
		for k := 0; k < 5; k++ {
			p := person{firsts[(n+k)%len(firsts)], fmt.Sprintf("%s-%d", lasts[(n*3+k)%len(lasts)], n)}
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

	// The ОД: three tours of twelve, team n taking fewer the higher its number.
	odID := createGameThroughForm(t, srv, festID, token, map[string]string{"game_type": "od", "od_tours": "3", "od_questions": "12"})
	entries := make([][]int, 36)
	for q := range entries {
		for n := 1; n <= 20; n++ {
			if (q*7+n*5)%23 < 23-n {
				entries[q] = append(entries[q], n)
			}
		}
	}
	if resp := scopedAPIRequest(t, srv, http.MethodPatch, fmt.Sprintf("/api/fest/%d/games/%d/state", festID, odID),
		map[string]any{"ops": []map[string]any{{"path": []any{"entries"}, "value": entries}}}, token); resp.Code != http.StatusOK {
		t.Fatalf("od: %d %s", resp.Code, resp.Body.String())
	}
	var odCode string
	if err := db.QueryRow(`select code from games where id = ?`, odID).Scan(&odCode); err != nil {
		t.Fatal(err)
	}

	// Fourteen student troikas: three players of one team, and every fourth
	// a mixed one of two teams.
	var troikaIDs []int64
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 14; n++ {
		team := players[n-1]
		names := []string{team[0].first + " " + team[0].last, team[1].first + " " + team[1].last, team[2].first + " " + team[2].last}
		if n%4 == 0 {
			other := players[n%14]
			names[2] = other[3].first + " " + other[3].last
			names = append(names, other[4].first+" "+other[4].last)
		}
		id, err := roster.SaveAssembledTx(context.Background(), tx, festID, 0, roster.AssembledInput{Name: fmt.Sprintf("Тройка %d", n), Players: names})
		if err != nil {
			t.Fatal(err)
		}
		troikaIDs = append(troikaIDs, id)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// Тройка: the отбор written, then two Swiss rounds played.
	troikaID := createSchemeGameFor(t, db, festID, "troika", "Тройка (студенты)", readFile(t, "../../../scripts/bugmajor/troika.dsl"), troikaIDs)
	seats := matchSeatIDs(t, db, troikaID, "s1-m1")
	var ops []map[string]any
	for side := range seats {
		for theme := 0; theme < 9; theme++ {
			for q := 0; q < 3; q++ {
				if count := (side*5 + theme*7 + q*3) % 4; count > 0 {
					ops = append(ops, map[string]any{"path": []any{"sides", side, "counts", theme, q}, "value": count})
				}
			}
		}
	}
	patchState(t, srv, festID, troikaID, "s1-m1", token, ops)
	finish(t, srv, festID, troikaID, "s1-m1", token)
	seedOf := map[int64]int{}
	for i, id := range standingsOrder(t, db, troikaID, "s1") {
		seedOf[id] = i + 1
	}
	for round := 1; round <= 2; round++ {
		for _, code := range roundBouts(t, db, troikaID, fmt.Sprintf("s2-r%d", round)) {
			playTroikaDemo(t, srv, db, festID, troikaID, code, token, seedOf)
		}
	}

	// Эрудит-секстет, students: seeded from the ОД, the first game played.
	esDSL := strings.Replace(readFile(t, "../../../scripts/bugmajor/es-students.dsl"), "seed: od\n", "seed: "+odCode+"\n", 1)
	esID := createSchemeGame(t, db, festID, "es", "Эрудит-секстет (студенты)", esDSL)
	if resp := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/games/%d/seed-import/run", festID, esID), nil, token); resp.Code != http.StatusOK {
		t.Fatalf("es seed: %d %s", resp.Code, resp.Body.String())
	}
	esSeeds := map[int64]int{}
	for _, row := range seedRanks(t, db, esID) {
		esSeeds[row[0]] = int(row[1])
	}
	for _, code := range roundBouts(t, db, esID, "s1-r1") {
		playES(t, srv, festID, esID, code, token, esSeeds)
	}
	t.Logf("fest %d: Тройка %d, ЭС %d, ОД %d in %s", festID, troikaID, esID, odID, path)
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
