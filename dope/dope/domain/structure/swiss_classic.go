package structure

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"

	dopestrings "dope/i18nstrings"

	"dope/dope/storage/store"
)

func init() {
	Register(swissClassic{})
	Register(swissPairing{})
}

// swissClassic is the Swiss system as chess and the Вопросики быстрые play it
// (regulations 3.1): a fixed number of rounds, every team in every round, each
// round pairing teams level on points or as close as they can be, and never
// the same two twice. Unlike the Major's `swiss`, nobody leaves early and no
// pairing is known before the round before it ends, so a round after the first
// is seated from a pairing stage the host calculates once that round is
// played: it ranks everyone so that ranks 2k−1 and 2k sit down at бой k.
// Calculating publishes the round, and a later correction of a result changes
// the table but not those pairs (3.1.10), unless the host calculates again.
//
// With an odd number of teams one sits out each round against the empty
// table: it plays the questions alone, and its бой counts like any other
// (3.1.4).
type swissClassic struct{}

func (swissClassic) Code() string { return "swiss_classic" }
func (swissClassic) Word() string { return "swiss_classic" }
func (swissClassic) Keys() []Key {
	return []Key{{Name: "participants"}, {Name: "rounds"}, {Name: "points", Cascade: true}, {Name: "zero_draw_points"}}
}

// SwissClassicConfig is what both the table and a round's pairing read: what
// a бой pays and how the table ranks.
type SwissClassicConfig struct {
	Code   string   `json:"code,omitempty"`
	Points RRPoints `json:"points"`
	// ZeroDraw is what a draw pays when neither side took a question: «за
	// ненулевую ничью — одно очко, за нулевую ничью — ноль».
	ZeroDraw float64    `json:"zeroDraw"`
	Order    []SortRule `json:"order"`
}

// swissClassicOrder is the table's order when the scheme names none
// (3.1.8): points, questions taken, Buchholz, the fewest answers conceded,
// then the seed (3.1.9).
var swissClassicOrder = []SortRule{
	{Metric: "points", Dir: "desc"},
	{Metric: "taken", Dir: "desc"},
	{Metric: "buchholz", Dir: "desc"},
	{Metric: "conceded", Dir: "asc"},
	{Metric: "seed", Dir: "asc"},
}

