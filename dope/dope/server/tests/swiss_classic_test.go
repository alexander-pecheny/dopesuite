package tests

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	dopeserver "dope/dope/server"
)

// The Вопросики быстрые qualifier (regulations 3.1) through the server: nine
// teams, so one plays the empty table every round. The first round seats the
// top half against the bottom; a later round is seated once the host
// calculates its pairs, and a result corrected after that changes the table
// but not the published pairs (3.1.10).
func TestSwissClassicBrainPlaysThrough(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	token := createTestSession(t, srv, systemUserID(t, srv.Eng().DB))
	db := srv.Eng().DB
	teams := seedParticipants(t, db, festID, 9)
	dsl := "[defaults]\nquestions: 5\n\n[scheme]\nkind: swiss_classic\nrounds: 3\nzero_draw_points: 0\n"
	gameID := createSchemeGameFor(t, db, festID, "brain", "Вопросики быстрые", dsl, teams)
	seat := map[int64]int{}
	for i, id := range teams {
		seat[id] = i + 1
	}
	seeds := func(code string) string {
		var out []int
		for _, id := range matchSeatIDs(t, db, gameID, code) {
			out = append(out, seat[id])
		}
		return fmt.Sprint(out)
	}
	play := func(code string, takenA, takenB int) {
		t.Helper()
		var ops []map[string]any
		for side, taken := range []int{takenA, takenB} {
			for row := range taken {
				ops = append(ops, map[string]any{"path": []any{"teams", side, "rows", row, "mark"}, "value": "right"})
			}
		}
		if len(ops) > 0 {
			patchState(t, srv, festID, gameID, code, token, ops)
		}
		finish(t, srv, festID, gameID, code, token)
	}

	want := []string{"[1 5]", "[2 6]", "[3 7]", "[4 8]", "[9]"}
	for i, code := range roundBouts(t, db, gameID, "s1-r1") {
		if got := seeds(code); got != want[i] {
			t.Fatalf("round 1 %s seats seeds %s, want %s", code, got, want[i])
		}
	}
	for _, code := range roundBouts(t, db, gameID, "s1-r1")[:4] {
		play(code, 2, 1)
	}
	if resp := calculatePairs(t, srv, festID, gameID, "s1-r2-pairs", token); resp != http.StatusBadRequest {
		t.Fatalf("pairs before the round ends = %d, want 400", resp)
	}
	play(roundBouts(t, db, gameID, "s1-r1")[4], 0, 0) // a 0:0 at the empty table
	if resp := calculatePairs(t, srv, festID, gameID, "s1-r2-pairs", token); resp != http.StatusOK {
		t.Fatalf("pairs = %d", resp)
	}

	// The winners pair first with last; among those on nought 9 has fewer
	// answers, so it meets 5. 8 is the lowest that has not sat out.
	want = []string{"[1 4]", "[2 3]", "[5 9]", "[6 7]", "[8]"}
	round2 := roundBouts(t, db, gameID, "s1-r2")
	for i, code := range round2 {
		if got := seeds(code); got != want[i] {
			t.Fatalf("round 2 %s seats seeds %s, want %s", code, got, want[i])
		}
	}

	// A team that takes a question at the empty table wins it.
	play(round2[4], 1, 0)
	if got := tablePoints(t, db, gameID, teams[7]); got != 2 {
		t.Fatalf("seed 8 has %v points after a 1:0 at the empty table, want 2", got)
	}

	// Seed 5 had in fact beaten seed 1: the table follows, the pairs do not.
	r1m1 := roundBouts(t, db, gameID, "s1-r1")[0]
	if resp := scopedAPIRequest(t, srv, http.MethodPost,
		fmt.Sprintf("/api/fest/%d/games/%d/matches/%s/finish", festID, gameID, r1m1),
		map[string]any{"finished": false}, token); resp.Code != http.StatusOK {
		t.Fatalf("reopen = %d", resp.Code)
	}
	play(r1m1, 0, 3)
	if got := seeds(round2[0]); got != "[1 4]" {
		t.Fatalf("a correction moved published pairs: %s", got)
	}
	table := standingsOrder(t, db, gameID, "s1-table")
	if seat[table[0]] != 5 {
		t.Fatalf("table leader is seed %d, want 5 after the correction", seat[table[0]])
	}
}

