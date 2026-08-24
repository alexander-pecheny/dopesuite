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
	EK     = "ek"     // эрудит-квартет (bracket of small matches)
	OD     = "od"     // ЧГК — командная викторина с раундами по минуте
	KSI    = "ksi"    // командная своя игра
	SI     = "si"     // личная своя игра — за столом игроки, а не команды
	Brain  = "brain"  // брейн-ринг — head-to-head buzzer бои
	Multi  = "multi"  // мультиигры — несколько мини-игр в одну посадку
	Troika = "troika" // троечка — бой двух троек по темам из трёх вопросов
)

// Default is the game type assumed when a game has none recorded.
const Default = EK

// Definition describes a game type for generic, type-agnostic server code.
// Add a new tournament format by registering a Definition here.
type Definition struct {
	Code  string // canonical game_type value
	Label string // short display label (Russian)
	// Individual reports whether a Participant of this format is one player
	// rather than a team (личная СИ). It decides what the game draws its
	// Participants from and how the UI names them.
	Individual bool
	// Page is the compiled page a Game of this type is served on, and Init the
	// payload it boots from. Личная СИ borrows ЭК's page for its bracket —
	// stage tabs, пересевы, бои — not КСИ's blank: a seat of one player has no
	// per-theme player cell, so the page draws it as one row where a team's
	// takes two.
	Page string
	Init InitKind
}

// InitKind names the init payload a page boots from: the flat game init
// (ЧГК, КСИ, брейн) or ЭК's bracket init.
type InitKind int

const (
	InitGame InitKind = iota
	InitEK
)

// Get returns the Definition of a game type; an unknown or empty type reads as
// the Default format, the way every page route treated it.
func Get(code string) Definition {
	if d, ok := registry[code]; ok {
		return d
	}
	return registry[Default]
}

// Known reports whether a code names a registered format — what a creation
// form asks instead of listing the formats it will accept.
func Known(code string) bool {
	_, ok := registry[code]
	return ok
}

// IsIndividual reports whether the format seats players rather than teams.
func IsIndividual(code string) bool {
	d, ok := registry[code]
	return ok && d.Individual
}

// registry is the single source of truth for known game types. Iteration order
// is never relied upon; look-ups go through the helpers below.
var registry = map[string]Definition{
	EK:    {Code: EK, Label: "ЭК", Page: "static/ek.html", Init: InitEK},
	OD:    {Code: OD, Label: "ЧГК", Page: "static/od.html"},
	KSI:   {Code: KSI, Label: "КСИ", Page: "static/si.html"},
	SI:    {Code: SI, Label: "Личная СИ", Individual: true, Page: "static/ek.html", Init: InitEK},
	Brain: {Code: Brain, Label: "Брейн", Page: "static/brain.html"},
	Multi: {Code: Multi, Label: "Мультиигры", Page: "static/multi.html"},
	// Тройка plays a bracket of бои, as брейн does, and boots the same payload:
	// its page fetches the бои itself and draws them its own way.
	Troika: {Code: Troika, Label: "Тройка", Page: "static/troika.html"},
}

// Label returns the short display label for a game type, falling back to the
// raw code for unknown types (matching the previous gameTypeLabel behaviour).
func Label(code string) string {
	if d, ok := registry[code]; ok {
		return d.Label
	}
	return code
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
