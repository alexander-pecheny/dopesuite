package structure

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"
)

func init() { Register(reseed{}) }

// reseed is the re-ranking Kind: it schedules nothing and seats nobody itself.
// It aggregates its source бои per contender — place_sum plus the sum of every
// Protocol metric, and регламент КИнСБФ 3.3.5's shares and разница — orders
// them by the configured sort rules, and hands out distinct ranks for later
// stages to seat by. Bands go first: a Participant just dropped from the сетка
// above outranks one that survived the сетка below, however the two played.
// True ties are split by deterministic Жребий lots from the game's seed, so a
// reseed recomputes identically every time.
type reseed struct{}

func (reseed) Code() string { return "reseed" }

// reseedContender is who the reseed ranks: the resolver names them, since who
// advances is a matter of resolved slots, and their band — Losses so far.
type reseedContender struct {
	Participant int64 `json:"participant"`
	Band        int   `json:"band"`
}

type reseedKindConfig struct {
	Seed       string            `json:"seed"`
	Sort       []SortRule        `json:"sort"`
	Contenders []reseedContender `json:"contenders"`
}

func reseedOrder(conf reseedKindConfig) []SortRule {
	if len(conf.Sort) == 0 {
		return []SortRule{{Metric: "place_sum", Dir: "asc"}}
	}
	return conf.Sort
}

func (reseed) Metrics() []string {
	return []string{"place_sum", "points_share", "taken_share", "taken_base", "diff", "draw"}
}

func (reseed) Order(cfg json.RawMessage) []SortRule {
	var conf reseedKindConfig
	if err := json.Unmarshal(cfg, &conf); err != nil {
		return nil
	}
	return reseedOrder(conf)
}

func (reseed) Standings(cfg json.RawMessage, results []MatchOutcome) ([]RankedEntry, error) {
	var conf reseedKindConfig
	if err := json.Unmarshal(cfg, &conf); err != nil {
		return nil, fmt.Errorf("reseed config: %w", err)
	}
	rules := reseedOrder(conf)

	// Without contenders everyone in the sources is ranked, in band 0.
	band := map[int64]int{}
	var order []int64
	for _, c := range conf.Contenders {
		if _, seen := band[c.Participant]; !seen {
			order = append(order, c.Participant)
		}
		band[c.Participant] = c.Band
	}
	restricted := len(order) > 0

	byParticipant := map[int64]*RankedEntry{}
	scratch := map[int64]map[string]float64{}
	entryFor := func(id int64) *RankedEntry {
		if e, ok := byParticipant[id]; ok {
			return e
		}
		e := &RankedEntry{Participant: id, Metrics: map[string]float64{}}
		byParticipant[id] = e
		scratch[id] = map[string]float64{}
		if !restricted {
			order = append(order, id)
		}
		return e
	}
	for _, match := range results {
		if !match.Finished {
			continue
		}
		takenSum := 0.0
		for _, slot := range match.Slots {
			takenSum += slot.Metrics[MetricTakenBase]
		}
		for _, slot := range match.Slots {
			if slot.Participant == 0 {
				continue
			}
			if _, ok := band[slot.Participant]; restricted && !ok {
				continue
			}
			entry := entryFor(slot.Participant)
			entry.Bouts = append(entry.Bouts, match.Code)
			entry.Metrics["place_sum"] += slot.Place
			for key, value := range slot.Metrics {
				entry.Metrics[key] += value
			}
			entry.Metrics["taken_base"] += slot.Metrics[MetricTakenBase]
			s := scratch[slot.Participant]
			s["points"] += BoutPoints(slot.Place)
			s["conceded_base"] += takenSum - slot.Metrics[MetricTakenBase]
			s["bouts"]++
			s["questions_asked"] += float64(match.Questions)
		}
	}
	entries := make([]RankedEntry, 0, len(order))
	for _, id := range order {
		entry := entryFor(id)
		s := scratch[id]
		if s["bouts"] > 0 {
			entry.Metrics["points_share"] = s["points"] / (2 * s["bouts"])
		}
		if s["questions_asked"] > 0 {
			entry.Metrics["taken_share"] = entry.Metrics["taken_base"] / s["questions_asked"]
		}
		entry.Metrics["diff"] = entry.Metrics["taken_base"] - s["conceded_base"]
		entry.Metrics["draw"] = 0
		entries = append(entries, *entry)
	}

	less := func(a, b RankedEntry) bool {
		if band[a.Participant] != band[b.Participant] {
			return band[a.Participant] < band[b.Participant]
		}
		for _, rule := range rules {
			x, y := a.Metrics[rule.Metric], b.Metrics[rule.Metric]
			if x == y {
				continue
			}
			if rule.Dir == "desc" {
				return x > y
			}
			return x < y
		}
		return a.Participant < b.Participant
	}
	tiedButDraw := func(a, b RankedEntry) bool {
		if band[a.Participant] != band[b.Participant] {
			return false
		}
		for _, rule := range rules {
			if rule.Metric != "draw" && a.Metrics[rule.Metric] != b.Metrics[rule.Metric] {
				return false
			}
		}
		return true
	}
	// First pass groups true ties, lots separate them, second pass is final.
	sort.SliceStable(entries, func(i, j int) bool { return less(entries[i], entries[j]) })
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
	sort.SliceStable(entries, func(i, j int) bool { return less(entries[i], entries[j]) })
	for i := range entries {
		entries[i].Rank = i + 1
	}
	return entries, nil
}

// deterministicLot derives a stable Жребий lot in [1, 1_000_000] from the
// game's fixed random seed and the participant, so the lottery order survives
// every recompute. A collision inside a tie is harmless: participant id breaks
// the residual tie.
func deterministicLot(seed string, participant int64) int64 {
	h := fnv.New64a()
	fmt.Fprintf(h, "%s:%d", seed, participant)
	return int64(h.Sum64()%1_000_000) + 1
}
