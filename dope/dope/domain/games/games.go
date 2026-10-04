// Package games holds the game-type-specific domain logic shared across the
// server. The system supports several tournament formats (EK — erudit-quartet,
// OD — team quiz / ChGK, KSI — team jeopardy) and is expected to
// grow to many more. Rather than scattering `switch gameType` blocks and bare
// "ek"/"od"/"ksi" string literals across the handler, export and import code,
// generic server code consults the registry defined here.
//
// This package is a leaf: it depends only on the standard library and the
// Catalog, and never on the server, database or HTTP layers, so per-game pure
// domain logic (state shapes, scoring, etc.) can live here without import
// cycles.
package games

import (
	"encoding/json"

	dopestrings "dope/i18nstrings"
)

// Canonical game_type codes as stored in the games.game_type column.
const (
	EK     = "ek"     // erudit-quartet (bracket of small matches)
	ES     = "es"     // erudit-sextet — EK's bout with up to three players on a theme
	OD     = "od"     // ChGK — team quiz with one-minute rounds
	KSI    = "ksi"    // team jeopardy
	SI     = "si"     // individual jeopardy — players at the table, not teams
	Brain  = "brain"  // brain-ring — head-to-head buzzer matches
	Multi  = "multi"  // multi — several minigames in one sitting
	Troika = "troika" // troika — a match of two troikas over themes of three questions
	Hamsa  = "hamsa"  // hamsa — four-seat bouts of five game rounds, the last one played on a bet
	KD     = "kd"     // friendship cup — an OD whose tables reshuffle their players every tour
)

// Default is the game type assumed when a game has none recorded.
const Default = EK

// Definition is everything the code outside the registries may ask about a
// format. A format is added by registering its Definition below and its
// Protocol (domain/protocol); no other code names a format by its code
// (ADR-0027). A fact about the document a bout holds belongs on the Protocol;
// a fact about the Game's place in the fest — what it seats, how it is
// created, edited, rostered, exported and journaled — belongs here.
type Definition struct {
	Code  string // canonical game_type value
	Label string // short display label (Russian)
	// Title is the name the creation form offers the format under, and the
	// title a new Game of it takes when the host gives none.
	Title string
	// Individual reports whether a Participant of this format is one player
	// rather than a team (individual SI). It decides what the game draws its
	// Participants from and how the UI names them.
	Individual bool
	// Troikas reports whether the format seats troikas (CONTEXT.md, Assembled team)
	// rather than fest teams: its seeds, its roster tab and its division-driven
	// entrant list all read the fest's troikas.
	Troikas bool
	// Page is the compiled page a Game of this type is served on, and Init the
	// payload it boots from. Individual SI borrows EK's page for its bracket —
	// stage tabs, reseeds, matches — not KSI's blank: a seat of one player has
	// no per-theme player cell, so the page draws it as one row where a team's
	// takes two.
	Page string
	Init InitKind
	// EKBout reports whether the format plays EK's bout: twelve themes of five
	// questions at 10..50, scored by store.BuildView, edited on EK's page, and
	// with a team's game roster kept in game_team_players.
	EKBout bool
	// Flat reports a flat Game: one Match whose document seats the whole fest
	// roster (or, in a friendship cup, its tables), built from the format's own
	// knobs on the creation form. It has no way to seat a chosen few, so it
	// refuses a chosen entrant list. Every other format is a Structure
	// described by a scheme and keeps an entrant list (ADR-0023).
	Flat bool
	// HandRoster reports whether the host may keep a team's roster by hand
	// for one Game (CONTEXT.md, Game roster): the buzzer formats that seat
	// teams. Its Protocol must say which players a bout used
	// (protocol.PlayersUser), which is what locks them on the roster.
	HandRoster bool
	// Divisions reports whether the Game's settings offer the fest's divisions
	// to show or hide: the formats whose results tabs carry the chips.
	Divisions bool
	// PlayerOverrides reports whether a fest player may be moved to another
	// team for a Game of this format (the players page's overrides).
	PlayerOverrides bool
	// DSL is how the format takes a scheme written in the DSL.
	DSL DSLUse
	// DefaultDSL is the scheme the creation form prefills for a fest of so many
	// participants; nil offers an empty editor.
	DefaultDSL func(participants int) string
	// UpgradeDSL, when set, is the scheme a clear writes for a Game of this
	// format that was made before the format had a DSL, from its stored
	// scheme JSON. Clearing such a Game moves it onto the DSL.
	UpgradeDSL func(participants int, schemeJSON string) string
	// PastedScheme reports whether the format may be created from a pasted
	// JSON scheme instead of a DSL (ADR-0006's escape hatch), and a Game of it
	// without a DSL is cleared by rebuilding that scheme.
	PastedScheme bool
	// ToursSeed reports whether a seed by players may rank this format's
	// Game after some of its tours only (`tours:` in `[init]`).
	ToursSeed bool
	// Results is the computed results view the results endpoint answers with;
	// nil answers that the format has none.
	Results func(schemeJSON, stateJSON string) (any, error)
	// Sheets is the layout of the Game's xlsx export.
	Sheets Sheets
	// Journal is how the host's history page describes the Game's edits.
	Journal Journal
}

