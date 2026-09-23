package tests

import (
	"database/sql"
	"fmt"
	"net/http"
	"sort"
	"testing"

	dopeserver "dope/dope/server"
)

// bugMajorESDSL is Bug Major III's Эрудит-секстет for the student зачёт,
// seeded from the ОД named by its code.
func bugMajorESDSL(odCode string) string {
	return fmt.Sprintf(`[init]
seed: %s
division: Студ

[scheme]
kind: placement
title: Групповой этап
participants: 12
match_size: 4
deal: snake
rotation: true
bout.stage: total + 200 - 50 * place
sorting: [stage, plus, correct_50, draw]
proceeding_participants: 8
---
kind: single_elimination
title: Плей-офф
participants: 8
match_size: 4
winning_places: 2
`, odCode)
}

// The student Эрудит-секстет: the twelve best student teams of the ОД, dealt
// into three groups by the snake, moved round the halls for the second game,
// ranked by the stage score over both, and the best eight sent to two
// semifinals 1-4-5-8 and 2-3-6-7.
func TestBugMajorESPlaysThrough(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	seedFestTeams(t, db, festID, 20)
	// Teams 1–14 are students; 15–20 are adults and must not be seeded.
	for n := 1; n <= 14; n++ {
		if _, err := db.Exec(`
insert into fest_team_flags(team_id, position, short, full)
select id, 0, 'Студ', 'Студенческая команда' from fest_teams where fest_id = ? and number = ?`, festID, n); err != nil {
			t.Fatal(err)
		}
	}
	odID := createGameThroughForm(t, srv, festID, token, map[string]string{"game_type": "od", "od_tours": "1", "od_questions": "20"})
	// Team n takes the first 21 − n questions, so the ОД ranks them 1..20 —
	// except the adults 15..20, who are put on top to show they are skipped.
	entries := make([][]int, 20)
	for q := range entries {
		for n := 1; n <= 20; n++ {
			score := 21 - n
			if n > 14 {
				score = 40 - n
			}
			if q < score {
				entries[q] = append(entries[q], n)
			}
		}
	}
	if resp := scopedAPIRequest(t, srv, http.MethodPatch, fmt.Sprintf("/api/fest/%d/games/%d/state", festID, odID),
		map[string]any{"ops": []map[string]any{{"path": []any{"entries"}, "value": entries}}}, token); resp.Code != http.StatusOK {
		t.Fatalf("od state: %d %s", resp.Code, resp.Body.String())
	}
	var odCode string
	if err := db.QueryRow(`select code from games where id = ?`, odID).Scan(&odCode); err != nil {
		t.Fatal(err)
	}

	esID := createSchemeGame(t, db, festID, "es", "ЭС студенты", bugMajorESDSL(odCode))
	if resp := scopedAPIRequest(t, srv, http.MethodPost,
		fmt.Sprintf("/api/fest/%d/games/%d/seed-import/run", festID, esID), nil, token); resp.Code != http.StatusOK {
		t.Fatalf("seed-import: %d %s", resp.Code, resp.Body.String())
	}
	numberOf := teamNumbers(t, db, festID)
	seedOf := map[int64]int{}
	for _, row := range seedRanks(t, db, esID) {
		if numberOf[row[0]] > 14 {
			t.Fatalf("adult team %d seeded", numberOf[row[0]])
		}
		// The two student teams below the twelve wait on the ladder.
		if row[1] <= 12 {
			seedOf[row[0]] = int(row[1])
		}
	}
	if len(seedOf) != 12 {
		t.Fatalf("seeded %d teams, want 12", len(seedOf))
	}

	// The snake: 1-6-7-12, 2-5-8-11, 3-4-9-10.
	for bout, want := range map[string]string{"s1-r1-m1": "[1 6 7 12]", "s1-r1-m2": "[2 5 8 11]", "s1-r1-m3": "[3 4 9 10]"} {
		if got := seedsAt(t, db, esID, bout, seedOf); got != want {
			t.Fatalf("%s seats seeds %s, want %s", bout, got, want)
		}
	}
	// Every бой the better seed takes more.
	for _, bout := range []string{"s1-r1-m1", "s1-r1-m2", "s1-r1-m3"} {
		playES(t, srv, festID, esID, bout, token, seedOf)
	}
	// Rotation: table 1 keeps its winner (seed 1) and takes the second of
	// table 3 (seed 4), the third of table 2 (seed 8) and the fourth of
	// table 1 (seed 12): with three tables the fourth comes back round.
	if got := seedsAt(t, db, esID, "s1-r2-m1", seedOf); got != "[1 4 8 12]" {
		t.Fatalf("game 2 table 1 seats seeds %s, want [1 4 8 12]", got)
	}
	for _, bout := range []string{"s1-r2-m1", "s1-r2-m2", "s1-r2-m3"} {
		playES(t, srv, festID, esID, bout, token, seedOf)
	}
	table := standingsOrder(t, db, esID, "s1-total")
	if len(table) != 12 {
		t.Fatalf("group table has %d rows", len(table))
	}
	semis := roundBouts(t, db, esID, "s2-r1")
	if len(semis) != 2 {
		t.Fatalf("semifinals = %v", semis)
	}
	rankOf := map[int64]int{}
	for i, id := range table {
		rankOf[id] = i + 1
	}
	for i, want := range []string{"[1 4 5 8]", "[2 3 6 7]"} {
		ids := matchSeatIDs(t, db, esID, semis[i])
		ranks := make([]int, len(ids))
		for k, id := range ids {
			ranks[k] = rankOf[id]
		}
		sort.Ints(ranks)
		if fmt.Sprint(ranks) != want {
			t.Fatalf("semifinal %d seats table ranks %v, want %s", i+1, ranks, want)
		}
	}
}

