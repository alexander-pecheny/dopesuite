package protocol

import (
	"encoding/json"
	"fmt"

	"dope/dope/domain/structure"
	"dope/dope/storage/store"
)

func init() { Register(ek{}) }

// ek wraps the existing EK (эрудит-квартет) pure scoring: state is
// store.MatchState, totals come from store.ScoreParticipant, and places are the
// host-entered ones (auto-placement arrives with the migration; parity with
// the current system requires manual places for now).
type ek struct{}

func (ek) Code() string { return "ek" }

func (ek) Params() []Param { return []Param{{Key: "themes", Config: "themes"}} }

func (ek) TeamBlob() bool { return true }

// An ЭК бой is a seat plan until it is finished; a re-seed may still move its
// teams, and their marks go with them.
func (ek) Started(state json.RawMessage) bool { return false }

func (ek) Metrics() []string {
	names := []string{"total", "plus", "shootoutTotal", "tiebreak"}
	for _, value := range store.QuestionValues {
		names = append(names, fmt.Sprintf("correct_%d", value), fmt.Sprintf("wrong_%d", value))
	}
	return names
}

type ekConfig struct {
	Participants int `json:"participants"`
	Themes       int `json:"themes"`
}

func (ek) EmptyState(cfg json.RawMessage) (json.RawMessage, error) {
	var conf ekConfig
	if len(cfg) > 0 {
		if err := json.Unmarshal(cfg, &conf); err != nil {
			return nil, fmt.Errorf("ek config: %w", err)
		}
	}
	state := store.MatchState{Participants: make([]store.ParticipantState, conf.Participants)}
	store.NormalizeStateTo(&state, conf.Themes)
	return json.Marshal(state)
}

func (ek) Score(cfg, stateJSON json.RawMessage) ([]structure.SlotOutcome, error) {
	var state store.MatchState
	if err := json.Unmarshal(stateJSON, &state); err != nil {
		return nil, fmt.Errorf("ek state: %w", err)
	}
	view := store.BuildView(state)
	outcomes := make([]structure.SlotOutcome, len(view.Participants))
	for i, team := range view.Participants {
		metrics := map[string]float64{
			"total":         float64(team.Total),
			"plus":          float64(team.Plus),
			"shootoutTotal": float64(team.ShootoutTotal),
			"tiebreak":      float64(team.Tiebreak),
		}
		for k, value := range store.QuestionValues {
			metrics[fmt.Sprintf("correct_%d", value)] = float64(team.CorrectCounts[k])
			metrics[fmt.Sprintf("wrong_%d", value)] = float64(team.WrongCounts[k])
		}
		outcomes[i] = structure.SlotOutcome{Place: team.Place, Metrics: metrics}
	}
	return outcomes, nil
}
