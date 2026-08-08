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
