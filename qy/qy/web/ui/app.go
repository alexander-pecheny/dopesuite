// Package ui is qy's thin overlay over DopeUIKit's kit: one page kind with a
// reading measure, and the richtext primitive that carries HTML the server
// rendered itself (a CMS page, a question). tags_gen.go re-exports the kit's
// constructors, so qy code imports this one package.
package ui

//go:generate go run pecheny.me/dopeuikit/cmd/uigen -core ../../../../dopeuikit/kit/vocab.json -overlay vocab.json -pkg ui -base pecheny.me/dopeuikit/kit -out tags_gen.go

import (
	_ "embed"

	base "pecheny.me/dopeuikit/kit"

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
	boxAttrs := []base.Attr{base.At("type", "checkbox")}
	boxAttrs = append(boxAttrs, base.IDAttr(p)...)
	boxAttrs = append(boxAttrs, base.CopyProps(p, "name", "value")...)
	boxAttrs = append(boxAttrs, base.CopyFlags(p, "checked")...)
	span := &base.Element{Tag: "span", Inline: c.Items(p.Inline)}
	return []base.Node{&base.Element{Tag: "label", Attrs: labelAttrs, Block: []base.Node{base.El("input", boxAttrs), span}}}
}

// Compile parses, validates and expands a .dopeui page (the kit's login page).
func Compile(name string, src []byte) ([]byte, error) { return app.Compile(name, src) }

// Render validates and prints a builder-made page.
func Render(doc *Doc) ([]byte, error) { return app.Render(doc) }
