package venues

import (
	"testing"
	"time"
)

func poll(kind string, perTeam bool) Voting {
	return Voting{
		Kind: kind, PerTeam: perTeam,
		Candidates: []Candidate{{ID: 1, Name: "Синхрон А"}, {ID: 2, Name: "Синхрон Б"}, {ID: 3, Name: "Асинхрон В"}},
	}
}

func scores(rows []TallyRow) map[int64]int {
	out := map[int64]int{}
	for _, r := range rows {
		out[r.Candidate.ID] = r.Score
	}
	return out
}

func TestTallyCountsEachKind(t *testing.T) {
	one := poll(KindOne, false)
	got := scores(Tally(one, []Ballot{{Choice: []int64{1}}, {Choice: []int64{1}}, {Choice: []int64{2}}}))
	if got[1] != 2 || got[2] != 1 || got[3] != 0 {
		t.Fatalf("one: %v", got)
	}

	any := poll(KindAny, false)
	got = scores(Tally(any, []Ballot{{Choice: []int64{1, 2}}, {Choice: []int64{2, 3}}}))
	if got[1] != 1 || got[2] != 2 || got[3] != 1 {
		t.Fatalf("any: %v", got)
	}

	// Ranked pays 3/2/1 and nothing past the third place.
	ranked := poll(KindRanked, false)
	rows := Tally(ranked, []Ballot{{Choice: []int64{3, 1, 2}}, {Choice: []int64{1}}})
	got = scores(rows)
	if got[3] != 3 || got[1] != 2+3 || got[2] != 1 {
		t.Fatalf("ranked: %v", got)
	}
	if rows[0].Candidate.ID != 1 {
		t.Fatalf("the winner is not first: %v", rows)
	}
}

func TestTallyIgnoresDiscardedAndCountsATeamOnce(t *testing.T) {
	v := poll(KindOne, false)
	got := scores(Tally(v, []Ballot{{Choice: []int64{1}}, {Choice: []int64{1}, Discarded: true}}))
	if got[1] != 1 {
		t.Fatalf("discarded counted: %v", got)
	}

	perTeam := poll(KindOne, true)
	got = scores(Tally(perTeam, []Ballot{
		{TeamName: " Мантисса ", Choice: []int64{1}},
		{TeamName: "мантисса", Choice: []int64{2}},
		{TeamName: "Вторая", Choice: []int64{1}},
	}))
	if got[1] != 1 || got[2] != 1 {
		t.Fatalf("per-team: %v — the team's latest ballot counts once", got)
	}
}

func TestNormalizeChoiceObeysTheKind(t *testing.T) {
	if got := NormalizeChoice(poll(KindOne, false), []int64{2, 1}); len(got) != 1 || got[0] != 2 {
		t.Fatalf("one: %v", got)
	}
	if got := NormalizeChoice(poll(KindAny, false), []int64{1, 1, 9, 3}); len(got) != 2 || got[1] != 3 {
		t.Fatalf("any: %v", got)
	}
	if got := NormalizeChoice(poll(KindRanked, false), []int64{3, 2, 1, 3}); len(got) != 3 {
		t.Fatalf("ranked: %v", got)
	}
}

func TestVotingWindow(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	v := Voting{OpensAt: "2026-09-01 10:00", ClosesAt: "2026-09-03 10:00"}
	if !v.Open(now) || v.Closed(now) {
		t.Fatal("inside the window the poll is open")
	}
	early := Voting{OpensAt: "2026-09-03 10:00"}
	if early.Open(now) {
		t.Fatal("before opens_at the poll is shut")
	}
	over := Voting{ClosesAt: "2026-09-01 10:00"}
	if over.Open(now) || !over.Closed(now) {
		t.Fatal("after closes_at the tally shows")
	}
	if !(Voting{}).Open(now) {
		t.Fatal("a poll with no window is open")
	}
}