func tablePoints(t *testing.T, db *sql.DB, gameID, participant int64) float64 {
	t.Helper()
	var metrics string
	if err := db.QueryRow(`
select st.metrics_json from stage_standings st join stages s on s.id = st.stage_id
where s.game_id = ? and s.code = 's1-table' and st.participant_id = ?`, gameID, participant).Scan(&metrics); err != nil {
		t.Fatal(err)
	}
	var m struct {
		Points float64 `json:"points"`
	}
	if err := json.Unmarshal([]byte(metrics), &m); err != nil {
		t.Fatal(err)
	}
	return m.Points
}

func calculatePairs(t *testing.T, srv *dopeserver.Server, festID, gameID int64, stage, token string) int {
	t.Helper()
	return scopedAPIRequest(t, srv, http.MethodPost,
		fmt.Sprintf("/api/fest/%d/games/%d/stages/%s/reseed", festID, gameID, stage), nil, token).Code
}

// The whole Ш bracket of the Вопросики быстрые: eleven teams play seven
// Swiss rounds, the best eight go into the play-off by the appendix's
// picture, and the upper final's winner meets the lower bracket's in a final
// of three. The better seed wins every бой, so the table is the seed and every
// seat can be named.
func TestVoronenokFastQuestionsPlaysThrough(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	token := createTestSession(t, srv, systemUserID(t, srv.Eng().DB))
	db := srv.Eng().DB
	teams := seedParticipants(t, db, festID, 11)
	gameID := createSchemeGameFor(t, db, festID, "brain", "Вопросики быстрые", readFile(t, "../../../scripts/voronenok/vb-sh.dsl"), teams)
	seedOf := map[int64]int{}
	for i, id := range teams {
		seedOf[id] = i + 1
	}
	play := func(code string) {
		t.Helper()
		ids := matchSeatIDs(t, db, gameID, code)
		var ops []map[string]any
		for side := range ids {
			better := len(ids) == 1 || seedOf[ids[side]] < seedOf[ids[1-side]]
			if better {
				ops = append(ops, map[string]any{"path": []any{"teams", side, "rows", 0, "mark"}, "value": "right"})
			}
		}
		patchState(t, srv, festID, gameID, code, token, ops)
		finish(t, srv, festID, gameID, code, token)
	}

	met := map[[2]int64]bool{}
	for round := 1; round <= 7; round++ {
		if round > 1 {
			if code := calculatePairs(t, srv, festID, gameID, fmt.Sprintf("s1-r%d-pairs", round), token); code != http.StatusOK {
				t.Fatalf("round %d pairs = %d", round, code)
			}
		}
		bouts := roundBouts(t, db, gameID, fmt.Sprintf("s1-r%d", round))
		if len(bouts) != 6 {
			t.Fatalf("round %d has %d бои, want 5 and the empty table", round, len(bouts))
		}
		for _, code := range bouts {
			ids := matchSeatIDs(t, db, gameID, code)
			if len(ids) == 2 {
				if met[[2]int64{ids[0], ids[1]}] {
					t.Fatalf("round %d: seeds %d and %d meet again", round, seedOf[ids[0]], seedOf[ids[1]])
				}
				met[[2]int64{ids[0], ids[1]}], met[[2]int64{ids[1], ids[0]}] = true, true
			}
			play(code)
		}
	}
	table := standingsOrder(t, db, gameID, "s1-table")
	rank := func(id int64) int {
		for i, x := range table {
			if x == id {
				return i + 1
			}
		}
		return 0
	}
	for code, want := range map[string][2]int{"s2-r1-m1": {1, 4}, "s2-r1-m2": {2, 3}, "s2-r1-m3": {5, 8}, "s2-r1-m4": {6, 7}} {
		ids := matchSeatIDs(t, db, gameID, code)
		if len(ids) != 2 || rank(ids[0]) != want[0] || rank(ids[1]) != want[1] {
			t.Fatalf("play-off %s seats table ranks %d, %d; want %v", code, rank(ids[0]), rank(ids[1]), want)
		}
	}
	for round := 1; round <= 4; round++ {
		for _, code := range roundBouts(t, db, gameID, fmt.Sprintf("s2-r%d", round)) {
			play(code)
		}
	}
	// III.1 is the upper final, won by the table's first; IV.1 seats the lower
	// final's winner (I.3's best) against III.1's loser, the table's second.
	finals := roundBouts(t, db, gameID, "s3-final")
	if len(finals) != 3 {
		t.Fatalf("final бои = %v, want three", finals)
	}
	ids := matchSeatIDs(t, db, gameID, finals[0])
	if len(ids) != 2 || rank(ids[0]) != 1 || rank(ids[1]) != 2 {
		t.Fatalf("final seats table ranks %d, %d; want 1 and 2", rank(ids[0]), rank(ids[1]))
	}
}

