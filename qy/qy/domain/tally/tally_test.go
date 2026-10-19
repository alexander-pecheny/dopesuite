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

func TestAtLeastTakesEveryTieAtTheCut(t *testing.T) {
	counts := []Count{{1, 1, 5}, {2, 2, 4}, {3, 3, 3}, {4, 4, 3}, {5, 5, 2}, {6, 6, 0}}
	got := ids(AtLeast(counts, 3, nil), func(c Count) int64 { return c.ID })
	if want := []int64{1, 2, 3, 4}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestAtLeastNeverTakesAnUnlikedQuestion(t *testing.T) {
	counts := []Count{{1, 1, 1}, {2, 2, 0}, {3, 3, 0}}
	if got := AtLeast(counts, 3, nil); len(got) != 1 {
		t.Errorf("got %v", got)
	}
}

func TestAtLeastBreaksOrderByNumber(t *testing.T) {
	counts := []Count{{10, 9, 2}, {11, 3, 2}}
	got := ids(AtLeast(counts, 1, nil), func(c Count) int64 { return c.ID })
	if want := []int64{11, 10}; !reflect.DeepEqual(got, want) {
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
	got := Shortlist(cands, 1, 1)
	want := []Pick{{1, 1, ByExpert}, {4, 4, ByPeople}, {6, 6, ByPin}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
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
