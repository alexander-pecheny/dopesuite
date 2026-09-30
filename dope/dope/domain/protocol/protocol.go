// Package protocol holds the Protocol half of the unified model
// (docs/unified-model.md, ADR-0001/0002): the registry of in-match rulesets.
// A Protocol owns one match's state shape (a JSON document), its scoring, and
// nothing else — the Structure layer consumes the scorer's per-slot output
// (place + metrics) and never looks inside the state.
//
// Like domain/games this package is a leaf: storage/store for the shared state
// vocabulary, never the server, HTTP or DB layers.
package protocol

import (
	"encoding/json"

	"dope/dope/domain/structure"
	"dope/dope/storage/store"
)

// Protocol is a registered in-match ruleset. EmptyState builds the pristine
// match state for a match config (participant count, tour composition, …).
// Score maps state to per-slot outcomes in slot order; it leaves Participant
// zero — who sits where is the Structure layer's knowledge. Both take the
// match config because scoring rules legitimately live there (tour
// composition, sticker rules, question values).
//
// Metrics names every metric Score can put on a slot. It is what makes a
// number rankable: a scheme may sort a group or a reseed by any name declared
// here, and the compiler rejects the ones nobody measures. A Protocol that
// starts measuring something new declares it and it is rankable everywhere —
// no Go change anywhere else (ADR-0008). It takes the match config because a
// Protocol whose document is configured — Multi, whose minigames are
// named by the scheme — measures a metric per configured part, and those
// names cannot be a constant list.
//
// Params are the DSL keys the Protocol accepts; TeamBlob says its state is
// the team-keyed blob EK edits and replays (matchops/MatchBlob), not an
// opaque document; Started says a host has entered something, which keeps a
// match's seats through a re-seed. No caller decides any of this by game type.
type Protocol interface {
	Code() string
	Params() []Param
	Metrics(cfg json.RawMessage) []string
	TeamBlob() bool
	EmptyState(cfg json.RawMessage) (json.RawMessage, error)
	Started(state json.RawMessage) bool
	Score(cfg, state json.RawMessage) ([]structure.SlotOutcome, error)
}

// Seat is one Participant a flat document lists: its number in the Game and
// the name and city the document knows it by.
type Seat struct {
	Number int64
	Name   string
	City   string
	// Declined marks a team that refused to play on — KSI's refusals tab —
	// so a seeding drawn from this game skips it.
	Declined bool
}

// Seater is the Protocol of a flat format — one Block, one match, the whole
// document on it — declaring who sits at that match: the Participants its
// document lists, in the order Score returns their outcomes. The Structure
// seats them from this, so a flat game ranks like every other.
type Seater interface {
	Seats(state json.RawMessage) []Seat
}

// Seats returns the seats a flat document lists, and whether the Protocol is
// a flat one at all.
func Seats(code string, state json.RawMessage) ([]Seat, bool) {
	p, ok := Get(code)
	if !ok {
		return nil, false
	}
	seater, ok := p.(Seater)
	if !ok {
		return nil, false
	}
	return seater.Seats(state), true
}

// Entered is a Seater that can also say, seat by seat, whether a host has
// entered anything against that seat. Started answers the same question for a
// whole document; this one answers it per team, which is what a roster
// re-import needs before it takes a team off the roster and its column with
// it.
type Entered interface {
	EnteredSeats(state json.RawMessage) []bool
}

// EnteredSeats reports, aligned with Seats, which seats of a flat document
// already carry something a host entered. ok is false for a Protocol that does
// not answer.
func EnteredSeats(code string, state json.RawMessage) ([]bool, bool) {
	p, found := Get(code)
	if !found {
		return nil, false
	}
	entered, ok := p.(Entered)
	if !ok {
		return nil, false
	}
	return entered.EnteredSeats(state), true
}

// Param is one DSL key a Protocol accepts and the stage-config field it
// compiles to: true/false when Bool, a bracketed list of counts when List,
// else a count, written as Default when the scheme is silent and Default is
// not zero.
type Param struct {
	Key     string
	Config  string
	Bool    bool
	List    bool
	Default int
}

// Metrics returns the metrics a protocol declares for a match config, or nil
// for an unknown code.
func Metrics(code string, cfg json.RawMessage) []string {
	if p, ok := Get(code); ok {
		return p.Metrics(cfg)
	}
	return nil
}

// Params returns the DSL params a protocol accepts, or nil for an unknown code.
func Params(code string) []Param {
	if p, ok := Get(code); ok {
		return p.Params()
	}
	return nil
}

// Started asks a game's Protocol whether a host has entered anything into a
// match; an unknown Protocol counts as started, so nothing of it is touched.
func Started(code, state string) bool {
	p, ok := Get(code)
	return !ok || p.Started(json.RawMessage(state))
}

// registry is the single source of truth for known protocols. Add a format by
// registering a Protocol — never by a switch on protocol codes elsewhere.
var registry = map[string]Protocol{}

