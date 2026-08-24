package structure

import (
	"encoding/json"
	"errors"
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
func (roundRobin) Word() string { return "roundrobin" }
func (roundRobin) Keys() []Key {
	return []Key{{Name: "groups"}, {Name: "group_size"}, {Name: "match_size"}, {Name: "rounds"}, {Name: "points", Cascade: true}, {Name: "metric"}, {Name: "slug"}}
}

// canonOrder is the round-robin comparator chain when a scheme names none:
// КИНСБФ's очки, then личная встреча, взятые, разница.
var canonOrder = []string{"points", "h2h", "taken", "diff"}

// Expand is a Block of Groups, one rr stage each, seated from the incoming
// Edge and ranked by the block's comparators.
func (roundRobin) Expand(b Block) (Outputs, error) {
	size, ok := b.Int("group_size")
	if !ok {
		return Outputs{}, errors.New("roundrobin: нужен group_size")
	}
	groups, ok := b.Int("groups")
	if !ok {
		groups = 1
	}
	if groups < 1 || size < 2 {
		return Outputs{}, errors.New("roundrobin: groups ≥ 1, group_size ≥ 2")
	}
	if err := b.Rounds(nil); err != nil {
		return Outputs{}, err
	}
	lanes, err := b.Venues()
	if err != nil {
		return Outputs{}, err
	}
	entrants, err := b.Entrants(groups, size)
	if err != nil {
		return Outputs{}, err
	}
	matchSize := 2
	if v, ok := b.Int("match_size"); ok {
		matchSize = v
	}
	order, err := blockOrder(b)
	if err != nil {
		return Outputs{}, err
	}
	points, err := rrPoints(b)
	if err != nil {
		return Outputs{}, err
	}
	metric, _ := b.Str("metric")
	slug, _ := b.Str("slug")
	out := Outputs{}
	for g := 1; g <= groups; g++ {
		code := fmt.Sprintf("%s-g%d", b.Code(), g)
		cfg := RRConfig{
			Code:     code,
			Entrants: entrants[g-1],
			Order:    order,
			Points:   &RRPoints{Win: points[0], Draw: points[1], Loss: points[2]},
			Venue:    lanes.Pick(g),
			Metric:   metric,
			Rules:    b.Rules(),
		}
		if matchSize > 2 {
			cfg.MatchSize = matchSize
		}
		cfg.Rounds, _ = b.Int("rounds")
		if _, err := b.Emit(Stage{Code: code, Title: b.GroupTitle(g, groups), Kind: "rr", Slug: slug,
			At: At{Group: GroupCode(groups, g)}, Config: cfg}); err != nil {
			return Outputs{}, err
		}
		stageCode := code
		label := fmt.Sprintf("Гр. %d", g)
		out.Groups = append(out.Groups, Feed{
			Stage: stageCode,
			Label: label,
			Place: func(p int) store.SchemeSlot {
				return store.SchemeSlot{
					Reseed: &store.SchemeReseedRef{Stage: stageCode, Rank: p},
					Label:  fmt.Sprintf("%s-%d", label, p),
				}
			},
		})
	}
	return out, nil
}

// blockOrder resolves the groups' comparator order. On a block with an incoming
// reseed the sorting key describes the Edge, so the groups fall back to
// [defaults] or the canon.
func blockOrder(b Block) ([]string, error) {
	var rules []store.SortRule
	var ok bool
	var err error
	if incoming, _ := b.Reseed(); !incoming {
		if rules, ok, err = b.Sorting(); err != nil {
			return nil, err
		}
	}
	if !ok {
		if rules, ok, err = b.DefaultSorting(); err != nil {
			return nil, err
		}
	}
	if !ok {
		return canonOrder, nil
	}
	known := b.Rankable("rr")
	order := make([]string, len(rules))
	for i, rule := range rules {
		if !known[rule.Metric] {
			return nil, UnrankableMetric(rule.Metric, known)
		}
		order[i] = rule.Metric
	}
	return order, nil
}

func rrPoints(b Block) ([]float64, error) {
	points, ok, err := b.NumList("points")
	if err != nil {
		return nil, err
	}
	if !ok {
		return []float64{2, 1, 0}, nil
	}
	if len(points) != 3 {
		return nil, Keyf("points", "points: жду [победа, ничья, поражение]")
	}
	return points, nil
}

func (roundRobin) Metrics() []string {
	return []string{"points", "h2h", "taken", "conceded", "diff", "place_sum", "bouts"}
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

func (roundRobin) Schedule(cfg json.RawMessage) ([]store.SchemeMatch, error) {
	var conf RRConfig
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
		// A Group holds one стол for the whole block, so the бои of a круг are
		// played one after another. Their order in the schedule is the заход.
		for wave, table := range round {
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
				Wave:             wave + 1,
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
func rrRounds(n, size int, conf RRConfig) ([][][]int, error) {
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

// multiSeatStandings ranks a group whose бои seat more than two. There is no
// личная встреча — a бой of three is not a duel — and no разница, so очки come
// from the block's scoring rule and every Protocol metric simply sums.
func multiSeatStandings(conf RRConfig, results []MatchOutcome) ([]RankedEntry, error) {
	rules, err := compileRules(conf.Rules)
	if err != nil {
		return nil, err
	}
	if len(rules.bout) == 0 {
		// «4 − место» generalised: a бой of k seats pays (k + 1) − место, so a
		// win at three seats is 3, and a shared place pays the mean of the
		// places it shares without anyone spelling that out.
		if rules, err = compileRules(&Rules{Bout: map[string]string{"points": "seats + 1 - place"}}); err != nil {
			return nil, err
		}
	}
	order := rrOrder(conf)
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
	shareRanks(ranked, order)
	return ranked, nil
}

// shareRanks numbers a sorted table, giving seats level on every key one
// rank — 1, 2, 2, 4.
func shareRanks(ranked []RankedEntry, order []string) {
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
}

// rrOrder is the group's ranking keys: the scheme's, else the two-seat
// default with личная встреча or the multi-seat one without.
func rrOrder(conf RRConfig) []string {
	if conf.Order != nil {
		return conf.Order
	}
	if conf.MatchSize > 2 {
		return []string{"points", "total", "plus"}
	}
	return canonOrder
}

// Order lists the group's keys as columns; личная встреча is a comparator
// over the tied, not a number a row carries, so it is not one.
func (roundRobin) Order(cfg json.RawMessage) []SortRule {
	var conf RRConfig
	if err := json.Unmarshal(cfg, &conf); err != nil {
		return nil
	}
	rules := sortRules(rrOrder(conf))
	out := rules[:0]
	for _, rule := range rules {
		if rule.Metric != "h2h" {
			out = append(out, rule)
		}
	}
	return out
}

// Standings is the cross-table. Defaults are the КИНСБФ canon (§4.2): 2/1/0
// over "taken", ranked очки → личная встреча among the tied → taken → diff.
func (roundRobin) Standings(cfg json.RawMessage, results []MatchOutcome, _ Inputs) ([]RankedEntry, error) {
	var conf RRConfig
	if err := json.Unmarshal(cfg, &conf); err != nil {
		return nil, fmt.Errorf("rr standings config: %w", err)
	}
	if conf.MatchSize > 2 {
		return multiSeatStandings(conf, results)
	}
	return duelStandings(Duel{Points: conf.Points, Metric: conf.Metric, Order: rrOrder(conf), Rules: conf.Rules}, results)
}
