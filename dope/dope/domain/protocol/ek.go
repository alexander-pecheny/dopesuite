package protocol

import (
	"encoding/json"
	"fmt"

	"dope/dope/domain/structure"
	"dope/dope/storage/store"
)

func init() { Register(ek{}) }

// ek wraps the existing EK (эрудит-квартет) pure scoring: state is
// store.MatchState, totals come from store.ScoreTeam, and places are the
// host-entered ones (auto-placement arrives with the migration; parity with
// the current system requires manual places for now).
type ek struct{}

func (ek) Code() string { return "ek" }

type ekConfig struct {
	Participants int `json:"participants"`
}

func (ek) EmptyState(cfg json.RawMessage) (json.RawMessage, error) {
	var conf ekConfig
	if len(cfg) > 0 {
		if err := json.Unmarshal(cfg, &conf); err != nil {
			return nil, fmt.Errorf("ek config: %w", err)
		}
	}
	state := store.MatchState{Teams: make([]store.TeamState, conf.Participants)}
	store.NormalizeState(&state)
	return json.Marshal(state)
}

func (ek) Score(cfg, stateJSON json.RawMessage) ([]structure.SlotOutcome, error) {
	var state store.MatchState
	if err := json.Unmarshal(stateJSON, &state); err != nil {
		return nil, fmt.Errorf("ek state: %w", err)
	}
	view := store.BuildView(state)
	outcomes := make([]structure.SlotOutcome, len(view.Teams))
	for i, team := range view.Teams {
		outcomes[i] = structure.SlotOutcome{
			Place: team.Place,
			Metrics: map[string]float64{
				"total":         float64(team.Total),
				"plus":          float64(team.Plus),
				"shootoutTotal": float64(team.ShootoutTotal),
				"tiebreak":      float64(team.Tiebreak),
			},
		}
	}
	return outcomes, nil
}
