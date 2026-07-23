package structure

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"

	"dope/dope/storage/store"
)

func init() { Register(reseed{}) }

// reseed is the re-ranking kind: it schedules nothing and seats nobody itself —
// it aggregates its source matches' outcomes per participant (place_sum plus
// the sum of every protocol metric), orders them by the configured sort rules,
// and hands out distinct ranks for later stages to seat by. Mirrors the
// resolver's reseed computation (docs/unified-model.md §2), including the
// deterministic Жребий lots for true ties.
type reseed struct{}

func (reseed) Code() string { return "reseed" }

type reseedKindConfig struct {
	Seed string `json:"seed"`
	Sort []struct {
		Metric string `json:"metric"`
		Dir    string `json:"dir"`
	} `json:"sort"`
}

func (reseed) Schedule(cfg json.RawMessage, results []MatchOutcome) ([]store.SchemeMatch, error) {
	return nil, nil
}

func (reseed) Standings(cfg json.RawMessage, results []MatchOutcome) ([]RankedEntry, error) {
	var conf reseedKindConfig
	if err := json.Unmarshal(cfg, &conf); err != nil {
		return nil, fmt.Errorf("reseed config: %w", err)
	}

	byParticipant := map[int64]*RankedEntry{}
	var order []int64
	for _, match := range results {
		if !match.Finished {
			continue
		}
		for _, slot := range match.Slots {
			if slot.Participant == 0 {
				continue
			}
			entry, ok := byParticipant[slot.Participant]
			if !ok {
				entry = &RankedEntry{Participant: slot.Participant, Metrics: map[string]float64{}}
				byParticipant[slot.Participant] = entry
				order = append(order, slot.Participant)
			}
			entry.Metrics["place_sum"] += slot.Place
			for key, value := range slot.Metrics {
				entry.Metrics[key] += value
			}
		}
	}
	entries := make([]RankedEntry, 0, len(order))
	for _, id := range order {
		entries = append(entries, *byParticipant[id])
	}

	tiedButDraw := func(a, b RankedEntry) bool {
		for _, rule := range conf.Sort {
			if rule.Metric == "draw" {
				continue
			}
			if a.Metrics[rule.Metric] != b.Metrics[rule.Metric] {
				return false
			}
		}
		return true
	}
	sortEntries := func() {
		sort.SliceStable(entries, func(i, j int) bool {
			for _, rule := range conf.Sort {
				a, b := entries[i].Metrics[rule.Metric], entries[j].Metrics[rule.Metric]
				if a == b {
					continue
				}
				if rule.Dir == "desc" {
					return a > b
				}
				return a < b
			}
			return entries[i].Participant < entries[j].Participant
		})
	}
	// First pass groups true ties, lots separate them, second pass is final.
	sortEntries()
	for i := 0; i < len(entries); {
		j := i + 1
		for j < len(entries) && tiedButDraw(entries[i], entries[j]) {
			j++
		}
		if j-i >= 2 {
			for k := i; k < j; k++ {
				entries[k].Metrics["draw"] = float64(deterministicLot(conf.Seed, entries[k].Participant))
			}
		}
		i = j
	}
	sortEntries()
	for i := range entries {
		entries[i].Rank = i + 1
	}
	return entries, nil
}

// deterministicLot derives a stable Жребий lot in [1, 1_000_000] from the
// game's fixed random seed, so a reseed recomputes identically every time.
// Same function as the resolver's — kept bit-identical for the parity gate.
func deterministicLot(seed string, participant int64) int64 {
	h := fnv.New64a()
	fmt.Fprintf(h, "%s:%d", seed, participant)
	return int64(h.Sum64()%1_000_000) + 1
}
