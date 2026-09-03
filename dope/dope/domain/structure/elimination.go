package structure

import (
	"fmt"
	"sort"
	"strconv"

	dopestrings "dope/i18nstrings"
)

// The elimination rounds, for both Kinds.
//
// An elimination is defined by how many Losses end a Participant's tournament —
// one, or two — and by nothing else. A Loss is failing to be among a Match's
// winning places, so a Match of four where two proceed eliminates two, exactly
// as a Match of two where one proceeds eliminates one. That is why EK's bracket
// of four-seat Matches and KINSBF's pods of two-seat Matches are the same Kind
// at different sizes (CONTEXT.md, "Loss").

// elimRound is one round of an elimination: how many Participants enter it,
// how many seats a Match has, and how many Matches there are. A round whose
// Matches seat every survivor is terminal — it decides the block's final
// places and nothing follows it.
type elimRound struct {
	entering int
	size     int
	bouts    int
	terminal bool
}

// survivors is how many Participants a round sends on.
func (r elimRound) survivors(winning int) int {
	if r.terminal {
		return 0
	}
	return r.bouts * winning
}

// planElimRounds lays out the rounds one bracket plays: each round splits its
// entrants into Matches of the size that round asks for, the winning places go
// on, and the block ends when a single Match seats everyone left. sizeFor is
// asked per round so a scheme can play its 1/4 three to a table and its 1/2
// four, the way EK does.
//
// maxRounds stops the bracket short of a final. TPSH plays two: three Matches
// of four each send two on, and those six are the winners — nobody plays again.
func planElimRounds(entrants, winning, maxRounds int, sizeFor func(round, entering int) int) ([]elimRound, error) {
	if winning < 1 {
		return nil, fmt.Errorf("winning_places должен быть хотя бы 1")
	}
	var rounds []elimRound
	entering := entrants
	for round := 1; ; round++ {
		if maxRounds > 0 && len(rounds) == maxRounds {
			return rounds, nil
		}
		size := sizeFor(round, entering)
		if size < 2 {
			return nil, fmt.Errorf("match_size должен быть хотя бы 2")
		}
		if size <= winning {
			return nil, fmt.Errorf("бой на %d мест не может выводить %d — победителей не меньше, чем мест", size, winning)
		}
		if entering <= size {
			rounds = append(rounds, elimRound{entering: entering, size: entering, bouts: 1, terminal: true})
			return rounds, nil
		}
		if entering%size != 0 {
			return nil, fmt.Errorf("раунд %d: %d участников не делятся на бои по %d", round, entering, size)
		}
		bouts := entering / size
		rounds = append(rounds, elimRound{entering: entering, size: size, bouts: bouts})
		next := bouts * winning
		if next >= entering {
			return nil, fmt.Errorf("раунд %d никого не выбивает: %d участников, %d проходит", round, entering, next)
		}
		entering = next
		if len(rounds) > 64 {
			return nil, fmt.Errorf("слишком много раундов — проверьте match_size и winning_places")
		}
	}
}

// elimRoundNames are the dotted-override keys a round answers to: `r{N}` by
// its number in the block, always. A halving bracket's last two rounds are
// the semifinal and the final by arithmetic, so those go by their names too,
// and by them first (the stage code); a round of twelve four-seat Matches is
// nobody's 1/16, so a scheme that wants that name writes it as a title.
func elimRoundNames(r elimRound, index int, winning int) []string {
	ordinal := fmt.Sprintf("r%d", index+1)
	if r.size == 2 && winning == 1 {
		switch r.entering {
		case 2:
			return []string{"final", ordinal}
		case 4:
			return []string{"semifinal", ordinal}
		}
	}
	return []string{ordinal}
}

func elimRoundTitle(r elimRound, index int, winning int) string {
	s := dopestrings.Default
	if r.size == 2 && winning == 1 {
		return seRoundTitle(r.entering)
	}
	if r.terminal {
		return s.Structure.Titles.Final()
	}
	return s.Structure.Titles.Round(strconv.Itoa(index + 1))
}

// snakeChunks splits 1..n into `bouts` Matches of equal size the way a group
// draw deals baskets: the first Match takes the best and the worst, so no
// Match is a group of death. It is the same shape StudChR's playoff sheets
// use — ranks 1, 12, 13, 24 meeting in the first Match of 24.
func snakeChunks(n, bouts int) [][]int {
	size := n / bouts
	out := make([][]int, bouts)
	rank := 1
	for column := 0; column < size; column++ {
		for i := 0; i < bouts; i++ {
			bout := i
			if column%2 == 1 {
				bout = bouts - 1 - i
			}
			out[bout] = append(out[bout], rank)
			rank++
		}
	}
	return out
}

// straightChunks splits 1..n into consecutive Matches — the bracket template,
// where a round's Match takes the previous round's Matches in order.
func straightChunks(n, bouts int) [][]int {
	size := n / bouts
	out := make([][]int, bouts)
	for i := 0; i < bouts; i++ {
		for k := 0; k < size; k++ {
			out[i] = append(out[i], i*size+k+1)
		}
	}
	return out
}

