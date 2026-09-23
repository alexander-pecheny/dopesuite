package tests

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"dope/dope/domain/core"
	"dope/dope/domain/imports"
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
	lasts := []string{"Жук", "Новик", "Мельник", "Шевчук", "Бондарь", "Лис", "Климович", "Гончар", "Ярошевич", "Сенкевич", "Кравец", "Ткач", "Коваль", "Швед", "Рыбак"}
	type person struct{ first, last string }
	var players [][]person
	var imported []roster.FestRosterImportTeam
	for n := 1; n <= teams; n++ {
		team := roster.FestRosterImportTeam{
			RatingID: int64(90000 + n),
			Name:     fmt.Sprintf("%s-%d", names[(n-1)%len(names)], (n-1)/len(names)+1),
			City:     cities[n%len(cities)],
			Number:   int64(n),
		}
		if n <= students {
			team.Flags = []roster.FestRosterFlag{{Short: "Студ", Full: "Студенческая команда"}}
		}
		var people []person
		for k := 0; k < 5; k++ {
			// Every one of the 180 people is a different pair of names.
			i := (n-1)*5 + k
			p := person{firsts[i/len(lasts)%len(firsts)], lasts[i%len(lasts)]}
			team.Players = append(team.Players, roster.FestRosterImportPlayer{RatingID: int64(900000 + n*10 + k), FirstName: p.first, LastName: p.last})
			people = append(people, p)
		}
		imported = append(imported, team)
		players = append(players, people)
	}
	if _, err := imports.ImportFestRoster(srv.Eng(), context.Background(), festID, 1, imported, imports.RosterChoice{}); err != nil {
		t.Fatalf("roster: %v", err)
	}
	// The import deals its own numbers; put them back in roster order so team
	// n is number n, the order the зачёты and the sample scores assume.
	for n, team := range imported {
		if _, err := db.Exec(`update fest_teams set number = ? where fest_id = ? and rating_id = ?`, -(n + 1), festID, team.RatingID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`update fest_teams set number = -number where fest_id = ? and number < 0`, festID); err != nil {
		t.Fatal(err)
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

// demoRoll is the demo's dice: the same bout fills the same way every run.
func demoRoll(parts ...int64) int {
	h := uint64(1469598103934665603)
	for _, p := range parts {
		h ^= uint64(p)
		h *= 1099511628211
	}
	return int(h % 100)
}

// demoRoster is a seat's people, in roster order.
func demoRoster(t *testing.T, db *sql.DB, participantID int64) []int64 {
	t.Helper()
	rows, err := db.Query(`select player_id from participant_players where participant_id = ? order by roster_order`, participantID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	return out
}

// bySeed orders a bout's seats best seed first.
func bySeed(ids []int64, seeds map[int64]int) []int {
	order := make([]int, len(ids))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return seeds[ids[order[a]]] < seeds[ids[order[b]]] })
	return order
}

// playESDemo plays an Эрудит-секстет бой in full: twelve темы, a team's five
// players seated two, one, one, one across each round's four темы, marks
// that favour the better seed, and no two teams level, so every place is
// clear and the next game can seat by it.
func playESDemo(t *testing.T, srv *dopeserver.Server, db *sql.DB, festID, gameID int64, code, token string, seeds map[int64]int) {
	t.Helper()
	ids := matchSeatIDs(t, db, gameID, code)
	rank := make([]int, len(ids))
	for r, seat := range bySeed(ids, seeds) {
		rank[seat] = r
	}
	marks := make([][12][5]string, len(ids))
	totals := make([]int, len(ids))
	var ops []map[string]any
	for seat, id := range ids {
		people := demoRoster(t, db, id)
		for theme := 0; theme < 12; theme++ {
			round, slot := theme/4, theme%4
			var sat []int64
			if len(people) > 0 {
				start := (round*2 + seat) % len(people)
				pick := func(k int) int64 { return people[(start+k)%len(people)] }
				switch slot {
				case 0:
					sat = []int64{pick(0), pick(1)}
				default:
					sat = []int64{pick(slot + 1)}
				}
			}
			ops = append(ops, map[string]any{"path": []any{"participants", fmt.Sprint(id), "themes", theme, "players"}, "value": sat})
			for q := 0; q < 5; q++ {
				roll := demoRoll(gameID, id, int64(theme), int64(q))
				switch {
				case roll < 50-10*rank[seat]-6*q:
					marks[seat][theme][q] = "right"
					totals[seat] += 10 * (q + 1)
				case roll < 62-10*rank[seat]:
					marks[seat][theme][q] = "wrong"
					totals[seat] -= 10 * (q + 1)
				}
			}
		}
	}
	// Level teams: the better seed takes one more вопрос until nobody is.
	for changed := true; changed; {
		changed = false
		for a := range ids {
			for b := range ids {
				if a == b || totals[a] != totals[b] || rank[a] > rank[b] {
					continue
				}
				for theme := 11; theme >= 0 && !changed; theme-- {
					for q := 0; q < 5 && !changed; q++ {
						if marks[a][theme][q] == "" {
							marks[a][theme][q] = "right"
							totals[a] += 10 * (q + 1)
							changed = true
						}
					}
				}
			}
		}
	}
	for seat, id := range ids {
		for theme := 0; theme < 12; theme++ {
			for q, mark := range marks[seat][theme] {
				if mark != "" {
					ops = append(ops, map[string]any{"path": []any{"participants", fmt.Sprint(id), "themes", theme, "answers", q}, "value": mark})
				}
			}
		}
	}
	patchState(t, srv, festID, gameID, code, token, ops)
	finish(t, srv, festID, gameID, code, token)
}

// playTroikaDemo plays a Тройка бой in full: each side's first three people
// in the кресла for every тема, turning round at the half, marks that favour
// the better seed, and the better seed ahead where the sheet came out level.
func playTroikaDemo(t *testing.T, srv *dopeserver.Server, db *sql.DB, festID, gameID int64, code, token string, seeds map[int64]int) {
	t.Helper()
	ids := matchSeatIDs(t, db, gameID, code)
	var themes int
	var values []int
	var raw string
	if err := db.QueryRow(`select state_json from matches where game_id = ? and code = ?`, gameID, code).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var state struct {
		Values []int `json:"values"`
	}
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		t.Fatal(err)
	}
	values = state.Values
	themes = len(values)
	rank := make([]int, len(ids))
	for r, seat := range bySeed(ids, seeds) {
		rank[seat] = r
	}
	marks := make([][][3][3]string, len(ids))
	totals := make([]int, len(ids))
	var ops []map[string]any
	for side, id := range ids {
		people := demoRoster(t, db, id)
		for len(people) < 3 {
			people = append(people, 0)
		}
		marks[side] = make([][3][3]string, themes)
		for theme := 0; theme < themes; theme++ {
			order := []int64{people[0], people[1], people[2]}
			if theme >= themes/2 {
				order = []int64{people[1], people[0], people[2]}
			}
			ops = append(ops, map[string]any{"path": []any{"sides", side, "themes", theme, "order"}, "value": order})
			for q := 0; q < 3; q++ {
				for chair := 0; chair < 3; chair++ {
					roll := demoRoll(gameID, id, int64(theme), int64(q), int64(chair))
					switch {
					case roll < 45-12*rank[side]:
						marks[side][theme][q][chair] = "right"
						totals[side] += values[theme]
					case roll < 60-12*rank[side]:
						marks[side][theme][q][chair] = "wrong"
					}
				}
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for a := range ids {
			for b := range ids {
				if a == b || totals[a] != totals[b] || rank[a] > rank[b] {
					continue
				}
				for theme := themes - 1; theme >= 0 && !changed; theme-- {
					for q := 0; q < 3 && !changed; q++ {
						for chair := 0; chair < 3 && !changed; chair++ {
							if marks[a][theme][q][chair] != "right" {
								marks[a][theme][q][chair] = "right"
								totals[a] += values[theme]
								changed = true
							}
						}
					}
				}
			}
		}
	}
	for side := range ids {
		for theme := 0; theme < themes; theme++ {
			for q := 0; q < 3; q++ {
				for chair := 0; chair < 3; chair++ {
					if mark := marks[side][theme][q][chair]; mark != "" {
						ops = append(ops, map[string]any{"path": []any{"sides", side, "themes", theme, "answers", q, chair}, "value": mark})
					}
				}
			}
		}
	}
	patchState(t, srv, festID, gameID, code, token, ops)
	finish(t, srv, festID, gameID, code, token)
}
