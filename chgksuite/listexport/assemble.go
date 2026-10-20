// Package listexport is what a List exports as: the 4s document, the game it
// is composed as, and the .hndt of its handouts, all assembled from the List's
// decrypted Cards.
//
// The browser keeps its own copy of this assembly (xy/web/ts/listexport.ts),
// because a bare .4s is written offline and the board's handout badge is drawn
// on every render, so neither can wait for a server. This package is the
// reference: xy-cli exports through it, and testdata/cases.json, which this
// side writes (go test ./listexport -update), is the corpus
// xy/jstest/listexport_parity.test.js holds the browser to.
package listexport

import "code.pecheny.me/pecheny/dopesuite/chgksuite/cardkind"

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
	// Game is the game the scope is composed as (cardkind.GameOf).
	Game string `json:"game"`
	// Hndt is the .hndt document: the tour's preamble, then one block per
	// question that carries a Handout. "" when there is neither.
	Hndt string `json:"hndt"`
}

// Assemble builds what cards export as.
func Assemble(cards []Card) Assembly {
	return Assembly{Source: source(cards), Game: game(cards), Hndt: hndt(cards)}
}

// game is the game a scope exports as, whatever the List Type says: a theme's
// name and author survive only an SI or troika compose, and a battle's heading
// only a brain one.
func game(cards []Card) string {
	kinds := make([]string, len(cards))
	for i, c := range cards {
		kinds[i] = c.Kind
	}
	return cardkind.GameOf(kinds)
}
