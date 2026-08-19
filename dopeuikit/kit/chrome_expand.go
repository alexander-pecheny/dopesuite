package kit

// The page/topbar chrome expanders: they read the App's Chrome (via ctx) and
// emit the document shell and header. Class names here (host-top, host-brand, …)
// are the design system's, matched by core.css.

func expandPage(ctx *ExpandCtx, p *Element) []Node {
	ch := ChromeOf(ctx)
	title, _ := Get(p, "title")

	head := []Node{
		El("meta", []Attr{At("charset", "utf-8")}),
		El("meta", []Attr{At("name", "viewport"), At("content", ch.Viewport)}),
		Inl("title", nil, &TextNode{Value: title}),
	}
	for _, href := range ch.FontPreloads {
		head = append(head, El("link", []Attr{At("rel", "preload"), At("href", href), At("as", "font"), At("type", "font/woff2"), BareAt("crossorigin")}))
	}
	for _, href := range ch.Stylesheets {
		head = append(head, El("link", []Attr{At("rel", "stylesheet"), At("href", href)}))
	}
	for _, l := range ch.HeadLinks {
		attrs := []Attr{At("rel", l.Rel), At("href", l.Href)}
		if l.Type != "" {
			attrs = append(attrs, At("type", l.Type))
		}
		if l.Sizes != "" {
			attrs = append(attrs, At("sizes", l.Sizes))
		}
		head = append(head, El("link", attrs))
	}
	for _, src := range ch.BootScripts {
		head = append(head, El("script", []Attr{At("src", src)}))
	}
	for _, src := range ch.ModuleBootScripts {
		head = append(head, El("script", []Attr{At("type", "module"), At("src", src)}))
	}
	if ch.HeadHook != nil {
		head = append(head, ch.HeadHook(ctx, p)...)
	}
	if v, ok := Get(p, "classicscripts"); ok {
		for _, name := range fields(v) {
			head = append(head, El("script", []Attr{BareAt("defer"), At("src", "/static/"+name)}))
		}
	}
	if v, ok := Get(p, "scripts"); ok {
		for _, name := range fields(v) {
			head = append(head, El("script", []Attr{At("type", "module"), At("src", "/static/"+name)}))
		}
	}

	kind, ok := Get(p, "kind")
	if !ok {
		kind = ch.DefaultKind
	}
	pk := ch.PageKindFor(kind)

	var header Node
	var overlays, mainKids []Node
	var pending []Node
	for _, c := range p.Block {
		e, isEl := c.(*Element)
		if !isEl {
			pending = append(pending, c)
			continue
		}
		switch ctx.Placement(e.Tag) {
		case "header":
			header = first(ctx.Expand(e))
			pending = nil
		case "overlay":
			overlays = append(overlays, pending...)
			overlays = append(overlays, ctx.Expand(e)...)
			pending = nil
		default:
			mainKids = append(mainKids, pending...)
			mainKids = append(mainKids, ctx.Expand(e)...)
			pending = nil
		}
	}
	mainKids = append(mainKids, pending...)

	if len(pk.Frame) > 0 {
		mainKids = []Node{El("div", []Attr{ClassAttr(pk.Frame...)}, mainKids...)}
	}
	var mainAttrs []Attr
	if len(pk.Main) > 0 {
		mainAttrs = []Attr{ClassAttr(pk.Main...)}
	}
	main := El("main", mainAttrs, mainKids...)

	body := []Node{main}
	if header != nil {
		body = append([]Node{header}, body...)
	}
	body = append(body, overlays...)

	var bodyAttrs []Attr
	if len(pk.Body) > 0 {
		bodyAttrs = append(bodyAttrs, ClassAttr(pk.Body...))
	}
	bodyAttrs = append(bodyAttrs, Passthrough(p)...)

	return []Node{
		&Doctype{},
		El("html", []Attr{At("lang", ch.Lang)},
			El("head", nil, head...),
			El("body", bodyAttrs, body...),
		),
	}
}

