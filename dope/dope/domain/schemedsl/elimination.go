package schemedsl

import (
	"fmt"

	"dope/dope/domain/structure"
)

// The elimination rounds, for both Kinds.
//
// An elimination is defined by how many Losses end a Participant's tournament —
// one, or two — and by nothing else. A Loss is failing to be among a бой's
// winning places, so a бой of four where two proceed eliminates two, exactly as
// a бой of two where one proceeds eliminates one. That is why ЭК's bracket of
// four-seat бои and КИнСБФ's pods of two-seat бои are the same Kind at
// different sizes (CONTEXT.md, «Loss»).

// elimRound is one round of an elimination: how many Participants enter it,
// how many seats a бой has, and how many бои there are. A round whose бои seat
// every survivor is terminal — it decides the block's final places and nothing
// follows it.
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
// entrants into бои of the size that round asks for, the winning places go on,
// and the block ends when a single бой seats everyone left. sizeFor is asked
// per round so a scheme can play its 1/4 three to a table and its 1/2 four, the
// way ЭК does.
func planElimRounds(entrants, winning int, sizeFor func(round, entering int) int) ([]elimRound, error) {
	if winning < 1 {
		return nil, fmt.Errorf("winning_places должен быть хотя бы 1")
	}
	var rounds []elimRound
	entering := entrants
	for round := 1; ; round++ {
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

// elimRoundNames are the dotted-override keys a round answers to. A halving
// bracket keeps the traditional names, because there «1/4 финала» is
// arithmetically what the round is; any other shape is named by its number,
// since a round of twelve four-seat бои is nobody's 1/16 by arithmetic — a
// scheme that wants that name writes it as a title.
func elimRoundNames(r elimRound, index int, winning int) []string {
	if r.size == 2 && winning == 1 {
		return seRoundNames(r.entering)
	}
	return []string{fmt.Sprintf("r%d", index+1)}
}

func elimRoundTitle(r elimRound, index int, winning int) string {
	if r.size == 2 && winning == 1 {
		return seRoundTitle(r.entering)
	}
	if r.terminal {
		return "Финал"
	}
	return fmt.Sprintf("Раунд %d", index+1)
}

// snakeChunks splits 1..n into `bouts` бои of equal size the way a group draw
// deals baskets: the first бой takes the best and the worst, so no бой is a
// group of death. It is the same shape СтудЧР's playoff sheets use — ranks
// 1, 12, 13, 24 meeting in the first бой of 24.
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

// straightChunks splits 1..n into consecutive бои — the bracket template, where
// a round's бой takes the previous round's бои in order.
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
// other shape uses the snake, which is what a multi-seat бой wants and what
// the СтудЧР sheets do.
func elimDraw(remaining, bouts, size, winning int) [][]int {
	if size == 2 && winning == 1 {
		order := structure.BracketOrder(remaining)
		out := make([][]int, bouts)
		for i := 0; i < bouts; i++ {
			out[i] = []int{order[2*i], order[2*i+1]}
		}
		return out
	}
	return snakeChunks(remaining, bouts)
}

// --- the bracket with lives ----------------------------------------------

// deSource is where one seat of a planned бой comes from: an entrant rank
// before anything has been played, or a place in an earlier бой.
type deSource struct {
	entrant int // 1-based entrant rank, 0 when the seat comes from a бой
	bout    int // index into dePlan.bouts
	place   int
	rank    int // 1-based rank among all survivors, for a re-ranked round
}

// deBout is one planned бой: the Losses its seats carry into it, and where
// those seats come from.
type deBout struct {
	losses  int
	sources []deSource
}

// dePlan is a whole elimination with lives: бои in play order, grouped into
// rounds, and who is left when it stops.
type dePlan struct {
	bouts    []deBout
	rounds   [][]int
	survivor []deSource // block qualifiers, best first
}

// planLives lays out an elimination where `lives` Losses end a tournament.
//
// Every round, each bracket (0 Losses, 1 Loss, …) plays its own бои; the
// winning places stay where they are and everyone else moves down a bracket or
// out. A bracket that is already down to its winning places stops playing — it
// has produced its finalists and waits. The block ends when the survivors fit
// the block's proceeding count, or when a single бой seats all of them: that
// бой is the final and decides their places.
//
// Ordering is the model's one real convention: survivors rank by bracket first
// and place second, and a Participant just dropped from the bracket above
// ranks ahead of one that survived the bracket below. Both are what «fewer
// losses first» means, and both are what the СтудЧР sheets do.
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
	plan := &dePlan{}
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
		if size := sizeFor(round, alive); alive <= size {
			seats := rankSources(flattenBrackets(brackets))
			plan.bouts = append(plan.bouts, deBout{losses: 0, sources: seats})
			plan.rounds = append(plan.rounds, []int{len(plan.bouts) - 1})
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
		brackets = next
		if round > 64 {
			return nil, fmt.Errorf("слишком много раундов — проверьте match_size и winning_places")
		}
	}
}

// bracketBoutSize picks the largest бой a bracket divides into evenly, capped
// by what the scheme asked for. СИ's playoff needs this: the same round seats
// its upper bracket of six three at a table and its lower bracket of twelve
// four at a table.
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
