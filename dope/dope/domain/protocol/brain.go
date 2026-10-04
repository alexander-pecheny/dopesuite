package protocol

import (
	"encoding/json"
	"fmt"
	"strings"

	"dope/dope/domain/games"
	"dope/dope/domain/structure"
)

func init() { Register(brain{}) }

// brain wraps games.ComputeBrainResults: state is games.BrainState, the match
// config is the brain scheme document (its questions count sizes the match).
// Places track the running score; the rr stage awards group points from them
// only once the match is finished (matches.status, structure.MatchOutcome).
type brain struct{}

func (brain) Code() string { return "brain" }

// questions is always written: reseed share metrics divide by it.
func (brain) Params() []Param {
	return []Param{
		{Key: "questions", Config: "questions", Default: games.BrainQuestionCount},
		{Key: "tiebreak_questions", Config: "tiebreakQuestions", Bool: true},
	}
}

func (brain) TeamBlob() bool { return false }

func (brain) Started(state json.RawMessage) bool { return games.BrainStateStarted(string(state)) }

// Metrics: questions taken, and the same without the shootout — the share
// denominator on a reseed counts the match's base questions.
func (brain) Metrics(json.RawMessage) []string { return []string{"taken", structure.MetricTakenBase} }

// ScoreMetric: a brain bout's score is the questions a side took.
func (brain) ScoreMetric() string { return "taken" }

// UsedPlayers: a brain row records the player who buzzed by name, against the
// side's seat.
func (brain) UsedPlayers(stateJSON json.RawMessage, seats []int64) []UsedPlayer {
	var state games.BrainState
	if json.Unmarshal(stateJSON, &state) != nil {
		return nil
	}
	var out []UsedPlayer
	for slot, team := range seats {
		if slot >= len(state.Teams) || team == 0 {
			continue
		}
		for _, row := range state.Teams[slot].Rows {
			if name := strings.TrimSpace(row.Player); name != "" {
				out = append(out, UsedPlayer{Team: team, Name: name})
			}
		}
	}
	return out
}

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
				"taken":                   float64(team.Taken),
				structure.MetricTakenBase: float64(team.TakenBase),
			},
		}
	}
	return outcomes, nil
}
