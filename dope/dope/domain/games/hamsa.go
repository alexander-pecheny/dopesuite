package games

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// Hamsa pure domain logic.
//
// A бой is five game rounds. The first four play темы of five вопросы each —
// five темы in rounds 1..3 and a single тема in round 4 — and each round
// multiplies the base номиналы (×1, ×2, ×3, ×4). The fifth round is one
// вопрос played on a Ставка: a secret bet the team writes down, added to its
// score when it answers and subtracted when it does not. A тема also records
// which of the team's six played it, because who sat for a тема is the whole
// of what the regulations ask a captain to decide.
//
// The document is keyed by Participant id, as EK's blob is, so a re-seat can
// never move one team's marks onto another. What a вопрос was worth is written
// into the document when the бой is built: it is a fact about the бой that
// played it, not about the scheme as it stands today.

const (
	// HamsaQuestions is a тема's вопросы, one per номинал.
	HamsaQuestions = 5
	// HamsaBetRight and HamsaBetWrong are the two answers a Ставка can take;
	// an empty answer is a round nobody has played yet.
	HamsaBetRight = "right"
	HamsaBetWrong = "wrong"
)

// The shape of a бой when the scheme says nothing: three rounds of five темы,
// a fourth of one, the base номиналы, and the multiplier each round pays at.
var (
	HamsaThemes      = []int{5, 5, 5, 1}
	HamsaMultipliers = []int{1, 2, 3, 4}
	HamsaValues      = []int{100, 200, 300, 400, 500}
)

// HamsaGameRound is one game round as the document records it: how many темы
// it played and what each of a тема's five вопросы was worth.
type HamsaGameRound struct {
	Themes int   `json:"themes"`
	Values []int `json:"values"`
}

// HamsaTheme is one тема on one team: who played it and the five marks —
// "right", "wrong" or "" for a вопрос nobody answered.
type HamsaTheme struct {
	Player  int64    `json:"player,omitempty"`
	Answers []string `json:"answers,omitempty"`
}

// HamsaBet is the Командный round: the points a team wrote down and whether
// the answer was accepted. The amount is stored exactly as the host typed it —
// the regulations cap it at the team's balance, and dope records rather than
// polices (docs/hamsa-plan.md, decision 5).
type HamsaBet struct {
	Amount *int   `json:"amount"`
	Answer string `json:"answer,omitempty"`
}

// HamsaParticipant is one team's side of the document: its темы in game-round
// order, its Ставка, the перестрелка темы a Блок that allows them may add, and
// the host's Pin.
type HamsaParticipant struct {
	Themes   []HamsaTheme `json:"themes,omitempty"`
	Bet      *HamsaBet    `json:"bet,omitempty"`
	Shootout []HamsaTheme `json:"shootout,omitempty"`
	Pin      *float64     `json:"pin,omitempty"`
}

// HamsaState mirrors matches.state_json.
type HamsaState struct {
	Rounds       []HamsaGameRound             `json:"rounds,omitempty"`
	Participants map[string]*HamsaParticipant `json:"participants,omitempty"`
}

// HamsaGameRounds resolves a бой's game rounds from its stage config: how many
// темы each round plays, what the base номиналы are, and the multiplier each
// round pays at. A list the scheme left short is padded from the defaults, so
// `multipliers: [1, 2]` still describes four rounds.
func HamsaGameRounds(themes, multipliers, values []int) []HamsaGameRound {
	base := make([]int, HamsaQuestions)
	for i := range base {
		base[i] = HamsaValues[i]
		if i < len(values) && values[i] != 0 {
			base[i] = values[i]
		}
	}
	count := len(themes)
	if count == 0 {
		count = len(HamsaThemes)
	}
	rounds := make([]HamsaGameRound, count)
	for r := range rounds {
		size := 0
		switch {
		case r < len(themes):
			size = themes[r]
		case r < len(HamsaThemes):
			size = HamsaThemes[r]
		}
		if size < 0 {
			size = 0
		}
		multiplier := r + 1
		switch {
		case r < len(multipliers) && multipliers[r] != 0:
			multiplier = multipliers[r]
		case r < len(HamsaMultipliers):
			multiplier = HamsaMultipliers[r]
		}
		round := HamsaGameRound{Themes: size, Values: make([]int, HamsaQuestions)}
		for q := range round.Values {
			round.Values[q] = base[q] * multiplier
		}
		rounds[r] = round
	}
	return rounds
}

