package tests

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dope/dope/domain/games"
	"dope/dope/domain/replay"
)

// Троечка VIII Octobearfest, replayed бой by бой from the tournament's own
// workbook (ADR-0010). Forty-eight teams, eight группы of six, then four of
// four, then two of four — a hundred and fifty-six бои, each holding dope's
// Σ and место against what the sheet printed, and every группа's table held
// against the sheet's both ways.
//
// The table is the point. Троечка's регламент ranks a группа on a рейтинговый
// балл — «1 за победу, 0 за поражение, 0.5 за ничью… дополнительно количество
// игровых очков, делённое на 50» — and that is the scheme's own arithmetic
// (`standings.rating: points + taken / 50`), not Go's. The sheet computed the
// same balls in its own formulas five months ago, so the две arithmetics
// meeting on 48 teams is the evidence that the DSL says what the регламент
// says.
//
// What the sheet cannot check: which кресло answered. It counts, per вопрос,
// how many of the three were right and never who, so the кресла the replay
// writes are synthesized (see playTroika) and the Статистика tab — the one
// thing that reads кресла — has no oracle here.
func TestTroikaOctobearfestReplay(t *testing.T) {
	script, err := replay.Parse(readFile(t, "../../../testdata/octobearfest2025/troika.transcript"))
	if err != nil {
		t.Fatal(err)
	}
	srv := newAuthTestServer(t)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	festID := newFest(t, db, "octobearfest-2025", "VIII Octobearfest", systemUserID(t, db))

	numbers := map[string]int{}
	for _, name := range rosterOf(script) {
		numbers[name] = len(numbers) + 1
	}
	teams := registerNumberedTeams(t, db, festID, numbers)

	gameID := createSchemeGameFor(t, db, festID, games.Troika, "Троечка",
		readFile(t, "../../../scripts/troika/troika.dsl"), idsFor(t, teams, rosterOf(script)))
	game := &serverGame{t: t, srv: srv, festID: festID, gameID: gameID, gameType: games.Troika, token: token}
	game.via = directTransport{game}

	findings, err := replay.Run(script, game)
	for _, finding := range findings {
		t.Errorf("Троечка: %s", finding)
	}
	if err != nil {
		t.Fatalf("Троечка: %v", err)
	}
	t.Logf("Троечка: %d боёв и %d таблиц сошлись", len(script.Bouts), len(script.Tables))
}

// A bracket game boots the bracket init payload — and on its own page. Тройка
// does both: it plays бои like ЭК and draws them quite differently, so serving
// it ЭК's HTML would boot ЭК's bundle and none of Тройка's would ever run.
// The bug this pins shipped once: the route hardcoded static/ek.html for every
// InitEK format and ignored what the Definition said its page was.
func TestBracketGamesAreServedTheirOwnPage(t *testing.T) {
	srv := newAuthTestServer(t)
	db := srv.Eng().DB
	festID := newFest(t, db, "pages", "Страницы", systemUserID(t, db))
	// The viewer route admits a public фест without a session.
	if _, err := db.Exec(`update fests set is_public = 1 where id = ?`, festID); err != nil {
		t.Fatal(err)
	}
	teams := registerNumberedTeams(t, db, festID, map[string]int{
		"А": 1, "Б": 2, "В": 3, "Г": 4, "Д": 5, "Е": 6, "Ж": 7, "З": 8,
	})
	ids := idsFor(t, teams, []string{"А", "Б", "В", "Г", "Д", "Е", "Ж", "З"})

	for _, c := range []struct{ gameType, dsl, bundle string }{
		{games.Troika, "[scheme]\nkind: roundrobin\ngroup_size: 8\nthemes: 6\n", "dist/troika.js"},
		{games.Brain, "[scheme]\nkind: roundrobin\ngroup_size: 8\nquestions: 5\n", "dist/brain.js"},
		{games.EK, "[scheme]\nkind: single_elimination\nparticipants: 8\nthemes: 6\n", "dist/ek.js"},
	} {
		gameID := createSchemeGameFor(t, db, festID, c.gameType, c.gameType, c.dsl, ids)
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/fest/%d/game/%d/", festID, gameID), nil)
		resp := httptest.NewRecorder()
		srv.HandleFestRouter(resp, req)
		if resp.Code != http.StatusOK {
			t.Errorf("%s: страница отдала %d", c.gameType, resp.Code)
			continue
		}
		if body := resp.Body.String(); !strings.Contains(body, c.bundle) {
			t.Errorf("%s: страница не грузит %s", c.gameType, c.bundle)
		}
	}
}
