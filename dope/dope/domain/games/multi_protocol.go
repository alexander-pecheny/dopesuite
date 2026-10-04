package games

import (
	"encoding/json"
	"fmt"

	"dope/dope/domain/structure"
	dopestrings "dope/i18nstrings"
)

// multiFormat is several minigames in one flat sheet; the DSL cannot
// describe it, since its shape is its minigames.
var multiFormat = Definition{Code: Multi, Label: dopestrings.Default.Games.Multi.Label(), Title: dopestrings.Default.Host.Games.TypeMulti(),
	Page: "static/multi.html", Flat: true, Divisions: true, DSL: DSLRefused,
	Sheets: SheetsMulti, Journal: JournalEvents, Protocol: multi{}}

// multi wraps ComputeMultiResults: state is MultiState and the
// match config is the Multi scheme document, whose minigames say how wide
// each sheet is and whose sorting says what breaks a tie on the total.
// Declined teams keep their slot but stay unplaced, as in KSI.
type multi struct{}

func (multi) Code() string { return "multi" }

func (multi) Params() []Param { return nil }

func (multi) TeamBlob() bool { return false }

// TakesGuests: the one-off teams of a music quiz sit in a Multi's document.
func (multi) TakesGuests() bool { return true }

func (multi) Started(state json.RawMessage) bool { return false }

// Metrics: the total, Σ+ and each minigame's subtotal. The minigames are the
// scheme's, so what a scheme may rank on depends on the config — which is
// why Metrics takes it.
func (multi) Metrics(cfg json.RawMessage) []string {
	var scheme MultiScheme
	if len(cfg) > 0 {
		_ = json.Unmarshal(cfg, &scheme)
	}
	return MultiMetricNames(scheme.Minigames)
}

func (multi) RatingRosterStateKey() string { return "participants" }

func (multi) EmptyState(cfg json.RawMessage) (json.RawMessage, error) {
	var scheme MultiScheme
	if len(cfg) > 0 {
		if err := json.Unmarshal(cfg, &scheme); err != nil {
			return nil, fmt.Errorf("multi config: %w", err)
		}
	}
	_, stateJSON := MultiEmptyGameJSON("", "", scheme.Minigames, scheme.Sorting)
	return stateJSON, nil
}

func (multi) Seats(stateJSON json.RawMessage) []Seat {
	var state MultiState
	_ = json.Unmarshal(stateJSON, &state)
	seats := make([]Seat, len(state.Participants))
	for i, p := range state.Participants {
		seats[i] = Seat{Number: int64(p.Number), Name: p.Name, Declined: KSIParticipantDeclined(state.Declined, p)}
	}
	return seats
}

