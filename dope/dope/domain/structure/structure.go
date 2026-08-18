// Package structure holds the Structure half of the unified model
// (docs/unified-model.md, ADR-0001): the registry of Stage Kinds — the
// composable tournament primitives (round-robin groups, elimination brackets,
// pods, reseeds) every game type builds its bracket from. A Kind has three
// separable roles, registered together: a Macro expands one Block of the
// scheme DSL into stages at compile time (macro.go — the Kind declares its
// keys and emits through the Block it is handed), an Expander produces a
// scheduled stage's бои from its typed config, a Ranker ranks a stage's
// participants from match outcomes at resolve time. Most Kinds are all
// three; the reseed is only a Ranker, the hand-authored kind only an
// Expander. The package never knows Protocol rules or DSL text; it consumes
// typed values and per-slot outcomes (place + metrics) the Protocol scorer
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
// Protocol scorer's per-slot output, in slot order. Questions is the bout's
// base question count (без перестрелок) — the denominator for share metrics.
type MatchOutcome struct {
	Code      string
	Finished  bool
	Round     int
	Questions int
	Slots     []SlotOutcome
}

// SlotOutcome is one seat's result in a match: who sat there, the effective
// place (scorer's ranking with any host override applied) and the protocol's
// metrics (e.g. "taken", "total"). Place is fractional because shared places
// are (e.g. EK's 1.5); 0 = not placed. The Protocol scorer leaves Participant
// zero — seats are the Structure layer's knowledge, joined in by the caller.
type SlotOutcome struct {
	Participant int64 // 0 = empty seat
	Place       float64
	Metrics     map[string]float64
}

// BoutPoints maps a 2-seat bout place to очки — 2/1/0 for победа/ничья/
// поражение (КИНСБФ 4.1). Places beyond the second clamp to 0: share metrics
// are only defined for head-to-head bouts.
func BoutPoints(place float64) float64 {
	points := 2 * (2 - place)
	if points < 0 {
		return 0
	}
	return points
}

// RankedEntry is one participant's row in a stage's standings. Equal ranks are
// shared on a full tie of the configured order keys; Rank 0 is unplaced — a
// pod's survivor whose места are still being played. Bouts are the codes of
// the бои the row was summed from, when the Kind counts them.
type RankedEntry struct {
	Rank        int
	Participant int64
	Metrics     map[string]float64
	Bouts       []string
}

// SortRule is one key of a Ranker's order — the column a standings table
// shows for it, and the direction it sorts by.
type SortRule = store.SortRule

// Expander is a Kind's compile-time role: Schedule produces the stage's
// matches from its config (entrant slot sources plus kind-specific options).
type Expander interface {
	Code() string
	Schedule(cfg json.RawMessage) ([]store.SchemeMatch, error)
}

// Ranker is a Kind's resolve-time role: Standings ranks the stage's
// participants from its matches' outcomes, and Order names the Metrics it
// ranked by, in order — what a table of those standings shows beside М.
// Metrics is what the Kind itself adds to a row over the Protocol's metrics,
// which every Kind sums; the compiler lets a scheme sort by nothing else.
type Ranker interface {
	Code() string
	Metrics() []string
	Standings(cfg json.RawMessage, results []MatchOutcome, in Inputs) ([]RankedEntry, error)
	Order(cfg json.RawMessage) []SortRule
}

// Inputs is what the resolver knows at ranking time that no config holds: the
// game's fixed random seed for tie lots, and, for a reseed, the Contenders it
// ranks — who advances is a matter of resolved slots.
type Inputs struct {
	Seed       string
	Contenders []Contender
}

// Contender is one Participant a reseed ranks and the band (Losses so far)
// the ranking runs inside.
type Contender struct {
	Participant int64
	Band        int
}

// One config type per Kind: the compiler writes it, the Kind reads it back,
// and a renamed field is a compile error on both sides. The JSON tags are the
// wire, which the client reads too.