// playES lets the better seed take more: seat of seed rank k in the бой
// answers (seats − k) questions of the first theme right.
func playES(t *testing.T, srv *dopeserver.Server, festID, gameID int64, code, token string, seedOf map[int64]int) {
	t.Helper()
	ids := matchSeatIDs(t, srv.Eng().DB, gameID, code)
	if len(ids) != 4 {
		t.Fatalf("%s seats %d", code, len(ids))
	}
	order := append([]int64(nil), ids...)
	sort.Slice(order, func(a, b int) bool { return seedOf[order[a]] < seedOf[order[b]] })
	var ops []map[string]any
	for rank, id := range order {
		for q := 0; q < len(order)-rank; q++ {
			ops = append(ops, map[string]any{"path": []any{"participants", fmt.Sprint(id), "themes", 0, "answers", q}, "value": "right"})
		}
	}
	patchState(t, srv, festID, gameID, code, token, ops)
	finish(t, srv, festID, gameID, code, token)
}

func teamNumbers(t *testing.T, db *sql.DB, festID int64) map[int64]int {
	t.Helper()
	rows, err := db.Query(`select id, coalesce(number, 0) from participants where fest_id = ?`, festID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			t.Fatal(err)
		}
		out[id] = n
	}
	return out
}

func seedRanks(t *testing.T, db *sql.DB, gameID int64) [][2]int64 {
	t.Helper()
	rows, err := db.Query(`select participant_id, number from game_assignments where game_id = ? and basket = 1 and participant_id is not null`, gameID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out [][2]int64
	for rows.Next() {
		var r [2]int64
		if err := rows.Scan(&r[0], &r[1]); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func seedsAt(t *testing.T, db *sql.DB, gameID int64, code string, seedOf map[int64]int) string {
	t.Helper()
	ids := matchSeatIDs(t, db, gameID, code)
	seeds := make([]int, len(ids))
	for i, id := range ids {
		seeds[i] = seedOf[id]
	}
	sort.Ints(seeds)
	return fmt.Sprint(seeds)
}
