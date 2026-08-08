package protocol

import (
	"encoding/json"
	"fmt"

	"dope/dope/domain/games"
	"dope/dope/domain/structure"
	"dope/dope/storage/store"
)

func init() { Register(si{}) }

// si is личная своя игра: the same themes × participants grid as КСИ, seating
// players instead of teams and far fewer of them — three or four at a table
// over six, eight or twelve themes, rather than a hall of twenty. Reusing КСИ's
// state document is deliberate: it is the same protocol, and it means the same
// page renders both (CONTEXT.md, «Protocol»).
type si struct{}

func (si) Code() string { return "si" }

// Metrics: сумма, сумма положительных ответов, и счётчики взятых по номиналам —
// СИ ранжирует по ним, когда суммы равны.
func (si) Metrics() []string {
	return []string{"total", "plus", "taken50", "taken40", "taken30", "taken20", "taken10"}
}

func (si) RatingRosterStateKey() string { return "participants" }

type siConfig struct {
	Themes       int `json:"themes"`
	Participants int `json:"participants"`
}

func (si) EmptyState(cfg json.RawMessage) (json.RawMessage, error) {
	var conf siConfig
	if len(cfg) > 0 {
		if err := json.Unmarshal(cfg, &conf); err != nil {
			return nil, fmt.Errorf("si config: %w", err)
		}
	}
	if conf.Themes <= 0 {
		conf.Themes = games.SIThemeCount
	}
	_, stateJSON := games.SIEmptyGameJSON("", "", conf.Themes, conf.Participants)
	return stateJSON, nil
}

func (si) Score(cfg, stateJSON json.RawMessage) ([]structure.SlotOutcome, error) {
	var state games.KSIState
	if err := json.Unmarshal(stateJSON, &state); err != nil {
		return nil, fmt.Errorf("si state: %w", err)
	}
	ranked, err := games.ComputeKSIResults(string(cfg), string(stateJSON), store.QuestionValues[:])
	if err != nil {
		return nil, fmt.Errorf("si score: %w", err)
	}
	outcomes := make([]structure.SlotOutcome, len(state.Participants))
	for i := range outcomes {
		outcomes[i] = structure.SlotOutcome{Metrics: map[string]float64{}}
	}
	for _, player := range ranked {
		metrics := map[string]float64{
			"total": float64(player.Total),
			"plus":  float64(player.Plus),
		}
		for _, value := range store.QuestionValues {
			metrics[fmt.Sprintf("taken%d", value)] = float64(player.Correct[value])
		}
		outcomes[player.Index] = structure.SlotOutcome{Place: player.Place, Metrics: metrics}
	}
	return outcomes, nil
}
