package structure

import (
	"encoding/json"
	"fmt"

	"dope/dope/storage/store"
)

func init() { Register(singleElim{}) }

// singleElim is the knockout kind: entrants (a power of two) are laid out in
// standard bracket order, each round's winners advance by fromMatch place-1
// refs, and an optional bronze бой seats the semifinal losers.
type singleElim struct{}

func (singleElim) Code() string { return "se" }

type seConfig struct {
	Code     string             `json:"code"`
	Venue    int                `json:"venue"`
	Bronze   bool               `json:"bronze"`
	Entrants []store.SchemeSlot `json:"entrants"`
}

// bracketOrder returns 1-based entrant ranks in standard bracket layout: the
// classic recursive fold that keeps top seeds apart until the late rounds
// (for 8: 1,8,4,5,3,6,2,7).
func bracketOrder(n int) []int {
	order := []int{1}
	for len(order) < n {
		grown := make([]int, 0, len(order)*2)
		mirror := len(order)*2 + 1
		for _, rank := range order {
			grown = append(grown, rank, mirror-rank)
		}
		order = grown
	}
	return order
}

func (singleElim) Schedule(cfg json.RawMessage, results []MatchOutcome) ([]store.SchemeMatch, error) {
	var conf seConfig
	if err := json.Unmarshal(cfg, &conf); err != nil {
		return nil, fmt.Errorf("se config: %w", err)
	}
	n := len(conf.Entrants)
	if n < 2 || n&(n-1) != 0 {
		return nil, fmt.Errorf("se: %d entrants, need a power of two", n)
	}

	var matches []store.SchemeMatch
	code := func(round, index int) string { return fmt.Sprintf("%s-r%d-%d", conf.Code, round, index) }
	emit := func(matchCode, title string, slots [2]store.SchemeSlot) {
		matches = append(matches, store.SchemeMatch{
			Code:             matchCode,
			Title:            title,
			Venue:            conf.Venue,
			ParticipantCount: 2,
			Slots:            slots[:],
		})
	}
	winnerOf := func(matchCode string) store.SchemeSlot {
		return store.SchemeSlot{FromMatch: &store.SchemeFromMatchRef{Match: matchCode, Place: 1}}
	}
	loserOf := func(matchCode string) store.SchemeSlot {
		return store.SchemeSlot{FromMatch: &store.SchemeFromMatchRef{Match: matchCode, Place: 2}}
	}

	rounds := 0
	for size := n; size > 1; size /= 2 {
		rounds++
	}
	order := bracketOrder(n)
	for i := 0; i < n/2; i++ {
		emit(code(1, i+1), roundTitle(rounds, 1, i+1),
			[2]store.SchemeSlot{conf.Entrants[order[2*i]-1], conf.Entrants[order[2*i+1]-1]})
	}
	for round := 2; round <= rounds; round++ {
		count := n >> uint(round)
		for i := 0; i < count; i++ {
			emit(code(round, i+1), roundTitle(rounds, round, i+1),
				[2]store.SchemeSlot{winnerOf(code(round-1, 2*i+1)), winnerOf(code(round-1, 2*i+2))})
		}
	}
	if conf.Bronze {
		semi := rounds - 1
		emit(fmt.Sprintf("%s-r%d-3p", conf.Code, rounds), "Матч за 3-е место",
			[2]store.SchemeSlot{loserOf(code(semi, 1)), loserOf(code(semi, 2))})
	}
	return matches, nil
}

func roundTitle(rounds, round, index int) string {
	switch rounds - round {
	case 0:
		return "Финал"
	case 1:
		return fmt.Sprintf("Полуфинал %d", index)
	default:
		return fmt.Sprintf("1/%d финала %d", 1<<uint(rounds-round), index)
	}
}

// Standings ranks by progression: the champion first, then losers by the round
// they fell in, late rounds ranking higher. Participants still alive share the
// top band; a finished bronze бой splits its two semifinal losers.
func (singleElim) Standings(cfg json.RawMessage, results []MatchOutcome) ([]RankedEntry, error) {
	var conf seConfig
	if err := json.Unmarshal(cfg, &conf); err != nil {
		return nil, fmt.Errorf("se standings config: %w", err)
	}
	rounds := 0
	for size := len(conf.Entrants); size > 1; size /= 2 {
		rounds++
	}

	const (
		keyChampion = 0.0
		keyAlive    = 0.5
	)
	band := map[int64]float64{}
	var appearance []int64
	seen := func(id int64) {
		if _, ok := band[id]; !ok && id != 0 {
			band[id] = keyAlive
			appearance = append(appearance, id)
		}
	}
	for _, match := range results {
		var round int
		bronze := false
		if _, err := fmt.Sscanf(match.Code, conf.Code+"-r%d-3p", &round); err == nil {
			bronze = true
		} else if _, err := fmt.Sscanf(match.Code, conf.Code+"-r%d-", &round); err != nil {
			continue
		}
		if len(match.Slots) != 2 {
			continue
		}
		a, b := match.Slots[0], match.Slots[1]
		seen(a.Participant)
		seen(b.Participant)
		if !match.Finished || a.Participant == 0 || b.Participant == 0 {
			continue
		}
		winner, loser := a, b
		if a.Place > b.Place {
			winner, loser = b, a
		}
		lostAt := float64(rounds - round + 1)
		switch {
		case bronze:
			// Both fell in the semifinal (one round before the бой's own code);
			// the бой orders them within that band.
			band[winner.Participant] = lostAt + 1 - 0.1
			band[loser.Participant] = lostAt + 1 + 0.1
		case round == rounds:
			band[winner.Participant] = keyChampion
			band[loser.Participant] = lostAt
		default:
			if band[winner.Participant] == keyAlive {
				band[winner.Participant] = keyAlive
			}
			band[loser.Participant] = lostAt
		}
	}

	ranked := make([]RankedEntry, 0, len(appearance))
	for _, id := range appearance {
		ranked = append(ranked, RankedEntry{Participant: id, Metrics: map[string]float64{}})
	}
	for i := 1; i < len(ranked); i++ {
		for j := i; j > 0 && band[ranked[j].Participant] < band[ranked[j-1].Participant]; j-- {
			ranked[j], ranked[j-1] = ranked[j-1], ranked[j]
		}
	}
	for i := range ranked {
		if i > 0 && band[ranked[i].Participant] == band[ranked[i-1].Participant] {
			ranked[i].Rank = ranked[i-1].Rank
		} else {
			ranked[i].Rank = i + 1
		}
	}
	return ranked, nil
}