// HamsaThemeCount is how many темы a бой of these rounds plays in all.
func HamsaThemeCount(rounds []HamsaGameRound) int {
	total := 0
	for _, round := range rounds {
		total += round.Themes
	}
	return total
}

// HamsaBaseValues is the scale the per-номинал counts are named after: what a
// вопрос is worth in the first game round, which is the base номинал itself.
func HamsaBaseValues(rounds []HamsaGameRound) []int {
	if len(rounds) > 0 && len(rounds[0].Values) == HamsaQuestions {
		return rounds[0].Values
	}
	return HamsaValues
}

// HamsaShootoutValues is what a перестрелка тема's вопросы are worth: the last
// game round's номиналы, since the tiebreak is another Персональный round.
func HamsaShootoutValues(rounds []HamsaGameRound) []int {
	if len(rounds) > 0 {
		return rounds[len(rounds)-1].Values
	}
	return HamsaValues
}

// HamsaEmptyStateJSON is the pristine document of one бой: the rounds it
// plays, and no participants — a бой's seats come from its Slots and its marks
// arrive as edits, so nothing is written for a team that has not played yet.
func HamsaEmptyStateJSON(rounds []HamsaGameRound) []byte {
	return []byte(mustJSON(HamsaState{Rounds: rounds}))
}

// HamsaStateStarted reports whether a host has entered anything — a mark, a
// player, a Ставка or a Pin. A started бой is one a scheme recompile must not
// reseat.
func HamsaStateStarted(stateJSON string) bool {
	state, err := ParseHamsaState(stateJSON)
	if err != nil {
		return true // unreadable state is data, not pristine
	}
	for _, section := range state.Participants {
		if section == nil {
			continue
		}
		if section.Pin != nil {
			return true
		}
		if section.Bet != nil && (section.Bet.Amount != nil || section.Bet.Answer != "") {
			return true
		}
		for _, themes := range [][]HamsaTheme{section.Themes, section.Shootout} {
			for _, theme := range themes {
				if theme.Player != 0 {
					return true
				}
				for _, mark := range theme.Answers {
					if mark != "" {
						return true
					}
				}
			}
		}
	}
	return false
}

// ParseHamsaState decodes a бой's document; the empty string is the pristine one.
func ParseHamsaState(stateJSON string) (HamsaState, error) {
	var state HamsaState
	if stateJSON == "" {
		return state, nil
	}
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return HamsaState{}, fmt.Errorf("parse hamsa state: %w", err)
	}
	return state, nil
}

// HamsaResult is one team's computed outcome of a бой.
type HamsaResult struct {
	Participant int64
	// Total is the whole score: the темы plus or minus the Ставка.
	Total int
	// Plus is what the темы paid without their penalties — Σ+ as EK counts it.
	// The Ставка stays out: it measures a gamble, not what the team took.
	Plus int
	// Bet is the Ставка's signed contribution to Total.
	Bet int
	// ShootoutTotal is the перестрелка темы, held outside Total and ranked on
	// after it.
	ShootoutTotal int
	// First is 1 for every team nobody finished ahead of — «первое место» as
	// the regulations count them, a shared top place counting for each team
	// that shares it.
	First float64
	// Place is what the бой came to, the host's Pin standing instead of the
	// computed place where there is one.
	Place   float64
	Pin     float64
	Pinned  bool
	Correct map[int]int // base номинал → вопросы taken at it
	Wrong   map[int]int // base номинал → вопросы lost at it
}

// hamsaValues is the номиналы of the тема at index i, walking the rounds in
// order. A тема past the rounds the document declares falls back to the last
// round's номиналы, so a бой built before the scheme grew a тема still scores.
func hamsaValues(rounds []HamsaGameRound, theme int) []int {
	seen := 0
	for _, round := range rounds {
		if theme < seen+round.Themes {
			return round.Values
		}
		seen += round.Themes
	}
	if len(rounds) > 0 {
		return rounds[len(rounds)-1].Values
	}
	return HamsaValues
}

