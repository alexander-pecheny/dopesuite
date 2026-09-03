package structure

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	dopestrings "dope/i18nstrings"

	"dope/dope/storage/store"
)

func init() { Register(flat{}) }

// flat is the degenerate Structure: one Match seating every Participant, which
// is the whole bracket of ChGK, OD and KSI. It exists so those games are said in
// the same language as the others rather than being the case with no Kind —
// the standings are then just the Protocol's own places.
type flat struct{}

func (flat) Code() string { return "flat" }
func (flat) Word() string { return "flat" }
func (flat) Keys() []Key  { return []Key{{Name: "participants"}} }

// Expand is the whole bracket of a flat game: one Match seating everyone. OD
// and KSI have always been this shape in the database; the Kind only lets a
// scheme say so.
func (flat) Expand(b Block) (Outputs, error) {
	s := dopestrings.Default
	participants, ok := b.Int("participants")
	if !ok {
		if b.Seeded() == 0 {
			return Outputs{}, errors.New("flat: нужен participants")
		}
		participants = b.Seeded()
	}
	if err := b.Rounds(nil); err != nil {
		return Outputs{}, err
	}
	proceeding, _ := b.Proceeding()
	lanes, err := b.Venues()
	if err != nil {
		return Outputs{}, err
	}
	entrants, err := b.Entrants(1, participants)
	if err != nil {
		return Outputs{}, err
	}
	code, title := b.Code(), b.Title(s.Structure.Flat.Game())
	cfg := FlatConfig{Code: code, Entrants: entrants[0], Title: title, Venue: lanes.Pick(1), Rules: b.Rules()}
	if order, ok, err := b.Sorting(); err != nil {
		return Outputs{}, err
	} else if ok {
		known := b.Rankable("flat")
		for _, rule := range order {
			if !known[rule.Metric] {
				return Outputs{}, UnrankableMetric(rule.Metric, known)
			}
			cfg.Order = append(cfg.Order, rule.Metric)
		}
	}
	if _, err := b.Emit(Stage{Code: code, Title: title, Kind: "flat", Config: cfg}); err != nil {
		return Outputs{}, err
	}
	return Outputs{Proceeding: proceeding, Groups: []Feed{{
		Stage: code,
		Label: title,
		// The block's standings rank, not the Match's place. A Match shares a
		// place between seats that tie, and a shared place names nobody —
		// TPSH's qualifier has ties inside its top 24, and "place 10.5" cannot
		// seat anyone. The standings apply the block's whole sorting chain and
		// rank distinctly.
		Place: func(p int) store.SchemeSlot {
			return store.SchemeSlot{
				Reseed: &store.SchemeReseedRef{Stage: code, Rank: p},
				Label:  fmt.Sprintf("%s-%d", title, p),
			}
		},
	}}}, nil
}

func (flat) Schedule(cfg json.RawMessage) ([]store.SchemeMatch, error) {
	s := dopestrings.Default
	var conf FlatConfig
	if err := json.Unmarshal(cfg, &conf); err != nil {
		return nil, fmt.Errorf("flat config: %w", err)
	}
	if len(conf.Entrants) == 0 {
		return nil, fmt.Errorf("flat: за столом никого")
	}
	title := conf.Title
	if title == "" {
		title = s.Structure.Flat.Game()
	}
	code := conf.Code
	if code == "" {
		code = "flat"
	}
	return []store.SchemeMatch{{
		Code:             code + "-m1",
		Title:            title,
		Venue:            conf.Venue,
		Round:            1,
		ParticipantCount: len(conf.Entrants),
		Slots:            conf.Entrants,
	}}, nil
}

// Standings ranks by the Protocol's own places — a flat game's Match already
// ranked everyone — with any scoring rules the scheme added on top.
func (flat) Standings(cfg json.RawMessage, results []MatchOutcome, _ Inputs) ([]RankedEntry, error) {
	var conf FlatConfig
	if err := json.Unmarshal(cfg, &conf); err != nil {
		return nil, fmt.Errorf("flat standings config: %w", err)
	}
	rules, err := compileRules(conf.Rules)
	if err != nil {
		return nil, err
	}
	var ranked []RankedEntry
	for _, match := range results {
		for seat, slot := range match.Slots {
			if slot.Participant == 0 {
				continue
			}
			metrics := map[string]float64{"place": slot.Place}
			for key, value := range slot.Metrics {
				metrics[key] = value
			}
			if match.Finished {
				if err := rules.applyBout(match, seat, metrics); err != nil {
					return nil, err
				}
			}
			if err := rules.applyStandings(metrics); err != nil {
				return nil, err
			}
			ranked = append(ranked, RankedEntry{Participant: slot.Participant, Metrics: metrics})
		}
	}
	order := flatOrder(conf)
	sort.SliceStable(ranked, func(i, j int) bool {
		for _, key := range order {
			a, b := ranked[i].Metrics[key], ranked[j].Metrics[key]
			if a == b {
				continue
			}
			if key == "place" || key == "place_sum" {
				// A seat the Protocol left unplaced — a KSI team that declined,
				// a Match not scored — ranks after everyone it did place.
				return (a != 0 && a < b) || b == 0
			}
			return a > b
		}
		return ranked[i].Participant < ranked[j].Participant
	})
	shareRanks(ranked, order)
	// A table sorted by its own keys shows the rank they give; one that only
	// keeps the Match's order shows the Match's own place, mean of a tie and all.
	if conf.Order != nil {
		for i := range ranked {
			ranked[i].Metrics["place"] = float64(ranked[i].Rank)
		}
	}
	return ranked, nil
}

func flatOrder(conf FlatConfig) []string {
	if conf.Order == nil {
		return []string{"place"}
	}
	return conf.Order
}

func (flat) Metrics() []string { return []string{"place"} }

func (flat) Order(cfg json.RawMessage) []SortRule {
	var conf FlatConfig
	if err := json.Unmarshal(cfg, &conf); err != nil {
		return nil
	}
	return sortRules(flatOrder(conf))
}
