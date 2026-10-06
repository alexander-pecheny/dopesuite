// Package cardkind is the list of Card Kinds and what each one does: whether a
// Card of that kind is numbered, which number sequence it counts in, whether it
// goes into the export, and so on. Every place that asks one of these questions
// asks it here, so a new kind is one row in this table rather than an edit in
// every file that used to spell the answer out.
//
// The browser reads the same table from web/ts/cardkind_gen.ts, which `go
// generate` writes from this one, and web/ts/cardkind.ts asks it the same
// questions. The server's allow-list is Valid, and a test holds the cards.kind
// CHECK constraint to Names.
package cardkind

//go:generate go run ./genkinds ../../web/ts/cardkind_gen.ts

// The kinds a Card can have, as they are stored.
const (
	// Normal is what a Card gets when nobody names a kind: the server's
	// default, and xy-cli's. It is shown and exported as written, unnumbered.
	Normal = "normal"
	// Question is one ChGK question.
	Question = "question"
	// Test is a test card from a Trello import, kept as it came.
	Test = "test"
	// Meta is a note that goes into the package as a `#` line.
	Meta = "meta"
	// Heading is a section title, a `##` line in the package.
	Heading = "heading"
	// Other is a note that belongs to no question.
	Other = "other"
	// Theme is one SI theme with its Ladder (ADR-0018).
	Theme = "theme"
	// HandoutsPreamble is a tour's ///preamble block for the .hndt. It is not
	// 4s and never goes into the package.
	HandoutsPreamble = "handouts_preamble"
)

// The number sequences a numbered Card counts in. Questions count 1, 2, 3…
// across a tour, and Themes keep a count of their own.
const (
	CounterQuestion = "question"
	CounterTheme    = "theme"
)

// The games a scope exports as.
const (
	GameChgk = "chgk"
	GameSI   = "si"
)

// Kind is one Card Kind and what it does.
type Kind struct {
	Name string `json:"name"`
	// Counter is the number sequence the kind is numbered in, "" when a Card
	// of this kind has no number. A numbered Card holds question fields, is
	// counted in the List's head and the board's title, and is what a Test
	// Session plays.
	Counter string `json:"counter"`
	// Exported is whether the kind's Cards go into the 4s the package is
	// rendered from.
	Exported bool `json:"exported"`
	// Handouts is whether the kind's Handout goes into the .hndt. Only a
	// question: the generator keys a handout by question number, and every
	// theme has a `№ 10`.
	Handouts bool `json:"handouts"`
	// Versioned is whether the kind's Cards show and edit Versions. A theme
	// does not: a Version is a whole body, and on a theme that would copy four
	// questions to reword one (ADR-0018).
	Versioned bool `json:"versioned"`
	// Marker is the 4s marker a plain-text Card of this kind is given on
	// export, so that 4s does not drop its first line. "" for none.
	Marker string `json:"marker"`
	// Section is whether the kind restarts the Theme count, as a section does
	// in chgksuite.
	Section bool `json:"section"`
	// SetsBase is whether a standalone `№№ N` in the Card sets the question
	// count for the Cards after it.
	SetsBase bool `json:"setsBase"`
	// Game is the game one Card of this kind makes its whole scope export as,
	// whatever the List Type says. "" for no say.
	Game string `json:"game"`
	// Pickable is whether the card editor's kind menu offers it.
	Pickable bool `json:"pickable"`
}

// All is every Card Kind, in the order the kind menu lists the pickable ones.
var All = []Kind{
	{Name: Question, Counter: CounterQuestion, Exported: true, Handouts: true, Versioned: true, Pickable: true},
	{Name: Theme, Counter: CounterTheme, Exported: true, Game: GameSI, Pickable: true},
	{Name: Meta, Exported: true, Marker: "#", SetsBase: true, Pickable: true},
	{Name: Heading, Exported: true, Marker: "##", Section: true, SetsBase: true, Pickable: true},
	{Name: Other, Exported: true, Pickable: true},
	{Name: Normal, Exported: true},
	{Name: Test, Exported: true},
	{Name: HandoutsPreamble},
}

var byName = func() map[string]Kind {
	out := make(map[string]Kind, len(All))
	for _, k := range All {
		out[k.Name] = k
	}
	return out
}()

// Of is the named kind; an unknown name is a Kind that does nothing.
func Of(name string) Kind { return byName[name] }

// Names is every kind's name, in table order.
func Names() []string {
	out := make([]string, len(All))
	for i, k := range All {
		out[i] = k.Name
	}
	return out
}

// Valid is whether name is a kind a Card may have.
func Valid(name string) bool {
	_, ok := byName[name]
	return ok
}

// Numbered is whether a Card of this kind gets a number.
func Numbered(name string) bool { return Of(name).Counter != "" }

// Exported is whether a Card of this kind goes into the package.
func Exported(name string) bool { return Of(name).Exported }

// GameOf is the game a scope of Cards with these kinds exports as: the game of
// the first kind that has a say, GameChgk when none does.
func GameOf(kinds []string) string {
	for _, k := range kinds {
		if g := Of(k).Game; g != "" {
			return g
		}
	}
	return GameChgk
}