// ComputeHamsaResults scores a бой. Seats are the Participants sitting at it,
// in slot order: a team that entered nothing still took a place, so the seats
// decide the rows rather than the document does. With no seats given — a unit
// test, an export — the document's own teams are scored, lowest id first.
func ComputeHamsaResults(stateJSON string, seats []int64) ([]HamsaResult, error) {
	state, err := ParseHamsaState(stateJSON)
	if err != nil {
		return nil, err
	}
	if seats == nil {
		seats = hamsaDocumentSeats(state)
	}
	base := HamsaBaseValues(state.Rounds)
	results := make([]HamsaResult, len(seats))
	for i, id := range seats {
		result := HamsaResult{
			Participant: id,
			Correct:     map[int]int{},
			Wrong:       map[int]int{},
		}
		for _, value := range base {
			result.Correct[value] = 0
			result.Wrong[value] = 0
		}
		section := state.Participants[strconv.FormatInt(id, 10)]
		if section != nil {
			for t, theme := range section.Themes {
				values := hamsaValues(state.Rounds, t)
				for q, mark := range theme.Answers {
					if q >= len(values) || q >= len(base) {
						continue
					}
					switch mark {
					case "right":
						result.Total += values[q]
						result.Plus += values[q]
						result.Correct[base[q]]++
					case "wrong":
						result.Total -= values[q]
						result.Wrong[base[q]]++
					}
				}
			}
			for _, theme := range section.Shootout {
				values := HamsaShootoutValues(state.Rounds)
				for q, mark := range theme.Answers {
					if q >= len(values) {
						continue
					}
					switch mark {
					case "right":
						result.ShootoutTotal += values[q]
					case "wrong":
						result.ShootoutTotal -= values[q]
					}
				}
			}
			if bet := section.Bet; bet != nil && bet.Amount != nil {
				switch bet.Answer {
				case HamsaBetRight:
					result.Bet = *bet.Amount
				case HamsaBetWrong:
					result.Bet = -*bet.Amount
				}
			}
			result.Total += result.Bet
			if section.Pin != nil {
				result.Pin, result.Pinned = *section.Pin, true
			}
		}
		results[i] = result
	}
	hamsaPlaces(results)
	return results, nil
}

// hamsaDocumentSeats is the document's own teams, lowest id first — a stable
// order for a caller with no seating to hand over.
func hamsaDocumentSeats(state HamsaState) []int64 {
	ids := make([]int64, 0, len(state.Participants))
	for key := range state.Participants {
		id, err := strconv.ParseInt(key, 10, 64)
		if err != nil {
			continue
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
	return ids
}

// hamsaPlaces ranks a бой by total, then by the перестрелка, then by Σ+.
// Teams level on all three share the mean of the places they cover — «команды,
// набравшие равное количество игровых очков, считаются разделившими
// соответствующие места» — and a host's Pin stands instead of the place it
// computed. First is then read off the places: every team nobody finished
// ahead of took a первое место.
func hamsaPlaces(results []HamsaResult) {
	order := make([]int, 0, len(results))
	for i := range results {
		order = append(order, i)
	}
	key := func(i int) [3]int {
		return [3]int{results[i].Total, results[i].ShootoutTotal, results[i].Plus}
	}
	sort.SliceStable(order, func(a, b int) bool {
		ka, kb := key(order[a]), key(order[b])
		for i := range ka {
			if ka[i] != kb[i] {
				return ka[i] > kb[i]
			}
		}
		return false
	})
	for start := 0; start < len(order); {
		end := start + 1
		for end < len(order) && key(order[end]) == key(order[start]) {
			end++
		}
		place := float64(start+end+1) / 2
		for i := start; i < end; i++ {
			results[order[i]].Place = place
		}
		start = end
	}
	for i := range results {
		if results[i].Pinned {
			results[i].Place = results[i].Pin
		}
	}
	best := 0.0
	for _, result := range results {
		if result.Place > 0 && (best == 0 || result.Place < best) {
			best = result.Place
		}
	}
	for i := range results {
		if best > 0 && results[i].Place == best {
			results[i].First = 1
		}
	}
}