// ExpandCrumbs renders a breadcrumb trail: every crumb is a real navigable
// prefix of the current URL, and the last one — the page you are on — is plain
// text rather than a link. Apps reach it through their topbar; it is exported so
// an app's own header primitive (dope's publictopbar) can place one too.
func ExpandCrumbs(ctx *ExpandCtx, p *Element) []Node {
	crumbs := childElements(p, "crumb")
	kids := make([]Node, 0, len(crumbs)*2)
	for i, c := range crumbs {
		if i > 0 {
			kids = append(kids, Inl("span", []Attr{ClassAttr("crumb-sep"), At("aria-hidden", "true")}, &TextNode{Value: "/"}))
		}
		kids = append(kids, crumbNode(ctx, c, i == len(crumbs)-1))
	}
	return one(El("nav", []Attr{ClassAttr("crumbs"), At("aria-label", "Навигация")}, kids...))
}

func crumbNode(ctx *ExpandCtx, c *Element, last bool) Node {
	classes := []string{"crumb"}
	if Flag(c, "home") {
		classes = append(classes, "crumb-home")
	}
	if last {
		classes = append(classes, "crumb-current")
	}
	href, linked := Get(c, "href")
	// The home crumb is an icon, so it needs the name spelling out; the rest say
	// what they are already.
	var extra []Attr
	if label, ok := Get(c, "label"); ok {
		extra = append(extra, At("aria-label", label), At("title", label))
	}
	if !linked || last {
		attrs := append([]Attr{ClassAttr(classes...)}, extra...)
		attrs = append(attrs, IDAttr(c)...)
		if last {
			attrs = append(attrs, At("aria-current", "page"))
		}
		return &Element{Tag: "span", Attrs: attrs, Inline: withIcon(c, ctx.Items(c.Inline))}
	}
	attrs := append([]Attr{ClassAttr(classes...), At("href", href)}, extra...)
	attrs = append(attrs, IDAttr(c)...)
	return &Element{Tag: "a", Attrs: attrs, Inline: withIcon(c, ctx.Items(c.Inline))}
}

func childElements(p *Element, tag string) []*Element {
	var out []*Element
	for _, n := range p.Block {
		if e, ok := n.(*Element); ok && e.Tag == tag {
			out = append(out, e)
		}
	}
	return out
}

func expandTopbar(ctx *ExpandCtx, p *Element) []Node {
	ch := ChromeOf(ctx)
	title, _ := Get(p, "title")
	titleid, hasTitleID := Get(p, "titleid")

	// A crumbs child is the heading when present: the full path replaces the
	// bare title, which said where you were but not how you got there.
	var crumbs *Element
	var rest []Node
	for _, n := range p.Block {
		if e, ok := n.(*Element); ok && e.Tag == "crumbs" {
			crumbs = e
			continue
		}
		rest = append(rest, n)
	}

	var heading []Node
	switch {
	case crumbs != nil:
		heading = []Node{El("div", []Attr{ClassAttr("host-brand")}, ctx.Expand(crumbs)...)}
	default:
		heading = []Node{Inl("h1", idIfSet(titleid, hasTitleID), &TextNode{Value: title})}
	}

	var actions []Node
	if !Flag(p, "nosync") {
		s := ch.TopbarSync
		state := s.State
		if v, ok := Get(p, "syncstate"); ok {
			state = v
		}
		actions = append(actions, El("span", []Attr{ClassAttr(s.Class), At("id", s.ID), At("data-state", state), At("aria-label", s.Label), At("title", s.Label)}))
	}
	actions = append(actions, ctx.Nodes(rest)...)

	kids := append(heading, El("div", []Attr{ClassAttr("host-actions")}, actions...))
	headerAttrs := []Attr{ClassAttr("host-top")}
	headerAttrs = append(headerAttrs, IDAttr(p)...)
	if Flag(p, "hidden") {
		headerAttrs = append(headerAttrs, BareAt("hidden"))
	}
	headerAttrs = append(headerAttrs, Passthrough(p)...)
	return one(El("header", headerAttrs, kids...))
}

func idIfSet(id string, has bool) []Attr {
	if has {
		return []Attr{At("id", id)}
	}
	return nil
}
