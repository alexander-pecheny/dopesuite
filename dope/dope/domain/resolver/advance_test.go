package resolver

import (
	"reflect"
	"testing"

	"dope/dope/domain/structure"
	"dope/dope/storage/store"
)

// fakeSources is the in-memory adapter: "A1/1" → team at place 1 of бой A1,
// "G/2" → team at rank 2 of stage G.
type fakeSources map[string]int64

func (f fakeSources) TeamAtMatchPlace(code string, place int) (int64, error) {
	return f[code+"/"+itoa(place)], nil
}
func (f fakeSources) TeamAtReseedRank(stage string, rank int) (int64, error) {
	return f[stage+"/"+itoa(rank)], nil
}
func itoa(n int) string { return string(rune('0' + n)) }

func fromMatch(code string, place int) store.SchemeSlot {
	return store.SchemeSlot{FromMatch: &store.SchemeFromMatchRef{Match: code, Place: place}}
}
func fromRank(stage string, rank int) store.SchemeSlot {
	return store.SchemeSlot{Reseed: &store.SchemeReseedRef{Stage: stage, Rank: rank}}
}

func TestSlotTransitionHoldsOnUnfinalAndReopensOnChange(t *testing.T) {
	cases := []struct {
		current, desired int64
		move, reopen     bool
	}{
		{0, 0, false, false}, // nothing to do
		{7, 0, false, false}, // source unticked: hold the occupant
		{7, 7, false, false}, // same team
		{0, 7, true, false},  // empty seat filled: no reopen
		{7, 9, true, true},   // another team moved in: reopen the бой
	}
	for _, c := range cases {
		move, reopen := slotTransition(c.current, c.desired)
		if move != c.move || reopen != c.reopen {
			t.Errorf("slotTransition(%d, %d) = %v,%v want %v,%v", c.current, c.desired, move, reopen, c.move, c.reopen)
		}
	}
}

func TestPrerequisitesNamesWhatBlocksOnce(t *testing.T) {
	src := fakeSources{"A1/1": 11, "A2/1": 0}
	cfg := store.StageConfig{Teams: []store.SchemeSlot{fromMatch("A1", 1), fromMatch("A2", 1), fromMatch("A2", 2), {}}}
	bouts := []Bout{{1, "A1", "finished"}, {2, "A2", "active"}}
	state, err := prerequisites(src, cfg, bouts)
	if err != nil {
		t.Fatal(err)
	}
	if state.Ready {
		t.Fatal("A2 is still playing")
	}
	if !reflect.DeepEqual(state.PendingMatches, []string{"A2"}) {
		t.Fatalf("pending = %v, want [A2] named once", state.PendingMatches)
	}
	if !reflect.DeepEqual(state.SourceMatchIDs, []int64{1, 2}) {
		t.Fatalf("sources = %v", state.SourceMatchIDs)
	}

	src["A2/1"], src["A2/2"] = 21, 22
	bouts[1].Status = "finished"
	state, _ = prerequisites(src, cfg, bouts)
	if !state.Ready || len(state.PendingMatches) != 0 {
		t.Fatalf("all sources final: want ready, got %+v", state)
	}
}

func TestPrerequisitesNeverReadyWithoutAdvancers(t *testing.T) {
	state, _ := prerequisites(fakeSources{}, store.StageConfig{Teams: []store.SchemeSlot{{Placeholder: "?"}}}, []Bout{{1, "A1", "finished"}})
	if state.Ready {
		t.Fatal("a reseed with no advancing selector has nothing to calculate")
	}
	state, _ = prerequisites(fakeSources{"G/1": 5}, store.StageConfig{Teams: []store.SchemeSlot{fromRank("G", 1)}}, nil)
	if state.Ready {
		t.Fatal("no source бои at all: not ready")
	}
}

func TestContendersCarryBandsAndStopAtAnUnfinalSource(t *testing.T) {
	cfg := store.StageConfig{
		Teams: []store.SchemeSlot{fromMatch("A1", 1), {Label: "bye"}, fromRank("G", 2), fromMatch("A1", 2)},
		Bands: []int{0, 0, 1},
	}
	src := fakeSources{"A1/1": 11, "G/2": 52, "A1/2": 12}
	who, ok, err := contenders(src, cfg)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	want := []structure.Contender{{Participant: 11, Band: 0}, {Participant: 52, Band: 1}, {Participant: 12, Band: 0}}
	if !reflect.DeepEqual(who, want) {
		t.Fatalf("contenders = %+v, want %+v", who, want)
	}
	delete(src, "G/2")
	if _, ok, _ := contenders(src, cfg); ok {
		t.Fatal("an unresolved rank ref must hold the whole reseed")
	}
}

func TestDesiredOccupantFollowsTheRef(t *testing.T) {
	src := fakeSources{"F/1": 3, "R/4": 9}
	if got, _ := desiredOccupant(src, store.SlotRef{Type: store.SlotFromMatch, Match: "F", Place: 1}); got != 3 {
		t.Fatalf("from_match → %d", got)
	}
	if got, _ := desiredOccupant(src, store.SlotRef{Type: store.SlotReseed, Stage: "R", Rank: 4}); got != 9 {
		t.Fatalf("reseed → %d", got)
	}
	if got, _ := desiredOccupant(src, store.SlotRef{Type: store.SlotSeed}); got != 0 {
		t.Fatalf("a seed slot is not advancement: %d", got)
	}
}
