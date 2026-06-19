package resolver

import (
	"testing"
)

// TestAssignDrawLots covers the deterministic Жребий lottery: only true ties draw
// lots, each team's lot is a stable function of the game seed, and recomputing
// (in any input order) yields identical lots and order. Untied teams stay at 0.
func TestAssignDrawLots(t *testing.T) {
	rules := []reseedSortRule{
		{Metric: "place_sum", Dir: "asc"},
		{Metric: "total", Dir: "desc"},
		{Metric: "draw", Dir: "desc"},
	}
	mk := func(team int64, place, total float64) reseedEntry {
		return reseedEntry{teamID: team, metrics: map[string]float64{"place_sum": place, "total": total}}
	}
	const seed = "seed-abc123"
	// Teams 1,2,3 tie completely; team 4 stands alone.
	entries := []reseedEntry{mk(1, 3, 100), mk(2, 3, 100), mk(3, 3, 100), mk(4, 3, 90)}

	sortReseedEntries(entries, rules)
	assignDrawLots(entries, rules, seed)
	sortReseedEntries(entries, rules)

	draws := map[int64]float64{}
	for _, e := range entries {
		draws[e.teamID] = e.metrics["draw"]
	}
	if draws[4] != 0 {
		t.Fatalf("untied team 4 got a lot %v, want 0", draws[4])
	}
	for _, team := range []int64{1, 2, 3} {
		if draws[team] == 0 {
			t.Fatalf("tied team %d got no lot", team)
		}
		if want := float64(deterministicLot(seed, team)); draws[team] != want {
			t.Fatalf("team %d lot %v, want deterministic %v", team, draws[team], want)
		}
	}

	// Recompute in a different input order with the SAME seed: order and lots must
	// be byte-identical (this is what makes untick/retick a no-op for reseed).
	next := []reseedEntry{mk(3, 3, 100), mk(1, 3, 100), mk(2, 3, 100), mk(4, 3, 90)}
	sortReseedEntries(next, rules)
	assignDrawLots(next, rules, seed)
	sortReseedEntries(next, rules)
	for idx, e := range next {
		if e.metrics["draw"] != draws[e.teamID] {
			t.Fatalf("team %d lot changed across recompute: %v != %v", e.teamID, e.metrics["draw"], draws[e.teamID])
		}
		if e.teamID != entries[idx].teamID {
			t.Fatalf("order changed across recompute at %d: %d != %d", idx, e.teamID, entries[idx].teamID)
		}
	}

	// A different seed produces a different lottery (so the seed actually matters).
	other := []reseedEntry{mk(1, 3, 100), mk(2, 3, 100), mk(3, 3, 100)}
	assignDrawLots(other, rules, "seed-xyz789")
	allSame := true
	for _, e := range other {
		if e.metrics["draw"] != draws[e.teamID] {
			allSame = false
			break
		}
	}
	if allSame {
		t.Fatalf("different seed produced identical lots — seed not influencing lottery")
	}
}
