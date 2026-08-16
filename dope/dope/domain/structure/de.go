package structure

import (
	"encoding/json"
	"fmt"
	"sort"
)

func init() { Register(pod{}) }

// pod is a Group of a double elimination — a table of four playing until two
// have lost twice — as a Ranker only: the DSL expands its бои, since a pod is
// hand-drawn into rounds. It ranks the never-eliminated first (fewest Losses),
// then the eliminated by how late their second Loss came; a tie the бои did
// not settle shares its place, and survivors of an unfinished pod stay
// unplaced — their места are still being played. Only an outright lost бой
// counts: a shared place is a tie, not a Loss.
type pod struct{}

func (pod) Code() string { return "de" }

type podConfig struct {
	Lives         int `json:"lives"`
	WinningPlaces int `json:"winning_places"`
}

// A pod's table shows М alone; Losses are how it ranks, not a column.
func (pod) Order(cfg json.RawMessage) []SortRule { return nil }

func (pod) Standings(cfg json.RawMessage, results []MatchOutcome) ([]RankedEntry, error) {
	var conf podConfig
	if err := json.Unmarshal(cfg, &conf); err != nil {
		return nil, fmt.Errorf("de config: %w", err)
	}
	if conf.Lives < 1 {
		conf.Lives = 2
	}
	if conf.WinningPlaces < 1 {
		conf.WinningPlaces = 1
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
			lost := slot.Place > float64(conf.WinningPlaces) && slot.Place == float64(int(slot.Place))
			if !lost {
				continue
			}
			losses[slot.Participant]++
			if losses[slot.Participant] == conf.Lives {
				if _, out := eliminated[slot.Participant]; !out {
					eliminated[slot.Participant] = match.Round
				}
			}
		}
	}
	// Rank key: alive before eliminated, fewer Losses first, later elimination
	// first. Equal keys are ties the pod never split — they share a place.
	key := func(id int64) int {
		if round, out := eliminated[id]; out {
			return 1000 - round
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
	return out, nil
}
