package structure

import (
	"fmt"
	"sort"

	"dope/dope/domain/expr"
)

// Scoring rules (ADR-0008) at the two grains a tournament actually uses.
//
// A bout rule is evaluated once per Participant per бой, over that бой's
// outcome, and its value is SUMMED into the standings — «4 − место» accumulates
// across the group. A standings rule is evaluated once, over those sums, and
// its value REPLACES anything of that name — «очки / (2 × бои)» cannot be
// summed, because a share of a share is not a share.
//
// The two are not interchangeable even for linear rules, which is why both
// exist and why a scheme says which it means.

// DerivedMetrics are the names the Structure layer itself puts on a standings
// row, whatever the Protocol measures — so a scheme may sort on them without
// declaring anything. Everything else a sorting key names must come from the
// Protocol's own declaration or from the scheme's scoring rules.
func DerivedMetrics() []string {
	return []string{
		"points",    // очки, by the block's scoring rule
		"place_sum", // сумма мест
		"bouts",     // сыгранные бои
		"losses",    // поражения — the currency the eliminations count
		"h2h",       // личная встреча, a comparator over the still-tied
		"draw",      // жребий
		"diff",      // разница
		"taken",     // взятые, summed from the Protocol's score metric
		"conceded",  // пропущенные

		// The reseed's shares, which only exist after the sum: доля очков от
		// возможных and доля взятых от заданных.
		"points_share",
		"taken_share",
		"taken_base",
	}
}

// Rules are the scheme's scoring rules, name → expression, at each grain.
type Rules struct {
	Bout      map[string]string `json:"bout"`
	Standings map[string]string `json:"standings"`
}

type namedRule struct {
	name string
	expr *expr.Expr
}

type compiledRules struct {
	bout      []namedRule
	standings []namedRule
}

// compileRules parses every rule and orders each grain so a rule that reads
// another's output runs after it. Order is derived, not authored: a scheme's
// keys are a set, and a YAML map has no order to rely on.
func compileRules(rules Rules) (*compiledRules, error) {
	bout, err := compileGrain("bout", rules.Bout)
	if err != nil {
		return nil, err
	}
	standings, err := compileGrain("standings", rules.Standings)
	if err != nil {
		return nil, err
	}
	return &compiledRules{bout: bout, standings: standings}, nil
}

func compileGrain(grain string, sources map[string]string) ([]namedRule, error) {
	if len(sources) == 0 {
		return nil, nil
	}
	parsed := make(map[string]*expr.Expr, len(sources))
	for name, src := range sources {
		e, err := expr.Parse(src)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", grain, name, err)
		}
		parsed[name] = e
	}
	names := make([]string, 0, len(parsed))
	for name := range parsed {
		names = append(names, name)
	}
	sort.Strings(names)

	var ordered []namedRule
	state := map[string]int{} // 0 unvisited, 1 in progress, 2 done
	var visit func(name string, trail []string) error
	visit = func(name string, trail []string) error {
		switch state[name] {
		case 2:
			return nil
		case 1:
			return fmt.Errorf("%s.%s: rule depends on itself (%v)", grain, name, append(trail, name))
		}
		state[name] = 1
		for _, dep := range parsed[name].Vars() {
			if _, ours := parsed[dep]; !ours {
				continue // a Protocol metric or a built-in, resolved at eval time
			}
			if err := visit(dep, append(trail, name)); err != nil {
				return err
			}
		}
		state[name] = 2
		ordered = append(ordered, namedRule{name: name, expr: parsed[name]})
		return nil
	}
	for _, name := range names {
		if err := visit(name, nil); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

// names lists every metric this grain defines, for the compiler's check that a
// sorting key exists.
func (r *compiledRules) names() []string {
	var out []string
	for _, rule := range r.bout {
		out = append(out, rule.name)
	}
	for _, rule := range r.standings {
		out = append(out, rule.name)
	}
	return out
}

// boutScope is what a bout rule can see: its own seat, the бой around it, and
// the other seats — indexed in seat order as opp1_, opp2_… plus the opp_,
// opp_max_ and opp_min_ aggregates, so «взято − пропущено» reads the same at
// two seats and at four.
func boutScope(match MatchOutcome, seat int) expr.Vars {
	mine := match.Slots[seat]
	scope := expr.Vars{
		"place":     mine.Place,
		"seats":     float64(len(match.Slots)),
		"questions": float64(match.Questions),
		"finished":  boolFloat(match.Finished),
	}
	tied := 0.0
	for i, other := range match.Slots {
		if i != seat && other.Participant != 0 && other.Place == mine.Place {
			tied++
		}
	}
	scope["tied"] = tied
	for key, value := range mine.Metrics {
		scope[key] = value
	}

	sums, maxes, mins := map[string]float64{}, map[string]float64{}, map[string]float64{}
	index := 0
	for i, other := range match.Slots {
		if i == seat {
			continue
		}
		index++
		prefix := fmt.Sprintf("opp%d_", index)
		scope[prefix+"place"] = other.Place
		for key, value := range other.Metrics {
			scope[prefix+key] = value
			sums[key] += value
			if current, seen := maxes[key]; !seen || value > current {
				maxes[key] = value
			}
			if current, seen := mins[key]; !seen || value < current {
				mins[key] = value
			}
		}
	}
	for key, value := range sums {
		scope["opp_"+key] = value
		scope["opp_max_"+key] = maxes[key]
		scope["opp_min_"+key] = mins[key]
	}
	return scope
}

// applyBout evaluates the bout rules for one seat and adds each result to that
// Participant's running metrics. Later rules see earlier ones, so a rule can
// build on another within the same бой.
func (r *compiledRules) applyBout(match MatchOutcome, seat int, into map[string]float64) error {
	if r == nil || len(r.bout) == 0 {
		return nil
	}
	scope := boutScope(match, seat)
	for _, rule := range r.bout {
		value, err := rule.expr.Eval(scope)
		if err != nil {
			return fmt.Errorf("bout.%s: %w", rule.name, err)
		}
		scope[rule.name] = value
		into[rule.name] += value
	}
	return nil
}

// applyStandings evaluates the standings rules over a Participant's summed
// metrics, each result replacing (never adding to) the name it defines.
func (r *compiledRules) applyStandings(metrics map[string]float64) error {
	if r == nil || len(r.standings) == 0 {
		return nil
	}
	scope := expr.Vars(metrics)
	for _, rule := range r.standings {
		value, err := rule.expr.Eval(scope)
		if err != nil {
			return fmt.Errorf("standings.%s: %w", rule.name, err)
		}
		metrics[rule.name] = value
	}
	return nil
}

func boolFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
