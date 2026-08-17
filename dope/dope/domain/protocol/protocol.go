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
// no Go change anywhere else (ADR-0008).
//
// Params are the DSL keys the Protocol accepts; TeamBlob says its state is
// the team-keyed blob ЭК edits and replays (matchops/MatchBlob), not an opaque
// document; Started says a host has entered something, which keeps a бой's
// seats through a re-seed. No caller decides any of this by game type.
type Protocol interface {
	Code() string
	Params() []Param
	Metrics() []string
	TeamBlob() bool
	EmptyState(cfg json.RawMessage) (json.RawMessage, error)
	Started(state json.RawMessage) bool
	Score(cfg, state json.RawMessage) ([]structure.SlotOutcome, error)
}

// Param is one DSL key a Protocol accepts and the stage-config field it
// compiles to: true/false when Bool, else a count, written as Default when
// the scheme is silent and Default is not zero.
type Param struct {
	Key     string
	Config  string
	Bool    bool
	Default int
}

// Metrics returns the metrics a protocol declares, or nil for an unknown code.
func Metrics(code string) []string {
	if p, ok := Get(code); ok {
		return p.Metrics()
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
// бой; an unknown Protocol counts as started, so nothing of it is touched.
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
}

// Get looks up a registered protocol by code.
func Get(code string) (Protocol, bool) {
	p, ok := registry[code]
	return p, ok
}

// RatingRosterOwner is implemented by protocols whose state embeds a roster
// owned by a rating.chgk.info import: the named top-level state key is
// immutable under host edits (re-import to change it).
type RatingRosterOwner interface {
	RatingRosterStateKey() string
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
