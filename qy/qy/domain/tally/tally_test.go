package tally

import (
	"reflect"
	"testing"
)

func ids[T any](xs []T, id func(T) int64) []int64 {
	out := make([]int64, len(xs))
	for i, x := range xs {
		out[i] = id(x)
	}
	return out
}

func countIDs(cs []Count) []int64 { return ids(cs, func(c Count) int64 { return c.ID }) }

func TestCutLeavesATieAtTheCutToTheTieBreak(t *testing.T) {
	counts := []Count{{1, 1, 5}, {2, 2, 4}, {3, 3, 3}, {4, 4, 3}, {5, 5, 3}, {6, 6, 2}}
	in, tied := Cut(counts, 3, nil)
	if got, want := countIDs(in), []int64{1, 2}; !reflect.DeepEqual(got, want) {
		t.Errorf("in: got %v, want %v", got, want)
	}
	if got, want := countIDs(tied), []int64{3, 4, 5}; !reflect.DeepEqual(got, want) {
		t.Errorf("tied: got %v, want %v", got, want)
	}
}

func TestCutTakesATieAboveTheCut(t *testing.T) {
	counts := []Count{{1, 1, 5}, {2, 2, 5}, {3, 3, 3}, {4, 4, 2}}
	in, tied := Cut(counts, 3, nil)
	if got, want := countIDs(in), []int64{1, 2, 3}; !reflect.DeepEqual(got, want) || tied != nil {
		t.Errorf("got %v and %v, want %v and no tie", got, countIDs(tied), want)
	}
}

func TestCutNeverTakesAnUnlikedQuestion(t *testing.T) {
	counts := []Count{{1, 1, 1}, {2, 2, 0}, {3, 3, 0}}
	if in, tied := Cut(counts, 3, nil); len(in) != 1 || tied != nil {
		t.Errorf("got %v, %v", in, tied)
	}
}

