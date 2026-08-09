package structure

import (
	"encoding/json"
	"fmt"
	"sort"

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
// override the built-in schedule: rounds of 1-based entrant-position tuples —
// partial round-robins are schedule data, not a new kind. MatchSize seats more
// than two per бой: личная СИ plays its group of nine three at a table.
type rrConfig struct {
	Code      string             `json:"code"`
	Label     string             `json:"label"`
	Title     string             `json:"title"`
	Venue     int                `json:"venue"`
	Entrants  []store.SchemeSlot `json:"entrants"`
	Pairings  [][][]int          `json:"pairings"`
	MatchSize int                `json:"matchSize"`
	Rounds    int                `json:"rounds"`
}

// rrCanonRounds are the community's canonical KINSBF group schedules, retained
// verbatim so existing sheets and dope groups agree bout-for-bout.
var rrCanonRounds = map[int][][][]int{
	2: {{{1, 2}}},
	3: {{{1, 2}}, {{1, 3}}, {{2, 3}}},
	4: {{{1, 2}, {3, 4}}, {{1, 4}, {2, 3}}, {{1, 3}, {2, 4}}},
}

// rrCanonTables holds the schedules for groups whose бой seats more than two,
// keyed by (entrants, seats). The 9×3 order is the СтудЧР СИ sheets'
// (generate_si.py GROUP_STAGE), so a dope group and the reference sheet name
// the same бои; it is the affine plane AG(2,3) with its parallel classes in
// that document's order.
var rrCanonTables = map[[2]int][][][]int{
	{9, 3}: {
		{{1, 2, 3}, {4, 5, 6}, {7, 8, 9}},
		{{1, 4, 7}, {2, 5, 8}, {3, 6, 9}},
		{{3, 5, 7}, {1, 6, 8}, {2, 4, 9}},
		{{1, 5, 9}, {2, 6, 7}, {3, 4, 8}},
	},
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
	size := conf.MatchSize
	if size <= 0 {
		size = 2
	}
	rounds, err := rrRounds(n, size, conf)
	if err != nil {
		return nil, err
	}
	if conf.Rounds > 0 {
		if conf.Rounds > len(rounds) {
			return nil, fmt.Errorf("rr: %d кругов на %d участников по %d — есть только %d", conf.Rounds, n, size, len(rounds))
		}
		rounds = rounds[:conf.Rounds]
	}
	title := conf.Title
	if title == "" {
		title = "Бой %d"
	}
	var matches []store.SchemeMatch
	seq := 0
	for circle, round := range rounds {
		for _, table := range round {
			seq++
			slots := make([]store.SchemeSlot, 0, len(table))
			for _, position := range table {
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
				Round:            circle + 1,
				ParticipantCount: len(slots),
				Slots:            slots,
			})
		}
	}
	return matches, nil
}

// rrRounds picks the group's schedule: the config's explicit pairings, the
// canon for its shape, else a construction — the circle method for бои of two,
// the affine plane for bigger tables.
func rrRounds(n, size int, conf rrConfig) ([][][]int, error) {
	if conf.Pairings != nil {
		return conf.Pairings, nil
	}
	if rounds, ok := rrCanonTables[[2]int{n, size}]; ok {
		return rounds, nil
	}
	if size == 2 {
		if rounds, ok := rrCanonRounds[n]; ok {
			return rounds, nil
		}
		return circleRounds(n), nil
	}
	rounds, ok := affineRounds(n, size)
	if !ok {
		return nil, fmt.Errorf("rr: нет расписания на %d участников по %d за столом", n, size)
	}
	return rounds, nil
}

// affineRounds is the resolvable schedule of the affine plane AG(2,m): m²
// seats split into m tables of m, m+1 times over, and no two Participants ever
// share a table twice. It exists exactly when m is prime — the sizes a real
// group stage uses (3, 5, 7) — so any other m reports no schedule rather than a
// broken one.
func affineRounds(n, m int) ([][][]int, bool) {
	if m < 2 || n != m*m || !isPrime(m) {
		return nil, false
	}
	point := func(x, y int) int { return x*m + y + 1 }
	rounds := make([][][]int, 0, m+1)
	vertical := make([][]int, m)
	for x := 0; x < m; x++ {
		for y := 0; y < m; y++ {
			vertical[x] = append(vertical[x], point(x, y))
		}
	}
	rounds = append(rounds, vertical)
	for slope := 0; slope < m; slope++ {
		lines := make([][]int, m)
		for c := 0; c < m; c++ {
			for x := 0; x < m; x++ {
				lines[c] = append(lines[c], point(x, (slope*x+c)%m))
			}
		}
		rounds = append(rounds, lines)
	}
	return rounds, true
}

func isPrime(n int) bool {
	if n < 2 {
		return false
	}
	for d := 2; d*d <= n; d++ {
		if n%d == 0 {
			return false
		}
	}
	return true
}