// elimDraw seats a re-ranked round. A halving bracket keeps the classic
// bracket order, which holds the top seeds apart until the late rounds; any
// other shape uses the snake, which is what a multi-seat Match wants and what
// the StudChR sheets do.
func elimDraw(remaining, bouts, size, winning int) [][]int {
	if size == 2 && winning == 1 {
		order := BracketOrder(remaining)
		out := make([][]int, bouts)
		for i := 0; i < bouts; i++ {
			out[i] = []int{order[2*i], order[2*i+1]}
		}
		return out
	}
	return snakeChunks(remaining, bouts)
}

// --- the bracket with lives ----------------------------------------------

// deSource is where one seat of a planned Match comes from: an entrant rank
// before anything has been played, or a place in an earlier Match.
type deSource struct {
	entrant int // 1-based entrant rank, 0 when the seat comes from a Match
	bout    int // index into dePlan.bouts
	place   int
	rank    int // 1-based rank among all survivors, for a re-ranked round
}

// deBout is one planned Match: the Losses its seats carry into it, and where
// those seats come from.
type deBout struct {
	losses  int
	sources []deSource
}

// dePlan is a whole elimination with lives: Matches in play order, grouped
// into rounds, and who is left when it stops.
type dePlan struct {
	lives, winning int
	bouts          []deBout
	rounds         [][]int
	// alive[r] is everyone still in at round r, in the rank order the reseed
	// hands out — including the brackets that sit the round out, because they
	// still hold their ranks and the Matches of that round are numbered around them.
	alive      [][]deSource
	aliveBands [][][2]int // per round, per alive entry: (Losses now, Losses before)
	survivor   []deSource // block qualifiers, best first
}

// planLives lays out an elimination where `lives` Losses end a tournament.
//
// Every round, each bracket (0 Losses, 1 Loss, …) plays its own Matches; the
// winning places stay where they are and everyone else moves down a bracket or
// out. A bracket that is already down to its winning places stops playing — it
// has produced its finalists and waits. The block ends when the survivors fit
// the block's proceeding count, or when a single Match seats all of them: that
// Match is the final and decides their places.
//
// Ordering is the model's one real convention: survivors rank by bracket first
// and place second, and a Participant just dropped from the bracket above
// ranks ahead of one that survived the bracket below. Both are what «fewer
// losses first» means, and both are what the StudChR sheets do.
func planLives(entrants, lives, winning, proceeding int, sizeFor func(round, members int) int) (*dePlan, error) {
	return planLivesDrawn(entrants, lives, winning, proceeding, sizeFor, snakeChunks)
}

// planLivesDrawn is planLives with the opening round's draw spelled out: a
// bracket seeded from a ranking deals it as a snake, while one fed by the
// previous block's template takes its entrants in the order that template
// already balanced them.
func planLivesDrawn(entrants, lives, winning, proceeding int, sizeFor func(round, members int) int, opening func(n, bouts int) [][]int) (*dePlan, error) {
	if lives < 1 {
		return nil, fmt.Errorf("нужна хотя бы одна жизнь")
	}
	if winning < 1 {
		return nil, fmt.Errorf("winning_places должен быть хотя бы 1")
	}
	if proceeding < 1 {
		proceeding = 1
	}
	plan := &dePlan{lives: lives, winning: winning}
	brackets := make([][]deSource, lives)
	for rank := 1; rank <= entrants; rank++ {
		brackets[0] = append(brackets[0], deSource{entrant: rank})
	}
	for round := 1; ; round++ {
		alive := 0
		for _, bracket := range brackets {
			alive += len(bracket)
		}
		if alive <= proceeding {
			plan.survivor = flattenBrackets(brackets)
			return plan, nil
		}
		aliveNow := flattenBrackets(brackets)
		bandsNow := make([][2]int, 0, len(aliveNow))
		for b := 0; b < lives; b++ {
			for _, member := range brackets[b] {
				from := b
				if member.entrant == 0 {
					from = plan.bouts[member.bout].losses
				}
				bandsNow = append(bandsNow, [2]int{b, from})
			}
		}
		if size := sizeFor(round, alive); alive <= size {
			seats := rankSources(flattenBrackets(brackets))
			plan.bouts = append(plan.bouts, deBout{losses: 0, sources: seats})
			plan.rounds = append(plan.rounds, []int{len(plan.bouts) - 1})
			plan.alive = append(plan.alive, aliveNow)
			plan.aliveBands = append(plan.aliveBands, bandsNow)
			last := len(plan.bouts) - 1
			for place := 1; place <= len(seats); place++ {
				plan.survivor = append(plan.survivor, deSource{bout: last, place: place})
			}
			return plan, nil
		}

		var roundBouts []int
		next := make([][]deSource, lives)
		rankBase := 0
		played := false
		for b := 0; b < lives; b++ {
			members := brackets[b]
			start := rankBase
			rankBase += len(members)
			if len(members) <= winning {
				next[b] = append(next[b], members...)
				continue
			}
			size, err := bracketBoutSize(len(members), winning, sizeFor(round, len(members)))
			if err != nil {
				return nil, fmt.Errorf("раунд %d, сетка %d: %w", round, b+1, err)
			}
			count := len(members) / size
			chunks := snakeChunks(len(members), count)
			if round == 1 && b == 0 {
				chunks = opening(len(members), count)
			}
			played = true
			var winners, losers []deSource
			for _, chunk := range chunks {
				seats := make([]deSource, len(chunk))
				for i, position := range chunk {
					seats[i] = members[position-1]
					seats[i].rank = start + position
				}
				plan.bouts = append(plan.bouts, deBout{losses: b, sources: seats})
				index := len(plan.bouts) - 1
				roundBouts = append(roundBouts, index)
				for place := 1; place <= size; place++ {
					who := deSource{bout: index, place: place}
					if place <= winning {
						winners = append(winners, who)
					} else {
						losers = append(losers, who)
					}
				}
			}
			// Droppers head the bracket below: fewer Losses so far, so ahead of
			// everyone who has been down here already.
			if b+1 < lives {
				next[b+1] = append(next[b+1], losers...)
			}
			next[b] = append(next[b], winners...)
		}
		if !played {
			plan.survivor = flattenBrackets(brackets)
			return plan, nil
		}
		plan.rounds = append(plan.rounds, roundBouts)
		plan.alive = append(plan.alive, aliveNow)
		plan.aliveBands = append(plan.aliveBands, bandsNow)
		brackets = next
		if round > 64 {
			return nil, fmt.Errorf("слишком много раундов — проверьте match_size и winning_places")
		}
	}
}