func (swissClassic) Expand(b Block) (Outputs, error) {
	s := dopestrings.Default
	participants, ok := b.Int("participants")
	if !ok {
		participants = b.Seeded()
	}
	if participants < 2 {
		return Outputs{}, Keyf("participants", "%s", s.Structure.SwissClassic.ParticipantsFew())
	}
	rounds, ok := b.Int("rounds")
	if !ok || rounds < 1 {
		return Outputs{}, Keyf("rounds", "%s", s.Structure.SwissClassic.RoundsRequired())
	}
	if rounds >= participants+participants%2 {
		return Outputs{}, Keyf("rounds", "%s", s.Structure.SwissClassic.RoundsTooMany(strconv.Itoa(rounds), strconv.Itoa(participants)))
	}
	conf, err := swissClassicConfig(b)
	if err != nil {
		return Outputs{}, err
	}
	names := make([]string, rounds)
	for r := range names {
		names[r] = fmt.Sprintf("r%d", r+1)
	}
	if err := b.BlockRounds(names); err != nil {
		return Outputs{}, err
	}

	var entry []store.SchemeSlot
	seedFrom := ""
	if b.First() {
		if entry, err = b.Seeds(participants); err != nil {
			return Outputs{}, err
		}
	} else {
		prev, _ := b.Prev()
		if len(prev.Groups) != 1 {
			return Outputs{}, errors.New(s.Structure.SwissClassic.NeedsRanking())
		}
		feed := prev.Groups[0]
		seedFrom = feed.Stage
		for rank := 1; rank <= participants; rank++ {
			entry = append(entry, feed.Place(rank))
		}
	}

	blockCode := b.Code()
	conf.Code = blockCode + "-table"
	pairs := participants / 2
	bye := store.SchemeSlot{Placeholder: "bye", Label: s.Structure.SwissClassic.Bye()}
	var sources []string
	for r, name := range names {
		lanes, err := b.Venues(name)
		if err != nil {
			return Outputs{}, err
		}
		stageCode := fmt.Sprintf("%s-%s", blockCode, name)
		// The first round seats the top half against the bottom half, first
		// with first (3.1.6); every later round sits down as its pairing ranks.
		seat := func(pair, side int) store.SchemeSlot {
			if r == 0 {
				return entry[pair-1+side*pairs]
			}
			return store.SchemeSlot{}
		}
		if r > 0 {
			pairing := fmt.Sprintf("%s-pairs", stageCode)
			title := s.Structure.SwissClassic.Pairing(strconv.Itoa(r + 1))
			if _, err := b.EmitPairing(pairing, title, At{BlockRound: r + 1}, entry, append([]string(nil), sources...), seedFrom, conf); err != nil {
				return Outputs{}, err
			}
			seat = func(pair, side int) store.SchemeSlot {
				slot := ReseedRank(pairing, 2*pair-1+side)
				slot.Label = s.Structure.SwissClassic.PairSeat(strconv.Itoa(r+1), strconv.Itoa(pair))
				return slot
			}
		}
		var matches []store.SchemeMatch
		for pair := 1; pair <= pairs; pair++ {
			matches = append(matches, store.SchemeMatch{
				Code:             fmt.Sprintf("%s-m%d", stageCode, pair),
				Venue:            lanes.Pick(pair),
				ParticipantCount: 2,
				Slots:            []store.SchemeSlot{seat(pair, 0), seat(pair, 1)},
			})
		}
		if participants%2 == 1 {
			last := entry[participants-1]
			if r > 0 {
				last = ReseedRank(fmt.Sprintf("%s-pairs", stageCode), participants)
				last.Label = s.Structure.SwissClassic.PairSeat(strconv.Itoa(r+1), strconv.Itoa(pairs+1))
			}
			matches = append(matches, store.SchemeMatch{
				Code:             fmt.Sprintf("%s-m%d", stageCode, pairs+1),
				Venue:            lanes.Pick(pairs + 1),
				ParticipantCount: 2,
				Slots:            []store.SchemeSlot{last, bye},
			})
		}
		emitted, err := b.Emit(Stage{
			Code:        stageCode,
			Title:       b.BlockRoundTitle([]string{name}, dopestrings.Default.Structure.Titles.BlockRound(strconv.Itoa(r+1))),
			Kind:        "matches",
			BlockRounds: []string{name},
			At:          At{BlockRound: r + 1},
			Matches:     matches,
			Waves:       true,
			Lanes:       lanes,
		})
		if err != nil {
			return Outputs{}, err
		}
		sources = append(sources, emitted...)
	}

	tableTitle := b.BlockRoundTitle(nil, s.Structure.SwissClassic.Table())
	if _, err := b.Emit(Stage{
		Code:     conf.Code,
		Title:    tableTitle,
		Kind:     "swiss_classic",
		Matches:  []store.SchemeMatch{},
		Sources:  sources,
		SeedFrom: seedFrom,
		Config:   conf,
	}); err != nil {
		return Outputs{}, err
	}
	proceeding, _ := b.Proceeding()
	return Outputs{Proceeding: proceeding, Groups: []Feed{{
		Stage: conf.Code,
		Label: tableTitle,
		Place: func(p int) store.SchemeSlot {
			return store.SchemeSlot{
				Reseed: &store.SchemeReseedRef{Stage: conf.Code, Rank: p},
				Label:  fmt.Sprintf("%s-%d", tableTitle, p),
			}
		},
	}}}, nil
}

