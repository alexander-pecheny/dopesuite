package protocol

import (
	"encoding/json"
	"fmt"

	"dope/dope/domain/games"
	"dope/dope/domain/structure"
)

func init() { Register(multi{}) }

// multi wraps games.ComputeMultiResults: state is games.MultiState and the
// match config is the Multi scheme document, whose minigames say how wide
// each sheet is and whose sorting says what breaks a tie on the total.
// Declined teams keep their slot but stay unplaced, as in KSI.
type multi struct{}

func (multi) Code() string { return "multi" }

func (multi) Params() []Param { return nil }

func (multi) TeamBlob() bool { return false }

func (multi) Started(state json.RawMessage) bool { return false }

// Metrics: the total, Σ+ and each minigame's subtotal. The minigames are the
// scheme's, so what a scheme may rank on depends on the config — which is
// why Metrics takes it.
func (multi) Metrics(cfg json.RawMessage) []string {
	var scheme games.MultiScheme
	if len(cfg) > 0 {
		_ = json.Unmarshal(cfg, &scheme)
	}
	return games.MultiMetricNames(scheme.Minigames)
}

func (multi) RatingRosterStateKey() string { return "participants" }

func (multi) EmptyState(cfg json.RawMessage) (json.RawMessage, error) {
	var scheme games.MultiScheme
	if len(cfg) > 0 {
		if err := json.Unmarshal(cfg, &scheme); err != nil {
			return nil, fmt.Errorf("multi config: %w", err)
		}
	}
	_, stateJSON := games.MultiEmptyGameJSON("", "", scheme.Minigames, scheme.Sorting)
	return stateJSON, nil
}

func (multi) Seats(stateJSON json.RawMessage) []Seat {
	var state games.MultiState
	_ = json.Unmarshal(stateJSON, &state)
	seats := make([]Seat, len(state.Participants))
	for i, p := range state.Participants {
		seats[i] = Seat{Number: int64(p.Number), Name: p.Name, Declined: games.KSIParticipantDeclined(state.Declined, p)}
	}
	return seats
}

// EnteredSeats: every minigame is a grid of one row per team, so a team
// carries something entered when any of its cells is non-zero, or when the host
// has marked it as having refused to play on. A row of nothing but zeroes reads
// as untouched, which is what an empty sheet of a {0,1} minigame is.
func (multi) EnteredSeats(stateJSON json.RawMessage) []bool {
	var state games.MultiState
	_ = json.Unmarshal(stateJSON, &state)
	out := make([]bool, len(state.Participants))
	for i, p := range state.Participants {
		out[i] = games.KSIParticipantDeclined(state.Declined, p)
	}
	for _, game := range state.Games {
		for row, cells := range game.Cells {
			if row >= len(out) {
				break
			}
			for _, cell := range cells {
				if cell != 0 {
					out[row] = true
					break
				}
			}
		}
	}
	return out
}

func (multi) Score(cfg, stateJSON json.RawMessage) ([]structure.SlotOutcome, error) {
	var state games.MultiState
	if err := json.Unmarshal(stateJSON, &state); err != nil {
		return nil, fmt.Errorf("multi state: %w", err)
	}
	ranked, err := games.ComputeMultiResults(string(cfg), string(stateJSON))
	if err != nil {
		return nil, fmt.Errorf("multi score: %w", err)
	}
	outcomes := make([]structure.SlotOutcome, len(state.Participants))
	for i := range outcomes {
		outcomes[i] = structure.SlotOutcome{Metrics: map[string]float64{}}
	}
	for _, team := range ranked {
		metrics := map[string]float64{
			"total": float64(team.Total),
			"plus":  float64(team.Plus),
		}
		for g, subtotal := range team.Games {
			metrics[fmt.Sprintf("game%d", g+1)] = float64(subtotal)
		}
		outcomes[team.Index] = structure.SlotOutcome{Place: team.Place, Metrics: metrics}
	}
	return outcomes, nil
}
