package structure

import (
	"fmt"
	"strconv"
	"strings"

	dopestrings "dope/i18nstrings"
)

// A double elimination whose rounds after the opening the regulations print
// bout by bout, rather than leaving them to the bracket's own pairing
// (docs/scheme-dsl.md, `bouts.rN`). Polyfest's personal SI seats its second
// round M = E-1 H-1 F-2 G-2, where the snake would seat E-1 F-2 G-1 H-2, and
// its KINSBF for eight lets the upper final's loser wait a round, which a
// bracket that plays every round in lockstep cannot do at all.
//
// The opening round still comes from the planner (or `opening:`); every later
// round is exactly what the scheme writes, one item per bout:
//
//	bouts.r2: [M: E-1 H-1 F-2 G-2, N: F-1 G-1 E-2 H-2, …]
//
// A seat is an earlier bout's name and a place in it. A bout is named by the
// prefix before «:», or else by its number in the block (1 is the first bout
// of the opening round). Who carries how many Losses follows from the seats,
// so the brackets are not written: a bout whose seats carry different counts
// is the grand final, and nothing can follow it.

// writtenBoutsKey is the DSL key a written round sits under, dotted by round.
const writtenBoutsKey = "bouts"

// placeSeparator joins a bout's name and a place in a written seat («E-1»).
const placeSeparator = "-"

// writtenBlockRounds reads bouts.r2, bouts.r3, … in order, stopping at the
// first round the scheme does not write. Any later one is then a gap.
func writtenBlockRounds(b Block) ([][]string, error) {
	s := dopestrings.Default
	if _, ok := b.Str(writtenBoutsKey); ok {
		return nil, Keyf(writtenBoutsKey, "%s", s.Structure.De.BoutsRound())
	}
	if _, ok := b.Str(writtenBoutsKey + ".r1"); ok {
		return nil, Keyf(writtenBoutsKey+".r1", "%s", s.Structure.De.BoutsRound())
	}
	var rounds [][]string
	for r := 2; r <= blockRoundLimit; r++ {
		key := fmt.Sprintf("%s.r%d", writtenBoutsKey, r)
		items, ok, err := b.List(key)
		if err != nil {
			return nil, err
		}
		if !ok {
			if next := fmt.Sprintf("%s.r%d", writtenBoutsKey, r+1); hasStr(b, next) {
				return nil, Keyf(next, "%s", s.Structure.De.BoutsGap(next, key))
			}
			break
		}
		rounds = append(rounds, items)
	}
	return rounds, nil
}

func hasStr(b Block, key string) bool {
	_, ok := b.Str(key)
	return ok
}

// openingNames are the names `opening:` gives its bouts («E: A1 B2 C3 D4»),
// in order; nil when it names none.
func openingNames(b Block) ([]string, error) {
	items, ok, err := b.List("opening")
	if err != nil || !ok {
		return nil, err
	}
	names := make([]string, len(items))
	for i, item := range items {
		if name, _, named := strings.Cut(item, ":"); named {
			names[i] = strings.TrimSpace(name)
		}
	}
	return names, nil
}

// writtenPlan is the bracket being built: the plan, each bout's name, and
// which seats of which bouts are already taken.
type writtenPlan struct {
	plan   *dePlan
	byName map[string]int
	// seatLosses[i][k] is how many Losses the k-th seat of bout i carries in.
	seatLosses [][]int
	taken      map[deSource]bool
	key        string // the key being read, for the complaint
}

// extendWritten appends the written rounds to a plan holding the opening
// round alone, and replaces its survivors with the last round's places.
func extendWritten(plan *dePlan, opening []string, rounds [][]string) error {
	w := &writtenPlan{plan: plan, byName: map[string]int{}, taken: map[deSource]bool{}, key: "opening"}
	plan.names = make([]string, len(plan.bouts))
	plan.titles = make([]string, len(plan.bouts))
	for i, bout := range plan.bouts {
		name := strconv.Itoa(i + 1)
		if i < len(opening) && opening[i] != "" {
			name = opening[i]
			plan.titles[i] = name
		}
		if err := w.name(i, name); err != nil {
			return err
		}
		losses := make([]int, len(bout.sources))
		for k := range losses {
			losses[k] = bout.losses
		}
		w.seatLosses = append(w.seatLosses, losses)
	}
	for r, items := range rounds {
		w.key = fmt.Sprintf("%s.r%d", writtenBoutsKey, r+2)
		var round []int
		for _, item := range items {
			index, err := w.bout(item)
			if err != nil {
				return err
			}
			round = append(round, index)
		}
		plan.blockRounds = append(plan.blockRounds, round)
	}
	return w.finish()
}