func TestCutBreaksOrderByNumber(t *testing.T) {
	counts := []Count{{10, 9, 3}, {11, 3, 3}, {12, 4, 1}}
	in, _ := Cut(counts, 2, nil)
	if got, want := countIDs(in), []int64{11, 10}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestShortlistFollowsTheRules(t *testing.T) {
	cands := []Candidate{
		{ID: 1, QY: 1, Expert: 4, People: 9},
		{ID: 2, QY: 2, Expert: 3},
		{ID: 3, QY: 3, Expert: 9, Excluded: true},
		{ID: 4, QY: 4, People: 5},
		{ID: 5, QY: 5, People: 1},
		{ID: 6, QY: 6, Pinned: true},
	}
	o := Shortlist(cands, 1, 1, nil)
	want := []Pick{{1, 1, ByExpert, false}, {4, 4, ByPeople, false}, {6, 6, ByPin, false}}
	if !reflect.DeepEqual(o.Picks, want) || len(o.Ties) != 0 {
		t.Errorf("got %v and ties %v\nwant %v", o.Picks, o.Ties, want)
	}
}

// Five questions tie for the expert jury's last two places.
func tiedCandidates() []Candidate {
	return []Candidate{
		{ID: 1, QY: 1, Expert: 9},
		{ID: 2, QY: 2, Expert: 5},
		{ID: 3, QY: 3, Expert: 5},
		{ID: 4, QY: 4, Expert: 5, People: 7},
		{ID: 5, QY: 5, Expert: 5},
		{ID: 6, QY: 6, Expert: 1, People: 4},
		{ID: 7, QY: 7, People: 2},
	}
}

func TestATieWaitsForItsRound(t *testing.T) {
	o := Shortlist(tiedCandidates(), 3, 1, nil)
	if o.Settled() || len(o.Ties) != 1 {
		t.Fatalf("got %+v", o)
	}
	tie := o.Ties[0]
	if tie.Why != ByExpert || tie.Places != 2 || !reflect.DeepEqual(tie.IDs, []int64{2, 3, 4, 5}) {
		t.Errorf("tie: %+v", tie)
	}
	// The people's cut is made as if 4 went through by the experts.
	want := []Pick{{1, 1, ByExpert, false}, {6, 6, ByPeople, false}}
	if !reflect.DeepEqual(o.Picks, want) {
		t.Errorf("got %v, want %v", o.Picks, want)
	}
	if got, want := o.Contenders(), []int64{1, 6, 2, 3, 4, 5}; !reflect.DeepEqual(got, want) {
		t.Errorf("contenders: got %v, want %v", got, want)
	}
}

func TestTheRoundFillsThePlacesLeft(t *testing.T) {
	marks := []Ballot{
		{2: 3, 3: 9, 4: 4, 5: 8, 7: 10},
		{2: 2, 3: 8, 4: 5, 5: 9},
		// A ballot that leaves a tied question unmarked is not counted.
		{2: 10, 3: 1},
	}
	o := Shortlist(tiedCandidates(), 3, 1, marks)
	if !o.Settled() || o.Ties[0].Ballots != 2 {
		t.Fatalf("got %+v", o)
	}
	// 4 lost the round, so it is back in the people's cut, and it beats 6 there.
	want := []Pick{{1, 1, ByExpert, false}, {3, 3, ByExpert, true}, {5, 5, ByExpert, true}, {4, 4, ByPeople, false}}
	if !reflect.DeepEqual(o.Picks, want) {
		t.Errorf("got %v, want %v", o.Picks, want)
	}
}

func TestARoundThatTiesAtTheCutIsNotSettled(t *testing.T) {
	marks := []Ballot{
		{2: 1, 3: 9, 4: 8, 5: 2},
		{2: 1, 3: 9, 4: 2, 5: 8},
	}
	o := Shortlist(tiedCandidates(), 3, 1, marks)
	if o.Settled() {
		t.Errorf("4 and 5 tie for the last place: %+v", o.Ties)
	}
}

func TestATieAtThePeoplesCut(t *testing.T) {
	cands := []Candidate{
		{ID: 1, QY: 1, Expert: 3, People: 9},
		{ID: 2, QY: 2, People: 4},
		{ID: 3, QY: 3, People: 4},
	}
	o := Shortlist(cands, 1, 1, []Ballot{{2: 4, 3: 6}})
	if !o.Settled() || o.Ties[0].Why != ByPeople {
		t.Fatalf("got %+v", o)
	}
	want := []Pick{{1, 1, ByExpert, false}, {3, 3, ByPeople, true}}
	if !reflect.DeepEqual(o.Picks, want) {
		t.Errorf("got %v, want %v", o.Picks, want)
	}
}

func TestBallotRanksShareTies(t *testing.T) {
	got := Ballot{1: 9, 2: 7, 3: 7, 4: 1}.Ranks()
	want := map[int64]float64{1: 1, 2: 2.5, 3: 2.5, 4: 4}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
}

func TestJuryRanksByRankSumThenPoints(t *testing.T) {
	ballots := []Ballot{
		{1: 10, 2: 9, 3: 1},
		{1: 2, 2: 3, 3: 1},
	}
	rows := Jury([]int64{1, 2, 3}, ballots)
	// 1 and 2 both have a rank sum of 3 and 12 points, so they share first place.
	if rows[0].RankSum != 3 || rows[1].RankSum != 3 || rows[0].Place != (Places{1, 2}) || rows[2].Place != (Places{3, 3}) {
		t.Errorf("got %+v", rows)
	}
	ballots[1][2] = 4
	rows = Jury([]int64{1, 2, 3}, ballots)
	if rows[0].ID != 2 || rows[0].Place != (Places{1, 1}) {
		t.Errorf("points should break the tie: %+v", rows)
	}
}

func TestPopularSharesFirstPlace(t *testing.T) {
	rows := Popular([]int64{1, 2, 3, 4}, []PopularVote{{1, 2, 3}, {2, 1, 4}})
	if rows[0].Points != 9 || rows[1].Points != 9 || rows[0].Place != (Places{1, 2}) {
		t.Errorf("got %+v", rows)
	}
	if rows[3].Points != 3 || rows[3].Place != (Places{3, 4}) {
		t.Errorf("got %+v", rows)
	}
}

func TestResultsOrderByTheBetterPlace(t *testing.T) {
	qy := map[int64]int{1: 11, 2: 22, 3: 33}
	ballots := []Ballot{{1: 10, 2: 5, 3: 1}}
	votes := []PopularVote{{3, 2, 1}}
	got := ids(Results(qy, ballots, votes), func(r Result) int64 { return r.ID })
	// 1 is first with the jury, 3 first with the voters; the jury place breaks
	// the tie between them.
	if want := []int64{1, 3, 2}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