// swissClassicConfig reads what a бой pays and the table's order.
func swissClassicConfig(b Block) (SwissClassicConfig, error) {
	points, err := rrPoints(b)
	if err != nil {
		return SwissClassicConfig{}, err
	}
	conf := SwissClassicConfig{Points: RRPoints{Win: points[0], Draw: points[1], Loss: points[2]}, ZeroDraw: points[1]}
	if v, ok := b.Int("zero_draw_points"); ok {
		conf.ZeroDraw = float64(v)
	}
	rules, ok, err := b.Sorting()
	if err != nil {
		return SwissClassicConfig{}, err
	}
	if !ok {
		conf.Order = swissClassicOrder
		return conf, nil
	}
	known := map[string]bool{}
	for _, metric := range (swissClassic{}).Metrics() {
		known[metric] = true
	}
	for _, rule := range rules {
		if !known[rule.Metric] {
			return SwissClassicConfig{}, UnrankableMetric(rule.Metric, known)
		}
	}
	conf.Order = rules
	return conf, nil
}

func (swissClassic) Metrics() []string {
	return []string{"points", "taken", "conceded", "buchholz", "bouts", "byes", "seed"}
}

func (swissClassic) Order(cfg json.RawMessage) []SortRule {
	var conf SwissClassicConfig
	if err := json.Unmarshal(cfg, &conf); err != nil {
		return nil
	}
	return conf.Order
}

func (swissClassic) Standings(cfg json.RawMessage, results []MatchOutcome, in Inputs) ([]RankedEntry, error) {
	var conf SwissClassicConfig
	if err := json.Unmarshal(cfg, &conf); err != nil {
		return nil, fmt.Errorf("swiss_classic config: %w", err)
	}
	records := swissRecords(conf, results, in.Seeds, nil)
	return records.table(conf.Order), nil
}

// swissRecord is one team's Swiss stage so far: its row, and whom it met.
type swissRecord struct {
	entry     RankedEntry
	opponents map[int64]bool
}

type swissBook struct {
	byID  map[int64]*swissRecord
	order []int64
}

// swissRecords sums every finished бой of the stage. A бой against the empty
// table has no second Participant: its team wins it by taking a question and
// draws it 0:0 otherwise, which is the zero draw. Buchholz is the points of
// every opponent the team met, the empty table not among them (3.1.4).
// Contenders, when given, are everyone the stage ranks, played or not.
func swissRecords(conf SwissClassicConfig, results []MatchOutcome, seeds map[int64]float64, contenders []int64) swissBook {
	book := swissBook{byID: map[int64]*swissRecord{}}
	record := func(id int64) *swissRecord {
		if r, ok := book.byID[id]; ok {
			return r
		}
		r := &swissRecord{
			entry: RankedEntry{Participant: id, Metrics: map[string]float64{
				"points": 0, "taken": 0, "conceded": 0, "buchholz": 0, "bouts": 0, "byes": 0,
			}},
			opponents: map[int64]bool{},
		}
		if seed, ok := seeds[id]; ok {
			r.entry.Metrics["seed"] = seed
		}
		book.byID[id] = r
		book.order = append(book.order, id)
		return r
	}
	for _, id := range contenders {
		record(id)
	}
	for _, match := range results {
		for i, mine := range match.Slots {
			if mine.Participant == 0 {
				continue
			}
			if _, ok := book.byID[mine.Participant]; contenders != nil && !ok {
				continue
			}
			r := record(mine.Participant)
			r.entry.Bouts = append(r.entry.Bouts, match.Code)
			if !match.Finished {
				continue
			}
			// The empty table's seat comes back unplaced, if at all.
			var opponent *SlotOutcome
			for j := range match.Slots {
				if j != i && match.Slots[j].Participant != 0 {
					opponent = &match.Slots[j]
				}
			}
			taken, conceded := mine.Metrics["taken"], 0.0
			won, lost := taken > 0, false
			if opponent != nil {
				conceded = opponent.Metrics["taken"]
				won, lost = mine.Place < opponent.Place, mine.Place > opponent.Place
			}
			m := r.entry.Metrics
			switch {
			case won:
				m["points"] += conf.Points.Win
			case lost:
				m["points"] += conf.Points.Loss
			case taken == 0 && conceded == 0:
				m["points"] += conf.ZeroDraw
			default:
				m["points"] += conf.Points.Draw
			}
			m["taken"] += taken
			m["conceded"] += conceded
			m["bouts"]++
			if opponent == nil {
				m["byes"]++
			} else {
				r.opponents[opponent.Participant] = true
			}
		}
	}
	for _, id := range book.order {
		r := book.byID[id]
		for opponent := range r.opponents {
			if o, ok := book.byID[opponent]; ok {
				r.entry.Metrics["buchholz"] += o.entry.Metrics["points"]
			}
		}
	}
	return book
}

