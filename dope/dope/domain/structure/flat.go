package structure

import (
	"encoding/json"
	"fmt"
	"sort"

	"dope/dope/storage/store"
)

func init() { Register(flat{}) }

// flat is the degenerate Structure: one Match seating every Participant, which
// is the whole bracket of ЧГК, ОД and КСИ. It exists so those games are said in
// the same language as the others rather than being the case with no Kind —
// the standings are then just the Protocol's own places.
type flat struct{}

func (flat) Code() string { return "flat" }

func (flat) Schedule(cfg json.RawMessage) ([]store.SchemeMatch, error) {
	var conf FlatConfig
	if err := json.Unmarshal(cfg, &conf); err != nil {
		return nil, fmt.Errorf("flat config: %w", err)
	}
	if len(conf.Entrants) == 0 {
		return nil, fmt.Errorf("flat: за столом никого")
	}
	title := conf.Title
	if title == "" {
		title = "Игра"
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
				// A seat the Protocol left unplaced — a КСИ team that declined,
				// a бой not scored — ranks after everyone it did place.
				return (a != 0 && a < b) || b == 0
			}
			return a > b
		}
		return ranked[i].Participant < ranked[j].Participant
	})
	shareRanks(ranked, order)
	// A table sorted by its own keys shows the rank they give; one that only
	// keeps the бой's order shows the бой's own place, mean of a tie and all.
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