// bracketBoutSize picks the largest Match a bracket divides into evenly, and
// caps it by what the scheme asked for. SI's playoff needs this: the same
// round seats its upper bracket of six three at a table and its lower bracket
// of twelve four at a table.
func bracketBoutSize(members, winning, want int) (int, error) {
	if want < 2 {
		want = 2
	}
	for size := want; size > winning; size-- {
		if members%size == 0 && members/size >= 1 {
			return size, nil
		}
	}
	return 0, fmt.Errorf("%d участников не делятся на бои не больше чем по %d, из которых выходит %d", members, want, winning)
}

func flattenBrackets(brackets [][]deSource) []deSource {
	var out []deSource
	for _, bracket := range brackets {
		out = append(out, bracket...)
	}
	return rankSources(out)
}

func rankSources(sources []deSource) []deSource {
	out := make([]deSource, len(sources))
	for i, source := range sources {
		source.rank = i + 1
		out[i] = source
	}
	return out
}

// eliminationStandings ranks an elimination's Participants the one way both
// Kinds do: by Losses. Alive first (fewer Losses first), then the eliminated,
// the later round of elimination first and, within it, fewer total Losses
// first — a placement Match (the bronze) adds a Loss to the one it places lower.
// Equal keys share a place; a survivor has no place until the Block is played
// out, since the Matches that decide it are still ahead. MatchOutcome.Round
// says when a Loss fell; a Match seats any number, and a shared (fractional)
// place is no Loss.
func eliminationStandings(lives, winningPlaces int, results []MatchOutcome) []RankedEntry {
	if lives < 1 {
		lives = 1
	}
	if winningPlaces < 1 {
		winningPlaces = 1
	}
	losses := map[int64]int{}
	eliminated := map[int64]int{}
	var order []int64
	seen := map[int64]bool{}
	allFinished := len(results) > 0
	for _, match := range results {
		for _, slot := range match.Slots {
			if slot.Participant != 0 && !seen[slot.Participant] {
				seen[slot.Participant] = true
				order = append(order, slot.Participant)
			}
		}
		if !match.Finished {
			allFinished = false
			continue
		}
		for _, slot := range match.Slots {
			if slot.Participant == 0 {
				continue
			}
			lost := slot.Place > float64(winningPlaces) && slot.Place == float64(int(slot.Place))
			if !lost {
				continue
			}
			losses[slot.Participant]++
			if losses[slot.Participant] == lives {
				if _, out := eliminated[slot.Participant]; !out {
					eliminated[slot.Participant] = match.Round
				}
			}
		}
	}
	key := func(id int64) int {
		if round, out := eliminated[id]; out {
			return 1000*(1000-round) + losses[id]
		}
		return losses[id] - 1000
	}
	ranked := make([]int64, len(order))
	copy(ranked, order)
	sort.SliceStable(ranked, func(i, j int) bool { return key(ranked[i]) < key(ranked[j]) })
	out := make([]RankedEntry, 0, len(ranked))
	for i, id := range ranked {
		entry := RankedEntry{Participant: id, Metrics: map[string]float64{"losses": float64(losses[id])}}
		if _, placed := eliminated[id]; placed || allFinished {
			if i > 0 && key(ranked[i-1]) == key(id) {
				entry.Rank = out[i-1].Rank
			} else {
				entry.Rank = i + 1
			}
		}
		out = append(out, entry)
	}
	return out
}
