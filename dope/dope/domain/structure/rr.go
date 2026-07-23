package structure

import (
	"encoding/json"
	"fmt"

	"dope/dope/storage/store"
)

func init() { Register(roundRobin{}) }

// roundRobin is the round-robin group kind: every entrant meets every other
// once (or exactly the pairings the config dictates), standings by a
// configurable head-to-head points rule over the protocol's score metric.
type roundRobin struct{}

func (roundRobin) Code() string { return "rr" }

// rrConfig is the rr stage config. Entrants are ordinary scheme slot sources
// (seed / fromMatch / stageRank / reseed refs), so the same group works over a
// fest seed draw or over qualifiers from a previous stage. Pairings, when set,
// override the built-in schedule: rounds of 1-based entrant-position pairs —
// partial round-robins are schedule data, not a new kind.
type rrConfig struct {
	Code     string             `json:"code"`
	Label    string             `json:"label"`
	Title    string             `json:"title"`
	Venue    int                `json:"venue"`
	Entrants []store.SchemeSlot `json:"entrants"`
	Pairings [][][2]int         `json:"pairings"`
}

// rrCanonRounds are the community's canonical KINSBF group schedules, retained
// verbatim so existing sheets and dope groups agree bout-for-bout.
var rrCanonRounds = map[int][][][2]int{
	2: {{{1, 2}}},
	3: {{{1, 2}}, {{1, 3}}, {{2, 3}}},
	4: {{{1, 2}, {3, 4}}, {{1, 4}, {2, 3}}, {{1, 3}, {2, 4}}},
}

func (roundRobin) Schedule(cfg json.RawMessage, results []MatchOutcome) ([]store.SchemeMatch, error) {
	var conf rrConfig
	if err := json.Unmarshal(cfg, &conf); err != nil {
		return nil, fmt.Errorf("rr config: %w", err)
	}
	n := len(conf.Entrants)
	if n < 2 {
		return nil, fmt.Errorf("rr: %d entrants, need at least 2", n)
	}
	rounds := conf.Pairings
	if rounds == nil {
		rounds = rrCanonRounds[n]
	}
	if rounds == nil {
		rounds = circleRounds(n)
	}
	title := conf.Title
	if title == "" {
		title = "Бой %d"
	}
	var matches []store.SchemeMatch
	seq := 0
	for _, round := range rounds {
		for _, pair := range round {
			seq++
			slots := make([]store.SchemeSlot, 0, 2)
			for _, position := range pair {
				if position < 1 || position > n {
					return nil, fmt.Errorf("rr: pairing position %d out of 1..%d", position, n)
				}
				slot := conf.Entrants[position-1]
				if slot.Label == "" && conf.Label != "" {
					slot.Label = fmt.Sprintf("%s%d", conf.Label, position)
				}
				slots = append(slots, slot)
			}
			matches = append(matches, store.SchemeMatch{
				Code:             fmt.Sprintf("%s-%d", conf.Code, seq),
				Title:            fmt.Sprintf(title, seq),
				Venue:            conf.Venue,
				ParticipantCount: 2,
				Slots:            slots,
			})
		}
	}
	return matches, nil
}

// circleRounds is the classic circle method: entrant 1 stays fixed, the rest
// rotate right one step per round, pairs form outside-in; odd counts get a
// silent bye. Pairs are emitted low position first.
func circleRounds(n int) [][][2]int {
	size := n
	if size%2 == 1 {
		size++ // position size == the bye
	}
	rot := make([]int, size-1)
	for i := range rot {
		rot[i] = i + 2
	}
	var rounds [][][2]int
	for r := 0; r < size-1; r++ {
		arr := append([]int{1}, rot...)
		var pairs [][2]int
		for i := 0; i < size/2; i++ {
			a, b := arr[i], arr[size-1-i]
			if a > n || b > n {
				continue
			}
			if a > b {
				a, b = b, a
			}
			pairs = append(pairs, [2]int{a, b})
		}
		rounds = append(rounds, pairs)
		copy(rot, append([]int{rot[len(rot)-1]}, rot[:len(rot)-1]...))
	}
	return rounds
}

// rrStandingsConfig tunes the cross-table: the head-to-head points rule, the
// protocol metric acting as the score, and the ranking key order. Defaults are
// the brain-ring canon: 2/1/0 over "taken", ranked О → ± → +.
type rrStandingsConfig struct {
	Points *struct {
		Win  float64 `json:"win"`
		Draw float64 `json:"draw"`
		Loss float64 `json:"loss"`
	} `json:"points"`
	Metric string   `json:"metric"`
	Order  []string `json:"order"`
}

func (roundRobin) Standings(cfg json.RawMessage, results []MatchOutcome) ([]RankedEntry, error) {
	var conf rrStandingsConfig
	if err := json.Unmarshal(cfg, &conf); err != nil {
		return nil, fmt.Errorf("rr standings config: %w", err)
	}
	win, draw, loss := 2.0, 1.0, 0.0
	if conf.Points != nil {
		win, draw, loss = conf.Points.Win, conf.Points.Draw, conf.Points.Loss
	}
	metric := conf.Metric
	if metric == "" {
		metric = "taken"
	}
	order := conf.Order
	if order == nil {
		order = []string{"points", "diff", "taken"}
	}

	byParticipant := map[int64]*RankedEntry{}
	var appearance []int64
	entry := func(id int64) *RankedEntry {
		if e, ok := byParticipant[id]; ok {
			return e
		}
		e := &RankedEntry{Participant: id, Metrics: map[string]float64{}}
		byParticipant[id] = e
		appearance = append(appearance, id)
		return e
	}
	for _, match := range results {
		if len(match.Slots) != 2 {
			continue
		}
		a, b := match.Slots[0], match.Slots[1]
		if a.Participant == 0 || b.Participant == 0 {
			continue
		}
		ea, eb := entry(a.Participant), entry(b.Participant)
		scoreA, scoreB := a.Metrics[metric], b.Metrics[metric]
		ea.Metrics["taken"] += scoreA
		ea.Metrics["conceded"] += scoreB
		eb.Metrics["taken"] += scoreB
		eb.Metrics["conceded"] += scoreA
		if match.Finished {
			switch {
			case a.Place < b.Place:
				ea.Metrics["points"] += win
				eb.Metrics["points"] += loss
			case a.Place > b.Place:
				ea.Metrics["points"] += loss
				eb.Metrics["points"] += win
			default:
				ea.Metrics["points"] += draw
				eb.Metrics["points"] += draw
			}
		}
	}
	ranked := make([]RankedEntry, 0, len(appearance))
	for _, id := range appearance {
		e := byParticipant[id]
		e.Metrics["diff"] = e.Metrics["taken"] - e.Metrics["conceded"]
		ranked = append(ranked, *e)
	}
	keyLess := func(x, y RankedEntry) int {
		for _, key := range order {
			if x.Metrics[key] != y.Metrics[key] {
				if x.Metrics[key] > y.Metrics[key] {
					return -1
				}
				return 1
			}
		}
		return 0
	}
	// Insertion sort: stable, so full ties keep first-appearance order.
	for i := 1; i < len(ranked); i++ {
		for j := i; j > 0 && keyLess(ranked[j], ranked[j-1]) < 0; j-- {
			ranked[j], ranked[j-1] = ranked[j-1], ranked[j]
		}
	}
	for i := range ranked {
		if i > 0 && keyLess(ranked[i], ranked[i-1]) == 0 {
			ranked[i].Rank = ranked[i-1].Rank
		} else {
			ranked[i].Rank = i + 1
		}
	}
	return ranked, nil
}
