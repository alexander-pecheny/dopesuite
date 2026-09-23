package structure

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	dopestrings "dope/i18nstrings"

	"dope/dope/storage/store"
)

func init() { Register(swiss{}) }

// swiss is the Swiss stage in the Major format: a Participant leaves the Block
// on its `wins`-th win, and goes on, or on its `losses`-th loss, and is out.
// Every round pairs the Participants that share a record, which is a pool;
// a pool is ranked by the seed its members entered the Block with and dealt
// into bouts by the snake, so the best seed meets the worst.
//
// How many seats a pool's bouts have, and how many of them win, is the
// organisers' plan rather than a rule: Bug Major's twelve play the first two
// rounds two to a bout and the rest three to a bout, where a pool of three
// sends one on and a pool of six sends two from each bout. So the Kind carries
// the plans it knows as data, keyed by the entrant count, and refuses any
// other count.
type swiss struct{}

func (swiss) Code() string { return "swiss" }
func (swiss) Word() string { return "swiss" }
func (swiss) Keys() []Key {
	return []Key{{Name: "participants"}, {Name: "wins"}, {Name: "losses"}}
}

// swissPool is one record's bouts in one round: the seats each bout has and how
// many of them win it.
type swissPool struct {
	wins, losses int
	seats        int
	winners      int
}

// swissPlan is a whole Swiss stage: its records to leave on, and its rounds.
type swissPlan struct {
	wins, losses int
	rounds       [][]swissPool
}

// swissPlans are the plans the Kind knows. Twelve is Bug Major III's
// (regulations IV.2.3 and its picture): six go on, one at 3–0, four at 3–1 and
// one at 3–2.
var swissPlans = map[int]swissPlan{
	12: {wins: 3, losses: 3, rounds: [][]swissPool{
		{{0, 0, 2, 1}},
		{{1, 0, 2, 1}, {0, 1, 2, 1}},
		{{2, 0, 3, 1}, {1, 1, 3, 2}, {0, 2, 3, 1}},
		{{2, 1, 3, 2}, {1, 2, 3, 1}},
		{{2, 2, 3, 1}},
	}},
}

// SwissConfig is the Block's table as its Ranker reads it back: what winning
// means in every bout, and the records that end a Participant's stage.
type SwissConfig struct {
	Code   string `json:"code,omitempty"`
	Wins   int    `json:"wins"`
	Losses int    `json:"losses"`
	// Winners is, per bout code, how many of its places win it.
	Winners map[string]int `json:"winners"`
}

// swissSeat is where one seat of a pool comes from: a place in a bout of the
// round before.
type swissSeat struct {
	bout  string
	label string
	place int
}