// RRConfig is a round-robin Group: its schedule and its cross-table rule.
type RRConfig struct {
	Code      string             `json:"code,omitempty"`
	Label     string             `json:"label,omitempty"`
	Title     string             `json:"title,omitempty"`
	Venue     int                `json:"venue,omitempty"`
	Entrants  []store.SchemeSlot `json:"entrants,omitempty"`
	Pairings  [][][]int          `json:"pairings,omitempty"`
	MatchSize int                `json:"matchSize,omitempty"`
	Rounds    int                `json:"rounds,omitempty"`
	Points    *RRPoints          `json:"points,omitempty"`
	Metric    string             `json:"metric,omitempty"`
	Order     []string           `json:"order,omitempty"`
	Rules     *Rules             `json:"rules,omitempty"`
}

type RRPoints struct {
	Win  float64 `json:"win"`
	Draw float64 `json:"draw"`
	Loss float64 `json:"loss"`
}

// FlatConfig is a flat game: one бой of every entrant.
type FlatConfig struct {
	Code     string             `json:"code,omitempty"`
	Title    string             `json:"title,omitempty"`
	Venue    int                `json:"venue,omitempty"`
	Entrants []store.SchemeSlot `json:"entrants,omitempty"`
	Order    []string           `json:"order,omitempty"`
	Rules    *Rules             `json:"rules,omitempty"`
}

type SEConfig struct {
	Code     string             `json:"code,omitempty"`
	Venue    int                `json:"venue,omitempty"`
	Bronze   bool               `json:"bronze,omitempty"`
	Entrants []store.SchemeSlot `json:"entrants,omitempty"`
}

type PodConfig struct {
	Lives         int `json:"lives,omitempty"`
	WinningPlaces int `json:"winning_places,omitempty"`
}

// ReseedConfig is a reseed's order; the compiler writes it on the stage's own
// Sort, which the store keeps at the envelope's top level.
type ReseedConfig struct {
	Sort []SortRule `json:"sort,omitempty"`
}

type ManualConfig struct {
	Matches []store.SchemeMatch `json:"matches"`
}

// MetricTakenBase is the Protocol metric a reseed's shares are built on —
// взятые without перестрелка. A Protocol that wants shares writes it.
const MetricTakenBase = "takenBase"

// The registries are the single source of truth for known stage kinds. Add a
// new structural primitive by registering it — never by a switch on kind
// codes elsewhere.
var (
	expanders = map[string]Expander{}
	rankers   = map[string]Ranker{}
)

// Register adds a stage kind under whichever roles it implements — Expander,
// Ranker, and Macro under its DSL word; a duplicate code or word, or a kind
// with no role, is a programming error.
func Register(kind interface{ Code() string }) {
	registered := false
	if macro, ok := kind.(Macro); ok {
		if _, dup := macros[macro.Word()]; dup {
			panic("structure: duplicate kind word " + macro.Word())
		}
		macros[macro.Word()] = macro
		registered = true
	}
	if expander, ok := kind.(Expander); ok {
		if _, dup := expanders[kind.Code()]; dup {
			panic("structure: duplicate stage kind " + kind.Code())
		}
		expanders[kind.Code()] = expander
		registered = true
	}
	if ranker, ok := kind.(Ranker); ok {
		if _, dup := rankers[kind.Code()]; dup {
			panic("structure: duplicate stage kind " + kind.Code())
		}
		rankers[kind.Code()] = ranker
		registered = true
	}
	if !registered {
		panic("structure: kind " + kind.Code() + " has no role — Macro, Expander or Ranker")
	}
}

// sortRules turns a Kind's order keys into SortRules: места ascend, every
// other Metric descends.
func sortRules(order []string) []SortRule {
	rules := make([]SortRule, 0, len(order))
	for _, key := range order {
		dir := "desc"
		if key == "place" || key == "place_sum" || key == "losses" {
			dir = "asc"
		}
		rules = append(rules, SortRule{Metric: key, Dir: dir})
	}
	return rules
}

// ExpanderFor looks up the Kind that schedules stages of this code.
func ExpanderFor(code string) (Expander, bool) {
	kind, ok := expanders[code]
	return kind, ok
}

// RankerFor looks up the Kind that ranks stages of this code.
func RankerFor(code string) (Ranker, bool) {
	kind, ok := rankers[code]
	return kind, ok
}

func RankerMetrics(code string) []string {
	if kind, ok := rankers[code]; ok {
		return kind.Metrics()
	}
	return nil
}