// InitKind names the init payload a page boots from: the flat game init
// (ChGK, KSI, brain) or EK's bracket init.
type InitKind int

const (
	InitGame InitKind = iota
	InitEK
)

// DSLUse is how a format takes a scheme in the DSL. The zero value is
// undeclared, which the registry's tests refuse.
type DSLUse int

const (
	_ DSLUse = iota
	// DSLAccepted: a Game may be created from a DSL, and its settings page does
	// not edit it.
	DSLAccepted
	// DSLEditable: a Game's settings page edits its DSL and recompiles it.
	DSLEditable
	// DSLRefused: the DSL cannot describe the format, so a Game is never
	// created from one (Multi's shape is its minigames).
	DSLRefused
)

// Sheets names the layout of a format's xlsx export; export/gameexport keeps a
// builder for each. The zero value is undeclared.
type Sheets int

const (
	_ Sheets = iota
	// SheetsODRating is the rating.chgk.info tournament-tours sheet, with the
	// teams' rating ids.
	SheetsODRating
	// SheetsODTables is the same sheet without rating ids: the friendship
	// cup's tables are no rating teams.
	SheetsODTables
	// SheetsKSI is the detailed and results sheets of a KSI document.
	SheetsKSI
	SheetsMulti
	// SheetsEK, SheetsBrain, SheetsTroika and SheetsHamsa are a sheet per
	// stage, a block per bout.
	SheetsEK
	SheetsBrain
	SheetsTroika
	SheetsHamsa
)

// Journal names how the history page reads a format's edits. The zero value
// is undeclared.
type Journal int

const (
	_ Journal = iota
	// JournalEvents lists the coarse events only.
	JournalEvents
	// JournalEKRows reads EK's answer and theme rows, with team and match names.
	JournalEKRows
	// JournalODPatches reads the patches of an OD document (its teams by number).
	JournalODPatches
	// JournalKSIPatches reads the patches of a KSI document.
	JournalKSIPatches
)

// Get returns the Definition of a game type; an unknown or empty type reads as
// the Default format, the way every page route treated it.
func Get(code string) Definition {
	if d, ok := byCode[code]; ok {
		return d
	}
	return byCode[Default]
}

// Lookup returns the Definition of a registered game type.
func Lookup(code string) (Definition, bool) {
	d, ok := byCode[code]
	return d, ok
}

// Known reports whether a code names a registered format — what a creation
// form asks instead of listing the formats it will accept.
func Known(code string) bool {
	_, ok := byCode[code]
	return ok
}

// All is every registered format, in the order the creation form offers them.
func All() []Definition {
	return append([]Definition(nil), registry...)
}

// Codes is the code of every registered format the predicate holds for, in
// registry order: what a query naming a set of formats binds.
func Codes(pred func(Definition) bool) []string {
	var out []string
	for _, d := range registry {
		if pred(d) {
			out = append(out, d.Code)
		}
	}
	return out
}

// EKShaped reports whether a format plays EK's bout (Definition.EKBout). EK
// and Erudit-Sextet differ in one thing only, how many players a team seats on
// a theme, so the generic code that used to name EK alone asks this.
func EKShaped(code string) bool {
	d, ok := byCode[code]
	return ok && d.EKBout
}

// IsIndividual reports whether the format seats players rather than teams.
func IsIndividual(code string) bool {
	d, ok := byCode[code]
	return ok && d.Individual
}

// SeatsTroikas reports whether the format seats troikas rather than teams.
func SeatsTroikas(code string) bool {
	d, ok := byCode[code]
	return ok && d.Troikas
}

// KeepsEntrantList reports whether a Game of the format keeps an entrant list
// the host edits on its entrants tab, and seats the entrants the creation
// form ticks: every registered format that is not flat (ADR-0023).
func KeepsEntrantList(code string) bool {
	d, ok := byCode[code]
	return ok && !d.Flat
}

func odResults(schemeJSON, stateJSON string) (any, error) {
	return ComputeODResults(schemeJSON, stateJSON)
}

// A friendship cup answers its personal standings; the tables' own totals are
// the OD sheet's, read from the state.
func kdResults(schemeJSON, stateJSON string) (any, error) {
	return ComputeKDResults(schemeJSON, stateJSON)
}

func brainDefaultDSL(participants int) string { return BrainDSL(participants, BrainQuestionCount) }

// A pre-DSL Brain gets its shortcut scheme re-expressed in the DSL, keeping
// the questions a bout played.
func brainUpgradeDSL(participants int, schemeJSON string) string {
	return BrainDSL(participants, BrainQuestions(schemeJSON))
}

