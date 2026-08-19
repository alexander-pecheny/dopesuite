package ui

import (
	"testing"

	"pecheny.me/dopeuikit/uitest"
)

// TestRealPages runs the kit's page contract over xy's six pages: every
// web/assets/ui/*.dopeui compiles, and every id and load-bearing class the
// page's script closure looks up exists in the compiled page.
func TestRealPages(t *testing.T) {
	uitest.PageContract{
		Compile:   Compile,
		PagesDir:  "../../web/assets/ui",
		StaticDir: "../../web/assets/static",
		Pages:     6,
		// authorsDatalist is built by the board script itself.
		IDsCreatedByJS: map[string]bool{"authorsDatalist": true},
		// host-actions is on every page (the topbar emits it); the rest are
		// board-only widgets.
		LoadBearing: map[string][]string{
			"":      {"host-actions"},
			"board": {"card-detail", "preview-screen-toggle"},
		},
	}.Run(t)
}
