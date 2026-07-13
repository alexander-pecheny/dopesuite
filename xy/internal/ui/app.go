// Package ui is xy's thin overlay over DopeUIKit's kit (pecheny.me/dopeuikit/
// kit): the xy-specific primitives (docoverlay/headrow/headactions/split/pane/
// previewtitle), the checkbox/editor expander overrides, the board mount kinds,
// and xy's PWA page chrome. The shared design system + DSL engine live in the
// kit; tags_gen.go re-exports every kit constructor/const so xy code imports
// this one package.
package ui

//go:generate go run pecheny.me/dopeuikit/cmd/uigen -core ../../../dopeuikit/kit/vocab.json -overlay vocab.json -pkg ui -base pecheny.me/dopeuikit/kit -out tags_gen.go

import (
	_ "embed"

	base "pecheny.me/dopeuikit/kit"
)

//go:embed vocab.json
var overlayVocab []byte

// viewportContent is xy's PWA viewport: no user zoom, cover the notch.
const viewportContent = "width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no, viewport-fit=cover"

var xyApp = mustApp()

func mustApp() *base.App {
	app, err := base.NewApp(base.Options{
		VocabOverlay: overlayVocab,
		Expand: map[string]base.ExpandFunc{
			"checkbox": expandCheckbox,
			"editor":   expandEditor,
			"previewtitle": func(c *base.ExpandCtx, p *base.Element) []base.Node {
				return one(base.Leaf(c, "h2", []string{"preview-title"}, nil, p))
			},
			"docoverlay":  expandDocoverlay,
			"headrow":     expandHeadrow,
			"headactions": expandHeadactions,
			"split":       expandSplit,
			"pane":        expandPane,
		},
		Mounts: xyMounts,
		Chrome: base.Chrome{
			Lang:         "ru",
			Viewport:     viewportContent,
			Stylesheets:  []string{"/static/styles.css"},
			FontPreloads: []string{"/static/fonts/noto-sans-400.woff2"},
			BootScripts:  []string{"/static/menu.js"},
			DefaultKind:  "sheet",
			PageKinds: map[string]base.PageKind{
				"sheet": {Body: []string{"host", "import-page"}, Main: []string{"match-main"}, Frame: []string{"sheet-frame", "import-frame"}},
				"full":  {Body: []string{"host"}, Main: []string{"match-main"}},
				"wide":  {Body: []string{"host"}, Main: []string{"board-main"}, Frame: []string{"import-form"}},
				"board": {Body: []string{"host", "board-page"}, Main: []string{"board-main"}},
			},
			TopbarSync: base.SyncSpec{ID: "status", Class: "sync-status", State: "saved", Label: "Готово"},
		},
	})
	if err != nil {
		panic(err)
	}
	return app
}

// Compile parses, validates and expands a .dopeui page against xy's vocabulary.
func Compile(name string, src []byte) ([]byte, error) { return xyApp.Compile(name, src) }

// Render validates and prints a builder-made tree (dynamic /admin pages).
func Render(doc *Doc) ([]byte, error) { return xyApp.Render(doc) }

func one(e *base.Element) []base.Node { return []base.Node{e} }
