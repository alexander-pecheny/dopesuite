package protocol

import (
	"encoding/json"

	"dope/dope/domain/games"
	"dope/dope/domain/structure"
)

func init() { Register(kd{}) }

// kd is the friendship cup (ADR-0026): an OD document whose teams are the
// tables. It scores the tables as OD does, but a table is nobody on the fest
// roster: it seats under a negative number, so no fest Participant is minted
// for it (flatgame skips such seats, as it does Multi's guest teams) — a
// table numbered 1 would otherwise rename fest team 1. It folds no roster in
// either: a roster import must leave the tables alone.
type kd struct{}

func (kd) Code() string { return games.KD }

func (kd) Params() []Param { return od{}.Params() }

func (kd) TeamBlob() bool { return false }

func (kd) Started(json.RawMessage) bool { return false }

func (kd) Metrics(cfg json.RawMessage) []string { return od{}.Metrics(cfg) }

// RatingRosterStateKey keeps the tables fixed: a PATCH may not rename or
// renumber them, only the players and the answers change.
func (kd) RatingRosterStateKey() string { return "teams" }

func (kd) EmptyState(cfg json.RawMessage) (json.RawMessage, error) {
	return od{}.EmptyState(cfg)
}

func (kd) Seats(stateJSON json.RawMessage) []Seat {
	var state games.ODState
	_ = json.Unmarshal(stateJSON, &state)
	seats := make([]Seat, len(state.Teams))
	for i, table := range state.Teams {
		seats[i] = Seat{Number: -(int64(i) + 1), Name: table.Name}
	}
	return seats
}

// UsesFestNumbers is false: the tables seat under negative numbers and no
// fest team is looked up by number, so the numbering guard does not apply to
// a friendship cup (ADR-0026).
func (kd) UsesFestNumbers() bool { return false }

// ValidateEdit refuses an edit that leaves the players wrong: a card that is
// not a whole number from 1, a card past what the tables tell apart, a card
// passed to somebody else while its holder is still registered, a player
// with no name.
func (kd) ValidateEdit(prev, next []byte) error {
	return games.ValidateKDPlayersEdit(prev, next)
}

func (kd) Score(cfg, stateJSON json.RawMessage) ([]structure.SlotOutcome, error) {
	return od{}.Score(cfg, stateJSON)
}
