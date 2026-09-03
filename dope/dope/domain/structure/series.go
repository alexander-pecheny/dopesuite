package structure

import (
	"encoding/json"
	"fmt"
)

func init() { Register(series{}) }

// series is a Round the same two Participants play several times — a best-of
// final, or Troika's final of three Matches. It is a ranking scope like a
// Group is, so it ranks through the very same duel table: points from each
// Match, the Protocol's metrics summed, the scheme's scoring rules over those
// sums, then the block's comparators.
//
// That is what makes "winning the first two Matches does not guarantee the
// overall win" expressible without a rule of its own: a series ranked on
// points with [1, 0, 0] IS best-of, and Troika writes its regulations instead.
type series struct{}

func (series) Code() string { return "series" }

// SeriesConfig is how a series is ranked — a two-seat table like any other.
// Its Matches are the stage's own, drawn by the block that emitted it, so they
// are not repeated here.
type SeriesConfig struct {
	Duel
}

func (series) Order(cfg json.RawMessage) []SortRule {
	var conf SeriesConfig
	if err := json.Unmarshal(cfg, &conf); err != nil {
		return nil
	}
	rules := sortRules(conf.Order)
	out := rules[:0]
	for _, rule := range rules {
		// Head-to-head is a comparator over the tied, not a number a row
		// carries — and inside a series it is the table itself.
		if rule.Metric != "h2h" {
			out = append(out, rule)
		}
	}
	return out
}

func (series) Metrics() []string {
	return []string{"points", "taken", "conceded", "diff", "place_sum", "bouts"}
}

// Standings ranks the series' two Participants over every Match of it.
func (series) Standings(cfg json.RawMessage, results []MatchOutcome, _ Inputs) ([]RankedEntry, error) {
	var conf SeriesConfig
	if err := json.Unmarshal(cfg, &conf); err != nil {
		return nil, fmt.Errorf("series standings config: %w", err)
	}
	return duelStandings(conf.Duel, results)
}
