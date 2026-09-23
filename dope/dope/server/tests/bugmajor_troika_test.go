package tests

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"testing"

	dopeserver "dope/dope/server"
)

// The whole of Bug Major's Тройка through the server: fourteen troikas write
// the отбор, the best twelve play the Swiss stage — its pools ranked and dealt
// by the server as the бои finish — and the six who reach three wins meet
// 1–6, 2–5, 3–4 in the play-off, whose winners sit down to a final of three.
func TestBugMajorTroikaPlaysThrough(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	token := createTestSession(t, srv, systemUserID(t, srv.Eng().DB))
	db := srv.Eng().DB
	troikas := seedParticipants(t, db, festID, 14)
	gameID := createSchemeGameFor(t, db, festID, "troika", "Тройка", readFile(t, "../../../scripts/bugmajor/troika.dsl"), troikas)

	// The отбор: the troika seated k-th writes k right answers fewer than the
	// one before it on the first тема за 3, so it ranks k-th. Troikas 13 and 14
	// write nothing and stay out.
	seats := matchSeatIDs(t, db, gameID, "s1-m1")
	if len(seats) != 14 {
		t.Fatalf("отбор seats %d", len(seats))
	}
	var ops []map[string]any
	for side := 0; side < 12; side++ {
		left := 12 - side
		for theme := 6; theme < 9 && left > 0; theme++ {
			for q := 0; q < 3 && left > 0; q++ {
				count := min(left, 3)
				ops = append(ops, map[string]any{"path": []any{"sides", side, "counts", theme, q}, "value": count})
				left -= count
			}
		}
	}
	patchState(t, srv, festID, gameID, "s1-m1", token, ops)
	finish(t, srv, festID, gameID, "s1-m1", token)
	qualifier := standingsOrder(t, db, gameID, "s1")
	for rank := 0; rank < 12; rank++ {
		if qualifier[rank] != seats[rank] {
			t.Fatalf("отбор rank %d = %d, want seat %d (%d)", rank+1, qualifier[rank], rank, seats[rank])
		}
	}
	seedOf := map[int64]int{}
	for i, id := range qualifier {
		seedOf[id] = i + 1
	}

	// Round 1 pairs 1–12 … 6–7.
	if got := matchSeatIDs(t, db, gameID, "s2-r1-m1"); len(got) != 2 || seedOf[got[0]] != 1 || seedOf[got[1]] != 12 {
		t.Fatalf("round 1 бой 1 seats seeds %d, %d", seedOf[got[0]], seedOf[got[1]])
	}

	// Every бой of the Swiss stage the better seed wins, so the records come
	// out the way the regulations' picture draws them.
	for round := 1; round <= 5; round++ {
		for _, code := range roundBouts(t, db, gameID, fmt.Sprintf("s2-r%d", round)) {
			ids := matchSeatIDs(t, db, gameID, code)
			if len(ids) < 2 {
				t.Fatalf("%s is seated by %d troikas", code, len(ids))
			}
			playBout(t, srv, festID, gameID, code, token, ids, seedOf)
		}
	}
	table := standingsOrder(t, db, gameID, "s2-table")
	gotSeeds := make([]int, 6)
	for i := range gotSeeds {
		gotSeeds[i] = seedOf[table[i]]
	}
	// 3-0: seed 1. 3-1: seeds 2, 3, 4, 5 (the better two of each 2-1 бой).
	// 3-2: the winner of the 2-2 бой.
	if gotSeeds[0] != 1 {
		t.Fatalf("Swiss table leads with seed %d, want the 3-0 seed 1 (top six %v)", gotSeeds[0], gotSeeds)
	}
	sorted := append([]int(nil), gotSeeds[1:5]...)
	sort.Ints(sorted)
	if fmt.Sprint(sorted) != "[2 3 4 5]" {
		t.Fatalf("the 3-1 troikas are seeds %v, want 2 3 4 5 (top six %v)", sorted, gotSeeds)
	}

	// The play-off seats 1–6, 2–5, 3–4 by the Swiss table.
	for i, code := range roundBouts(t, db, gameID, "s3-r1") {
		ids := matchSeatIDs(t, db, gameID, code)
		if len(ids) != 2 || ids[0] != table[i] || ids[1] != table[5-i] {
			t.Fatalf("play-off %s seats %v, want table ranks %d and %d", code, ids, i+1, 6-i)
		}
		playBout(t, srv, festID, gameID, code, token, ids, seedOf)
	}
	finals := roundBouts(t, db, gameID, "s3-r2")
	if len(finals) != 1 {
		t.Fatalf("гранд-финал бои = %v", finals)
	}
	if ids := matchSeatIDs(t, db, gameID, finals[0]); len(ids) != 3 {
		t.Fatalf("гранд-финал seats %v, want the three play-off winners", ids)
	}
}