// table ranks the book by the order, every rank its own.
func (book swissBook) table(order []SortRule) []RankedEntry {
	entries := make([]RankedEntry, 0, len(book.order))
	for _, id := range book.order {
		entries = append(entries, book.byID[id].entry)
	}
	sort.SliceStable(entries, func(i, j int) bool { return LessBy(order)(entries[i], entries[j]) })
	for i := range entries {
		entries[i].Rank = i + 1
	}
	return entries
}

// swissPairing is a round's pairing stage: a reseed whose ranks are seats,
// 2k−1 and 2k at бой k, and the team that sits out last.
type swissPairing struct{}

func (swissPairing) Code() string { return "swiss_pairing" }

func (swissPairing) Metrics() []string { return []string{"points", "taken", "seed"} }

func (swissPairing) Order(json.RawMessage) []SortRule {
	return []SortRule{{Metric: "points", Dir: "desc"}, {Metric: "taken", Dir: "desc"}, {Metric: "seed", Dir: "asc"}}
}

func (swissPairing) Standings(cfg json.RawMessage, results []MatchOutcome, in Inputs) ([]RankedEntry, error) {
	var conf SwissClassicConfig
	if err := json.Unmarshal(cfg, &conf); err != nil {
		return nil, fmt.Errorf("swiss_pairing config: %w", err)
	}
	ids := make([]int64, 0, len(in.Contenders))
	for _, c := range in.Contenders {
		ids = append(ids, c.Participant)
	}
	book := swissRecords(conf, results, in.Seeds, ids)
	pairs, bye := pairRound(book, conf.Order)
	var out []RankedEntry
	for _, pair := range pairs {
		for _, id := range pair {
			out = append(out, book.byID[id].entry)
		}
	}
	if bye != 0 {
		out = append(out, book.byID[bye].entry)
	}
	for i := range out {
		out[i].Rank = i + 1
	}
	return out, nil
}

// pairRound pairs one round (3.1.7). With an odd field the team lowest in
// the table that has not yet sat out does so. The rest are lined up by
// points, then questions taken, then seed, and split into groups level on
// points; the regulations' pairing within and between those groups is the
// round's pairs unless it repeats a meeting, in which case the pairing with
// the fewest repeats and the closest points wins, the regulations' own the
// tiebreak.
func pairRound(book swissBook, order []SortRule) (pairs [][2]int64, bye int64) {
	table := book.table(order)
	if len(table)%2 == 1 {
		bye = table[len(table)-1].Participant
		for i := len(table) - 1; i >= 0; i-- {
			if book.byID[table[i].Participant].entry.Metrics["byes"] == 0 {
				bye = table[i].Participant
				break
			}
		}
	}
	var lineup []RankedEntry
	for _, e := range table {
		if e.Participant != bye {
			lineup = append(lineup, e)
		}
	}
	sort.SliceStable(lineup, func(i, j int) bool {
		return LessBy([]SortRule{{Metric: "points", Dir: "desc"}, {Metric: "taken", Dir: "desc"}, {Metric: "seed", Dir: "asc"}})(lineup[i], lineup[j])
	})
	ids := make([]int64, len(lineup))
	points := map[int64]float64{}
	for i, e := range lineup {
		ids[i] = e.Participant
		points[e.Participant] = e.Metrics["points"]
	}
	met := func(a, b int64) bool { return book.byID[a].opponents[b] }
	return bestPairing(ids, points, met, regulationPairing(ids, points)), bye
}

