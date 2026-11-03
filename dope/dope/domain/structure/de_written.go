package structure

import (
	"fmt"
	"sort"
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
// A seat is an earlier round's bout and a place in it. A written bout is
// named by the prefix before «:»; an opening bout the table leaves unnamed
// answers to its number in the round. Who carries how many Losses follows from the seats,
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

// SplitBoutItem reads one bout of a written table, «E: A1 B2 C3 D4»: its name,
// if the scheme gave one, and its seats. The opening's seats are the
// compiler's to resolve and the names the Kind's, so both read them here.
func SplitBoutItem(item string) (name, seats string, named bool) {
	name, seats, named = strings.Cut(item, ":")
	if !named {
		return "", item, false
	}
	return strings.TrimSpace(name), seats, true
}

// openingNames are the names `opening:` gives its bouts, in order: "" for a
// bout it leaves unnamed, and nil when there is no `opening:` at all.
func openingNames(b Block) ([]string, []bool, error) {
	items, ok, err := b.List("opening")
	if err != nil || !ok {
		return nil, nil, err
	}
	names := make([]string, len(items))
	named := make([]bool, len(items))
	for i, item := range items {
		names[i], _, named[i] = SplitBoutItem(item)
	}
	return names, named, nil
}

// writtenPlan is the bracket being built: the plan, each bout's name, and
// which places of which bouts a later bout already takes.
type writtenPlan struct {
	plan   *dePlan
	byName map[string]int
	// pending are the names of the round being read: a bout may not take a
	// seat from a bout of its own round, so they join byName once it is read.
	pending map[string]int
	taken   map[deSource]bool
	key     string // the key being read, for the complaint
}

// nameOpening names the opening round's bouts: by `opening:` where it names
// them, which also titles and letters them, else by their number.
func nameOpening(plan *dePlan, names []string, named []bool) (*writtenPlan, error) {
	w := &writtenPlan{plan: plan, byName: map[string]int{}, pending: map[string]int{}, taken: map[deSource]bool{}, key: "opening"}
	plan.names = make([]string, len(plan.bouts))
	plan.titles = make([]string, len(plan.bouts))
	for i, index := range plan.blockRounds[0] {
		name := strconv.Itoa(i + 1)
		if i < len(named) && named[i] {
			name = names[i]
			plan.titles[index] = name
		}
		if err := w.name(index, name); err != nil {
			return nil, err
		}
	}
	w.settle()
	return w, nil
}

// extend appends the written rounds to a plan holding the opening round
// alone, and replaces its survivors with the last round's places.
func (w *writtenPlan) extend(rounds [][]string, proceeding int) error {
	for r, items := range rounds {
		w.key = fmt.Sprintf("%s.r%d", writtenBoutsKey, r+2)
		if len(items) == 0 {
			return Keyf(w.key, "%s", dopestrings.Default.Structure.De.BoutsEmpty(w.key))
		}
		var round []int
		for _, item := range items {
			index, err := w.bout(item)
			if err != nil {
				return err
			}
			round = append(round, index)
		}
		w.settle()
		w.plan.blockRounds = append(w.plan.blockRounds, round)
	}
	return w.finish(proceeding)
}

func (w *writtenPlan) name(index int, name string) error {
	_, dup := w.byName[name]
	_, dupHere := w.pending[name]
	if dup || dupHere || name == "" || strings.Contains(name, placeSeparator) {
		return Keyf(w.key, "%s", dopestrings.Default.Structure.De.BoutsName(w.key, name))
	}
	w.pending[name] = index
	w.plan.names[index] = name
	return nil
}

// settle makes the round just read available to the rounds after it.
func (w *writtenPlan) settle() {
	for name, index := range w.pending {
		w.byName[name] = index
	}
	clear(w.pending)
}

// bout reads one written bout, «M: E-1 H-1 F-2 G-2», and appends it. A written
// bout is always named: its number in the block would be neither its title
// nor anything the regulations call it.
func (w *writtenPlan) bout(item string) (int, error) {
	s := dopestrings.Default
	name, seats, named := SplitBoutItem(item)
	if !named {
		return 0, Keyf(w.key, "%s", s.Structure.De.BoutsUnnamed(w.key, strings.TrimSpace(item)))
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
	index := len(w.plan.bouts)
	w.plan.bouts = append(w.plan.bouts, bout)
	w.plan.names = append(w.plan.names, "")
	w.plan.titles = append(w.plan.titles, name)
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

// carriedLosses is how many Losses a bout's place leaves its holder with. A
// final's places are its standings, not Losses.
func (plan *dePlan) carriedLosses(source deSource) int {
	bout := plan.bouts[source.bout]
	if !bout.final && source.place > plan.winning {
		return bout.losses + 1
	}
	return bout.losses
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
	carried := w.plan.carriedLosses(source)
	if carried >= w.plan.lives {
		return deSource{}, 0, Keyf(w.key, "%s", dopestrings.Default.Structure.De.BoutsOut(w.key, token))
	}
	w.taken[source] = true
	return source, carried, nil
}

// finish checks that everyone still in before the last round plays on, and
// makes the last round's places the block's survivors: fewer Losses first,
// as the planner ranks them, then in the order the round lists them.
func (w *writtenPlan) finish(proceeding int) error {
	plan := w.plan
	last := map[int]bool{}
	for _, index := range plan.blockRounds[len(plan.blockRounds)-1] {
		last[index] = true
	}
	plan.survivor = nil
	for index, bout := range plan.bouts {
		for place := 1; place <= len(bout.sources); place++ {
			source := deSource{bout: index, place: place}
			alive := plan.carriedLosses(source) < plan.lives
			switch {
			case last[index] && alive:
				plan.survivor = append(plan.survivor, source)
			case alive && !w.taken[source]:
				token := plan.names[index] + placeSeparator + strconv.Itoa(place)
				return Keyf(w.key, "%s", dopestrings.Default.Structure.De.BoutsUnused(token))
			}
		}
	}
	sort.SliceStable(plan.survivor, func(i, j int) bool {
		return plan.carriedLosses(plan.survivor[i]) < plan.carriedLosses(plan.survivor[j])
	})
	if proceeding > len(plan.survivor) {
		return Keyf("proceeding_participants", "%s", dopestrings.Default.Structure.De.BoutsProceeding(strconv.Itoa(proceeding), strconv.Itoa(len(plan.survivor))))
	}
	plan.survivor = rankSources(plan.survivor)
	return nil
}
