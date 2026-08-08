package tests

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"testing"

	dopeserver "dope/dope/server"
	"dope/dope/web/hostpages"
)

// createSchemeGame makes a game of any type from a scheme DSL, the way the host
// page does, and returns its id.
func createSchemeGame(t *testing.T, db *sql.DB, festID int64, gameType, label, dsl string) int64 {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	gameID, err := hostpages.CreateSchemeGameTx(context.Background(), tx, festID, gameType, label, dsl)
	if err != nil {
		t.Fatalf("create %s game: %v", gameType, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return gameID
}

func seedFestPlayers(t *testing.T, db *sql.DB, festID int64, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		if _, err := db.Exec(`
insert into fest_players(fest_id, first_name, last_name) values(?, ?, ?)`,
			festID, "Игрок", fmt.Sprintf("%03d", i)); err != nil {
			t.Fatalf("insert fest player %d: %v", i, err)
		}
	}
}

func seedFestTeams(t *testing.T, db *sql.DB, festID int64, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		if _, err := db.Exec(`
insert into fest_teams(fest_id, name, city, position, number) values(?, ?, '', ?, ?)`,
			festID, fmt.Sprintf("Участник %d", i), i, i); err != nil {
			t.Fatalf("insert fest team %d: %v", i, err)
		}
	}
}

