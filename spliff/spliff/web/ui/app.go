// Package ui is Spliff's thin overlay over DopeUIKit's kit: the mount kinds its
// pages hand over to the browser at, and the page Chrome. There are no
// primitives of its own and no generated builder — every page Spliff serves is
// either a .dopeui source compiled through Compile or one of the /admin pages
// built with the kit's own typed builder, which knows the core vocabulary.
//
// Chrome.Lang is "en": Spliff is English-only (spliff/CONTEXT.md), and the
// <html lang> it stamps is also what picks the English Catalog for the shared
// login and menu scripts at runtime.
package ui

import (
	_ "embed"

	kitstrings "pecheny.me/dopeuikit/i18nstrings"
	base "pecheny.me/dopeuikit/kit"

	spliffstrings "spliff/i18nstrings"
)

//go:embed vocab.json
var overlayVocab []byte

var app = mustApp()

func mustApp() *base.App {
	a, err := base.NewApp(base.Options{
		Strings:      spliffstrings.Default,
		KitStrings:   kitstrings.EN,
		VocabOverlay: overlayVocab,
		Mounts:       mounts,
		Chrome: base.CoreChrome().With(base.Chrome{
			Lang: "en",
			// The audience enters bills at the table, so the phone is the
			// design target and the notch is real estate.
			Viewport: "width=device-width, initial-scale=1, viewport-fit=cover",
			TopbarSync: base.SyncSpec{
				ID: "status", Class: "sync-status", State: "saved",
				Label: kitstrings.EN.Chrome.Sync.Saved(),
			},
		}),
	})
	if err != nil {
		panic(err)
	}
	return a
}

// mounts says what each kind hands over as. Most of Spliff's are the kit's own
// list — a Group is read as rows of name-and-amount — so they emit a <ul
// class="list"> and the page's script fills it with .list-row children. Only the
// two shapes the design system has no name for carry a class of Spliff's own.
var mounts = map[string]base.MountSpec{
	"groups-list":         {Tag: "ul", Classes: []string{"list"}},
	"group-balances":      {Tag: "ul", Classes: []string{"list"}},
	"group-transfers":     {Tag: "ul", Classes: []string{"list"}},
	"group-feed":          {Tag: "ul", Classes: []string{"list"}},
	"group-members":       {Tag: "ul", Classes: []string{"list"}},
	"group-invites":       {Tag: "ul", Classes: []string{"list"}},
	"transaction-history": {Tag: "ul", Classes: []string{"list"}},
	// The editor's member rows: one grid so the three columns line up down the
	// form however long the names are.
	"transaction-body": {Tag: "div", Classes: []string{"split-rows"}},
	// The Photos side by side and scrollable, rather than reflowing.
	"transaction-photos": {Tag: "div", Classes: []string{"photo-strip"}},
	"join-body":          {Tag: "div", Classes: []string{"u-col", "u-gap-sm"}},
}

// Compile parses, validates and expands a .dopeui page against Spliff's
// vocabulary.
func Compile(name string, src []byte) ([]byte, error) { return app.Compile(name, src) }

// Render validates and prints a builder-made tree (the /admin pages).
func Render(doc *base.Doc) ([]byte, error) { return app.Render(doc) }
