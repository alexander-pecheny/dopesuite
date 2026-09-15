package ui

import (
	"testing"

	kit "pecheny.me/dopeuikit/kit"
	"pecheny.me/dopeuikit/uitest"

	spliffstrings "spliff/i18nstrings"
)

// TestRealPages runs the kit's page contract over Spliff's pages: every
// web/assets/ui/*.dopeui compiles, and every id its script looks up exists in
// the compiled page. A renamed mount or a dropped field fails here rather than
// in front of somebody entering a bill.
func TestRealPages(t *testing.T) {
	uitest.PageContract{
		Compile:   Compile,
		PagesDir:  "../assets/ui",
		StaticDir: "../assets/static",
		Pages:     5,
		Provided:  map[string][]byte{"login": kit.LoginPage(spliffstrings.Default.Auth.Page.Title(), "/")},
	}.Run(t)
}
