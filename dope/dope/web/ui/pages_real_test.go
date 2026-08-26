package ui

import (
	"testing"

	kit "pecheny.me/dopeuikit/kit"
	"pecheny.me/dopeuikit/uitest"
)

// TestRealPages runs the kit's page contract over dope's pages: every
// web/assets/ui/*.dopeui compiles, and every id and load-bearing markup the
// page's scripts look up exists in the compiled page (see DOPE-INVENTORY §JS
// contract: the game pages measure .sheet-frame and mount into .table-host;
// login sets the title on `.host-top h1`).
func TestRealPages(t *testing.T) {
	uitest.PageContract{
		Compile:   Compile,
		PagesDir:  "../assets/ui",
		StaticDir: "../assets/static",
		Pages:     7,
		Provided:  map[string][]byte{"login": kit.LoginPage("Вход · Фест", "/host")},
		LoadBearing: map[string][]string{
			"":       {"host-actions"},
			"login":  {"host-top", "<h1>"},
			"ek":     {"game-host-top", "sheet-frame", "table-host"},
			"od":     {"game-host-top", "sheet-frame", "table-host", "od-header-progress"},
			"si":     {"game-host-top", "sheet-frame", "table-host"},
			"brain":  {"game-host-top", "sheet-frame", "table-host"},
			"multi":  {"game-host-top", "sheet-frame", "table-host"},
			"troika": {"game-host-top", "sheet-frame", "table-host"},
		},
	}.Run(t)
}