// A группа whose бой seats three: the schedule is twelve бои of three seats, and
// очки come from the block's scoring rule rather than from a win/draw/loss
// triple that a бой of three has no room for.
func TestMultiSeatGroupPlaysAndRanks(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	token := createTestSession(t, srv, systemUserID(t, srv.Eng().DB))
	seedFestPlayers(t, srv.Eng().DB, festID, 9)

	dsl := "[scheme]\ntype: roundrobin\nteams_in_group: 9\nmatch_size: 3\nthemes: 2\n" +
		"bout.points: seats + 1 - place\nsorting: [points, total]\n"
	gameID := createSchemeGame(t, srv.Eng().DB, festID, "si", "Личная СИ", dsl)

	var bouts, seats int
	if err := srv.Eng().DB.QueryRow(`
select count(*), coalesce(min(participant_count), 0) from matches where game_id = ?`, gameID).Scan(&bouts, &seats); err != nil {
		t.Fatalf("count matches: %v", err)
	}
	if bouts != 12 || seats != 3 {
		t.Fatalf("боёв = %d по %d мест, want 12 по 3", bouts, seats)
	}

	// Every player must be seated: nine slots' worth of distinct participants.
	var distinct int
	if err := srv.Eng().DB.QueryRow(`
select count(distinct participant_id) from match_slots ms
join matches m on m.id = ms.match_id where m.game_id = ?`, gameID).Scan(&distinct); err != nil {
		t.Fatalf("count participants: %v", err)
	}
	if distinct != 9 {
		t.Fatalf("рассажено участников = %d, want 9", distinct)
	}

	// Play one бой and check the standings pay by place: 3/2/1 at three seats.
	code := firstMatchCode(t, srv.Eng().DB, gameID)
	patchSIMarks(t, srv, festID, gameID, code, token)
	if resp := scopedAPIRequest(t, srv, http.MethodPost,
		fmt.Sprintf("/api/fest/%d/games/%d/matches/%s/finish", festID, gameID, code),
		map[string]any{"finished": true}, token); resp.Code != http.StatusOK {
		t.Fatalf("finish %s = %d, body %s", code, resp.Code, resp.Body.String())
	}

	rows, err := srv.Eng().DB.Query(`
select place from match_results r join matches m on m.id = r.match_id
where m.code = ? and m.game_id = ? order by place`, code, gameID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var places []float64
	for rows.Next() {
		var place float64
		if err := rows.Scan(&place); err != nil {
			t.Fatal(err)
		}
		places = append(places, place)
	}
	if len(places) != 3 {
		t.Fatalf("мест в протоколе = %d, want 3", len(places))
	}
	if places[0] != 1 || places[1] != 2 || places[2] != 3 {
		t.Fatalf("места = %v, want [1 2 3]", places)
	}
}

// An elimination where a бой seats four and two proceed: the bracket halves
// every round, and a Participant is out on its first Loss — which here means
// finishing third or fourth.
func TestMultiSeatEliminationAdvancesTwo(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	seedFestPlayers(t, srv.Eng().DB, festID, 16)

	dsl := "[scheme]\ntype: single_elimination\nteams: 16\nmatch_size: 4\nwinning_places: 2\nthemes: 2\n"
	gameID := createSchemeGame(t, srv.Eng().DB, festID, "si", "Личная СИ", dsl)

	rows, err := srv.Eng().DB.Query(`
select s.code, count(m.id), min(m.participant_count) from stages s
join matches m on m.stage_id = s.id where s.game_id = ? group by s.code order by min(s.position)`, gameID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type round struct {
		code  string
		bouts int
		seats int
	}
	var rounds []round
	for rows.Next() {
		var r round
		if err := rows.Scan(&r.code, &r.bouts, &r.seats); err != nil {
			t.Fatal(err)
		}
		rounds = append(rounds, r)
	}
	want := []struct{ bouts, seats int }{{4, 4}, {2, 4}, {1, 4}}
	if len(rounds) != len(want) {
		t.Fatalf("раундов = %d (%v), want %d", len(rounds), rounds, len(want))
	}
	for i, w := range want {
		if rounds[i].bouts != w.bouts || rounds[i].seats != w.seats {
			t.Fatalf("раунд %d = %d боёв по %d, want %d по %d", i+1, rounds[i].bouts, rounds[i].seats, w.bouts, w.seats)
		}
	}

	// The second round's first бой takes places 1 and 2 of each of the first
	// two бои — two proceed, so a Loss is finishing outside them.
	var sources []string
	slotRows, err := srv.Eng().DB.Query(`
select ms.source_ref_json from match_slots ms
join matches m on m.id = ms.match_id
join stages s on s.id = m.stage_id
where s.game_id = ? and s.code = ? and m.code like '%-m1'
order by ms.slot_index`, gameID, rounds[1].code)
	if err != nil {
		t.Fatal(err)
	}
	defer slotRows.Close()
	for slotRows.Next() {
		var ref string
		if err := slotRows.Scan(&ref); err != nil {
			t.Fatal(err)
		}
		sources = append(sources, ref)
	}
	if len(sources) != 4 {
		t.Fatalf("во втором раунде %d мест, want 4", len(sources))
	}
	for i, want := range []string{`"place":1`, `"place":2`, `"place":1`, `"place":2`} {
		if !contains(sources[i], want) {
			t.Fatalf("место %d ссылается на %s, want %s", i, sources[i], want)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func firstMatchCode(t *testing.T, db *sql.DB, gameID int64) string {
	t.Helper()
	var code string
	if err := db.QueryRow(`select code from matches where game_id = ? order by id limit 1`, gameID).Scan(&code); err != nil {
		t.Fatalf("first match: %v", err)
	}
	return code
}

// patchSIMarks gives the three seats 2, 1 and 0 taken questions, so the бой
// ranks 1/2/3 cleanly.
func patchSIMarks(t *testing.T, srv *dopeserver.Server, festID, gameID int64, code, token string) {
	t.Helper()
	ops := []map[string]any{
		{"path": []any{"themes", 0, "answers", 0, 0}, "value": "right"},
		{"path": []any{"themes", 1, "answers", 0, 0}, "value": "right"},
		{"path": []any{"themes", 0, "answers", 1, 0}, "value": "right"},
	}
	if resp := scopedAPIRequest(t, srv, http.MethodPatch,
		fmt.Sprintf("/api/fest/%d/games/%d/matches/%s/state", festID, gameID, code),
		map[string]any{"ops": ops}, token); resp.Code != http.StatusOK {
		t.Fatalf("patch %s = %d, body %s", code, resp.Code, resp.Body.String())
	}
}
