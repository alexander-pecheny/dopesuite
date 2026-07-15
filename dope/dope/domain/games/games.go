// Package games holds the game-type-specific domain logic shared across the
// server. The system supports several tournament formats (EK — эрудит-квартет,
// OD — командная викторина / ЧГК, KSI — командная своя игра) and is expected to
// grow to many more. Rather than scattering `switch gameType` blocks and bare
// "ek"/"od"/"ksi" string literals across the handler, export and import code,
// generic server code consults the registry defined here.
//
// This package is a leaf: it depends only on the standard library and never on
// the server, database or HTTP layers, so per-game pure domain logic (state
// shapes, scoring, etc.) can live here without import cycles.
package games

import "encoding/json"

// Canonical game_type codes as stored in the games.game_type column.
const (
	EK    = "ek"    // эрудит-квартет (bracket of small matches)
	OD    = "od"    // ЧГК — командная викторина с раундами по минуте
	KSI   = "ksi"   // командная своя игра
	SI    = "si"    // legacy alias used by some viewers/renderers for KSI
	BRAIN = "brain" // брейн — round-robin groups of head-to-head бои (bracket family)
)

// Default is the game type assumed when a game has none recorded.
const Default = EK

// Definition describes a game type for generic, type-agnostic server code.
// Add a new tournament format by registering a Definition here.
type Definition struct {
	Code  string // canonical game_type value
	Label string // short display label (Russian)
}

// registry is the single source of truth for known game types. Iteration order
// is never relied upon; look-ups go through the helpers below.
var registry = map[string]Definition{
	EK:    {Code: EK, Label: "ЭК"},
	OD:    {Code: OD, Label: "ЧГК"},
	KSI:   {Code: KSI, Label: "КСИ"},
	SI:    {Code: SI, Label: "СИ"},
	BRAIN: {Code: BRAIN, Label: "Брейн"},
}

// Label returns the short display label for a game type, falling back to the
// raw code for unknown types (matching the previous gameTypeLabel behaviour).
func Label(code string) string {
	if d, ok := registry[code]; ok {
		return d.Label
	}
	return code
}

// IsChGK reports whether code is a ЧГК-family flat-grid game (OD, KSI, SI), as
// opposed to a per-match bracket game (EK, брейн). Used to collapse
// viewer/snapshot routing for the flat-grid types.
func IsChGK(code string) bool {
	switch code {
	case OD, KSI, SI:
		return true
	default:
		return false
	}
}

// mustJSON marshals value to a JSON string, returning "{}" on the (impossible
// for these inputs) marshal error. Mirrors the server-side helper of the same
// name so the pure per-game builders below produce identical bytes.
func mustJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// KSIThemeCount is the fixed number of themes in a KSI (team jeopardy) game.
const KSIThemeCount = 20
