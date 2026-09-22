package structure

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	dopestrings "dope/i18nstrings"

	"dope/dope/storage/store"
)

func init() { Register(placement{}) }

// placement is Хамса's групповой этап: Rounds of multi-seat Matches where
// nobody is eliminated and what is carried forward is the place each
// Participant took.
//
// The first Round is dealt in straight bands of the seed — 1–4 to the first
// table, 5–8 to the second, 9–12 to the third. Every later Round's table k
// seats place k of each table before it, which accounts for as many seats per
// table as there are tables. The places left over — place 4 of each table,
// where twelve play four to a table — fill nothing by rule: they are a Draw
// the host enters on the day, and until then those seats stand empty rather
// than guessed (CONTEXT.md, «Draw»).
//
// The Block is one ranking scope, not one per Round: «главным критерием
// является наименьшая сумма мест, занятых командами в обеих играх ГЭ». So it
// emits a Round of Matches per Round and one table over all of them.
type placement struct{}

func (placement) Code() string { return "placement" }
func (placement) Word() string { return "placement" }
func (placement) Keys() []Key {
	return []Key{{Name: "participants"}, {Name: "match_size"}, {Name: "rounds"}}
}

// placementCanonOrder is what a Block ranks by when the scheme names nothing:
// the сумма мест, then the points scored, then the first places taken.
var placementCanonOrder = []string{"place_sum", "total", "first"}

// Expand lays out the Block: `rounds` Rounds of `participants / match_size`
// Matches, and the table that ranks them together.
func (placement) Expand(b Block) (Outputs, error) {
	s := dopestrings.Default
	participants, ok := b.Int("participants")
	if !ok {
		if b.Seeded() == 0 {
			return Outputs{}, errors.New(s.Structure.Placement.ParticipantsMissing())
		}
		participants = b.Seeded()
	}
	matchSize, ok := b.Int("match_size")
	if !ok || matchSize < 2 {
		return Outputs{}, Keyf("match_size", "%s", s.Structure.Placement.MatchSizeMissing())
	}
	if participants < matchSize || participants%matchSize != 0 {
		return Outputs{}, Keyf("participants", "%s", s.Structure.Placement.NotDivisible(
			strconv.Itoa(participants), strconv.Itoa(matchSize)))
	}
	tables := participants / matchSize
	if tables > matchSize {
		return Outputs{}, Keyf("match_size", "%s", s.Structure.Placement.TooManyTables(
			strconv.Itoa(tables), strconv.Itoa(matchSize)))
	}
	blockRounds := 2
	if v, ok := b.Int("rounds"); ok {
		blockRounds = v
	}
	if blockRounds < 1 {
		return Outputs{}, Keyf("rounds", "%s", s.Structure.Placement.BlockRoundsMin())
	}
	names := make([]string, blockRounds)
	for r := range names {
		names[r] = fmt.Sprintf("r%d", r+1)
	}
	if err := b.BlockRounds(names); err != nil {
		return Outputs{}, err
	}
	order, err := placementOrder(b)
	if err != nil {
		return Outputs{}, err
	}
	proceeding, _ := b.Proceeding()

	blockCode := b.Code()
	seeds, err := b.Seeds(participants)
	if err != nil {
		return Outputs{}, err
	}
	bands := straightChunks(participants, tables)

	var sources []string
	var prev []string
	for r := 1; r <= blockRounds; r++ {
		name := names[r-1]
		lanes, err := b.Venues(name)
		if err != nil {
			return Outputs{}, err
		}
		stageCode := fmt.Sprintf("%s-%s", blockCode, name)
		matches := make([]store.SchemeMatch, tables)
		codes := make([]string, tables)
		for i := 1; i <= tables; i++ {
			code := fmt.Sprintf("%s-m%d", stageCode, i)
			codes[i-1] = code
			var slots []store.SchemeSlot
			if r == 1 {
				for _, rank := range bands[i-1] {
					slots = append(slots, seeds[rank-1])
				}
			} else {
				// Place i of every table of the Round before: the winners meet
				// the winners, the runners-up the runners-up.
				for t, from := range prev {
					slots = append(slots, LabelledFromMatch(from, s.Structure.Titles.Bout(strconv.Itoa(t+1)), i))
				}
				for k := tables; k < matchSize; k++ {
					slots = append(slots, drawSlot(fmt.Sprintf("%s-d%d", code, k-tables+1), prev, tables, matchSize))
				}
			}
			matches[i-1] = store.SchemeMatch{
				Code:             code,
				Title:            s.Structure.Titles.Bout(strconv.Itoa(i)),
				Venue:            lanes.Pick(i),
				ParticipantCount: len(slots),
				Slots:            slots,
			}
		}
		emitted, err := b.Emit(Stage{
			Code:        stageCode,
			Title:       b.BlockRoundTitle([]string{name}, s.Structure.Titles.BlockRound(strconv.Itoa(r))),
			Kind:        "matches",
			BlockRounds: []string{name},
			At:          At{BlockRound: r},
			Matches:     matches,
			Waves:       true,
			Lanes:       lanes,
		})
		if err != nil {
			return Outputs{}, err
		}
		sources = append(sources, emitted...)
		prev = codes
	}

	// The Block's own table. It holds no Matches of its own — it ranks the
	// Rounds together, which is the only ranking the регламент asks for — so it
	// names them as its sources, exactly as a reseed does.
	tableCode := blockCode + "-total"
	tableTitle := b.BlockRoundTitle(nil, s.Structure.Placement.Table())
	if _, err := b.Emit(Stage{
		Code:    tableCode,
		Title:   tableTitle,
		Kind:    "placement",
		Matches: []store.SchemeMatch{},
		Sources: sources,
		Config:  PlacementConfig{Code: tableCode, MatchSize: matchSize, Order: order, Rules: b.Rules()},
	}); err != nil {
		return Outputs{}, err
	}
	return Outputs{Proceeding: proceeding, Groups: []Feed{{
		Stage: tableCode,
		Label: tableTitle,
		Place: func(p int) store.SchemeSlot {
			return store.SchemeSlot{
				Reseed: &store.SchemeReseedRef{Stage: tableCode, Rank: p},
				Label:  fmt.Sprintf("%s-%d", tableTitle, p),
			}
		},
	}}}, nil
}

