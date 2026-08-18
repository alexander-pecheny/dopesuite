package structure

import (
	"fmt"
	"sort"
	"strings"

	"dope/dope/storage/store"
)

// Macro is a Kind's compile-time role at Block grain — CONTEXT.md's own word
// for a Kind, «a registered macroexpansion algorithm that turns a Block's
// config into Rounds of Matches». It declares the DSL keys it reads, and
// expands one Block through the Block it is handed: reading typed values,
// asking for столы and entrants, emitting stages in schedule order, and
// returning what the next Block's Edge may seat from. It is pure: no I/O, no
// registry lookups by name, no Protocol. Word is the DSL's name for the Kind
// («roundrobin»); Code stays the Ranker's («rr»).
type Macro interface {
	Word() string
	Keys() []Key
	Expand(b Block) (Outputs, error)
}

// Key is one DSL key a Kind reads beyond the common ones. Round marks a key
// that takes a Round suffix (match_size.r3, best_of.final); Cascade one that
// may also stand in [defaults] (rr's points).
type Key struct {
	Name    string
	Round   bool
	Cascade bool
}

// Block is everything a Macro may touch: one Block as the compiler resolved
// it. Reads name only the Kind's declared keys and the common ones; the
// compiler pins every complaint to a line, so a Kind returns a KeyError to
// name the key at fault and a plain error otherwise. Emission order is
// schedule order: it decides Position and the буквы.
type Block interface {
	Code() string // «s2» — every code the Kind emits starts with it
	First() bool  // no Block before it: seats come from the seed
	Seeded() int  // how many entrants the caller seeded; 0 = positional Посев placeholders

	Int(key string) (int, bool)
	Bool(key string) (bool, bool)
	Str(key string) (string, bool)
	IntList(key string) ([]int, bool, error)
	Rounds(names []string) error // the Round names the Kind generates (none: it has no addressable Rounds); a dotted key naming another is refused

	Sorting() ([]store.SortRule, bool, error)
	DefaultSorting() ([]store.SortRule, bool, error)
	Rankable(ranker string) map[string]bool
	Rules() *Rules
	Reseed() (incoming bool, round string)
	Proceeding() (int, bool)
	Prev() (Outputs, bool)

	Title(fallback string) string
	GroupTitle(group, groups int) string
	RoundTitle(names []string, derived string) string

	Venues(names ...string) (Lanes, error)
	Entrants(groups, size int) ([][]store.SchemeSlot, error) // the incoming Edge dealt into groups
	Seeds(count int) ([]store.SchemeSlot, error)             // the seed ranks 1..count, first Block only
	Reseeded(groups, size int) ([][]store.SchemeSlot, error) // the incoming Edge re-ranked, then dealt
	Sources(otherwise, self []string) ([]string, error)      // what a reseed sums over, stats_from applied

	Emit(s Stage) ([]string, error)
	EmitReseed(code string, at At, contenders []store.SchemeSlot, bands []int, sources []string) (string, error)
}

// Stage is one stage a Kind emits: a scheduled one (Kind rr or flat, its typed
// Config, no Matches — the Kind's Expander schedules it) or a drawn one (its
// бои as laid out; Kind «matches», or «de» for a pod). Rounds are the Round
// names whose dotted params reach this stage. Waves splits a drawn stage into
// as many turns at the столы as it needs.
type Stage struct {
	Code, Title, Kind, Slug string
	Rounds                  []string
	At                      At
	Config                  any
	Matches                 []store.SchemeMatch
	Waves                   bool
	Lanes                   Lanes
}

// At is where a stage sits in its Block: which Round its бои play (0 for a
// stage spanning Rounds), which Group it ranks.
type At struct {
	Round int
	Group string
}

// Outputs is what a Block offers the next Block's Edge: per Group, a way to
// reference each place; Terminal marks a Block nothing can follow.
type Outputs struct {
	Groups     []Feed
	Proceeding int
	Terminal   bool
}

type Feed struct {
	Stage string
	Label string
	Place func(p int) store.SchemeSlot
}

// Lanes are the столы open to a stage: a declared subset, else all of them.
type Lanes struct {
	Restricted []int
	Total      int
}

func (l Lanes) Pick(i int) int {
	if len(l.Restricted) == 0 {
		return (i-1)%l.Total + 1
	}
	return l.Restricted[(i-1)%len(l.Restricted)]
}

func (l Lanes) PerWave() int {
	if len(l.Restricted) == 0 {
		return l.Total
	}
	return len(l.Restricted)
}

// ReseedEveryRound is the `reseed:` word for a Block that re-ranks its incoming
// Edge and every Round after it — what ТПШ does, and what `true` already means
// on a bracket with lives.
const ReseedEveryRound = "every"

// KeyError is a Kind's complaint about one key; the compiler pins it to that
// key's line.
type KeyError struct {
	Key string
	Msg string
}

func (e KeyError) Error() string { return e.Msg }

func Keyf(key, format string, args ...any) error {
	return KeyError{Key: key, Msg: fmt.Sprintf(format, args...)}
}

var macros = map[string]Macro{}

func MacroFor(word string) (Macro, bool) {
	m, ok := macros[word]
	return m, ok
}

func Words() []string {
	words := make([]string, 0, len(macros))
	for w := range macros {
		words = append(words, w)
	}
	sort.Strings(words)
	return words
}

func FromMatch(matchCode string, place int) store.SchemeSlot {
	return store.SchemeSlot{FromMatch: &store.SchemeFromMatchRef{Match: matchCode, Place: place}}
}

// LabelledFromMatch is FromMatch with the label a host reads while the seat
// is still empty — «Бой 3, м. 2», not the internal бой code.
func LabelledFromMatch(matchCode, boutLabel string, place int) store.SchemeSlot {
	slot := FromMatch(matchCode, place)
	slot.Label = fmt.Sprintf("%s, м. %d", boutLabel, place)
	return slot
}

func ReseedRank(stage string, rank int) store.SchemeSlot {
	return store.SchemeSlot{
		Reseed: &store.SchemeReseedRef{Stage: stage, Rank: rank},
		Label:  fmt.Sprintf("Пересев-%d", rank),
	}
}

// GroupCode names a Group only where a Block has more than one: a single-group
// block is the Block, and labelling it «группа 1» invents a distinction the
// tournament does not make.
func GroupCode(groups, group int) string {
	if groups <= 1 {
		return ""
	}
	return fmt.Sprint(group)
}

// SortedNames renders a name set for an error message, so a typo is answered
// with the list the author could have meant.
func SortedNames(set map[string]bool) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// UnrankableMetric is the complaint every Kind makes about a sorting key
// nothing measures.
func UnrankableMetric(metric string, known map[string]bool) error {
	return fmt.Errorf("sorting: %s не считается — ни протокол, ни правила подсчёта такой метрики не дают (есть %s)",
		metric, strings.Join(SortedNames(known), ", "))
}