// registry is the single source of truth for known game types, in the order
// the creation form offers them.
var registry = []Definition{
	{Code: OD, Label: dopestrings.Default.Games.Od.Label(), Title: dopestrings.Default.Host.Games.TypeOd(),
		Page: "static/od.html", Flat: true, Divisions: true, DSL: DSLAccepted, ToursSeed: true,
		Results: odResults, Sheets: SheetsODRating, Journal: JournalODPatches},
	{Code: KSI, Label: dopestrings.Default.Games.Ksi.Label(), Title: dopestrings.Default.Host.Games.TypeKsi(),
		Page: "static/si.html", Flat: true, Divisions: true, PlayerOverrides: true, DSL: DSLAccepted,
		Sheets: SheetsKSI, Journal: JournalKSIPatches},
	{Code: Brain, Label: dopestrings.Default.Games.Brain.Label(), Title: dopestrings.Default.Host.Games.TypeBrain(),
		Page: "static/brain.html", HandRoster: true, DSL: DSLEditable,
		DefaultDSL: brainDefaultDSL, UpgradeDSL: brainUpgradeDSL, Sheets: SheetsBrain, Journal: JournalEvents},
	{Code: EK, Label: dopestrings.Default.Games.Ek.Label(), Title: dopestrings.Default.Host.Games.TypeEk(),
		Page: "static/ek.html", Init: InitEK, EKBout: true, HandRoster: true, PlayerOverrides: true,
		DSL: DSLAccepted, PastedScheme: true, Sheets: SheetsEK, Journal: JournalEKRows},
	// Erudit-Sextet rides EK's page and EK's scoring — twelve themes of five,
	// the same shootout — and differs only in seating up to three players on a
	// theme, which is a Protocol param, not a renderer of its own. Its history
	// lists events only: the history page reads EK's rows for EK alone.
	{Code: ES, Label: dopestrings.Default.Games.Es.Label(), Title: dopestrings.Default.Host.Games.TypeEs(),
		Page: "static/ek.html", Init: InitEK, EKBout: true, HandRoster: true, PlayerOverrides: true,
		DSL: DSLAccepted, PastedScheme: true, Sheets: SheetsEK, Journal: JournalEvents},
	// Individual SI's export is KSI's document sheets, which is what it has
	// always been given, though its Games are brackets on EK's page.
	{Code: SI, Label: dopestrings.Default.Games.Si.Label(), Title: dopestrings.Default.Host.Games.TypeSi(),
		Individual: true, Page: "static/ek.html", Init: InitEK, DSL: DSLAccepted,
		DefaultDSL: SIDefaultDSL, Sheets: SheetsKSI, Journal: JournalEvents},
	{Code: Multi, Label: dopestrings.Default.Games.Multi.Label(), Title: dopestrings.Default.Host.Games.TypeMulti(),
		Page: "static/multi.html", Flat: true, Divisions: true, DSL: DSLRefused,
		Sheets: SheetsMulti, Journal: JournalEvents},
	// Troika plays a bracket of matches, as brain does, and boots the same
	// payload: its page fetches the matches itself and draws them its own way.
	{Code: Troika, Label: dopestrings.Default.Games.Troika.Label(), Title: dopestrings.Default.Host.Games.TypeTroika(),
		Troikas: true, Page: "static/troika.html", DSL: DSLAccepted,
		DefaultDSL: TroikaDefaultDSL, Sheets: SheetsTroika, Journal: JournalEvents},
	// Hamsa plays a bracket of four-seat bouts and boots the bracket payload,
	// as Troika does: the page fetches its matches and draws them its own way.
	{Code: Hamsa, Label: dopestrings.Default.Games.Hamsa.Label(), Title: dopestrings.Default.Host.Games.TypeHamsa(),
		Page: "static/hamsa.html", HandRoster: true, DSL: DSLAccepted,
		DefaultDSL: HamsaDefaultDSL, Sheets: SheetsHamsa, Journal: JournalEvents},
	// The friendship cup is an OD document whose teams are the tables, and
	// rides OD's page: the entry, the detailed sheet and the screen board
	// work on tables unchanged, and the page adds the personal standings.
	// Its tables carry no Flags, so it offers no divisions.
	{Code: KD, Label: dopestrings.Default.Games.Kd.Label(), Title: dopestrings.Default.Host.Games.TypeKd(),
		Page: "static/od.html", Flat: true, DSL: DSLAccepted,
		Results: kdResults, Sheets: SheetsODTables, Journal: JournalODPatches},
}

// byCode indexes the registry.
var byCode = func() map[string]Definition {
	out := make(map[string]Definition, len(registry))
	for _, d := range registry {
		if _, dup := out[d.Code]; dup {
			panic("games: duplicate format " + d.Code)
		}
		out[d.Code] = d
	}
	return out
}()

// Label returns the short display label for a game type, falling back to the
// raw code for unknown types (matching the previous gameTypeLabel behaviour).
func Label(code string) string {
	if d, ok := byCode[code]; ok {
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
