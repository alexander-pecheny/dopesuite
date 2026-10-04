package games

import (
	"encoding/json"
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

// ClearKeeper is a Protocol whose document holds something a clear must carry
// over: a friendship cup's registered players, a Multi's guest teams. Clearing
// wipes what was played, not who plays.
type ClearKeeper interface {
	KeepOnClear(oldScheme, oldState string, scheme, state []byte) ([]byte, []byte, error)
}
