package ui

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

// Chrome is the app's page shell: language, viewport, head assets, page kinds
// and the topbar sync default. HeadHook lets an extension contribute head nodes
// right after the boot scripts (e.g. dope's init-payload marker).
type Chrome struct {
	Lang         string
	Viewport     string
	Stylesheets  []string
	FontPreloads []string
	BootScripts  []string
	PageKinds    map[string]PageKind
	DefaultKind  string
	TopbarSync   SyncSpec
	HeadHook     func(ctx *ExpandCtx, p *Element) []Node
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
