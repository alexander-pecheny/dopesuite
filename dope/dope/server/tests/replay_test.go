package tests

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"dope/dope/domain/games"
	"dope/dope/domain/replay"
)

// A four-team bracket of two-seat бои, replayed end to end. Small enough to
// write the expected numbers by hand, which is the point: it proves the harness
// itself before the harness is trusted about a real tournament.
//
// ЭК scoring: a taken answer at position i is worth (i+1)×10, a lost one the
// same negative. So `---R-` is 40 and `R----` is 10.
const miniTranscript = `[game]
type: ek
title: Мини-ЭК

[roster]
1 | Ктулху          | Москва
2 | ВШЭстером       | Санкт-Петербург
3 | Ушки на макушке | Казань
4 | Мыслители       | Новосибирск

[s1/r1/w1/m1] жребий
Ктулху          | ---R- | 40 | 1
ВШЭстером       | R---- | 10 | 2

[s1/r1/w1/m2] жребий
Ушки на макушке | --R-- | 30 | 1
Мыслители       | -R--- | 20 | 2

[s1/r2/w1/m1]
Ктулху          | ----R | 50 | 1
Ушки на макушке | R---- | 10 | 2
`

// newReplayGame builds the fest a transcript describes and creates its game.
// The roster order is the seeding — for a scheme with no жребий at all, like
// личная СИ, it is the tournament's only input.
func newReplayGame(t *testing.T, dsl, gameType, title string, roster []string) *serverGame {
	t.Helper()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	for i, name := range roster {
		var err error
		if games.IsIndividual(gameType) {
			first, last, _ := strings.Cut(name, " ")
			_, err = db.Exec(`
insert into fest_players(fest_id, first_name, last_name) values(?, ?, ?)`, festID, first, last)
		} else {
			_, err = db.Exec(`
insert into fest_teams(fest_id, name, city, position, number) values(?, ?, '', ?, ?)`,
				festID, name, i+1, i+1)
		}
		if err != nil {
			t.Fatalf("состав: %v", err)
		}
	}
	gameID := createSchemeGame(t, db, festID, gameType, title, dsl)
	return &serverGame{
		t: t, srv: srv, festID: festID, gameID: gameID,
		token: createTestSession(t, srv, systemUserID(t, srv.Eng().DB)),
	}
}

// replayFromTranscript runs a committed transcript against the scheme it names
// and reports every disagreement, so one failing бой does not hide the rest.
func replayFromTranscript(t *testing.T, name, gameType, title string) {
	t.Helper()
	src, err := os.ReadFile("../../../testdata/studchr2026/" + name + ".transcript")
	if err != nil {
		t.Skipf("стенограммы нет: %v", err)
	}
	script, err := replay.Parse(string(src))
	if err != nil {
		t.Fatal(err)
	}
	dsl, err := os.ReadFile("../../../scripts/studchr/" + script.Scheme)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(script.Roster))
	for i, entrant := range script.Roster {
		names[i] = entrant.Name
	}
	findings, err := replay.Run(script, newReplayGame(t, string(dsl), gameType, title, names))
	// Findings first even when the run died: a бой that could not be played at
	// all is usually explained by the disagreements that came before it.
	for _, f := range findings {
		t.Errorf("%s", f)
	}
	if err != nil {
		t.Fatalf("прогон: %v", err)
	}
}

func TestReplayAgreesWithItsTranscript(t *testing.T) {
	script, err := replay.Parse(miniTranscript)
	if err != nil {
		t.Fatal(err)
	}
	game := newReplayGame(t,
		"[defaults]\nvenues: 2\n\n[scheme]\ntype: single_elimination\nteams: 4\nthemes: 1\n",
		"ek", "Мини-ЭК", []string{"Ктулху", "ВШЭстером", "Ушки на макушке", "Мыслители"})

	findings, err := replay.Run(script, game)
	if err != nil {
		t.Fatalf("прогон: %v", err)
	}
	if len(findings) != 0 {
		for _, f := range findings {
			t.Errorf("расхождение: %s", f)
		}
	}
}