// Register adds a protocol; duplicate codes are a programming error. The
// store learns which Protocols are team blobs here, being a leaf that cannot ask.
func Register(p Protocol) {
	if _, dup := registry[p.Code()]; dup {
		panic("protocol: duplicate protocol " + p.Code())
	}
	registry[p.Code()] = p
	if p.TeamBlob() {
		store.RegisterTeamBlob(p.Code())
	}
	if seater, ok := p.(SeatsPlayers); ok && seater.SeatsPlayers() {
		store.RegisterSeatRoster(p.Code())
	}
	// How many players a theme seats when no stage config says: the store
	// loads matches without knowing Protocols, so the default travels down
	// with the registration rather than being looked up by game type.
	for _, param := range p.Params() {
		if param.Key == SeatsParam && param.Default > 0 {
			store.RegisterSeatCap(p.Code(), param.Default)
		}
	}
}

// SeatsParam is the DSL key naming how many players a team sends to one theme
// — the seating. A Protocol that has the notion declares it with a Default,
// which is the cap a match plays at when no Block overrides it.
const SeatsParam = "players"

// Get looks up a registered protocol by code.
func Get(code string) (Protocol, bool) {
	p, ok := registry[code]
	return p, ok
}

// SeatedScorer is implemented by a Protocol whose document is keyed by
// Participant rather than by slot — Hamsa's is, so that a re-seat can never
// move one team's marks onto another. Score cannot answer in slot order out of
// such a document, so the scorer hands over the seats it holds; a seat that
// entered nothing still took a place, which is why the seating decides the
// rows and the document does not.
type SeatedScorer interface {
	ScoreSeated(cfg, state json.RawMessage, seats []int64) ([]structure.SlotOutcome, error)
}

// ScoreSeats scores a match through its Protocol, telling a SeatedScorer who
// is sitting at it. Every other Protocol answers in slot order already.
func ScoreSeats(p Protocol, cfg, state json.RawMessage, seats []int64) ([]structure.SlotOutcome, error) {
	if seated, ok := p.(SeatedScorer); ok {
		return seated.ScoreSeated(cfg, state, seats)
	}
	return p.Score(cfg, state)
}

// LateEditor is implemented by a Protocol whose document keeps an entry a
// host makes after a bout is finished: Hamsa's lot among teams that share a
// place, which is only known once the bout is over. Every other path stays
// closed on a finished bout.
type LateEditor interface {
	EditableWhenFinished(path []json.RawMessage) bool
}

// EditableWhenFinished reports whether every one of these paths may be written
// on a finished bout of the Protocol.
func EditableWhenFinished(code string, paths [][]json.RawMessage) bool {
	p, ok := Get(code)
	if !ok {
		return false
	}
	late, ok := p.(LateEditor)
	if !ok || len(paths) == 0 {
		return false
	}
	for _, path := range paths {
		if !late.EditableWhenFinished(path) {
			return false
		}
	}
	return true
}

// SeatsPlayers is implemented by a Protocol whose matches field named
// players — Troika records which of a team's three sat in which chair — so
// each seat wants its roster on the view. A team-blob Protocol already gets
// one.
type SeatsPlayers interface {
	SeatsPlayers() bool
}

// RatingRosterOwner is implemented by protocols whose state embeds a roster
// owned by a rating.chgk.info import: the named top-level state key is
// immutable under host edits (re-import to change it).
type RatingRosterOwner interface {
	RatingRosterStateKey() string
}

// FestNumbering is implemented by a Protocol that can say its document never
// refers to a fest team by its number. The numbering guard exists because a
// flat document finds a Participant by number. A Protocol whose seats are
// nobody on the fest roster (a friendship cup's tables) has nothing the guard
// protects, so an unnumbered fest team must not stop its entry.
type FestNumbering interface {
	UsesFestNumbers() bool
}

// UsesFestNumbers reports whether a game type's document depends on the fest
// teams' numbers. It is true unless the Protocol says otherwise, and for an
// unknown type.
func UsesFestNumbers(code string) bool {
	p, ok := Get(code)
	if !ok {
		return true
	}
	if n, ok := p.(FestNumbering); ok {
		return n.UsesFestNumbers()
	}
	return true
}

// EditValidator is implemented by a Protocol whose document holds entries a
// host types in free form, which the scorer cannot take as they come (a
// friendship cup's players). ValidateEdit sees the document before and after
// one edit and refuses the edit, in the host's words, when it leaves the
// document wrong. Nothing of a refused edit is written.
type EditValidator interface {
	ValidateEdit(prev, next []byte) error
}

// ValidateEdit asks a game type's Protocol whether an edit of its document
// may stand. A Protocol that does not validate accepts every edit.
func ValidateEdit(code string, prev, next []byte) error {
	p, ok := Get(code)
	if !ok {
		return nil
	}
	if v, ok := p.(EditValidator); ok {
		return v.ValidateEdit(prev, next)
	}
	return nil
}

// RatingRosterStateKey returns the protocol's immutable rating-roster state
// key, if the protocol declares one.
func RatingRosterStateKey(code string) (string, bool) {
	if p, ok := Get(code); ok {
		if owner, ok := p.(RatingRosterOwner); ok {
			return owner.RatingRosterStateKey(), true
		}
	}
	return "", false
}
