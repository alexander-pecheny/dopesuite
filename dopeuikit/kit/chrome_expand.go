package kit

// The page/topbar chrome expanders: they read the App's Chrome (via ctx) and
// emit the document shell and header. Class names here (host-top, host-brand, …)
// are the design system's, matched by core.css.

func expandPage(ctx *ExpandCtx, p *Element) []Node {
	ch := ctx.Chrome()
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
	for _, src := range ch.BootScripts {
		head = append(head, El("script", []Attr{At("src", src)}))
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

func expandTopbar(ctx *ExpandCtx, p *Element) []Node {
	ch := ctx.Chrome()
	title, _ := Get(p, "title")
	titleid, hasTitleID := Get(p, "titleid")

	var heading []Node
	if home, ok := Get(p, "home"); ok {
		brandTitle := Inl("h1", classAndID([]string{"host-title"}, titleid, hasTitleID), &TextNode{Value: title})
		heading = []Node{El("div", []Attr{ClassAttr("host-brand")},
			Inl("a", []Attr{ClassAttr("host-home"), At("href", home), At("aria-label", "Все доски"), At("title", "Все доски")}, &TextNode{Value: "🏠"}),
			Inl("span", []Attr{ClassAttr("host-sep"), At("aria-hidden", "true")}, &TextNode{Value: "/"}),
			brandTitle,
		)}
	} else {
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
	actions = append(actions, ctx.Nodes(p.Block)...)

	kids := append(heading, El("div", []Attr{ClassAttr("host-actions")}, actions...))
	headerAttrs := []Attr{ClassAttr("host-top")}
	headerAttrs = append(headerAttrs, IDAttr(p)...)
	if Flag(p, "hidden") {
		headerAttrs = append(headerAttrs, BareAt("hidden"))
	}
	headerAttrs = append(headerAttrs, Passthrough(p)...)
	return one(El("header", headerAttrs, kids...))
}

func classAndID(classes []string, id string, has bool) []Attr {
	out := []Attr{ClassAttr(classes...)}
	if has {
		out = append(out, At("id", id))
	}
	return out
}

func idIfSet(id string, has bool) []Attr {
	if has {
		return []Attr{At("id", id)}
	}
	return nil
}
