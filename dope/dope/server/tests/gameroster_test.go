package tests

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"testing"

	"dope/dope/domain/replay"
	"dope/dope/web/hostpages"
)

// A фест's Games rarely share an entrant list. СтудЧР-2026 registered 65 teams;
// its ОД seated all of them, its ЭК seated 48 and its брейн a different 48. So
// who plays a Game, under which number, is the Game's own knowledge (ADR-0009),
// and until it was one фест per Game — which split a championship into three.
func TestGameSeatsItsOwnEntrants(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	all := seedParticipants(t, db, festID, 6)
	dsl := "[scheme]\ntype: roundrobin\nteams_in_group: 4\nquestions: 5\n"
	first := createSchemeGameFor(t, db, festID, "brain", "Первая", dsl, all[:4])
	second := createSchemeGameFor(t, db, festID, "brain", "Вторая", dsl, all[2:6])

	for _, c := range []struct {
		game int64
		want []int64
	}{{first, all[:4]}, {second, all[2:6]}} {
		got := gameEntrants(t, db, c.game)
		if len(got) != len(c.want) {
			t.Fatalf("игра %d: участников %d, want %d", c.game, len(got), len(c.want))
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Fatalf("игра %d: состав %v, want %v", c.game, got, c.want)
			}
		}
		// Numbers are dealt from 1 inside the Game, so the same team is «2» in one
		// and «4» in another — which is exactly what a fest-wide number cannot say.
		if n := entrantNumber(t, db, c.game, c.want[0]); n != 1 {
			t.Errorf("игра %d: первый участник под номером %d, want 1", c.game, n)
		}
	}
	if a, b := entrantNumber(t, db, first, all[2]), entrantNumber(t, db, second, all[2]); a == b {
		t.Errorf("одна команда носит номер %d в обеих играх — номер не игровой", a)
	}
}

// A Game created without a selection seats the whole фест, as it always has.
func TestGameWithoutSelectionSeatsTheFest(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	seedFestTeams(t, db, festID, 4)

	gameID := createSchemeGame(t, db, festID, "brain", "Брейн",
		"[scheme]\ntype: roundrobin\nteams_in_group: 4\nquestions: 5\n")
	if got := len(gameEntrants(t, db, gameID)); got != 4 {
		t.Fatalf("участников = %d, want 4", got)
	}
}

