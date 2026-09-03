package structure

import "sort"

// The two-seat table: the one place duels are ranked, whether they are a
// round-robin Group's Matches or a Round played as a series. Every ranking
// scope ranks the same way — points from each Match's outcome, the Protocol's
// metrics summed, the scheme's scoring rules over those sums, then the block's
// comparators — so best-of is not a rule of its own but the default points
// sorted on points.

// Duel is what a two-seat table needs: the win/draw/loss values, the Protocol
// metric taken counts, the comparator order and the scheme's scoring rules.
type Duel struct {
	Points *RRPoints `json:"points,omitempty"`
	Metric string    `json:"metric,omitempty"`
	Order  []string  `json:"order,omitempty"`
	Rules  *Rules    `json:"rules,omitempty"`
}

// duelStandings ranks everyone who met one-on-one in results.
func duelStandings(conf Duel, results []MatchOutcome) ([]RankedEntry, error) {
	rules, err := compileRules(conf.Rules)
	if err != nil {
		return nil, err
	}
	order := conf.Order
	metric := conf.Metric
	if metric == "" {
		metric = "taken"
	}
	win, draw, loss := 2.0, 1.0, 0.0
	if conf.Points != nil {
		win, draw, loss = conf.Points.Win, conf.Points.Draw, conf.Points.Loss
	}

	byParticipant := map[int64]*RankedEntry{}
	var appearance []int64
	entry := func(id int64) *RankedEntry {
		if e, ok := byParticipant[id]; ok {
			return e
		}
		// Every metric this table keeps starts at zero rather than absent: a
		// scoring rule reads them by name, and expr refuses a name it does not
		// know, so a Group whose Matches have not been played yet would
		// otherwise fail to rank at all instead of ranking everyone level.
		e := &RankedEntry{Participant: id, Metrics: map[string]float64{
			"points": 0, "taken": 0, "conceded": 0, "diff": 0, "place_sum": 0, "bouts": 0,
		}}
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
		if !match.Finished {
			continue
		}
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
		for seat, e := range []*RankedEntry{ea, eb} {
			e.Metrics["place_sum"] += match.Slots[seat].Place
			e.Metrics["bouts"]++
			if err := rules.applyBout(match, seat, e.Metrics); err != nil {
				return nil, err
			}
		}
	}
	ranked := make([]RankedEntry, 0, len(appearance))
	for _, id := range appearance {
		e := byParticipant[id]
		e.Metrics["diff"] = e.Metrics["taken"] - e.Metrics["conceded"]
		if err := rules.applyStandings(e.Metrics); err != nil {
			return nil, err
		}
		ranked = append(ranked, *e)
	}

	// Head-to-head needs each finished Match's point split, not just totals.
	type duel struct {
		a, b   int64
		pa, pb float64
	}
	var duels []duel
	for _, match := range results {
		if !match.Finished || len(match.Slots) != 2 {
			continue
		}
		a, b := match.Slots[0], match.Slots[1]
		if a.Participant == 0 || b.Participant == 0 {
			continue
		}
		switch {
		case a.Place < b.Place:
			duels = append(duels, duel{a.Participant, b.Participant, win, loss})
		case a.Place > b.Place:
			duels = append(duels, duel{a.Participant, b.Participant, loss, win})
		default:
			duels = append(duels, duel{a.Participant, b.Participant, draw, draw})
		}
	}

	// Each key partitions the still-tied group; "h2h" is relative to that
	// group — the points the tied teams took in their Matches against each other.
	// Keys are consumed in order and never re-applied to later sub-ties, so a
	// still-tied pair inside a mini-table falls through to the next key.
	seats := make([]int, len(ranked))
	for i := range seats {
		seats[i] = i
	}
	tieGroup := make([]int, len(ranked))
	groupSeq := 0
	var arrange func(ids []int, keyIdx int)
	arrange = func(ids []int, keyIdx int) {
		if len(ids) < 2 || keyIdx >= len(order) {
			groupSeq++
			for _, i := range ids {
				tieGroup[i] = groupSeq
			}
			return
		}
		val := func(i int) float64 { return ranked[i].Metrics[order[keyIdx]] }
		if order[keyIdx] == "h2h" {
			tied := make(map[int64]bool, len(ids))
			for _, i := range ids {
				tied[ranked[i].Participant] = true
			}
			sub := map[int64]float64{}
			for _, d := range duels {
				if tied[d.a] && tied[d.b] {
					sub[d.a] += d.pa
					sub[d.b] += d.pb
				}
			}
			val = func(i int) float64 { return sub[ranked[i].Participant] }
		}
		ascending := Ascending(order[keyIdx])
		sort.SliceStable(ids, func(x, y int) bool {
			if ascending {
				return val(ids[x]) < val(ids[y])
			}
			return val(ids[x]) > val(ids[y])
		})
		start := 0
		for end := 1; end <= len(ids); end++ {
			if end == len(ids) || val(ids[end]) != val(ids[start]) {
				arrange(ids[start:end], keyIdx+1)
				start = end
			}
		}
	}
	arrange(seats, 0)

	out := make([]RankedEntry, len(ranked))
	for pos, i := range seats {
		out[pos] = ranked[i]
		if pos > 0 && tieGroup[i] == tieGroup[seats[pos-1]] {
			out[pos].Rank = out[pos-1].Rank
		} else {
			out[pos].Rank = pos + 1
		}
	}
	return out, nil
}