// regulationPairing pairs a lineup the way 3.1.7 describes, rematches aside.
// Within a group level on points the first meets the last, the second the
// second-to-last. Where a group must pair across, an odd group sends its
// middle team and an even group its last team up and its first team down.
func regulationPairing(ids []int64, points map[int64]float64) [][2]int64 {
	var pairs [][2]int64
	var carried int64
	for start := 0; start < len(ids); {
		end := start
		for end < len(ids) && points[ids[end]] == points[ids[start]] {
			end++
		}
		group := append([]int64(nil), ids[start:end]...)
		start = end
		odd := len(group)%2 == 1
		take := func(i int) int64 {
			id := group[i]
			group = append(group[:i], group[i+1:]...)
			return id
		}
		var down int64
		switch {
		case carried != 0 && odd:
			pairs = append(pairs, [2]int64{carried, take(len(group) / 2)})
		case carried != 0:
			pairs = append(pairs, [2]int64{carried, take(len(group) - 1)})
			down = take(0)
		case odd:
			down = take(len(group) / 2)
		}
		for i := 0; i < len(group)/2; i++ {
			pairs = append(pairs, [2]int64{group[i], group[len(group)-1-i]})
		}
		carried = down
	}
	return pairs
}

// Pairing costs, lexicographic by weight: a rematch outweighs any spread of
// points, and any spread outweighs leaving the regulations' own pair.
const (
	rematchCost    = 1_000_000
	spreadCost     = 100
	deviationCost  = 1
	pairingNodeCap = 2_000_000
)

// bestPairing returns the regulations' pairing when it repeats no meeting,
// and otherwise searches for the cheapest one.
func bestPairing(ids []int64, points map[int64]float64, met func(a, b int64) bool, preferred [][2]int64) [][2]int64 {
	partner := map[int64]int64{}
	for _, p := range preferred {
		partner[p[0]], partner[p[1]] = p[1], p[0]
	}
	cost := func(a, b int64) int {
		c := 0
		if met(a, b) {
			c += rematchCost
		}
		spread := points[a] - points[b]
		c += int(spread*spread*spreadCost + 0.5)
		if partner[a] != b {
			c += deviationCost
		}
		return c
	}
	best := 0
	for _, p := range preferred {
		best += cost(p[0], p[1])
	}
	bestPairs := preferred
	if best < rematchCost {
		return orderPairs(preferred, ids)
	}

	used := make([]bool, len(ids))
	var current [][2]int64
	nodes := 0
	var search func(spent int)
	search = func(spent int) {
		nodes++
		if spent >= best || nodes > pairingNodeCap {
			return
		}
		first := -1
		for i := range ids {
			if !used[i] {
				first = i
				break
			}
		}
		if first < 0 {
			best = spent
			bestPairs = append([][2]int64(nil), current...)
			return
		}
		used[first] = true
		var options []int
		for j := first + 1; j < len(ids); j++ {
			if !used[j] {
				options = append(options, j)
			}
		}
		sort.SliceStable(options, func(x, y int) bool {
			return cost(ids[first], ids[options[x]]) < cost(ids[first], ids[options[y]])
		})
		for _, j := range options {
			used[j] = true
			current = append(current, [2]int64{ids[first], ids[j]})
			search(spent + cost(ids[first], ids[j]))
			current = current[:len(current)-1]
			used[j] = false
		}
		used[first] = false
	}
	search(0)
	return orderPairs(bestPairs, ids)
}

// orderPairs puts the pairs in lineup order, the better-placed team first in
// each, so бой 1 is the top of the table.
func orderPairs(pairs [][2]int64, ids []int64) [][2]int64 {
	at := map[int64]int{}
	for i, id := range ids {
		at[id] = i
	}
	out := make([][2]int64, len(pairs))
	for i, p := range pairs {
		if at[p[1]] < at[p[0]] {
			p[0], p[1] = p[1], p[0]
		}
		out[i] = p
	}
	sort.Slice(out, func(i, j int) bool { return at[out[i][0]] < at[out[j][0]] })
	return out
}