// The assertion the coordinate scheme exists for: the second round's seating is
// derived, so if the resolver seats somebody else the replay must say so rather
// than quietly playing the sheet's names into whatever бой it found.
func TestReplayCatchesAWrongSeating(t *testing.T) {
	// Swap the finalists in the transcript. Dope will seat the actual winners,
	// Ктулху and Ушки на макушке, so both seats disagree.
	bent := strings.Replace(miniTranscript,
		"[s1/r2/w1/m1]\nКтулху          | ----R | 50 | 1\nУшки на макушке | R---- | 10 | 2",
		"[s1/r2/w1/m1]\nВШЭстером       | ----R | 50 | 1\nМыслители       | R---- | 10 | 2", 1)
	if bent == miniTranscript {
		t.Fatal("подмена не сработала — тест ничего не проверяет")
	}
	script, err := replay.Parse(bent)
	if err != nil {
		t.Fatal(err)
	}
	game := newReplayGame(t,
		"[defaults]\nvenues: 2\n\n[scheme]\ntype: single_elimination\nteams: 4\nthemes: 1\n",
		"ek", "Мини-ЭК", []string{"Ктулху", "ВШЭстером", "Ушки на макушке", "Мыслители"})

	findings, _ := replay.Run(script, game)
	var seating *replay.Finding
	for i := range findings {
		if findings[i].Field == "посадка" {
			seating = &findings[i]
		}
	}
	if seating == nil {
		t.Fatalf("резольвер посадил не тех, и никто не заметил: %v", findings)
	}
	if !strings.Contains(seating.Ours, "Ктулху") {
		t.Errorf("расхождение не показывает, кого посадили мы: %+v", seating)
	}
}

// A Draw is written into the Edges, not into the seat, so it survives the
// resolver recomputing an earlier round. This is what caught out the first ЭК
// transfer, where hand-seated rounds kept reverting.
func TestReplayDrawSurvivesRecompute(t *testing.T) {
	script, err := replay.Parse(miniTranscript)
	if err != nil {
		t.Fatal(err)
	}
	game := newReplayGame(t,
		"[defaults]\nvenues: 2\n\n[scheme]\ntype: single_elimination\nteams: 4\nthemes: 1\n",
		"ek", "Мини-ЭК", []string{"Ктулху", "ВШЭстером", "Ушки на макушке", "Мыслители"})
	if _, err := replay.Run(script, game); err != nil {
		t.Fatalf("прогон: %v", err)
	}

	final := replay.Coord{Block: "s1", Round: 2, Wave: 1, Match: 1}
	before, err := game.Seats(final)
	if err != nil {
		t.Fatal(err)
	}

	// Reopen and re-close the opening бой: the resolver re-seats everything
	// downstream from its Edges.
	first := replay.Coord{Block: "s1", Round: 1, Wave: 1, Match: 1}
	_, code, err := game.matchAt(first)
	if err != nil {
		t.Fatal(err)
	}
	for _, finished := range []bool{false, true} {
		resp := scopedAPIRequest(t, game.srv, "POST",
			fmt.Sprintf("/api/fest/%d/games/%d/matches/%s/finish", game.festID, game.gameID, code),
			map[string]any{"finished": finished}, game.token)
		if resp.Code != 200 {
			t.Fatalf("finished=%v: %d %s", finished, resp.Code, resp.Body.String())
		}
	}

	after, err := game.Seats(final)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(before, ",") != strings.Join(after, ",") {
		t.Errorf("после пересчёта финал сел иначе: было %v, стало %v", before, after)
	}
	if len(after) != 2 {
		t.Errorf("в финале %d мест, want 2", len(after))
	}
}

// ЭК of СтудЧР-2026, replayed from its committed transcript: 48 teams, 25 бои
// over five rounds, every round drawn by hand. This is the harness doing the
// job it exists for — dope scores what the hosts entered and has to arrive at
// the same Σ and the same место the tournament published.
func TestReplayStudchrEK(t *testing.T) {
	replayFromTranscript(t, "ek", "ek", "ЭК")
}

// Личная СИ, the longest game of the championship: 54 players, six групп of
// nine playing four круги three at a table, then a play-off of 24 on two lives
// with a пересев before every round — 96 бои in all.
//
// Not one of them is a жребий. The roster order is the whole input: the snake
// deals the групп from it, and every play-off бой is seated from the round
// before. So this replay asserts the entire seating of the tournament, which is
// what makes it the harder half of the harness.
func TestReplayStudchrSI(t *testing.T) {
	replayFromTranscript(t, "si", "si", "СИ")
}

// ТПШ: 91 players write one common отбор, and the best 24 play a bracket that
// stops after its second stage — the six left are the winners, there is no
// final. The отбор is the interesting half: its 91 places are ranked by Σ, then
// Σ+, then how many 50s, 40s, 30s and 20s each player took, and dope has to
// derive every one of them.
func TestReplayStudchrTPSh(t *testing.T) {
	replayFromTranscript(t, "tpsh", "si", "ТПШ")
}
