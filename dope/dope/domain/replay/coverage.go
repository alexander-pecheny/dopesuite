package replay

import (
	"fmt"
	"slices"
)

// Kinds names the kinds of input a Match hands a Game: how its seating arrives
// and which parts of a Play it carries. Two Matches with the same kinds drive
// the same calls on the Game with the same shapes of state patch, so a replay
// that has played one of each has exercised every operation the whole
// transcript would. Over HTTP that is every endpoint and every kind of patch the
// handlers are asked to take; the scoring and the seating of the rest are the
// direct replay's job.
//
// A brain side's question count is its own kind: a count past the Match's
// regular questions is a shootout, which appends rows to both sides.
func (b Bout) Kinds() []string {
	kinds := []string{"finish"}
	if b.Draw {
		kinds = append(kinds, "seat")
	} else {
		kinds = append(kinds, "seated")
	}
	if len(b.Drawn) > 0 {
		kinds = append(kinds, "draw")
	}
	for _, seat := range b.Seats {
		if slices.ContainsFunc(seat.Marks, func(theme [5]Mark) bool { return theme != [5]Mark{} }) {
			kinds = append(kinds, "marks")
		}
		if slices.ContainsFunc(seat.Players, func(p string) bool { return p != "" }) {
			kinds = append(kinds, "players")
		}
		if len(seat.Questions) > 0 {
			kinds = append(kinds, fmt.Sprintf("questions/%d", len(seat.Questions)))
		}
		if slices.ContainsFunc(seat.Questions, func(a Answer) bool { return a.Player != "" }) {
			kinds = append(kinds, "buzzer")
		}
		if len(seat.Counts) > 0 {
			kinds = append(kinds, "counts")
		}
		if seat.Shootout != 0 {
			kinds = append(kinds, "shootout")
		}
		if seat.Bet != nil {
			kinds = append(kinds, "bet")
		}
		if seat.Pinned {
			kinds = append(kinds, "pin")
		}
	}
	slices.Sort(kinds)
	return slices.Compact(kinds)
}

// Kinds is every kind any of the script's Matches carries, sorted.
func (s Script) Kinds() []string {
	var kinds []string
	for _, bout := range s.Bouts {
		kinds = append(kinds, bout.Kinds()...)
	}
	slices.Sort(kinds)
	return slices.Compact(kinds)
}

// CoveringPrefix is the shortest run of Matches from the start whose kinds are
// all of the script's. A Match depends on every one before it (the seating is
// derived from their results), so a prefix is the cheapest subset that can
// still be played as written.
func (s Script) CoveringPrefix() int {
	want := len(s.Kinds())
	seen := map[string]bool{}
	for i, bout := range s.Bouts {
		for _, kind := range bout.Kinds() {
			seen[kind] = true
		}
		if len(seen) == want {
			return i + 1
		}
	}
	return len(s.Bouts)
}

// Slice is the script cut to Matches from..to-1, to be played after the ones
// before it: their overrides, the lineups only when it starts at the first
// Match (they are written once, before it), and none of the assertions that
// need the whole game played (the stats and the tables).
func (s Script) Slice(from, to int) Script {
	to = min(to, len(s.Bouts))
	cut := s
	cut.Bouts = s.Bouts[from:to]
	cut.Stats, cut.Tables = nil, nil
	if from > 0 {
		cut.Lineups = nil
	}
	played := map[Coord]bool{}
	for _, bout := range cut.Bouts {
		played[bout.At] = true
	}
	cut.Overrides = nil
	for _, over := range s.Overrides {
		if played[over.At] {
			cut.Overrides = append(cut.Overrides, over)
		}
	}
	return cut
}
