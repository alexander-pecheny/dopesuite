// Package listexport is what a List exports as: the 4s document, the game it
// is composed as, and the .hndt of its handouts, all assembled from the List's
// decrypted Cards.
//
// The browser keeps its own copy of this assembly (web/ts/listexport.ts),
// because a bare .4s is written offline and the board's handout badge is drawn
// on every render, so neither can wait for a server. This package is the
// reference: xy-cli exports through it, and testdata/cases.json, which this
// side writes (go test ./internal/listexport -update), is the corpus
// jstest/listexport_parity.test.js holds the browser to.
package listexport

// Card is one Card as the assembly reads it, in board order.
type Card struct {
	ID          int64  `json:"id"`
	Kind        string `json:"kind"`
	Desc        string `json:"desc"`
	HandoutMeta string `json:"handoutMeta,omitempty"`
}

// Assembly is everything a List exports from.
type Assembly struct {
	// Source is the 4s document: the Cards' descriptions in board order, each
	// one's Versions folded back into one question, blank-line separated.
	Source string `json:"source"`
	// Game is "si" when the scope holds a Theme and "chgk" otherwise.
	Game string `json:"game"`
	// Hndt is the .hndt document: the tour's preamble, then one block per
	// question that carries a Handout. "" when there is neither.
	Hndt string `json:"hndt"`
}

// Assemble builds what cards export as.
func Assemble(cards []Card) Assembly {
	return Assembly{Source: source(cards), Game: game(cards), Hndt: hndt(cards)}
}

// game is the game a scope exports as: one Theme makes it SI, whatever the
// List Type says, since a theme's name and author survive only an SI compose.
func game(cards []Card) string {
	for _, c := range cards {
		if c.Kind == "theme" {
			return "si"
		}
	}
	return "chgk"
}