// playBout lets the better seed win: each seat k answers right on the first
// тема as many times as there are seats below it.
func playBout(t *testing.T, srv *dopeserver.Server, festID, gameID int64, code, token string, ids []int64, seedOf map[int64]int) {
	t.Helper()
	order := make([]int, len(ids))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return seedOf[ids[order[a]]] < seedOf[ids[order[b]]] })
	var ops []map[string]any
	for rank, side := range order {
		for chair := 0; chair < len(ids)-1-rank; chair++ {
			ops = append(ops, map[string]any{"path": []any{"sides", side, "themes", 0, "answers", 0, chair}, "value": "right"})
		}
	}
	if len(ops) > 0 {
		patchState(t, srv, festID, gameID, code, token, ops)
	}
	finish(t, srv, festID, gameID, code, token)
}

func patchState(t *testing.T, srv *dopeserver.Server, festID, gameID int64, code, token string, ops []map[string]any) {
	t.Helper()
	if resp := scopedAPIRequest(t, srv, http.MethodPatch,
		fmt.Sprintf("/api/fest/%d/games/%d/matches/%s/state", festID, gameID, code),
		map[string]any{"ops": ops}, token); resp.Code != http.StatusOK {
		t.Fatalf("patch %s = %d, body %s", code, resp.Code, resp.Body.String())
	}
}

func finish(t *testing.T, srv *dopeserver.Server, festID, gameID int64, code, token string) {
	t.Helper()
	if resp := scopedAPIRequest(t, srv, http.MethodPost,
		fmt.Sprintf("/api/fest/%d/games/%d/matches/%s/finish", festID, gameID, code),
		map[string]any{"finished": true}, token); resp.Code != http.StatusOK {
		t.Fatalf("finish %s = %d, body %s", code, resp.Code, resp.Body.String())
	}
}

// roundBouts lists the бои of a stage, or of its waves, in schedule order.
func roundBouts(t *testing.T, db *sql.DB, gameID int64, stage string) []string {
	t.Helper()
	rows, err := db.Query(`
select m.code from matches m join stages s on s.id = m.stage_id
where m.game_id = ? and (s.code = ? or s.code like ? || '-w%')
order by s.position, m.position, m.id`, gameID, stage, stage)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			t.Fatal(err)
		}
		out = append(out, code)
	}
	return out
}

// standingsOrder is a stage's table, participant ids by rank.
func standingsOrder(t *testing.T, db *sql.DB, gameID int64, stage string) []int64 {
	t.Helper()
	rows, err := db.Query(`
select st.participant_id, st.metrics_json from stage_standings st join stages s on s.id = st.stage_id
where s.game_id = ? and s.code = ? order by st.rank`, gameID, stage)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		var metrics string
		if err := rows.Scan(&id, &metrics); err != nil {
			t.Fatal(err)
		}
		var m map[string]float64
		_ = json.Unmarshal([]byte(metrics), &m)
		out = append(out, id)
	}
	return out
}