func (w *writtenPlan) name(index int, name string) error {
	if _, dup := w.byName[name]; dup || name == "" || strings.Contains(name, placeSeparator) {
		return Keyf(w.key, "%s", dopestrings.Default.Structure.De.BoutsName(w.key, name))
	}
	w.byName[name] = index
	w.plan.names[index] = name
	return nil
}

// bout reads one written bout, «M: E-1 H-1 F-2 G-2», and appends it.
func (w *writtenPlan) bout(item string) (int, error) {
	s := dopestrings.Default
	index := len(w.plan.bouts)
	name, seats, named := strings.Cut(item, ":")
	name = strings.TrimSpace(name)
	title := name
	if !named {
		name, seats, title = strconv.Itoa(index+1), item, ""
	}
	tokens := strings.Fields(seats)
	if len(tokens) <= w.plan.winning {
		return 0, Keyf(w.key, "%s", s.Structure.De.BoutsSize(w.key, strings.TrimSpace(item), strconv.Itoa(w.plan.winning)))
	}
	bout := deBout{}
	losses := make([]int, 0, len(tokens))
	for _, token := range tokens {
		source, carried, err := w.seat(token)
		if err != nil {
			return 0, err
		}
		bout.sources = append(bout.sources, source)
		losses = append(losses, carried)
	}
	bout.losses, bout.final = seatLossesOf(losses)
	w.plan.bouts = append(w.plan.bouts, bout)
	w.plan.names = append(w.plan.names, "")
	w.plan.titles = append(w.plan.titles, title)
	w.seatLosses = append(w.seatLosses, losses)
	return index, w.name(index, name)
}

// seatLossesOf is the Losses a bout is played on: one count for a bout inside
// a bracket, the fewest for the final that seats both.
func seatLossesOf(losses []int) (int, bool) {
	low, mixed := losses[0], false
	for _, l := range losses[1:] {
		if l != losses[0] {
			mixed = true
		}
		low = min(low, l)
	}
	return low, mixed
}

// seat reads «E-1»: who finished first in bout E, and the Losses they carry.
func (w *writtenPlan) seat(token string) (deSource, int, error) {
	cut := strings.LastIndex(token, placeSeparator)
	if cut <= 0 {
		return deSource{}, 0, Keyf(w.key, "%s", dopestrings.Default.Structure.De.BoutsSeat(w.key, token))
	}
	index, known := w.byName[token[:cut]]
	if !known {
		return deSource{}, 0, Keyf(w.key, "%s", dopestrings.Default.Structure.De.BoutsUnknown(w.key, token[:cut]))
	}
	place, err := strconv.Atoi(token[cut+1:])
	if err != nil || place < 1 || place > len(w.plan.bouts[index].sources) {
		return deSource{}, 0, Keyf(w.key, "%s", dopestrings.Default.Structure.De.BoutsPlace(w.key, token))
	}
	if w.plan.bouts[index].final {
		return deSource{}, 0, Keyf(w.key, "%s", dopestrings.Default.Structure.De.BoutsAfterFinal(w.key, token))
	}
	source := deSource{bout: index, place: place}
	if w.taken[source] {
		return deSource{}, 0, Keyf(w.key, "%s", dopestrings.Default.Structure.De.BoutsTwice(w.key, token))
	}
	carried := w.plan.bouts[index].losses
	if place > w.plan.winning {
		carried++
	}
	if carried >= w.plan.lives {
		return deSource{}, 0, Keyf(w.key, "%s", dopestrings.Default.Structure.De.BoutsOut(w.key, token))
	}
	w.taken[source] = true
	return source, carried, nil
}

// finish checks that everyone still in before the last round plays on, and
// makes the last round's places the block's survivors.
func (w *writtenPlan) finish() error {
	plan := w.plan
	last := map[int]bool{}
	for _, index := range plan.blockRounds[len(plan.blockRounds)-1] {
		last[index] = true
	}
	plan.survivor = nil
	for index, bout := range plan.bouts {
		for place := 1; place <= len(bout.sources); place++ {
			source := deSource{bout: index, place: place}
			alive := bout.final || bout.losses+boolInt(place > plan.winning) < plan.lives
			switch {
			case last[index] && alive:
				plan.survivor = append(plan.survivor, source)
			case alive && !w.taken[source]:
				token := plan.names[index] + placeSeparator + strconv.Itoa(place)
				return Keyf(w.key, "%s", dopestrings.Default.Structure.De.BoutsUnused(token))
			}
		}
	}
	plan.survivor = rankSources(plan.survivor)
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
