package structure

import (
	"encoding/json"
	"fmt"
)

func init() { Register(series{}) }

// series is a Round the same two Participants play several times — a финал
// «до большинства побед», or Троечка's финал of three боёв. It is a ranking
// scope like a группа is, so it ranks through the very same duel table: очки
// from each бой, the Protocol's metrics summed, the scheme's scoring rules
// over those sums, then the block's comparators.
//
// That is what makes «победа в первых двух боях не гарантирует общую победу»
// expressible without a rule of its own: a series ranked on points with
// [1, 0, 0] IS до большинства побед, and Троечка writes its регламент instead.
type series struct{}

func (series) Code() string { return "series" }

// SeriesConfig is how a series is ranked. Its бои are the stage's own, drawn
// by the block that emitted it, so they are not repeated here.
type SeriesConfig struct {
	Points *RRPoints `json:"points,omitempty"`
	Metric string    `json:"metric,omitempty"`
	Order  []string  `json:"order,omitempty"`
	Rules  *Rules    `json:"rules,omitempty"`
}

func (s SeriesConfig) duel() Duel {
	return Duel{Points: s.Points, Metric: s.Metric, Order: s.Order, Rules: s.Rules}
}

func (series) Order(cfg json.RawMessage) []SortRule {
	var conf SeriesConfig
	if err := json.Unmarshal(cfg, &conf); err != nil {
		return nil
	}
	order := conf.Order
	if len(order) == 0 {
		order = []string{"points", "taken"}
	}
	rules := sortRules(order)
	out := rules[:0]
	for _, rule := range rules {
		// Личная встреча is a comparator over the tied, not a number a row
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

// Standings ranks the series' two Participants over every бой of it.
func (series) Standings(cfg json.RawMessage, results []MatchOutcome, _ Inputs) ([]RankedEntry, error) {
	var conf SeriesConfig
	if err := json.Unmarshal(cfg, &conf); err != nil {
		return nil, fmt.Errorf("series standings config: %w", err)
	}
	duel := conf.duel()
	if len(duel.Order) == 0 {
		// Absent a scheme's own comparators a series is до большинства побед:
		// the бои taken, then what was scored in them.
		duel.Order = []string{"points", "taken"}
	}
	return duelStandings(duel, results)
}