// circleRounds is the classic circle method: entrant 1 stays fixed, the rest
// rotate right one step per round, pairs form outside-in; odd counts get a
// silent bye. Pairs are emitted low position first.
func circleRounds(n int) [][][]int {
	size := n
	if size%2 == 1 {
		size++ // position size == the bye
	}
	rot := make([]int, size-1)
	for i := range rot {
		rot[i] = i + 2
	}
	var rounds [][][]int
	for r := 0; r < size-1; r++ {
		arr := append([]int{1}, rot...)
		var pairs [][]int
		for i := 0; i < size/2; i++ {
			a, b := arr[i], arr[size-1-i]
			if a > n || b > n {
				continue
			}
			if a > b {
				a, b = b, a
			}
			pairs = append(pairs, []int{a, b})
		}
		rounds = append(rounds, pairs)
		copy(rot, append([]int{rot[len(rot)-1]}, rot[:len(rot)-1]...))
	}
	return rounds
}

// rrStandingsConfig tunes the cross-table: the head-to-head points rule, the
// protocol metric acting as the score, and the ranking key order. Defaults are
// the КИНСБФ canon (§4.2): 2/1/0 over "taken", ranked очки → личная встреча
// among the tied ("h2h") → taken → diff.
type rrStandingsConfig struct {
	Points *struct {
		Win  float64 `json:"win"`
		Draw float64 `json:"draw"`
		Loss float64 `json:"loss"`
	} `json:"points"`
	Metric    string   `json:"metric"`
	Order     []string `json:"order"`
	MatchSize int      `json:"matchSize"`
	Rules     Rules    `json:"rules"`
}

// multiSeatStandings ranks a group whose бои seat more than two. There is no
// личная встреча — a бой of three is not a duel — and no разница, so очки come
// from the block's scoring rule and every Protocol metric simply sums.
func multiSeatStandings(conf rrStandingsConfig, results []MatchOutcome) ([]RankedEntry, error) {
	rules, err := compileRules(conf.Rules)
	if err != nil {
		return nil, err
	}
	if len(rules.bout) == 0 {
		// «4 − место» generalised: a бой of k seats pays (k + 1) − место, so a
		// win at three seats is 3, and a shared place pays the mean of the
		// places it shares without anyone spelling that out.
		if rules, err = compileRules(Rules{Bout: map[string]string{"points": "seats + 1 - place"}}); err != nil {
			return nil, err
		}
	}
	order := conf.Order
	if order == nil {
		order = []string{"points", "total", "plus"}
	}
	byParticipant := map[int64]*RankedEntry{}
	var appearance []int64
	for _, match := range results {
		for seat, slot := range match.Slots {
			if slot.Participant == 0 {
				continue
			}
			entry, ok := byParticipant[slot.Participant]
			if !ok {
				entry = &RankedEntry{Participant: slot.Participant, Metrics: map[string]float64{}}
				byParticipant[slot.Participant] = entry
				appearance = append(appearance, slot.Participant)
			}
			for key, value := range slot.Metrics {
				entry.Metrics[key] += value
			}
			if !match.Finished {
				continue
			}
			entry.Metrics["place_sum"] += slot.Place
			entry.Metrics["bouts"]++
			if err := rules.applyBout(match, seat, entry.Metrics); err != nil {
				return nil, err
			}
		}
	}
	ranked := make([]RankedEntry, 0, len(appearance))
	for _, id := range appearance {
		entry := byParticipant[id]
		if err := rules.applyStandings(entry.Metrics); err != nil {
			return nil, err
		}
		ranked = append(ranked, *entry)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		for _, key := range order {
			a, b := ranked[i].Metrics[key], ranked[j].Metrics[key]
			if a == b {
				continue
			}
			if key == "place_sum" {
				return a < b
			}
			return a > b
		}
		return ranked[i].Participant < ranked[j].Participant
	})
	same := func(a, b RankedEntry) bool {
		for _, key := range order {
			if a.Metrics[key] != b.Metrics[key] {
				return false
			}
		}
		return true
	}
	for i := range ranked {
		if i > 0 && same(ranked[i], ranked[i-1]) {
			ranked[i].Rank = ranked[i-1].Rank
		} else {
			ranked[i].Rank = i + 1
		}
	}
	return ranked, nil
}

func (roundRobin) Standings(cfg json.RawMessage, results []MatchOutcome) ([]RankedEntry, error) {
	var conf rrStandingsConfig
	if err := json.Unmarshal(cfg, &conf); err != nil {
		return nil, fmt.Errorf("rr standings config: %w", err)
	}
	if conf.MatchSize > 2 || len(conf.Rules.Bout)+len(conf.Rules.Standings) > 0 {
		return multiSeatStandings(conf, results)
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
		order = []string{"points", "h2h", "taken", "diff"}
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

	// Личная встреча needs each finished бой's point split, not just totals.
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
	// group — the points the tied teams took in their бои against each other.
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
		sort.SliceStable(ids, func(x, y int) bool { return val(ids[x]) > val(ids[y]) })
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