// The Вопросики быстрые seed (3.1.5): the Вопросики's first two tours without
// the appeals, a tie broken by the rating and then by the second tour. Team 2's
// appeal on question 2 is accepted, which lifts it past team 1 in the ОД, but
// not in the seed.
func TestVoronenokSeedsOnTwoToursBeforeTheRulings(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	seedFestTeams(t, db, festID, 4)
	if _, err := db.Exec(`update fests set contested = 1 where id = ?`, festID); err != nil {
		t.Fatal(err)
	}
	odID := createGameThroughForm(t, srv, festID, token, map[string]string{"game_type": "od", "od_tours": "3", "od_questions": "2"})
	var odCode string
	if err := db.QueryRow(`select code from games where id = ?`, odID).Scan(&odCode); err != nil {
		t.Fatal(err)
	}
	od := fmt.Sprintf("/api/fest/%d/games/%d", festID, odID)
	call := func(method, path string, body any) {
		t.Helper()
		if resp := scopedAPIRequest(t, srv, method, od+path, body, token); resp.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", method, path, resp.Code, resp.Body.String())
		}
	}
	// Tours 1–2 leave teams 3 and 4 on two each and level on rating; tour 2
	// puts 4 first. Team 1 sweeps tour 3, which the seed does not count.
	call(http.MethodPatch, "/state", map[string]any{"ops": []map[string]any{
		{"path": []any{"entries"}, "value": [][]int{{1, 2}, {3}, {3, 4}, {4}, {1}, {1}}},
		{"path": []any{"completed"}, "value": []bool{true, true, true, true, true, true}},
	}})
	call(http.MethodPost, "/contested", map[string]any{"question": 1, "number": 2, "answer": "Пушкин"})
	call(http.MethodPut, "/contested/ruling", map[string]any{"question": 1, "answer": "Пушкин", "status": "accepted"})

	numberOf := teamNumbers(t, db, festID)
	seedOrder := func(beforeRulings bool) string {
		t.Helper()
		dsl := fmt.Sprintf("[init]\nseed: %s[tours 1..2]\nsorting: [total, rating, tour2]\nbefore_rulings: %t\n\n[scheme]\nkind: swiss_classic\nparticipants: 4\nrounds: 3\n", odCode, beforeRulings)
		gameID := createSchemeGame(t, db, festID, "brain", fmt.Sprintf("ВБ %t", beforeRulings), dsl)
		resp := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/games/%d/seed-import/run", festID, gameID), nil, token)
		if resp.Code != http.StatusOK {
			t.Fatalf("seed-import %d %s", resp.Code, resp.Body.String())
		}
		var order []int
		for _, row := range decodeJSON[esSeedView](t, resp).Rows {
			order = append(order, numberOf[row.TeamID])
		}
		return fmt.Sprint(order)
	}
	if got := seedOrder(true); got != "[4 3 1 2]" {
		t.Fatalf("seed before the rulings = %s, want [4 3 1 2]", got)
	}
	if got := seedOrder(false); got != "[4 3 2 1]" {
		t.Fatalf("seed with the rulings = %s, want [4 3 2 1]", got)
	}
}
