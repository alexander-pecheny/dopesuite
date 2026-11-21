// Package ui is qy's thin overlay over DopeUIKit's kit: one page kind with a
// reading measure, and the richtext primitive that carries HTML the server
// rendered itself (a CMS page, a question). tags_gen.go re-exports the kit's
// constructors, so qy code imports this one package.
package ui

//go:generate go run code.pecheny.me/pecheny/dopesuite/dopeuikit/cmd/uigen -core ../../../../dopeuikit/kit/vocab.json -overlay vocab.json -pkg ui -base code.pecheny.me/pecheny/dopesuite/dopeuikit/kit -out tags_gen.go

import (
	_ "embed"

	"code.pecheny.me/pecheny/dopesuite/dopeuikit/icons"
	base "code.pecheny.me/pecheny/dopesuite/dopeuikit/kit"

	qystrings "qy/i18nstrings"
)

//go:embed vocab.json
var overlayVocab []byte

var app = mustApp()

func mustApp() *base.App {
	a, err := base.NewApp(base.Options{
		Strings:      qystrings.Default,
		VocabOverlay: overlayVocab,
		// A like and a ballot are posted forms, so a checkbox needs the name
		// and value the kit's own does not carry (dope extends it the same way).
		ExtendProps: map[string][]base.PropSpec{"checkbox": {{Name: "name"}, {Name: "value"}}},
		Expand: map[string]base.ExpandFunc{
			"richtext": expandRichtext,
			"checkbox": expandCheckbox,
			"toggle":   expandToggle,
		},
		Chrome: base.CoreChrome().With(base.Chrome{
			// The kit's sheet caps a section at a form's 420px. Questions and
			// rules are read, so qy's pages take a reading measure instead.
			PageKinds: map[string]base.PageKind{
				"wide": {Body: []string{"host"}, Main: []string{"match-main"}, Frame: []string{"sheet-frame", "wide-frame"}},
			},
		}),
	})
	if err != nil {
		panic(err)
	}
	return a
}

// expandRichtext wraps markup the server already rendered in the prose block
// qy's stylesheet sets. Its child is a Raw node, which is why the primitive
// exists: everything else in the vocabulary escapes its text.
func expandRichtext(c *base.ExpandCtx, p *base.Element) []base.Node {
	return []base.Node{base.El("div", []base.Attr{base.ClassAttr("richtext")}, c.Nodes(p.Block)...)}
}

func expandCheckbox(c *base.ExpandCtx, p *base.Element) []base.Node {
	labelAttrs := []base.Attr{base.ClassAttr(base.GrowClasses([]string{"checkbox"}, p)...)}
	labelAttrs = append(labelAttrs, base.MetaAttrs(p)...)
	span := &base.Element{Tag: "span", Inline: c.Items(p.Inline)}
	return []base.Node{&base.Element{Tag: "label", Attrs: labelAttrs, Block: []base.Node{boxInput(p, "checkbox"), span}}}
}

// boxInput is the checkbox or radio a label wraps, posted under its name.
func boxInput(p *base.Element, typ string) base.Node {
	attrs := []base.Attr{base.At("type", typ)}
	attrs = append(attrs, base.IDAttr(p)...)
	attrs = append(attrs, base.CopyProps(p, "name", "value")...)
	return base.El("input", append(attrs, base.CopyFlags(p, "checked")...))
}

// expandToggle is a checkbox drawn as a button, pressed while it is checked:
// the jury's like. An exclusive one is a radio, one of a group: the favourite.
// The box itself is hidden by the stylesheet and still takes the focus and the
// clicks through its label, so a form posts it like any other.
func expandToggle(c *base.ExpandCtx, p *base.Element) []base.Node {
	labelAttrs := append([]base.Attr{base.ClassAttr("btn", "btn-ghost", "toggle")}, base.MetaAttrs(p)...)
	typ := "checkbox"
	if base.Flag(p, "exclusive") {
		typ = "radio"
	}
	kids := []base.Node{boxInput(p, typ)}
	if name, ok := base.Get(p, "icon"); ok {
		if body, found := icons.Body(name); found {
			kids = append(kids, base.Raw(`<svg class="ico ico-lead" viewBox="0 0 24 24" fill="none" stroke="currentColor" `+
				`stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">`+body+`</svg>`))
		}
	}
	kids = append(kids, &base.Element{Tag: "span", Inline: c.Items(p.Inline)})
	return []base.Node{&base.Element{Tag: "label", Attrs: labelAttrs, Block: kids}}
}

// Compile parses, validates and expands a .dopeui page (the kit's login page).
func Compile(name string, src []byte) ([]byte, error) { return app.Compile(name, src) }

// Render validates and prints a builder-made page.
func Render(doc *Doc) ([]byte, error) { return app.Render(doc) }
