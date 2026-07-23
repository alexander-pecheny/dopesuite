package structure

import (
	"encoding/json"
	"fmt"

	"dope/dope/storage/store"
)

func init() { Register(manual{}) }

// manual is the hand-authored kind (legacy stage_type "matches"): the schedule
// is exactly the match list the scheme author wrote, and the stage ranks
// nobody — advancement out of it is by fromMatch place refs alone.
type manual struct{}

func (manual) Code() string { return "matches" }

func (manual) Schedule(cfg json.RawMessage, results []MatchOutcome) ([]store.SchemeMatch, error) {
	var conf struct {
		Matches []store.SchemeMatch `json:"matches"`
	}
	if err := json.Unmarshal(cfg, &conf); err != nil {
		return nil, fmt.Errorf("matches config: %w", err)
	}
	return conf.Matches, nil
}

func (manual) Standings(cfg json.RawMessage, results []MatchOutcome) ([]RankedEntry, error) {
	return nil, nil
}
