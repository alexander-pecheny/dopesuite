package tests

import (
	"fmt"
	"os"
	"strings"
	"testing"

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

func newReplayGame(t *testing.T, dsl, gameType, title string, roster []string) *serverGame {
	t.Helper()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	for i, name := range roster {
		if _, err := db.Exec(`
insert into fest_teams(fest_id, name, city, position, number) values(?, ?, '', ?, ?)`,
			festID, name, i+1, i+1); err != nil {
			t.Fatalf("состав: %v", err)
		}
	}
	gameID := createSchemeGame(t, db, festID, gameType, title, dsl)
	return &serverGame{
		t: t, srv: srv, festID: festID, gameID: gameID,
		token: createTestSession(t, srv, systemUserID(t, srv.Eng().DB)),
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
	src, err := os.ReadFile("../../../testdata/studchr2026/ek.transcript")
	if err != nil {
		t.Skip("стенограммы ЭК нет")
	}
	script, err := replay.Parse(string(src))
	if err != nil {
		t.Fatal(err)
	}
	dsl, err := os.ReadFile("../../../scripts/studchr/ek.dsl")
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(script.Roster))
	for i, entrant := range script.Roster {
		names[i] = entrant.Name
	}
	game := newReplayGame(t, string(dsl), "ek", "ЭК", names)

	findings, err := replay.Run(script, game)
	if err != nil {
		t.Fatalf("прогон: %v", err)
	}
	for _, f := range findings {
		t.Errorf("%s", f)
	}
}
