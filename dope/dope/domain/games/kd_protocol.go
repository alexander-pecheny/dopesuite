package games

import (
	"encoding/json"
	"strconv"

	dopestrings "dope/i18nstrings"

	corei18n "pecheny.me/dopecore/i18nstrings"
)

// kdFormat is the friendship cup: an OD document whose teams are the tables,
// riding OD's page. The entry, the detailed sheet and the screen board work
// on tables unchanged, and the page adds the personal standings. Its tables
// carry no Flags, so it offers no divisions.
var kdFormat = Definition{Code: KD, Label: dopestrings.Default.Games.Kd.Label(), Title: dopestrings.Default.Host.Games.TypeKd(),
	Page: "static/od.html", Flat: true, DSL: DSLAccepted,
	Results: kdResults, Sheets: SheetsODTables, Journal: JournalODPatches, Protocol: kd{}}

// A friendship cup answers its personal standings; the tables' own totals are
// the OD sheet's, read from the state.
func kdResults(schemeJSON, stateJSON string) (any, error) {
	return ComputeKDResults(schemeJSON, stateJSON)
}

// kd is the friendship cup (ADR-0026): an OD document whose teams are the
// tables. It plays OD's sheet (odSheet: the params, the metrics, the empty
// state and the scoring), but a table is nobody on the fest roster: it seats
// under a negative number, so no fest Participant is minted for it
// (flatgame skips such seats, as it does Multi's guest teams) — a table
// numbered 1 would otherwise rename fest team 1. It folds no roster in
// either: a roster import must leave the tables alone.
type kd struct{ odSheet }

func (kd) Code() string { return KD }

func (kd) Seats(stateJSON json.RawMessage) []Seat {
	var state ODState
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
	return ValidateKDPlayersEdit(prev, next)
}

// A friendship cup's tables must be a prime, and its tours no more than its
// tables: two cards share a table at most once only while the tours are no
// more than the tables, since in tour n+1 every card is back at its first
// table. The tables are named and numbered here; no roster is folded in.
func (kd) PristineGame(slug, title string, shape Shape) ([]byte, []byte, error) {
	if !IsPrime(shape.Tables) {
		return nil, nil, corei18n.User(dopestrings.Default.Gamebuild.Create.KdTablesPrime(strconv.Itoa(shape.Tables)))
	}
	if len(shape.Tours) > shape.Tables {
		return nil, nil, corei18n.User(dopestrings.Default.Gamebuild.Create.KdToursTables(strconv.Itoa(len(shape.Tours)), strconv.Itoa(shape.Tables)))
	}
	scheme, state := KDEmptyGameJSON(slug, title, shape.Tours, shape.Tables, kdTableName)
	return scheme, state, nil
}

func kdTableName(n int) string { return dopestrings.Default.Gamebuild.Kd.Table(strconv.Itoa(n)) }

func (kd) ShapeOf(schemeJSON string) Shape {
	return Shape{Tours: ParseTourComp(schemeJSON), Tables: KDTables(schemeJSON)}
}

// KeepOnClear keeps a friendship cup's players: clearing it wipes the
// answers, not the registration desk's work. A document that does not parse
// stops the clear, since going on would drop them.
func (kd) KeepOnClear(_, oldState string, scheme, state []byte) ([]byte, []byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(oldState), &fields); err != nil {
		return nil, nil, err
	}
	players := fields["players"]
	if len(players) == 0 {
		return scheme, state, nil
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(state, &doc); err != nil {
		return nil, nil, err
	}
	doc["players"] = players
	out, err := json.Marshal(doc)
	return scheme, out, err
}
