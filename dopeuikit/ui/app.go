package ui

// ExpandFunc expands one app/core primitive into HTML nodes. InlineFunc does
// the same for an inline primitive (usable inside text runs).
type ExpandFunc func(ctx *ExpandCtx, p *Element) []Node
type InlineFunc func(ctx *ExpandCtx, p *Element) Item

// MountSpec maps a mount kind to the empty JS-owned container it renders as.
type MountSpec struct {
	Tag     string
	Classes []string
}

// Options configures an App. The engine is design-system-agnostic: the caller
// (the kit design-system layer, or a test) supplies the Base vocabulary, the
// full Expand/Inline expander tables, mount kinds, and page Chrome. VocabOverlay
// is merged over Base (adding primitives / enums / enum values; re-declaring a
// Base primitive is an error). ExtendProps adds props to an existing primitive.
type Options struct {
	Base         *Vocab
	VocabOverlay []byte
	ExtendProps  map[string][]PropSpec // extra props added to existing primitives
	Expand       map[string]ExpandFunc
	Inline       map[string]InlineFunc
	Mounts       map[string]MountSpec
	Chrome       Chrome
}

// App is a configured DSL instance: a merged vocabulary plus the expander
// tables that turn a primitive tree into HTML.
type App struct {
	vocab  *Vocab
	expand map[string]ExpandFunc
	inline map[string]InlineFunc
	mounts map[string]MountSpec
	chrome Chrome
}

// NewApp builds an App from opts.Base plus the given overlay. Expand/Inline are
// the complete expander tables (the kit layers core under the app's overrides
// before calling this). The vocab overlay may only add — re-declaring a Base
// primitive is an error.
func NewApp(opts Options) (*App, error) {
	if opts.Base == nil {
		return nil, errf("", 0, "ui.NewApp: Options.Base vocabulary is required")
	}
	vocab := opts.Base
	if len(opts.VocabOverlay) > 0 {
		merged, err := vocab.Merge(opts.VocabOverlay)
		if err != nil {
			return nil, err
		}
		vocab = merged
	}
	if len(opts.ExtendProps) > 0 {
		vocab = vocab.WithExtraProps(opts.ExtendProps)
	}
	app := &App{
		vocab:  vocab,
		expand: map[string]ExpandFunc{},
		inline: map[string]InlineFunc{},
		mounts: map[string]MountSpec{},
		chrome: opts.Chrome.withDefaults(),
	}
	for k, v := range opts.Expand {
		app.expand[k] = v
	}
	for k, v := range opts.Inline {
		app.inline[k] = v
	}
	for k, v := range opts.Mounts {
		app.mounts[k] = v
	}
	return app, nil
}

// Vocab returns the app's merged vocabulary (consulted by cmd/uigen and tests).
func (a *App) Vocab() *Vocab { return a.vocab }

// Compile parses, validates and expands a .dopeui page. name labels errors as
// "name:line: message". The single meaningful top-level node must be the
// vocabulary's declared root primitive (the design system's `page`).
func (a *App) Compile(name string, src []byte) ([]byte, error) {
	doc, err := Parse(name, src)
	if err != nil {
		return nil, err
	}
	if err := requireRoot(name, doc, a.vocab.Root); err != nil {
		return nil, err
	}
	if err := Validate(a.vocab, name, doc); err != nil {
		return nil, err
	}
	return a.render(doc), nil
}

// Render validates a builder-made tree and prints it.
func (a *App) Render(doc *Doc) ([]byte, error) {
	if err := Validate(a.vocab, "", doc); err != nil {
		return nil, err
	}
	return a.render(doc), nil
}

// requireRoot enforces that a page file's single meaningful top-level node is
// the vocabulary's declared root primitive. An empty root leaves it unchecked.
func requireRoot(name string, doc *Doc, root string) error {
	if root == "" {
		return nil
	}
	var roots []*Element
	for _, n := range doc.Nodes {
		switch v := n.(type) {
		case *Element:
			roots = append(roots, v)
		case *Comment, *BlankLine:
		default:
			return errf(name, 0, "a page's top level may only contain a single `%s` node", root)
		}
	}
	if len(roots) != 1 || roots[0].Tag != root {
		line := 0
		if len(roots) > 0 {
			line = roots[0].Line
		}
		return errf(name, line, "a page file must have exactly one top-level `%s` node", root)
	}
	return nil
}