// EnteredSeats: every minigame is a grid of one row per team, so a team
// carries something entered when any of its cells is non-zero, or when the host
// has marked it as having refused to play on. A row of nothing but zeroes reads
// as untouched, which is what an empty sheet of a {0,1} minigame is.
func (multi) EnteredSeats(stateJSON json.RawMessage) []bool {
	var state MultiState
	_ = json.Unmarshal(stateJSON, &state)
	out := make([]bool, len(state.Participants))
	for i, p := range state.Participants {
		out[i] = KSIParticipantDeclined(state.Declined, p)
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
	var state MultiState
	if err := json.Unmarshal(stateJSON, &state); err != nil {
		return nil, fmt.Errorf("multi state: %w", err)
	}
	ranked, err := ComputeMultiResults(string(cfg), string(stateJSON))
	if err != nil {
		return nil, fmt.Errorf("multi score: %w", err)
	}
	outcomes := make([]structure.SlotOutcome, len(state.Participants))
	for i := range outcomes {
		outcomes[i] = structure.SlotOutcome{Metrics: map[string]float64{}}
	}
	for _, team := range ranked {
		metrics := map[string]float64{
			"total":       float64(team.Total),
			"plus":        float64(team.Plus),
			MultiPlaceSum: team.PlaceSum,
		}
		for g, subtotal := range team.Games {
			metrics[fmt.Sprintf("game%d", g+1)] = float64(subtotal)
		}
		outcomes[team.Index] = structure.SlotOutcome{Place: team.Place, Metrics: metrics}
	}
	return outcomes, nil
}

// A Multi's minigames and the fest's tiebreak, which a clear keeps: it wipes
// what was played, not what the Game is.
func (multi) PristineGame(slug, title string, shape Shape) ([]byte, []byte, error) {
	scheme, state := MultiEmptyGameJSON(slug, title, shape.Minigames, shape.Sorting)
	return scheme, state, nil
}

func (multi) ShapeOf(schemeJSON string) Shape {
	var sc MultiScheme
	_ = json.Unmarshal([]byte(schemeJSON), &sc)
	return Shape{Minigames: sc.Minigames, Sorting: sc.Sorting}
}

// KeepOnClear keeps a Multi's guest teams, which are part of who plays.
func (multi) KeepOnClear(oldScheme, _ string, scheme, state []byte) ([]byte, []byte, error) {
	return KeepMultiGuests(oldScheme, scheme, state)
}

// Multi carries its roster the way KSI does — the participants list plus
// one cell grid per minigame, each row a team — so the fold is the same:
// rewrite the list, and follow every team's row across the reorder. The
// game's guest teams are not on the roster, so they stay, after the fest's.
func (multi) FoldRoster(schemeJSON, stateJSON string, teams []RosterTeam, _ map[int]int) ([]byte, []byte, error) {
	state, err := RawJSONObject(stateJSON)
	if err != nil {
		return nil, nil, err
	}
	oldParticipants := ParseKSIParticipants(state["participants"])
	participants := append(teamParticipantsFromRoster(teams), MultiGuests(oldParticipants)...)
	return rewriteMultiParticipants(schemeJSON, state, oldParticipants, participants)
}

// rewriteMultiParticipants writes a new team list into a Multi document, in
// the scheme and in the state, and moves every team's cells to its new row.
func rewriteMultiParticipants(schemeJSON string, state map[string]json.RawMessage, oldParticipants, participants []KSIParticipant) ([]byte, []byte, error) {
	scheme, err := RawJSONObject(schemeJSON)
	if err != nil {
		return nil, nil, err
	}
	participantsJSON, err := json.Marshal(participants)
	if err != nil {
		return nil, nil, err
	}
	scheme["participants"] = participantsJSON
	schemeOut, err := json.Marshal(scheme)
	if err != nil {
		return nil, nil, err
	}

	var minigames struct {
		Minigames []MultiGame `json:"minigames"`
	}
	_ = json.Unmarshal(schemeOut, &minigames)

	state["participants"] = participantsJSON

	var grids []map[string]json.RawMessage
	if raw, ok := state["games"]; ok && len(raw) > 0 {
		_ = json.Unmarshal(raw, &grids)
	}
	for len(grids) < len(minigames.Minigames) {
		grids = append(grids, map[string]json.RawMessage{})
	}
	grids = grids[:len(minigames.Minigames)]
	for i, game := range minigames.Minigames {
		if grids[i] == nil {
			grids[i] = map[string]json.RawMessage{}
		}
		var cells [][]int
		if raw, ok := grids[i]["cells"]; ok && len(raw) > 0 {
			_ = json.Unmarshal(raw, &cells)
		}
		cellsJSON, err := json.Marshal(RemapAnswerMatrix(cells, oldParticipants, participants, len(game.Columns)))
		if err != nil {
			return nil, nil, err
		}
		grids[i]["cells"] = cellsJSON
	}
	gridsJSON, err := json.Marshal(grids)
	if err != nil {
		return nil, nil, err
	}
	state["games"] = gridsJSON
	stateOut, err := json.Marshal(state)
	return schemeOut, stateOut, err
}
