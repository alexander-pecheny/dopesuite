package kit

// PageKind maps a page `kind` to the body/main class lists plus an optional
// frame wrapper around main's content.
type PageKind struct{ Body, Main, Frame []string }

// SyncSpec is the topbar's auto-emitted sync-status dot (default data-state and
// label; a page may override the state via topbar `syncstate`, or drop the dot
// with `nosync`).
type SyncSpec struct {
	ID    string
	Class string
	State string
	Label string
}

// HeadLink is a plain <link> in the head — icons and the web-app manifest.
// Empty fields are omitted.
type HeadLink struct{ Rel, Type, Sizes, Href string }

// Chrome is the app's page shell: language, viewport, head assets, page kinds
// and the topbar sync default. HeadHook lets an extension contribute head nodes
// right after the boot scripts (e.g. dope's init-payload marker). It rides in
// the engine's Options.Env; ChromeOf reads it back.
type Chrome struct {
	Lang         string
	Viewport     string
	Stylesheets  []string
	FontPreloads []string
	HeadLinks    []HeadLink
	BootScripts  []string
	// ModuleBootScripts load on every page like BootScripts but as ES modules
	// (deferred by the browser) — for app chrome that doesn't need to block
	// first paint the way the theme boot does.
	ModuleBootScripts []string
	PageKinds         map[string]PageKind
	DefaultKind       string
	TopbarSync        SyncSpec
	HeadHook          func(ctx *ExpandCtx, p *Element) []Node
}

func (c Chrome) withDefaults() Chrome {
	if c.Lang == "" {
		c.Lang = "ru"
	}
	if c.DefaultKind == "" {
		c.DefaultKind = "sheet"
	}
	return c
}

// PageKindFor returns the PageKind for a kind name (a zero PageKind when unset).
func (c Chrome) PageKindFor(name string) PageKind {
	if pk, ok := c.PageKinds[name]; ok {
		return pk
	}
	return PageKind{}
}

// ChromeOf is the app's Chrome, as kit.NewApp stored it.
func ChromeOf(ctx *ExpandCtx) Chrome { return ctx.Env().(Chrome) }

// With overlays d on c: a set field of d replaces c's, PageKinds merge by name.
// An app states only where it departs from CoreChrome.
func (c Chrome) With(d Chrome) Chrome {
	if d.Lang != "" {
		c.Lang = d.Lang
	}
	if d.Viewport != "" {
		c.Viewport = d.Viewport
	}
	if d.Stylesheets != nil {
		c.Stylesheets = d.Stylesheets
	}
	if d.FontPreloads != nil {
		c.FontPreloads = d.FontPreloads
	}
	if d.HeadLinks != nil {
		c.HeadLinks = d.HeadLinks
	}
	if d.BootScripts != nil {
		c.BootScripts = d.BootScripts
	}
	if d.ModuleBootScripts != nil {
		c.ModuleBootScripts = d.ModuleBootScripts
	}
	if d.DefaultKind != "" {
		c.DefaultKind = d.DefaultKind
	}
	if d.TopbarSync != (SyncSpec{}) {
		c.TopbarSync = d.TopbarSync
	}
	if d.HeadHook != nil {
		c.HeadHook = d.HeadHook
	}
	if len(d.PageKinds) > 0 {
		kinds := make(map[string]PageKind, len(c.PageKinds)+len(d.PageKinds))
		for k, v := range c.PageKinds {
			kinds[k] = v
		}
		for k, v := range d.PageKinds {
			kinds[k] = v
		}
		c.PageKinds = kinds
	}
	return c
}
