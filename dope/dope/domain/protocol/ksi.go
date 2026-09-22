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

func (ksi) Params() []Param { return []Param{{Key: "themes", Config: "themes"}} }

func (ksi) TeamBlob() bool { return false }

func (ksi) Started(state json.RawMessage) bool { return false }

// Metrics: the total and the sum of positive answers.
func (ksi) Metrics(json.RawMessage) []string { return []string{"total", "plus"} }

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

func (ksi) Seats(stateJSON json.RawMessage) []Seat {
	var state struct {
		Participants json.RawMessage `json:"participants"`
		Declined     map[string]bool `json:"declined"`
	}
	_ = json.Unmarshal(stateJSON, &state)
	participants := games.ParseKSIParticipants(state.Participants)
	seats := make([]Seat, len(participants))
	for i, p := range participants {
		seats[i] = Seat{Number: int64(p.Number), Name: p.Name, Declined: games.KSIParticipantDeclined(state.Declined, p)}
	}
	return seats
}

// EnteredSeats: a KSI sheet is one row per team, so a team carries something
// entered when any of its answer cells is filled, when it has chosen a sticker,
// or when the host has marked it as having refused to play on.
func (ksi) EnteredSeats(stateJSON json.RawMessage) []bool {
	var state struct {
		Participants json.RawMessage `json:"participants"`
		Declined     map[string]bool `json:"declined"`
		Stickers     [][]string      `json:"stickers"`
		Themes       []struct {
			Answers [][]string `json:"answers"`
		} `json:"themes"`
	}
	_ = json.Unmarshal(stateJSON, &state)
	participants := games.ParseKSIParticipants(state.Participants)
	out := make([]bool, len(participants))
	for i, p := range participants {
		out[i] = games.KSIParticipantDeclined(state.Declined, p)
	}
	markRow := func(row int, filled bool) {
		if filled && row < len(out) {
			out[row] = true
		}
	}
	for _, theme := range state.Themes {
		for row, answers := range theme.Answers {
			markRow(row, anyFilled(answers))
		}
	}
	for row, stickers := range state.Stickers {
		markRow(row, anyFilled(stickers))
	}
	return out
}

func anyFilled(cells []string) bool {
	for _, cell := range cells {
		if cell != "" {
			return true
		}
	}
	return false
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
