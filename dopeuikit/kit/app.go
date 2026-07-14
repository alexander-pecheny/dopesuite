// Package kit is DopeUIKit's shared design system: the core vocabulary
// (vocab.json), the core expanders (page/topbar/col/row/button/modal/…), the
// generated typed builder (tags_gen.go), and core.css + fonts. It registers the
// core through the same public ui.Options API the apps use — it is the first
// overlay. kit.NewApp pre-registers the core and layers the app's overlay on
// top; apps import kit, never ui directly.
package kit

import base "pecheny.me/dopeuikit/ui"

// Options configures a kit App: the app's vocab overlay (merged over the core),
// its expander overrides / additions, mount kinds, and page Chrome. It mirrors
// ui.Options minus Base — kit supplies the core as the base.
type Options struct {
	VocabOverlay []byte
	ExtendProps  map[string][]PropSpec
	Expand       map[string]ExpandFunc
	Inline       map[string]InlineFunc
	Mounts       map[string]MountSpec
	Chrome       Chrome
}

// NewApp builds an App with the core pre-registered and the app's overlay on
// top: core expanders first, then opts.Expand (so an app may override a core
// primitive such as checkbox/editor); the vocab overlay is merged over the core
// — re-declaring a core primitive is an error.
func NewApp(opts Options) (*App, error) {
	expand := map[string]ExpandFunc{}
	for k, v := range coreExpanders {
		expand[k] = v
	}
	for k, v := range opts.Expand {
		expand[k] = v
	}
	inline := map[string]InlineFunc{}
	for k, v := range coreInline {
		inline[k] = v
	}
	for k, v := range opts.Inline {
		inline[k] = v
	}
	return base.NewApp(base.Options{
		Base:         CoreVocab,
		VocabOverlay: opts.VocabOverlay,
		ExtendProps:  opts.ExtendProps,
		Expand:       expand,
		Inline:       inline,
		Mounts:       opts.Mounts,
		Chrome:       opts.Chrome,
	})
}

// CoreChrome is the design system's default page shell (sheet/full kinds, the
// sync dot). Apps may start from it or supply their own Chrome.
func CoreChrome() Chrome {
	return Chrome{
		Lang:         "ru",
		Viewport:     "width=device-width, initial-scale=1",
		Stylesheets:  []string{"/static/styles.css"},
		FontPreloads: []string{"/static/fonts/noto-sans-var.woff2"},
		BootScripts:  []string{"/static/menu.js"},
		DefaultKind:  "sheet",
		PageKinds: map[string]PageKind{
			"sheet": {Body: []string{"host"}, Main: []string{"match-main"}, Frame: []string{"sheet-frame"}},
			"full":  {Body: []string{"host"}, Main: []string{"match-main"}},
		},
		TopbarSync: SyncSpec{ID: "status", Class: "sync-status", State: "saved", Label: "Готово"},
	}
}

// DefaultApp is a core-only App with the default Chrome — used by cmd/uic and
// the kit fixtures. Apps build their own via NewApp with an overlay.
var DefaultApp = mustApp(NewApp(Options{Chrome: CoreChrome()}))

func mustApp(a *App, err error) *App {
	if err != nil {
		panic(err)
	}
	return a
}

// Compile / Render at package level use DefaultApp (core vocabulary only).
func Compile(name string, src []byte) ([]byte, error) { return DefaultApp.Compile(name, src) }
func Render(doc *Doc) ([]byte, error)                 { return DefaultApp.Render(doc) }