func (swiss) Expand(b Block) (Outputs, error) {
	s := dopestrings.Default
	participants, ok := b.Int("participants")
	if !ok {
		participants = b.Seeded()
	}
	plan, known := swissPlans[participants]
	if !known {
		sizes := make([]string, 0, len(swissPlans))
		for n := range swissPlans {
			sizes = append(sizes, strconv.Itoa(n))
		}
		sort.Strings(sizes)
		return Outputs{}, Keyf("participants", "%s", s.Structure.Swiss.ParticipantsUnsupported(
			strconv.Itoa(participants), strings.Join(sizes, ", ")))
	}
	for key, want := range map[string]int{"wins": plan.wins, "losses": plan.losses} {
		if v, ok := b.Int(key); ok && v != want {
			return Outputs{}, Keyf(key, "%s", s.Structure.Swiss.PlanMismatch(
				strconv.Itoa(participants), strconv.Itoa(plan.wins), strconv.Itoa(plan.losses)))
		}
	}
	names := make([]string, len(plan.rounds))
	for r := range names {
		names[r] = fmt.Sprintf("r%d", r+1)
	}
	if err := b.BlockRounds(names); err != nil {
		return Outputs{}, err
	}

	// The first round pairs ranks: the entry order of the Block before, or
	// the Game's seed when the Swiss opens the Game.
	var entry []store.SchemeSlot
	seedFrom := ""
	if b.First() {
		seeds, err := b.Seeds(participants)
		if err != nil {
			return Outputs{}, err
		}
		entry = seeds
	} else {
		prev, _ := b.Prev()
		if len(prev.Groups) != 1 {
			return Outputs{}, errors.New(s.Structure.Swiss.NeedsRanking())
		}
		feed := prev.Groups[0]
		seedFrom = feed.Stage
		for rank := 1; rank <= participants; rank++ {
			entry = append(entry, feed.Place(rank))
		}
	}

	blockCode := b.Code()
	conf := SwissConfig{Code: blockCode + "-table", Wins: plan.wins, Losses: plan.losses, Winners: map[string]int{}}
	// Where each record's members come from in the next round: the winning
	// places of the pool one win behind and the losing places of the pool one
	// loss behind.
	next := map[[2]int][]swissSeat{}
	var sources []string
	proceeding := 0
	for r, round := range plan.rounds {
		name := names[r]
		lanes, err := b.Venues(name)
		if err != nil {
			return Outputs{}, err
		}
		stageCode := fmt.Sprintf("%s-%s", blockCode, name)
		var matches []store.SchemeMatch
		arrivals := map[[2]int][]swissSeat{}
		for _, pool := range round {
			record := [2]int{pool.wins, pool.losses}
			bouts := 0
			var dealt [][]store.SchemeSlot
			if r == 0 {
				bouts = participants / pool.seats
				for _, chunk := range snakeChunks(participants, bouts) {
					var slots []store.SchemeSlot
					for _, rank := range chunk {
						slots = append(slots, entry[rank-1])
					}
					dealt = append(dealt, slots)
				}
			} else {
				members := next[record]
				if len(members) == 0 || len(members)%pool.seats != 0 {
					return Outputs{}, fmt.Errorf("swiss: pool %d-%d of round %d has %d seats for bouts of %d",
						pool.wins, pool.losses, r+1, len(members), pool.seats)
				}
				bouts = len(members) / pool.seats
				if bouts == 1 {
					var slots []store.SchemeSlot
					for _, m := range members {
						slots = append(slots, LabelledFromMatch(m.bout, m.label, m.place))
					}
					dealt = append(dealt, slots)
				} else {
					// Several bouts: the pool is ranked by seed first, on its
					// own, and dealt by the snake.
					poolCode := fmt.Sprintf("%s-p%d%d", stageCode, pool.wins, pool.losses)
					contenders := make([]store.SchemeSlot, len(members))
					for i, m := range members {
						contenders[i] = FromMatch(m.bout, m.place)
					}
					title := s.Structure.Swiss.Pool(strconv.Itoa(pool.wins), strconv.Itoa(pool.losses))
					if _, err := b.EmitPool(poolCode, title, At{BlockRound: r + 1}, contenders, seedFrom); err != nil {
						return Outputs{}, err
					}
					for _, chunk := range snakeChunks(len(members), bouts) {
						var slots []store.SchemeSlot
						for _, rank := range chunk {
							slot := ReseedRank(poolCode, rank)
							slot.Label = s.Structure.Swiss.PoolRank(strconv.Itoa(pool.wins), strconv.Itoa(pool.losses), strconv.Itoa(rank))
							slots = append(slots, slot)
						}
						dealt = append(dealt, slots)
					}
				}
			}
			for _, slots := range dealt {
				index := len(matches) + 1
				code := fmt.Sprintf("%s-m%d", stageCode, index)
				title := s.Structure.Swiss.Bout(strconv.Itoa(index), strconv.Itoa(pool.wins), strconv.Itoa(pool.losses))
				matches = append(matches, store.SchemeMatch{
					Code:             code,
					Title:            title,
					Venue:            lanes.Pick(index),
					ParticipantCount: len(slots),
					Slots:            slots,
				})
				conf.Winners[code] = pool.winners
				for place := 1; place <= pool.seats; place++ {
					seat := swissSeat{bout: code, label: title, place: place}
					if place <= pool.winners {
						if pool.wins+1 == plan.wins {
							proceeding++
						} else {
							arrivals[[2]int{pool.wins + 1, pool.losses}] = append(arrivals[[2]int{pool.wins + 1, pool.losses}], seat)
						}
					} else if pool.losses+1 < plan.losses {
						arrivals[[2]int{pool.wins, pool.losses + 1}] = append(arrivals[[2]int{pool.wins, pool.losses + 1}], seat)
					}
				}
			}
			delete(next, record)
		}
		for record, seats := range next {
			if len(seats) > 0 {
				return Outputs{}, fmt.Errorf("swiss: round %d leaves pool %d-%d unplayed", r+1, record[0], record[1])
			}
		}
		next = arrivals
		emitted, err := b.Emit(Stage{
			Code:        stageCode,
			Title:       b.BlockRoundTitle([]string{name}, s.Structure.Titles.BlockRound(strconv.Itoa(r+1))),
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
	for record, seats := range next {
		if len(seats) > 0 {
			return Outputs{}, fmt.Errorf("swiss: the plan ends with pool %d-%d unplayed", record[0], record[1])
		}
	}

	// The Block's own table ranks every round together by record, and holds
	// no bouts of its own: it names the rounds, as a placement table does.
	tableTitle := b.BlockRoundTitle(nil, s.Structure.Swiss.Table())
	if _, err := b.Emit(Stage{
		Code:     conf.Code,
		Title:    tableTitle,
		Kind:     "swiss",
		Matches:  []store.SchemeMatch{},
		Sources:  sources,
		SeedFrom: seedFrom,
		Config:   conf,
	}); err != nil {
		return Outputs{}, err
	}
	if v, ok := b.Proceeding(); ok {
		proceeding = v
	}
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

// Metrics: the record, and the seed the Block was entered with — the last
// word between two Participants that share a record.
func (swiss) Metrics() []string { return []string{"wins", "losses", "seed"} }

func (swiss) Order(json.RawMessage) []SortRule {
	return []SortRule{{Metric: "wins", Dir: "desc"}, {Metric: "losses", Dir: "asc"}, {Metric: "seed", Dir: "asc"}}
}

// Standings ranks by record. Those who reached the wins go first, by fewer
// losses; those who reached the losses go last, by more wins; anyone still
// playing sits between. A bout's place wins it when it is among the bout's
// winning places; a shared place spanning the cut says nothing, which is what
// a shootout is played for.
func (swiss) Standings(cfg json.RawMessage, results []MatchOutcome, in Inputs) ([]RankedEntry, error) {
	var conf SwissConfig
	if err := json.Unmarshal(cfg, &conf); err != nil {
		return nil, fmt.Errorf("swiss config: %w", err)
	}
	byParticipant := map[int64]*RankedEntry{}
	var order []int64
	for _, match := range results {
		for _, slot := range match.Slots {
			if slot.Participant == 0 {
				continue
			}
			entry, ok := byParticipant[slot.Participant]
			if !ok {
				entry = &RankedEntry{Participant: slot.Participant, Metrics: map[string]float64{"wins": 0, "losses": 0}}
				if seed, ok := in.Seeds[slot.Participant]; ok {
					entry.Metrics["seed"] = seed
				}
				byParticipant[slot.Participant] = entry
				order = append(order, slot.Participant)
			}
			for key, value := range slot.Metrics {
				entry.Metrics[key] += value
			}
			entry.Bouts = append(entry.Bouts, match.Code)
			if !match.Finished || slot.Place != float64(int(slot.Place)) || slot.Place <= 0 {
				continue
			}
			if int(slot.Place) <= conf.Winners[match.Code] {
				entry.Metrics["wins"]++
			} else {
				entry.Metrics["losses"]++
			}
		}
	}
	status := func(e *RankedEntry) int {
		switch {
		case conf.Wins > 0 && int(e.Metrics["wins"]) >= conf.Wins:
			return 0
		case conf.Losses > 0 && int(e.Metrics["losses"]) >= conf.Losses:
			return 2
		}
		return 1
	}
	entries := make([]RankedEntry, 0, len(order))
	for _, id := range order {
		entries = append(entries, *byParticipant[id])
	}
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := &entries[i], &entries[j]
		if sa, sb := status(a), status(b); sa != sb {
			return sa < sb
		}
		if a.Metrics["wins"] != b.Metrics["wins"] {
			return a.Metrics["wins"] > b.Metrics["wins"]
		}
		if a.Metrics["losses"] != b.Metrics["losses"] {
			return a.Metrics["losses"] < b.Metrics["losses"]
		}
		sa, oka := a.Metrics["seed"]
		sb, okb := b.Metrics["seed"]
		if oka != okb {
			return oka
		}
		if sa != sb {
			return sa < sb
		}
		return a.Participant < b.Participant
	})
	for i := range entries {
		entries[i].Rank = i + 1
	}
	return entries, nil
}
