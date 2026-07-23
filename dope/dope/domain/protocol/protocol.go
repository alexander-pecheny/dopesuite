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
)

// Protocol is a registered in-match ruleset. EmptyState builds the pristine
// match state for a match config (participant count, tour composition, …).
// Score maps state to per-slot outcomes in slot order; it leaves Participant
// zero — who sits where is the Structure layer's knowledge. Both take the
// match config because scoring rules legitimately live there (tour
// composition, sticker rules, question values).
type Protocol interface {
	Code() string
	EmptyState(cfg json.RawMessage) (json.RawMessage, error)
	Score(cfg, state json.RawMessage) ([]structure.SlotOutcome, error)
}

// registry is the single source of truth for known protocols. Add a format by
// registering a Protocol — never by a switch on protocol codes elsewhere.
var registry = map[string]Protocol{}

// Register adds a protocol; duplicate codes are a programming error.
func Register(p Protocol) {
	if _, dup := registry[p.Code()]; dup {
		panic("protocol: duplicate protocol " + p.Code())
	}
	registry[p.Code()] = p
}

// Get looks up a registered protocol by code.
func Get(code string) (Protocol, bool) {
	p, ok := registry[code]
	return p, ok
}