// drawSlot is one seat the host draws: it names the places nothing else
// carried forward — place tables+1 and up of every table of the Round before —
// and stays empty until somebody is seated in it.
func drawSlot(code string, prev []string, tables, matchSize int) store.SchemeSlot {
	draw := &store.SchemeDraw{Code: code}
	for _, from := range prev {
		for place := tables + 1; place <= matchSize; place++ {
			draw.Candidates = append(draw.Candidates, store.SchemeFromMatchRef{Match: from, Place: place})
		}
	}
	return store.SchemeSlot{Draw: draw, Label: dopestrings.Default.Structure.Placement.DrawSeat()}
}

// placementOrder resolves the Block's comparator chain: the scheme's own, then
// [defaults], then the canon.
func placementOrder(b Block) ([]string, error) {
	rules, ok, err := b.Sorting()
	if err != nil {
		return nil, err
	}
	if !ok {
		if rules, ok, err = b.DefaultSorting(); err != nil {
			return nil, err
		}
	}
	if !ok {
		return placementCanonOrder, nil
	}
	known := b.Rankable("placement")
	order := make([]string, len(rules))
	for i, rule := range rules {
		if !known[rule.Metric] {
			return nil, UnrankableMetric(rule.Metric, known)
		}
		order[i] = rule.Metric
	}
	return order, nil
}

func placementConf(cfg json.RawMessage) (PlacementConfig, error) {
	var conf PlacementConfig
	if err := json.Unmarshal(cfg, &conf); err != nil {
		return PlacementConfig{}, fmt.Errorf("placement config: %w", err)
	}
	if len(conf.Order) == 0 {
		conf.Order = placementCanonOrder
	}
	return conf, nil
}

// Metrics: the сумма мест and the бои it was summed over, as every multi-seat
// table counts them, plus the seed rank — «более высокое место, занятое
// командой на этапе КСИ», the last comparator the регламент names and the one
// thing no бой can measure.
func (placement) Metrics() []string { return []string{"place_sum", "bouts", "seed"} }

func (placement) Order(cfg json.RawMessage) []SortRule {
	conf, err := placementConf(cfg)
	if err != nil {
		return nil
	}
	return sortRules(conf.Order)
}

// Standings ranks the Block's Participants over every Round it played, the way
// any multi-seat table does: the Protocol's metrics sum, the places sum, and
// the scheme's comparators order them. The seed rank rides along so a scheme
// can close its chain with it.
func (placement) Standings(cfg json.RawMessage, results []MatchOutcome, in Inputs) ([]RankedEntry, error) {
	conf, err := placementConf(cfg)
	if err != nil {
		return nil, err
	}
	return multiSeatStandings(RRConfig{
		Code:      conf.Code,
		MatchSize: conf.MatchSize,
		Order:     conf.Order,
		Rules:     conf.Rules,
	}, results, in.Seeds)
}
