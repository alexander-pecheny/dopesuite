package games

import (
	"encoding/json"
	"strconv"

	dopestrings "dope/i18nstrings"

	corei18n "pecheny.me/dopecore/i18nstrings"
)

// Shape is what a flat Game is made of besides who plays it: the knobs its
// creation form takes, and what a clear reads back out of its scheme. Each
// flat Protocol reads the fields it has and ignores the rest.
type Shape struct {
	// Tours is how many questions each tour holds (OD, friendship cup).
	Tours []int
	// Tables is how many tables a friendship cup seats (a prime).
	Tables int
	// Themes and Stickers are a KSI's themes and its optional stickers block.
	Themes   int
	Stickers json.RawMessage
	// Minigames and Sorting are a Multi's minigames and the comparators that
	// break a tie on the total.
	Minigames []MultiGame
	Sorting   []string
}

// PristineBuilder is a flat Protocol that builds its own Game: the empty
// scheme and document a Game of this shape starts from, before the fest
// roster is folded in (RosterFolder). Creation and Clear both build through
// it, so a cleared Game is exactly a new one.
type PristineBuilder interface {
	PristineGame(slug, title string, shape Shape) (scheme, state []byte, err error)
	// ShapeOf reads the shape back out of a stored scheme, which is what a
	// clear rebuilds the Game from.
	ShapeOf(schemeJSON string) Shape
}

// PristineGame builds a flat Game's empty scheme and document through its
// Protocol; ok is false for a Protocol that does not build its own Game.
func PristineGame(code, slug, title string, shape Shape) (scheme, state []byte, ok bool, err error) {
	builder, ok := pristineBuilder(code)
	if !ok {
		return nil, nil, false, nil
	}
	scheme, state, err = builder.PristineGame(slug, title, shape)
	return scheme, state, true, err
}

// ShapeOf reads a flat Game's shape out of its stored scheme.
func ShapeOf(code, schemeJSON string) Shape {
	if builder, ok := pristineBuilder(code); ok {
		return builder.ShapeOf(schemeJSON)
	}
	return Shape{}
}

func pristineBuilder(code string) (PristineBuilder, bool) {
	p, ok := ProtocolOf(code)
	if !ok {
		return nil, false
	}
	builder, ok := p.(PristineBuilder)
	return builder, ok
}

// ClearKeeper is a Protocol whose document holds something a clear must carry
// over: a friendship cup's registered players, a Multi's guest teams. Clearing
// wipes what was played, not who plays.
type ClearKeeper interface {
	KeepOnClear(oldScheme, oldState string, scheme, state []byte) ([]byte, []byte, error)
}

// KeepsOnClear reports whether a clear must read the Game's document before it
// deletes it, to carry part of it over.
func KeepsOnClear(code string) bool {
	p, ok := ProtocolOf(code)
	if !ok {
		return false
	}
	_, ok = p.(ClearKeeper)
	return ok
}

// KeepOnClear carries over what the Protocol keeps through a clear into the
// pristine scheme and document; a Protocol that keeps nothing returns them
// as they are.
func KeepOnClear(code, oldScheme, oldState string, scheme, state []byte) ([]byte, []byte, error) {
	p, ok := ProtocolOf(code)
	if !ok {
		return scheme, state, nil
	}
	keeper, ok := p.(ClearKeeper)
	if !ok {
		return scheme, state, nil
	}
	return keeper.KeepOnClear(oldScheme, oldState, scheme, state)
}

// An OD Game's tours. A stored scheme with none is read as one tour of
// fifteen, which is what a clear always rebuilt it with.
func (od) PristineGame(slug, title string, shape Shape) ([]byte, []byte, error) {
	scheme, state := ODEmptyGameJSON(slug, title, shape.Tours)
	return scheme, state, nil
}

func (od) ShapeOf(schemeJSON string) Shape {
	tours := ParseTourComp(schemeJSON)
	if len(tours) == 0 {
		tours = []int{15}
	}
	return Shape{Tours: tours}
}

// A friendship cup's tables must be a prime, and its tours no more than its
// tables: two cards share a table at most once only while the tours are no
// more than the tables, since in tour n+1 every card is back at its first
// table. The tables are named and numbered here; no roster is folded in.
func (kd) PristineGame(slug, title string, shape Shape) ([]byte, []byte, error) {
	if !IsPrime(shape.Tables) {
		return nil, nil, corei18n.User(dopestrings.Default.Gamebuild.Create.KdTablesPrime(strconv.Itoa(shape.Tables)))
	}
	if len(shape.Tours) > shape.Tables {
		return nil, nil, corei18n.User(dopestrings.Default.Gamebuild.Create.KdToursTables(strconv.Itoa(len(shape.Tours)), strconv.Itoa(shape.Tables)))
	}
	scheme, state := KDEmptyGameJSON(slug, title, shape.Tours, shape.Tables, kdTableName)
	return scheme, state, nil
}

func kdTableName(n int) string { return dopestrings.Default.Gamebuild.Kd.Table(strconv.Itoa(n)) }

func (kd) ShapeOf(schemeJSON string) Shape {
	return Shape{Tours: ParseTourComp(schemeJSON), Tables: KDTables(schemeJSON)}
}

// KeepOnClear keeps a friendship cup's players: clearing it wipes the
// answers, not the registration desk's work. A document that does not parse
// stops the clear, since going on would drop them.
func (kd) KeepOnClear(_, oldState string, scheme, state []byte) ([]byte, []byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(oldState), &fields); err != nil {
		return nil, nil, err
	}
	players := fields["players"]
	if len(players) == 0 {
		return scheme, state, nil
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(state, &doc); err != nil {
		return nil, nil, err
	}
	doc["players"] = players
	out, err := json.Marshal(doc)
	return scheme, out, err
}

// A KSI's themes, and its stickers block, which a clear keeps so a stickers
// game stays one. A stored scheme with no themes is read as twenty.
func (ksi) PristineGame(slug, title string, shape Shape) ([]byte, []byte, error) {
	scheme, state := KSIStickersEmptyGameJSON(slug, title, shape.Themes, shape.Stickers)
	return scheme, state, nil
}

func (ksi) ShapeOf(schemeJSON string) Shape {
	var sc struct {
		Themes   int             `json:"themes"`
		Stickers json.RawMessage `json:"stickers"`
	}
	_ = json.Unmarshal([]byte(schemeJSON), &sc)
	if sc.Themes <= 0 {
		sc.Themes = KSIThemeCount
	}
	return Shape{Themes: sc.Themes, Stickers: sc.Stickers}
}

// A Multi's minigames and the fest's tiebreak, which a clear keeps: it wipes
// what was played, not what the Game is.
func (multi) PristineGame(slug, title string, shape Shape) ([]byte, []byte, error) {
	scheme, state := MultiEmptyGameJSON(slug, title, shape.Minigames, shape.Sorting)
	return scheme, state, nil
}

func (multi) ShapeOf(schemeJSON string) Shape {
	var sc MultiScheme
	_ = json.Unmarshal([]byte(schemeJSON), &sc)
	return Shape{Minigames: sc.Minigames, Sorting: sc.Sorting}
}

// KeepOnClear keeps a Multi's guest teams, which are part of who plays.
func (multi) KeepOnClear(oldScheme, _ string, scheme, state []byte) ([]byte, []byte, error) {
	return KeepMultiGuests(oldScheme, scheme, state)
}
