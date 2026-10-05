package resolver

import (
	"reflect"
	"testing"

	"dope/dope/storage/store"
)

// facts is DrawFacts given by hand.
type facts struct {
	places []HeldPlace
	ranks  []HeldRank
	played map[string]bool
	draws  []*store.SchemeDraw
	seated map[int64]bool
}

func (f facts) FinishedPlaces([]string) ([]HeldPlace, error) { return f.places, nil }
func (f facts) Ranks([]string) ([]HeldRank, map[string]bool, error) {
	return f.ranks, f.played, nil
}
func (f facts) Draws() ([]*store.SchemeDraw, error)             { return f.draws, nil }
func (f facts) SeatedBesideDraws(int64) (map[int64]bool, error) { return f.seated, nil }

func ids(views []store.DrawCandidateView) []int64 {
	out := []int64{}
	for _, v := range views {
		out = append(out, v.ID)
	}
	return out
}

// The Octobearfest Тройка: the 1/16 draws the runners-up of four groups. Its
// candidates are the second places, offered once the groups are played out;
// its substitutes are whoever finished below every rank a draw takes, less
// anyone who already sits in the 1/16 on their own place.
func TestADrawOffersItsRanksAndTheTablesBelowThem(t *testing.T) {
	draw := &store.SchemeDraw{Code: "d1", Ranks: []store.SchemeReseedRef{{Stage: "g1", Rank: 2}, {Stage: "g2", Rank: 2}}}
	f := facts{
		ranks: []HeldRank{
			{Stage: "g1", Rank: 1, ID: 11}, {Stage: "g1", Rank: 2, ID: 12}, {Stage: "g1", Rank: 3, ID: 13}, {Stage: "g1", Rank: 4, ID: 14},
			{Stage: "g2", Rank: 1, ID: 21}, {Stage: "g2", Rank: 2, ID: 22}, {Stage: "g2", Rank: 3, ID: 23},
		},
		played: map[string]bool{"g1": true, "g2": true},
		draws:  []*store.SchemeDraw{draw, {Code: "d2", Ranks: []store.SchemeReseedRef{{Stage: "g1", Rank: 3}}}},
		seated: map[int64]bool{23: true},
	}
	candidates, err := drawCandidates(f, draw)
	if err != nil || !reflect.DeepEqual(ids(candidates), []int64{12, 22}) {
		t.Fatalf("candidates = %v (%v), want the two runners-up", ids(candidates), err)
	}
	// g1's third is drawn by d2, so only its fourth is left; g2's third already sits.
	substitutes, err := drawSubstitutes(f, 1, draw)
	if err != nil || !reflect.DeepEqual(ids(substitutes), []int64{14}) {
		t.Fatalf("substitutes = %v (%v), want g1's fourth alone", ids(substitutes), err)
	}
}

// A table still being played offers nobody: its ranks are provisional.
func TestADrawWaitsForItsTablesToBePlayedOut(t *testing.T) {
	draw := &store.SchemeDraw{Code: "d1", Ranks: []store.SchemeReseedRef{{Stage: "g1", Rank: 2}}}
	f := facts{ranks: []HeldRank{{Stage: "g1", Rank: 2, ID: 12}, {Stage: "g1", Rank: 3, ID: 13}}, played: map[string]bool{}}
	candidates, _ := drawCandidates(f, draw)
	substitutes, _ := drawSubstitutes(f, 1, draw)
	if len(candidates)+len(substitutes) != 0 {
		t.Fatalf("an unplayed table offered %v and %v", ids(candidates), ids(substitutes))
	}
}

// Хамса's lot draws by bout places, and such a draw has no substitutes.
func TestADrawByBoutPlacesHasNoSubstitutes(t *testing.T) {
	draw := &store.SchemeDraw{Code: "lot", Candidates: []store.SchemeFromMatchRef{{Match: "s1-m1", Place: 4}, {Match: "s1-m2", Place: 4}}}
	f := facts{places: []HeldPlace{{Match: "s1-m1", Place: 4, ID: 5}, {Match: "s1-m2", Place: 3, ID: 6}, {Match: "s1-m2", Place: 4, ID: 7}}}
	candidates, err := drawCandidates(f, draw)
	if err != nil || !reflect.DeepEqual(ids(candidates), []int64{5, 7}) {
		t.Fatalf("candidates = %v (%v)", ids(candidates), err)
	}
	if substitutes, _ := drawSubstitutes(f, 1, draw); len(substitutes) != 0 {
		t.Fatalf("substitutes = %v, want none", ids(substitutes))
	}
}
