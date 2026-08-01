package protocol

import (
	"encoding/json"
	"fmt"

	"dope/dope/domain/games"
	"dope/dope/domain/structure"
)

func init() { Register(brain{}) }

// brain wraps games.ComputeBrainResults: state is games.BrainState, the match
// config is the brain scheme document (its questions count sizes the бой).
// Places track the running score; the rr stage awards group points from them
// only once the match is finished (matches.status, structure.MatchOutcome).
type brain struct{}

func (brain) Code() string { return "brain" }

func (brain) EmptyState(cfg json.RawMessage) (json.RawMessage, error) {
	return games.BrainEmptyStateJSON(games.BrainQuestions(string(cfg))), nil
}

func (brain) Score(cfg, stateJSON json.RawMessage) ([]structure.SlotOutcome, error) {
	results, err := games.ComputeBrainResults(string(stateJSON))
	if err != nil {
		return nil, fmt.Errorf("brain score: %w", err)
	}
	outcomes := make([]structure.SlotOutcome, len(results))
	for i, team := range results {
		outcomes[i] = structure.SlotOutcome{
			Place: team.Place,
			Metrics: map[string]float64{
				"taken":     float64(team.Taken),
				"takenBase": float64(team.TakenBase),
			},
		}
	}
	return outcomes, nil
}
