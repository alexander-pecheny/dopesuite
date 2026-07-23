// Package structure holds the Structure half of the unified model
// (docs/unified-model.md, ADR-0001): the registry of Stage Kinds — the
// composable tournament primitives (round-robin groups, elimination brackets,
// reseeds) every game type builds its bracket from. A Stage Kind owns two
// separable concerns: producing a stage's match schedule and ranking the
// stage's participants from match outcomes. It never knows Protocol rules;
// it only consumes per-slot outcomes (place + metrics) the Protocol scorer
// produced.
//
// This package is a leaf next to domain/games: it may import storage/store for
// the scheme vocabulary but never the server, HTTP or DB layers.
package structure

import (
	"encoding/json"

	"dope/dope/storage/store"
)

// MatchOutcome is one match of a stage as the Structure layer sees it: the
// Protocol scorer's per-slot output, in slot order.
type MatchOutcome struct {
	Code     string
	Finished bool
	Slots    []SlotOutcome
}

// SlotOutcome is one seat's result in a match: who sat there, the effective
// place (scorer's ranking with any host override applied) and the protocol's
// metrics (e.g. "taken", "total").
type SlotOutcome struct {
	Participant int64 // 0 = empty seat
	Place       int   // 1-based; 0 = not placed
	Metrics     map[string]float64
}

// RankedEntry is one participant's row in a stage's standings. Equal ranks are
// shared on a full tie of the configured order keys.
type RankedEntry struct {
	Rank        int
	Participant int64
	Metrics     map[string]float64
}

// StageKind is a registered structural primitive. Schedule produces the
// stage's matches from its config (entrant slot sources plus kind-specific
// options); for static kinds it is complete upfront and results are ignored,
// while incremental kinds (swiss) extend the schedule as results arrive.
// Standings ranks the stage's participants from its matches' outcomes.
type StageKind interface {
	Code() string
	Schedule(cfg json.RawMessage, results []MatchOutcome) ([]store.SchemeMatch, error)
	Standings(cfg json.RawMessage, results []MatchOutcome) ([]RankedEntry, error)
}

// registry is the single source of truth for known stage kinds. Add a new
// structural primitive by registering a StageKind — never by a switch on kind
// codes elsewhere.
var registry = map[string]StageKind{}

// Register adds a stage kind; duplicate codes are a programming error.
func Register(kind StageKind) {
	if _, dup := registry[kind.Code()]; dup {
		panic("structure: duplicate stage kind " + kind.Code())
	}
	registry[kind.Code()] = kind
}

// Kind looks up a registered stage kind by code.
func Kind(code string) (StageKind, bool) {
	kind, ok := registry[code]
	return kind, ok
}
