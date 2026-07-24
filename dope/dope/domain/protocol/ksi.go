package protocol

import (
	"encoding/json"
	"fmt"

	"dope/dope/domain/games"
	"dope/dope/domain/structure"
	"dope/dope/storage/store"
)

func init() { Register(ksi{}) }

// ksi wraps games.ComputeKSIResults: state is games.KSIState, the match config
// is the KSI scheme document (its stickers block selects the stickers
// variant). Declined teams keep their slot but stay unplaced.
type ksi struct{}

func (ksi) Code() string { return "ksi" }

func (ksi) RatingRosterStateKey() string { return "participants" }

func (ksi) EmptyState(cfg json.RawMessage) (json.RawMessage, error) {
	var conf struct {
		Themes   int             `json:"themes"`
		Stickers json.RawMessage `json:"stickers"`
	}
	if len(cfg) > 0 {
		if err := json.Unmarshal(cfg, &conf); err != nil {
			return nil, fmt.Errorf("ksi config: %w", err)
		}
	}
	if conf.Themes <= 0 {
		conf.Themes = games.KSIThemeCount
	}
	var stateJSON []byte
	if len(conf.Stickers) > 0 {
		_, stateJSON = games.KSIStickersEmptyGameJSON("", "", conf.Themes, conf.Stickers)
	} else {
		_, stateJSON = games.KSIEmptyGameJSON("", "", conf.Themes)
	}
	return stateJSON, nil
}

func (ksi) Score(cfg, stateJSON json.RawMessage) ([]structure.SlotOutcome, error) {
	var state games.KSIState
	if err := json.Unmarshal(stateJSON, &state); err != nil {
		return nil, fmt.Errorf("ksi state: %w", err)
	}
	ranked, err := games.ComputeKSIResults(string(cfg), string(stateJSON), store.QuestionValues[:])
	if err != nil {
		return nil, fmt.Errorf("ksi score: %w", err)
	}
	outcomes := make([]structure.SlotOutcome, len(state.Participants))
	for i := range outcomes {
		outcomes[i] = structure.SlotOutcome{Metrics: map[string]float64{}}
	}
	for _, team := range ranked {
		outcomes[team.Index] = structure.SlotOutcome{
			Place: team.Place,
			Metrics: map[string]float64{
				"total": float64(team.Total),
				"plus":  float64(team.Plus),
			},
		}
	}
	return outcomes, nil
}