// seedParticipants registers n teams as фест Participants, numbered in order.
func seedParticipants(t *testing.T, db *sql.DB, festID int64, n int) []int64 {
	t.Helper()
	var out []int64
	for i := 1; i <= n; i++ {
		res, err := db.Exec(`
insert into participants(fest_id, name, city, number) values(?, ?, '', ?)`,
			festID, fmt.Sprintf("Команда %d", i), i)
		if err != nil {
			t.Fatalf("участник %d: %v", i, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	return out
}

func createSchemeGameFor(t *testing.T, db *sql.DB, festID int64, gameType, label, dsl string, entrants []int64) int64 {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	gameID, err := hostpages.CreateSchemeGameForTx(context.Background(), tx, festID, gameType, label, dsl, entrants)
	if err != nil {
		t.Fatalf("создать %s: %v", label, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return gameID
}

func festParticipantIDs(t *testing.T, db *sql.DB, festID int64) []int64 {
	t.Helper()
	rows, err := db.Query(`select id from participants where fest_id = ? order by number, id`, festID)
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

func gameEntrants(t *testing.T, db *sql.DB, gameID int64) []int64 {
	t.Helper()
	rows, err := db.Query(`
select participant_id from game_participants where game_id = ? order by position`, gameID)
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

func entrantNumber(t *testing.T, db *sql.DB, gameID, participantID int64) int {
	t.Helper()
	var number int
	err := db.QueryRow(`
select number from game_participants where game_id = ? and participant_id = ?`, gameID, participantID).Scan(&number)
	if err == sql.ErrNoRows {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return number
}

// The championship on one фест. СтудЧР-2026 registered 65 teams; its ЭК seated
// 48 of them and its брейн a different 48, overlapping in 43. Before a Game could
// carry its own entrant list this needed three фесты, which is what ADR-0009 was
// written about.
func TestStudchrGamesShareOneFest(t *testing.T) {
	ek := transcriptRoster(t, "ek")
	brain := transcriptRoster(t, "brain")
	if len(ek) != 48 || len(brain) != 48 {
		t.Fatalf("составы: ЭК %d, брейн %d", len(ek), len(brain))
	}

	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	registry := map[string]int64{}
	for _, name := range union(ek, brain) {
		res, err := db.Exec(`
insert into participants(fest_id, name, city, number) values(?, ?, '', ?)`,
			festID, name, len(registry)+1)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		registry[name] = id
	}
	if len(registry) != 53 {
		t.Fatalf("в реестре %d команд, want 53 — 48 и 48 с пересечением в 43", len(registry))
	}

	pick := func(names []string) []int64 {
		out := make([]int64, len(names))
		for i, name := range names {
			out[i] = registry[name]
		}
		return out
	}
	ekDSL := readFile(t, "../../../scripts/studchr/ek.dsl")
	brainDSL := readFile(t, "../../../scripts/studchr/brain.dsl")
	ekGame := createSchemeGameFor(t, db, festID, "ek", "ЭК", ekDSL, pick(ek))
	brainGame := createSchemeGameFor(t, db, festID, "brain", "Брейн", brainDSL, pick(brain))

	for _, c := range []struct {
		name string
		game int64
		want []string
	}{{"ЭК", ekGame, ek}, {"брейн", brainGame, brain}} {
		got := gameEntrants(t, db, c.game)
		if len(got) != len(c.want) {
			t.Fatalf("%s: участников %d, want %d", c.name, len(got), len(c.want))
		}
		for i, name := range c.want {
			if got[i] != registry[name] {
				t.Fatalf("%s: на %d-м месте посева не %s", c.name, i+1, name)
			}
		}
	}
	// The same team plays both under different numbers, which is the whole point.
	for name := range registry {
		a, b := entrantNumber(t, db, ekGame, registry[name]), entrantNumber(t, db, brainGame, registry[name])
		if a > 0 && b > 0 && a != b {
			return
		}
	}
	t.Error("ни одна команда не носит разные номера в двух играх — номер всё ещё фестовый")
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func transcriptRoster(t *testing.T, name string) []string {
	t.Helper()
	script, err := replay.Parse(readFile(t, "../../../testdata/studchr2026/"+name+".transcript"))
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(script.Roster))
	for i, entrant := range script.Roster {
		out[i] = entrant.Name
	}
	return out
}

func union(lists ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, list := range lists {
		for _, name := range list {
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out
}

// A recompile keeps the Game's entrants. Reading the фест's registry instead
// would recompile a game of four against a roster of six and refuse the scheme
// it was created from.
func TestRecompileKeepsGameEntrants(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	all := seedParticipants(t, db, festID, 6)

	dsl := "[scheme]\ntype: roundrobin\nteams_in_group: 4\nquestions: 5\n"
	gameID := createSchemeGameFor(t, db, festID, "brain", "Брейн", dsl, all[1:5])

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := hostpages.RecompileSchemeGameTx(context.Background(), tx, festID, gameID,
		dsl+"bout.points: seats + 1 - place\n"); err != nil {
		t.Fatalf("пересборка: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	got := gameEntrants(t, db, gameID)
	if len(got) != 4 || got[0] != all[1] {
		t.Fatalf("после пересборки состав %v, want %v", got, all[1:5])
	}
}

// A фест may hold two games of one type under names of their own: СтудЧР played
// личная СИ and ТПШ, and both are `si`. Only a collision earns a suffix.
func TestGameKeepsItsGivenTitle(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	seedFestPlayers(t, db, festID, 4)

	dsl := "[scheme]\ntype: roundrobin\nteams_in_group: 4\nthemes: 2\n"
	for _, want := range []string{"СИ", "ТПШ", "СИ 2"} {
		base := want
		if want == "СИ 2" {
			base = "СИ" // the third asks for a name already taken
		}
		gameID := createSchemeGame(t, db, festID, "si", base, dsl)
		var title string
		if err := db.QueryRow(`select title from games where id = ?`, gameID).Scan(&title); err != nil {
			t.Fatal(err)
		}
		if title != want {
			t.Errorf("игра названа %q, want %q", title, want)
		}
	}
}

// The numbering guard asks the Game, not the фест's registry (ADR-0009). A
// фест may register a team that plays nothing — СтудЧР registered 65 and its ЭК
// seated 48 — and an unnumbered row it never seats says nothing about whether
// the ЭК can be scored.
func TestNumberingGuardAsksTheGame(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	all := seedParticipants(t, db, festID, 4)

	// A registry row with no number, of a kind the game does not seat.
	if _, err := db.Exec(`
insert into fest_teams(fest_id, name, city, position, number) values(?, 'Не играет', '', 99, null)`,
		festID); err != nil {
		t.Fatal(err)
	}

	gameID := createSchemeGameFor(t, db, festID, "brain", "Брейн",
		"[scheme]\ntype: roundrobin\nteams_in_group: 4\nquestions: 5\n", all)
	code := firstMatchCode(t, db, gameID)

	resp := scopedAPIRequest(t, srv, http.MethodPatch,
		"/api/fest/"+itoa(festID)+"/games/"+itoa(gameID)+"/matches/"+code+"/state",
		map[string]any{"ops": []map[string]any{{"path": []any{"teams", 0, "rows", 0, "mark"}, "value": "right"}}}, token)
	if resp.Code == http.StatusConflict {
		t.Fatalf("игру заблокировали из-за команды, которая в ней не играет: %s", resp.Body.String())
	}
	if resp.Code != http.StatusOK {
		t.Fatalf("правка боя: %d %s", resp.Code, resp.Body.String())
	}
}
