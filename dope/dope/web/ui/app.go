// Package ui is dope's thin overlay over DopeUIKit's kit (pecheny.me/dopeuikit/
// kit): the dope-specific game topbar primitive, the table-host mount kind, the
// page `init` marker prop, and dope's page chrome (sheet + the ek/viewer/od/si
// game kinds). The shared design system + DSL engine live in the kit; tags_gen.go
// re-exports every kit constructor/const so dope code imports this one package.
package ui

//go:generate go run pecheny.me/dopeuikit/cmd/uigen -core ../../../../dopeuikit/kit/vocab.json -overlay vocab.json -pkg ui -base pecheny.me/dopeuikit/kit -out tags_gen.go

import (
	_ "embed"

	base "pecheny.me/dopeuikit/kit"
)

//go:embed vocab.json
var overlayVocab []byte

// viewportContent is dope's plain (non-PWA) viewport.
const viewportContent = "width=device-width, initial-scale=1"

var dopeApp = mustApp()

func mustApp() *base.App {
	app, err := base.NewApp(base.Options{
		VocabOverlay: overlayVocab,
		ExtendProps: map[string][]base.PropSpec{
			"page":     {{Name: "init"}, {Name: "refresh"}},
			"checkbox": {{Name: "name"}, {Name: "value"}},
			"summary":  {{Name: "btn", Kind: "bare"}},
		},
		Expand: map[string]base.ExpandFunc{
			"gametopbar":   expandGameTopbar,
			"publictopbar": expandPublicTopbar,
			"link":         expandLink,
			"codedisplay":  expandCodeDisplay,
			"note":         expandNote,
			"checkbox":     expandCheckbox,
			"summary":      expandSummary,
			"palette":      expandPalette,
			"swatchradio":  expandSwatchradio,
			"numbersform":  expandNumbersform,
			"numberlist":   expandNumberlist,
			"numberrow":    expandNumberrow,
			"pickgroup":    expandPickgroup,
			"actionlist":   expandActionlist,
			"actionrow":    expandActionrow,
			"rowlink":      expandRowlink,
		},
		Inline: map[string]base.InlineFunc{
			"link": inlineLink,
		},
		Mounts: dopeMounts,
		Chrome: base.Chrome{
			Lang:         "ru",
			Viewport:     viewportContent,
			Stylesheets:  []string{"/static/styles.css"},
			FontPreloads: []string{"/static/fonts/noto-sans-var.woff2"},
			BootScripts:  []string{"/static/menu.js"},
			DefaultKind:  "sheet",
			PageKinds: map[string]base.PageKind{
				"public": {Body: []string{"public"}, Main: []string{"public-main"}},
				"sheet":  {Body: []string{"host", "import-page"}, Main: []string{"match-main"}, Frame: []string{"sheet-frame", "import-frame"}},
				"ek":     {Body: []string{"host", "host-compact", "ek-page"}, Main: []string{"match-main"}, Frame: []string{"sheet-frame", "fight-frame"}},
				"viewer": {Body: []string{"host", "host-compact", "viewer-readonly", "ek-page"}, Main: []string{"match-main"}, Frame: []string{"sheet-frame", "fight-frame"}},
				"od":     {Body: []string{"host", "host-compact", "od-page"}, Main: []string{"match-main"}, Frame: []string{"sheet-frame", "fight-frame"}},
				"si":     {Body: []string{"host", "host-compact", "si-page"}, Main: []string{"match-main"}, Frame: []string{"sheet-frame", "fight-frame"}},
			},
			TopbarSync: base.SyncSpec{ID: "status", Class: "sync-status", State: "saved", Label: "Готово"},
			HeadHook:   headHook,
		},
	})
	if err != nil {
		panic(err)
	}
	return app
}

// headHook emits, between the boot script and the page scripts, dope's per-request
// init marker and/or a meta-refresh. The marker is a NON-executable
// `<script type="application/json">` data block (not inline JS), so a strict
// `script-src 'self'` CSP needs no nonce/unsafe-inline; init.js reads the block
// into window.<name>. serve_html.go splices the route JSON over the exact
// byte-string `null;/*__X_INIT__*/`, so the block body must contain it verbatim.
// The refresh prop drives the register code stage's 2s auto-advance.
func headHook(_ *base.ExpandCtx, p *base.Element) []base.Node {
	var out []base.Node
	if name, ok := base.Get(p, "init"); ok {
		marker := "null;/*" + name + "*/"
		out = append(out, base.Inl("script", []base.Attr{
			base.At("type", "application/json"),
			base.At("id", name),
			base.At("data-dope-init", ""),
		}, &base.TextNode{Value: marker}))
	}
	if sec, ok := base.Get(p, "refresh"); ok {
		out = append(out, base.El("meta", []base.Attr{base.At("http-equiv", "refresh"), base.At("content", sec)}))
	}
	return out
}

// Refresh sets the page's meta-refresh interval (seconds); headHook emits the tag.
func Refresh(sec string) base.Attr { return base.Attr{Name: "refresh", Value: sec} }

// Btn styles a disclosure's <summary> as a button (create-fest on the host home).
func Btn() base.Attr { return base.Attr{Name: "btn", Bare: true} }

// dopeMounts maps each dope mount kind to the empty JS-owned container it renders.
var dopeMounts = map[string]base.MountSpec{
	"table-host": {Tag: "div", Classes: []string{"table-host"}},
}

// Compile parses, validates and expands a .dopeui page against dope's vocabulary.
func Compile(name string, src []byte) ([]byte, error) { return dopeApp.Compile(name, src) }

// Render validates and prints a builder-made tree (for the later template ports).
func Render(doc *Doc) ([]byte, error) { return dopeApp.Render(doc) }

func one(e *base.Element) []base.Node { return []base.Node{e} }
