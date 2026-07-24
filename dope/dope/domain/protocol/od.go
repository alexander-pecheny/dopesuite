package protocol

import (
	"encoding/json"
	"fmt"

	"dope/dope/domain/games"
	"dope/dope/domain/structure"
)

func init() { Register(od{}) }

// od wraps the existing OD (ЧГК) pure scoring: state is games.ODState, ranked
// by games.ComputeODResults. The match config is the OD scheme document (its
// tourComp drives the tour split). Teams tied on total share a place, matching
// the results page's tie-grouped labels.
type od struct{}

func (od) Code() string { return "od" }

func (od) RatingRosterStateKey() string { return "teams" }

func (od) EmptyState(cfg json.RawMessage) (json.RawMessage, error) {
	_, stateJSON := games.ODEmptyGameJSON("", "", games.ParseTourComp(string(cfg)))
	return stateJSON, nil
}

func (od) Score(cfg, stateJSON json.RawMessage) ([]structure.SlotOutcome, error) {
	results, err := games.ComputeODResults(string(cfg), string(stateJSON))
	if err != nil {
		return nil, fmt.Errorf("od score: %w", err)
	}
	outcomes := make([]structure.SlotOutcome, resultTeamCount(results))
	place, placed := 0.0, 0
	for rank, team := range results.Teams {
		if rank == 0 || team.Total != results.Teams[rank-1].Total {
			place = float64(placed + 1)
		}
		placed++
		outcome := structure.SlotOutcome{
			Metrics: map[string]float64{
				"total":  float64(team.Total),
				"rating": float64(team.Rating),
			},
		}
		if team.Place != "" {
			outcome.Place = place
		}
		outcomes[team.Index] = outcome
	}
	return outcomes, nil
}

func resultTeamCount(results games.ODResults) int {
	max := 0
	for _, team := range results.Teams {
		if team.Index+1 > max {
			max = team.Index + 1
		}
	}
	return max
}
