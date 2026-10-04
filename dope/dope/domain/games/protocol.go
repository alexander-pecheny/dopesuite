package games

// This file is the Protocol half of a format's registration (the unified
// model, docs/unified-model.md, ADR-0001/0002): the in-match ruleset a bout
// plays. A Protocol owns one match's state shape (a JSON document), its
// scoring, and nothing else — the Structure layer consumes the scorer's
// per-slot output (place + metrics) and never looks inside the state. Each
// format's Protocol sits beside its document in <format>_protocol.go and
// rides on its Definition; an optional capability is asked for with As.

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

// ProtocolOf returns the Protocol a game type's bouts play; ok is false for
// an unknown type.
func ProtocolOf(code string) (Protocol, bool) {
	d, ok := byCode[code]
	if !ok {
		return nil, false
	}
	return d.Protocol, true
}

// As returns a game type's Protocol as one of its optional capabilities, the
// interfaces below (ADR-0012's pattern): Seater, Grower, PlayersUser,
// PristineBuilder and the rest. ok is false for an unknown type and for a
// Protocol that does not have the capability. A capability whose absence
// means something other than "no" keeps a named function here instead
// (Started, UsesFestNumbers, ValidateEdit, EditableWhenFinished).
func As[C any](code string) (C, bool) {
	p, _ := ProtocolOf(code)
	c, ok := p.(C)
	return c, ok
}

// The store, a leaf that cannot ask, learns here what it needs to know of
// each format's Protocol: which are team blobs, which seat named players,
// which bout score a sheet prints, and the seat cap a match plays at when no
// stage config says. A Definition whose Protocol is missing or answers to
// another code is a programming error.
func init() {
	for _, d := range registry {
		p := d.Protocol
		if p == nil || p.Code() != d.Code {
			panic("games: format " + d.Code + " registers no Protocol of its own code")
		}
		if p.TeamBlob() {
			store.RegisterTeamBlob(p.Code())
		}
		if seater, ok := p.(SeatsPlayers); ok && seater.SeatsPlayers() {
			store.RegisterSeatRoster(p.Code())
		}
		if scorer, ok := p.(ScoreMetricer); ok {
			store.RegisterScoreMetric(p.Code(), scorer.ScoreMetric())
		}
		for _, param := range p.Params() {
			if param.Key == SeatsParam && param.Default > 0 {
				store.RegisterSeatCap(p.Code(), param.Default)
			}
		}
	}
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

// SeatsParam is the DSL key naming how many players a team sends to one theme
// — the seating. A Protocol that has the notion declares it with a Default,
// which is the cap a match plays at when no Block overrides it.
const SeatsParam = "players"

// Metrics returns the metrics a protocol declares for a match config, or nil
// for an unknown code.
func Metrics(code string, cfg json.RawMessage) []string {
	if p, ok := ProtocolOf(code); ok {
		return p.Metrics(cfg)
	}
	return nil
}

// Params returns the DSL params a protocol accepts, or nil for an unknown code.
func Params(code string) []Param {
	if p, ok := ProtocolOf(code); ok {
		return p.Params()
	}
	return nil
}

// Started asks a game's Protocol whether a host has entered anything into a
// match; an unknown Protocol counts as started, so nothing of it is touched.
func Started(code, state string) bool {
	p, ok := ProtocolOf(code)
	return !ok || p.Started(json.RawMessage(state))
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

// Entered is a Seater that can also say, seat by seat, whether a host has
// entered anything against that seat, aligned with Seats. Started answers the
// same question for a whole document; this one answers it per team, which is
// what a roster re-import needs before it takes a team off the roster and its
// column with it.
type Entered interface {
	Seater
	EnteredSeats(state json.RawMessage) []bool
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

// Grower is implemented by a Protocol whose started bout can take more seats
// and keep what it holds: a troika game's written qualifier, where a late troika
// writes the same paper. ok is false when this document cannot grow.
type Grower interface {
	GrowSeats(state json.RawMessage, seats int) (json.RawMessage, bool, error)
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
	late, ok := As[LateEditor](code)
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
	if n, ok := As[FestNumbering](code); ok {
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
	if v, ok := As[EditValidator](code); ok {
		return v.ValidateEdit(prev, next)
	}
	return nil
}

// ScoreMetricer is a Protocol whose bout score, as a sheet prints it, is one
// of its metrics rather than the total: brain counts the questions a side
// took. The store reads it (store.ScoreMetric) for the match summaries, and
// the replay harness for the transcript's Σ.
type ScoreMetricer interface {
	ScoreMetric() string
}

// UsedPlayer is one player a bout's document names against a team: by id
// where the document records ids, by Name where it records the name.
type UsedPlayer struct {
	Team   int64
	Player int64
	Name   string
}

// PlayersUser is a Protocol whose bouts name the players who answered for a
// team. A hand roster may not drop a player a bout names
// (Definition.HandRoster), so every format with hand rosters has one.
// seats are the bout's Participants by slot index (0 for an empty seat).
type PlayersUser interface {
	UsedPlayers(state json.RawMessage, seats []int64) []UsedPlayer
}

// GuestHost is a Protocol whose document may list guest teams a host adds by
// name on the Game's page (CONTEXT.md, Guest team).
type GuestHost interface {
	TakesGuests() bool
}
