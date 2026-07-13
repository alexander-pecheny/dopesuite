package ui

import "strings"

// render expands a validated primitive tree into an HTML node tree and prints
// it. This is the ONLY file that knows HTML tags and CSS classes.
func render(doc *Doc) []byte {
	return printDoc(&Doc{Nodes: expandNodes(doc.Nodes)})
}

// ---- expansion: primitive tree -> HTML node tree ----------------------------

func expandNodes(nodes []Node) []Node {
	var out []Node
	for _, n := range nodes {
		out = append(out, expandNode(n)...)
	}
	return out
}

func expandNode(n Node) []Node {
	switch v := n.(type) {
	case *Element:
		return expandPrim(v)
	case *RunNode:
		return []Node{&RunNode{Items: expandItems(v.Items)}}
	case *TextNode, *Comment, *BlankLine:
		return []Node{n}
	}
	return nil
}

func expandItems(items []Item) []Item {
	var out []Item
	for _, it := range items {
		switch v := it.(type) {
		case *TextNode:
			out = append(out, v)
		case *Element:
			out = append(out, inlinePrim(v))
		}
	}
	return out
}

func inlinePrim(p *Element) Item {
	switch p.Tag {
	case "code":
		return &Element{Tag: "code", Attrs: rootAttrs(nil, p), Inline: expandItems(p.Inline)}
	case "muted":
		return &Element{Tag: "span", Attrs: rootAttrs([]string{"muted"}, p), Inline: expandItems(p.Inline)}
	case "unreaddot":
		return &Element{Tag: "span", Attrs: unreadAttrs(p)}
	default: // strong
		return &Element{Tag: "strong", Attrs: rootAttrs(nil, p), Inline: expandItems(p.Inline)}
	}
}

func expandPrim(p *Element) []Node {
	switch p.Tag {
	case "page":
		return expandPage(p)
	case "topbar":
		return one(expandTopbar(p))
	case "iconbtn":
		return one(expandIconbtn(p))
	case "iconlink":
		return one(expandIconlink(p))
	case "col":
		return one(hel("div", rootAttrs(flexClasses("u-col", p), p), expandNodes(p.Block)...))
	case "row":
		return one(hel("div", rootAttrs(flexClasses("u-row", p), p), expandNodes(p.Block)...))
	case "spacer":
		return one(hel("div", rootAttrs([]string{"u-spacer"}, p)))
	case "section":
		return one(hel("section", rootAttrs([]string{"auth-step"}, p), expandNodes(p.Block)...))
	case "text":
		return one(leaf("p", nil, nil, p))
	case "hint":
		return one(leaf("p", []string{"auth-hint"}, nil, p))
	case "subhead":
		return one(leaf("h2", []string{"auth-subhead"}, nil, p))
	case "label":
		return one(leaf("label", []string{"card-section-label"}, forAttr(p), p))
	case "bigcode":
		return one(leaf("p", []string{"register-code"}, nil, p))
	case "message":
		return one(leaf("pre", []string{"import-message"}, nil, p))
	case "previewtitle":
		return one(leaf("h2", []string{"preview-title"}, nil, p))
	case "listtitle":
		return one(leaf("span", []string{"list-row-title"}, nil, p))
	case "strong", "code", "muted":
		return []Node{inlinePrim(p).(Node)}
	case "form":
		return one(expandForm(p))
	case "textfield":
		return one(input("text", p, "name", "placeholder", "autocomplete", "spellcheck", "autocapitalize", "autocorrect", "value", "maxlength"))
	case "password":
		return one(input("password", p, "name", "placeholder", "autocomplete", "spellcheck", "autocapitalize", "autocorrect", "value", "maxlength"))
	case "filefield":
		return one(input("file", p, "accept"))
	case "numfield":
		return one(expandNumfield(p))
	case "colorfield":
		return one(hel("input", colorAttrs(p)))
	case "sliderrow":
		return one(expandSliderrow(p))
	case "checkbox":
		return one(expandCheckbox(p))
	case "selectfield":
		return one(expandSelect(p))
	case "option":
		return one(expandOption(p))
	case "editor":
		return one(expandEditor(p))
	case "button":
		return one(expandButton(p))
	case "field":
		return one(expandField(p))
	case "unreaddot":
		return one(hel("span", unreadAttrs(p)))
	case "modal":
		return one(expandModal(p))
	case "docoverlay":
		return one(expandDocoverlay(p))
	case "headrow":
		return one(hel("div", rootAttrs([]string{headClass(p, "preview-head", "card-detail-head")}, p), expandNodes(p.Block)...))
	case "headactions":
		return one(hel("div", rootAttrs([]string{headClass(p, "preview-head-actions", "card-head-actions")}, p), expandNodes(p.Block)...))
	case "split":
		return one(hel("div", rootAttrs([]string{"handouts-body"}, p), expandNodes(p.Block)...))
	case "pane":
		return one(hel("div", rootAttrs([]string{"handouts-pane", paneClass(p)}, p), expandNodes(p.Block)...))
	case "tabs":
		return one(hel("div", rootAttrs([]string{"card-view-tabs"}, p, at("role", "tablist")), expandNodes(p.Block)...))
	case "tab":
		return one(expandTab(p))
	case "tabpanel":
		return one(hel("div", tabpanelAttrs(p), expandNodes(p.Block)...))
	case "list":
		return one(hel("ul", rootAttrs([]string{"list"}, p), expandNodes(p.Block)...))
	case "listrow":
		return one(expandListrow(p))
	case "table":
		return one(expandTable(p))
	case "trow":
		return one(hel("tr", rootAttrs(nil, p), expandNodes(p.Block)...))
	case "hcell":
		return one(leaf("th", nil, nil, p))
	case "cell":
		return one(leaf("td", nil, nil, p))
	case "mount":
		return one(expandMount(p))
	}
	return nil
}

func one(e *Element) []Node { return []Node{e} }

// ---- page chrome ------------------------------------------------------------

const viewportContent = "width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no, viewport-fit=cover"

func expandPage(p *Element) []Node {
	title, _ := gv(p, "title")
	head := []Node{
		hel("meta", []Attr{at("charset", "utf-8")}),
		hel("meta", []Attr{at("name", "viewport"), at("content", viewportContent)}),
		hinline("title", nil, &TextNode{Value: title}),
		hel("link", []Attr{at("rel", "preload"), at("href", "/static/fonts/noto-sans-400.woff2"), at("as", "font"), at("type", "font/woff2"), bareAt("crossorigin")}),
		hel("link", []Attr{at("rel", "stylesheet"), at("href", "/static/styles.css")}),
		hel("script", []Attr{at("src", "/static/menu.js")}),
	}
	if v, ok := gv(p, "classicscripts"); ok {
		for _, name := range strings.Fields(v) {
			head = append(head, hel("script", []Attr{bareAt("defer"), at("src", "/static/"+name)}))
		}
	}
	if v, ok := gv(p, "scripts"); ok {
		for _, name := range strings.Fields(v) {
			head = append(head, hel("script", []Attr{at("type", "module"), at("src", "/static/"+name)}))
		}
	}

	bodyCls, mainCls, sheet := pageClasses(p)

	var header Node
	var overlays, mainKids []Node
	var pending []Node
	for _, c := range p.Block {
		e, isEl := c.(*Element)
		if !isEl {
			pending = append(pending, c)
			continue
		}
		switch e.Tag {
		case "topbar":
			header = expandTopbar(e)
			pending = nil
		case "modal", "docoverlay":
			overlays = append(overlays, pending...)
			overlays = append(overlays, expandPrim(e)...)
			pending = nil
		default:
			mainKids = append(mainKids, pending...)
			mainKids = append(mainKids, expandPrim(e)...)
			pending = nil
		}
	}
	mainKids = append(mainKids, pending...)

	if sheet {
		mainKids = []Node{hel("div", []Attr{ca("sheet-frame", "import-frame")}, mainKids...)}
	}
	if kind, _ := gv(p, "kind"); kind == "wide" {
		// a centred, capped content column on the otherwise full-bleed board-main
		mainKids = []Node{hel("div", []Attr{ca("import-form")}, mainKids...)}
	}
	main := hel("main", []Attr{ca(strings.Fields(mainCls)...)}, mainKids...)

	body := []Node{main}
	if header != nil {
		body = append([]Node{header}, body...)
	}
	body = append(body, overlays...)

	return []Node{
		&Doctype{},
		hel("html", []Attr{at("lang", "ru")},
			hel("head", nil, head...),
			hel("body", []Attr{ca(strings.Fields(bodyCls)...)}, body...),
		),
	}
}

func pageClasses(p *Element) (body, main string, sheet bool) {
	kind, _ := gv(p, "kind")
	switch kind {
	case "full":
		return "host", "match-main", false
	case "wide":
		return "host", "board-main", false
	case "board":
		return "host board-page", "board-main", false
	default: // sheet
		return "host import-page", "match-main", true
	}
}

func expandTopbar(p *Element) *Element {
	title, _ := gv(p, "title")
	titleid, hasTitleID := gv(p, "titleid")

	var heading []Node
	if home, ok := gv(p, "home"); ok {
		brandTitle := hinline("h1", classAndID([]string{"host-title"}, titleid, hasTitleID), &TextNode{Value: title})
		heading = []Node{hel("div", []Attr{ca("host-brand")},
			hinline("a", []Attr{ca("host-home"), at("href", home), at("aria-label", "Все доски"), at("title", "Все доски")}, &TextNode{Value: "🏠"}),
			hinline("span", []Attr{ca("host-sep"), at("aria-hidden", "true")}, &TextNode{Value: "/"}),
			brandTitle,
		)}
	} else {
		heading = []Node{hinline("h1", idIfSet(titleid, hasTitleID), &TextNode{Value: title})}
	}

	actions := []Node{hel("span", []Attr{ca("sync-status"), at("id", "status"), at("data-state", "saved"), at("aria-label", "Готово"), at("title", "Готово")})}
	actions = append(actions, expandNodes(p.Block)...)

	kids := append(heading, hel("div", []Attr{ca("host-actions")}, actions...))
	// title is the h1 text, not a header tooltip: skip metaAttrs' title here.
	headerAttrs := []Attr{ca("host-top")}
	headerAttrs = append(headerAttrs, idAttr(p)...)
	if flagSet(p, "hidden") {
		headerAttrs = append(headerAttrs, bareAt("hidden"))
	}
	headerAttrs = append(headerAttrs, passthrough(p)...)
	return hel("header", headerAttrs, kids...)
}

func expandIconbtn(p *Element) *Element {
	label, _ := gv(p, "label")
	classes := []string{"action-icon"}
	badgeid, hasBadge := gv(p, "badgeid")
	if hasBadge {
		classes = append(classes, "notif-toggle")
	}
	attrs := []Attr{ca(classes...)}
	attrs = append(attrs, idAttr(p)...)
	attrs = append(attrs, at("type", "button"), at("aria-label", label), at("title", tooltip(p, label)))
	attrs = append(attrs, passthrough(p)...)
	items := expandItems(p.Inline)
	if hasBadge {
		items = append(items, &Element{Tag: "span", Attrs: []Attr{ca("notif-badge"), at("id", badgeid), bareAt("hidden")}})
	}
	return &Element{Tag: "button", Attrs: attrs, Inline: items}
}

func expandIconlink(p *Element) *Element {
	label, _ := gv(p, "label")
	href, _ := gv(p, "href")
	attrs := []Attr{ca("action-icon"), at("href", href), at("aria-label", label), at("title", tooltip(p, label))}
	attrs = append(attrs, passthrough(p)...)
	return &Element{Tag: "a", Attrs: attrs, Inline: expandItems(p.Inline)}
}

// ---- controls ---------------------------------------------------------------

func expandForm(p *Element) *Element {
	dirClass := "u-row"
	if d, _ := gv(p, "dir"); d == "col" {
		dirClass = "u-col"
	}
	classes := []string{dirClass, gapClass(p, "sm")}
	attrs := []Attr{ca(growClasses(classes, p)...)}
	attrs = append(attrs, idAttr(p)...)
	attrs = append(attrs, copyProps(p, "autocomplete", "method", "action")...)
	attrs = append(attrs, metaAttrs(p)...)
	return hel("form", attrs, expandNodes(p.Block)...)
}

func input(typ string, p *Element, valueProps ...string) *Element {
	attrs := []Attr{ca(growClasses([]string{"input"}, p)...)}
	attrs = append(attrs, idAttr(p)...)
	attrs = append(attrs, at("type", typ))
	attrs = append(attrs, copyProps(p, valueProps...)...)
	attrs = append(attrs, copyFlags(p, "required", "autofocus")...)
	attrs = append(attrs, metaAttrs(p)...)
	return hel("input", attrs)
}

func expandNumfield(p *Element) *Element {
	base := []string{"input"}
	if flagSet(p, "narrow") {
		base = append(base, "lists-move-pos")
	}
	attrs := []Attr{ca(growClasses(base, p)...)}
	attrs = append(attrs, idAttr(p)...)
	attrs = append(attrs, at("type", "number"))
	attrs = append(attrs, copyProps(p, "min", "max", "step", "placeholder")...)
	attrs = append(attrs, metaAttrs(p)...)
	return hel("input", attrs)
}

func colorAttrs(p *Element) []Attr {
	attrs := []Attr{ca(growClasses([]string{"label-color-input"}, p)...)}
	attrs = append(attrs, idAttr(p)...)
	attrs = append(attrs, at("type", "color"))
	attrs = append(attrs, copyProps(p, "value")...)
	attrs = append(attrs, metaAttrs(p)...)
	return attrs
}

func expandSliderrow(p *Element) *Element {
	id, _ := gv(p, "id")
	label, _ := gv(p, "label")
	valueid, _ := gv(p, "valueid")
	head := hel("div", []Attr{ca("sizes-row-head")},
		hinline("label", []Attr{ca("appearance-row-label"), at("for", id)}, &TextNode{Value: label}),
		hel("span", []Attr{ca("sizes-value"), at("id", valueid)}),
	)
	rangeAttrs := []Attr{at("type", "range"), at("id", id)}
	rangeAttrs = append(rangeAttrs, copyProps(p, "min", "max", "step")...)
	kids := []Node{head, hel("input", rangeAttrs)}
	if hint, ok := gv(p, "hint"); ok {
		kids = append(kids, hinline("p", []Attr{ca("sizes-hint")}, &TextNode{Value: hint}))
	}
	// The universal id is consumed by the range input; keep it off the row.
	outer := rootAttrs([]string{"sizes-row"}, p)
	return hel("div", dropAttr(outer, "id"), kids...)
}

func expandCheckbox(p *Element) *Element {
	classes := []string{"attach-lossless"}
	switch k, _ := gv(p, "kind"); k {
	case "preview":
		classes = append(classes, "preview-screen-toggle")
	case "card-preview":
		classes = append(classes, "card-preview-screen")
	}
	labelAttrs := []Attr{ca(growClasses(classes, p)...)}
	labelAttrs = append(labelAttrs, metaAttrs(p)...)
	boxAttrs := []Attr{at("type", "checkbox")}
	boxAttrs = append(boxAttrs, idAttr(p)...)
	boxAttrs = append(boxAttrs, copyFlags(p, "checked")...)
	items := []Item{hel("input", boxAttrs)}
	items = append(items, expandItems(p.Inline)...)
	return &Element{Tag: "label", Attrs: labelAttrs, Inline: items}
}

func expandSelect(p *Element) *Element {
	base := []string{"input"}
	if flagSet(p, "compact") {
		base = append(base, "card-kind-select")
	}
	attrs := []Attr{ca(growClasses(base, p)...)}
	attrs = append(attrs, idAttr(p)...)
	attrs = append(attrs, metaAttrs(p)...)
	return hel("select", attrs, expandNodes(p.Block)...)
}

func expandEditor(p *Element) *Element {
	var classes []string
	switch k, _ := gv(p, "kind"); k {
	case "comment":
		classes = []string{"card-desc", "comment-input"}
	case "handouts":
		classes = []string{"card-desc", "handouts-textarea"}
	case "importsrc":
		classes = []string{"input", "handouts-textarea", "import-textarea"}
	default:
		classes = []string{"card-desc"}
	}
	attrs := []Attr{ca(growClasses(classes, p)...)}
	attrs = append(attrs, idAttr(p)...)
	attrs = append(attrs, copyProps(p, "placeholder", "rows", "name", "spellcheck")...)
	attrs = append(attrs, copyFlags(p, "readonly", "required")...)
	attrs = append(attrs, metaAttrs(p)...)
	e := &Element{Tag: "textarea", Attrs: attrs}
	if len(p.Inline) > 0 {
		e.Inline = expandItems(p.Inline)
	} else {
		e.Block = expandNodes(p.Block)
	}
	return e
}

func expandButton(p *Element) *Element {
	classes := []string{"btn"}
	if k, _ := gv(p, "kind"); k == "ghost" {
		classes = append(classes, "btn-ghost")
	}
	if flagSet(p, "small") {
		classes = append(classes, "btn-small")
	}
	classes = growClasses(classes, p)

	href, hasHref := gv(p, "href")
	if hasHref || flagSet(p, "download") {
		attrs := []Attr{ca(classes...)}
		attrs = append(attrs, idAttr(p)...)
		if hasHref {
			attrs = append(attrs, at("href", href))
		}
		attrs = append(attrs, copyFlags(p, "download")...)
		attrs = append(attrs, metaAttrs(p)...)
		return &Element{Tag: "a", Attrs: attrs, Inline: expandItems(p.Inline)}
	}
	typ := "button"
	if flagSet(p, "submit") {
		typ = "submit"
	}
	attrs := []Attr{ca(classes...)}
	attrs = append(attrs, idAttr(p)...)
	attrs = append(attrs, at("type", typ))
	attrs = append(attrs, copyFlags(p, "disabled")...)
	attrs = append(attrs, metaAttrs(p)...)
	return &Element{Tag: "button", Attrs: attrs, Inline: expandItems(p.Inline)}
}

func expandField(p *Element) *Element {
	label, _ := gv(p, "label")
	attrs := rootAttrs([]string{"field"}, p)
	kids := []Node{hinline("span", nil, &TextNode{Value: label})}
	kids = append(kids, expandNodes(p.Block)...)
	return hel("label", attrs, kids...)
}

func unreadAttrs(p *Element) []Attr {
	attrs := []Attr{ca(growClasses([]string{"unread-dot"}, p)...)}
	attrs = append(attrs, idAttr(p)...)
	attrs = append(attrs, bareAt("hidden"))
	if v, ok := gv(p, "title"); ok {
		attrs = append(attrs, at("title", v))
	}
	attrs = append(attrs, passthrough(p)...)
	return attrs
}

// ---- overlays & compound widgets --------------------------------------------

func expandModal(p *Element) *Element {
	id, _ := gv(p, "id")
	label, _ := gv(p, "label")
	innerCls := []string{"appearance-modal"}
	switch v, _ := gv(p, "variant"); v {
	case "lists":
		innerCls = append(innerCls, "lists-manage")
	case "sizes":
		innerCls = append(innerCls, "sizes-modal")
	}
	var kids []Node
	if title, ok := gv(p, "title"); ok {
		kids = append(kids, hinline("h2", []Attr{ca("appearance-modal-title")}, &TextNode{Value: title}))
	}
	kids = append(kids, expandNodes(p.Block)...)
	if done, ok := gv(p, "done"); ok {
		attrs := []Attr{ca("appearance-modal-done")}
		if did, ok := gv(p, "doneid"); ok {
			attrs = append(attrs, at("id", did))
		}
		attrs = append(attrs, at("type", "button"))
		kids = append(kids, &Element{Tag: "button", Attrs: attrs, Inline: []Item{&TextNode{Value: done}}})
	}
	inner := hel("div", []Attr{ca(innerCls...), at("role", "dialog"), at("aria-modal", "true"), at("aria-label", label)}, kids...)
	return hel("div", []Attr{ca("appearance-modal-overlay"), at("id", id), bareAt("hidden")}, inner)
}

func expandDocoverlay(p *Element) *Element {
	overlayCls := "card-overlay preview-overlay"
	docCls := "preview-doc"
	if k, _ := gv(p, "kind"); k == "detail" {
		overlayCls, docCls = "card-overlay", "card-detail"
	} else {
		switch v, _ := gv(p, "variant"); v {
		case "handouts":
			docCls = "preview-doc handouts-doc"
		case "import":
			overlayCls, docCls = "card-overlay import-overlay", "preview-doc import-doc"
		}
	}
	id, _ := gv(p, "id")
	docAttrs := []Attr{ca(strings.Fields(docCls)...), at("role", "dialog"), at("aria-modal", "true")}
	if label, ok := gv(p, "label"); ok {
		docAttrs = append(docAttrs, at("aria-label", label))
	}
	doc := hel("div", docAttrs, expandNodes(p.Block)...)
	return hel("div", []Attr{ca(strings.Fields(overlayCls)...), at("id", id), bareAt("hidden")}, doc)
}

func expandTab(p *Element) *Element {
	view, _ := gv(p, "view")
	attrs := []Attr{ca("card-view-tab")}
	attrs = append(attrs, idAttr(p)...)
	attrs = append(attrs, at("type", "button"), at("role", "tab"), at("data-view", view))
	return &Element{Tag: "button", Attrs: attrs, Inline: expandItems(p.Inline)}
}

func tabpanelAttrs(p *Element) []Attr {
	attrs := []Attr{ca("card-view")}
	attrs = append(attrs, idAttr(p)...)
	attrs = append(attrs, bareAt("hidden"))
	attrs = append(attrs, passthrough(p)...)
	return attrs
}

func expandListrow(p *Element) *Element {
	if href, ok := gv(p, "href"); ok {
		a := hel("a", []Attr{ca("list-row"), at("href", href)}, expandNodes(p.Block)...)
		return hel("li", rootAttrs(nil, dropAttrPrim(p, "href")), a)
	}
	return hel("li", rootAttrs([]string{"list-row"}, p), expandNodes(p.Block)...)
}

func expandTable(p *Element) *Element {
	var headRows, bodyRows []Node
	for _, c := range p.Block {
		row, ok := c.(*Element)
		if !ok || row.Tag != "trow" {
			continue
		}
		tr := expandPrim(row)
		if allHeaderCells(row) {
			headRows = append(headRows, tr...)
		} else {
			bodyRows = append(bodyRows, tr...)
		}
	}
	var kids []Node
	if len(headRows) > 0 {
		kids = append(kids, hel("thead", nil, headRows...))
	}
	if len(bodyRows) > 0 {
		kids = append(kids, hel("tbody", nil, bodyRows...))
	}
	return hel("table", rootAttrs([]string{"data-table"}, p), kids...)
}

func allHeaderCells(row *Element) bool {
	saw := false
	for _, c := range row.Block {
		if e, ok := c.(*Element); ok {
			if e.Tag != "hcell" {
				return false
			}
			saw = true
		}
	}
	return saw
}

var mountKinds = map[string][2]string{
	"kanban":            {"div", "kanban"},
	"board-grid":        {"div", "board-grid"},
	"timeline":          {"div", "timeline"},
	"label-picker":      {"div", "label-picker"},
	"label-add-row":     {"div", "label-add-row"},
	"attachments":       {"div", "attachments"},
	"card-fields":       {"div", "card-fields"},
	"preview-body":      {"div", "preview-body"},
	"card-preview-body": {"div", "preview-body card-preview-body"},
	"handouts-pdf":      {"div", "handouts-pdf"},
	"import-preview":    {"div", "handouts-pane import-preview"},
	"lists-manage-rows": {"div", "lists-manage-rows"},
	"members-list":      {"div", "members-list"},
	"token-list":        {"ul", "token-list"},
	"token-value":       {"code", "token-value"},
	"card-copy-msg":     {"div", "card-copy-msg"},
	"import-count":      {"span", "import-count"},
	"card-title":        {"h2", "card-detail-title"},
}

func expandMount(p *Element) *Element {
	kind, _ := gv(p, "kind")
	tc := mountKinds[kind]
	return hel(tc[0], rootAttrs(strings.Fields(tc[1]), p), expandNodes(p.Block)...)
}

// ---- prop helpers -----------------------------------------------------------

func gv(p *Element, name string) (string, bool) {
	for _, a := range p.Attrs {
		if a.Name == name && !a.Bare {
			return a.Value, true
		}
	}
	return "", false
}

func flagSet(p *Element, name string) bool {
	for _, a := range p.Attrs {
		if a.Name == name && a.Bare {
			return true
		}
	}
	return false
}

func idAttr(p *Element) []Attr {
	if v, ok := gv(p, "id"); ok {
		return []Attr{at("id", v)}
	}
	return nil
}

func forAttr(p *Element) []Attr {
	if v, ok := gv(p, "for"); ok {
		return []Attr{at("for", v)}
	}
	return nil
}

func expandOption(p *Element) *Element {
	v, _ := gv(p, "value")
	attrs := []Attr{at("value", v)}
	attrs = append(attrs, idAttr(p)...)
	attrs = append(attrs, metaAttrs(p)...)
	return &Element{Tag: "option", Attrs: attrs, Inline: expandItems(p.Inline)}
}

func copyProps(p *Element, names ...string) []Attr {
	var out []Attr
	for _, n := range names {
		if v, ok := gv(p, n); ok {
			out = append(out, at(n, v))
		}
	}
	return out
}

func copyFlags(p *Element, names ...string) []Attr {
	var out []Attr
	for _, n := range names {
		if flagSet(p, n) {
			out = append(out, bareAt(n))
		}
	}
	return out
}

func passthrough(p *Element) []Attr {
	var out []Attr
	for _, a := range p.Attrs {
		if strings.HasPrefix(a.Name, "data-") || strings.HasPrefix(a.Name, "aria-") {
			out = append(out, a)
		}
	}
	return out
}

// metaAttrs appends the universal props that sit after a primitive's own
// structural attrs: title (tooltip), hidden, then data-*/aria-*.
func metaAttrs(p *Element) []Attr {
	var out []Attr
	if v, ok := gv(p, "title"); ok {
		out = append(out, at("title", v))
	}
	if flagSet(p, "hidden") {
		out = append(out, bareAt("hidden"))
	}
	return append(out, passthrough(p)...)
}

// rootAttrs assembles a container's attributes: class (base + u-grow), id, any
// extra structural attrs, then universal meta.
func rootAttrs(classes []string, p *Element, extra ...Attr) []Attr {
	classes = growClasses(classes, p)
	var out []Attr
	if len(classes) > 0 {
		out = append(out, ca(classes...))
	}
	out = append(out, idAttr(p)...)
	out = append(out, extra...)
	return append(out, metaAttrs(p)...)
}

func leaf(tag string, classes []string, extra []Attr, p *Element) *Element {
	attrs := rootAttrs(classes, p, extra...)
	if len(p.Inline) > 0 {
		return &Element{Tag: tag, Attrs: attrs, Inline: expandItems(p.Inline)}
	}
	return &Element{Tag: tag, Attrs: attrs, Block: expandNodes(p.Block)}
}

func flexClasses(base string, p *Element) []string {
	classes := []string{base, gapClass(p, "")}
	if a, ok := gv(p, "align"); ok && a != "stretch" {
		classes = append(classes, "u-align-"+a)
	}
	if j, ok := gv(p, "justify"); ok && j != "start" {
		classes = append(classes, "u-justify-"+j)
	}
	if flagSet(p, "wrap") {
		classes = append(classes, "u-wrap")
	}
	return dropEmpty(classes)
}

// gapClass returns the u-gap-* token for the gap prop, or def when the prop is
// absent; "none"/"" yield no class (the default flex gap is 0).
func gapClass(p *Element, def string) string {
	g, ok := gv(p, "gap")
	if !ok {
		g = def
	}
	if g == "" || g == "none" {
		return ""
	}
	return "u-gap-" + g
}

func growClasses(base []string, p *Element) []string {
	if flagSet(p, "grow") {
		return append(base, "u-grow")
	}
	return base
}

func headClass(p *Element, doc, detail string) string {
	if k, _ := gv(p, "kind"); k == "detail" {
		return detail
	}
	return doc
}

func paneClass(p *Element) string {
	if k, _ := gv(p, "kind"); k == "preview" {
		return "handouts-preview"
	}
	return "handouts-src"
}

func tooltip(p *Element, fallback string) string {
	if v, ok := gv(p, "title"); ok {
		return v
	}
	return fallback
}

func classAndID(classes []string, id string, has bool) []Attr {
	out := []Attr{ca(classes...)}
	if has {
		out = append(out, at("id", id))
	}
	return out
}

func idIfSet(id string, has bool) []Attr {
	if has {
		return []Attr{at("id", id)}
	}
	return nil
}

func dropAttr(attrs []Attr, name string) []Attr {
	var out []Attr
	for _, a := range attrs {
		if a.Name != name {
			out = append(out, a)
		}
	}
	return out
}

func dropAttrPrim(p *Element, name string) *Element {
	return &Element{Tag: p.Tag, Attrs: dropAttr(p.Attrs, name), Block: p.Block, Inline: p.Inline, Line: p.Line}
}

func dropEmpty(ss []string) []string {
	var out []string
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ---- html node constructors -------------------------------------------------

func hel(tag string, attrs []Attr, children ...Node) *Element {
	return &Element{Tag: tag, Attrs: attrs, Block: children}
}

func hinline(tag string, attrs []Attr, items ...Item) *Element {
	return &Element{Tag: tag, Attrs: attrs, Inline: items}
}

func ca(classes ...string) Attr  { return Attr{Name: "class", Value: strings.Join(classes, " ")} }
func at(name, value string) Attr { return Attr{Name: name, Value: value} }
func bareAt(name string) Attr    { return Attr{Name: name, Bare: true} }

// ---- printer: HTML node tree -> bytes ---------------------------------------

var htmlVoid = map[string]bool{"meta": true, "link": true, "input": true, "br": true, "img": true, "hr": true, "source": true}

var textEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

var attrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "\n", "&#10;")

func indent(depth int) string { return strings.Repeat("  ", depth) }

func printDoc(doc *Doc) []byte {
	var b strings.Builder
	printNodes(&b, doc.Nodes, 0)
	return []byte(b.String())
}

func printNodes(b *strings.Builder, nodes []Node, depth int) {
	for _, n := range nodes {
		printNode(b, n, depth)
	}
}

func printNode(b *strings.Builder, n Node, depth int) {
	switch v := n.(type) {
	case *Doctype:
		b.WriteString(indent(depth))
		b.WriteString("<!doctype html>\n")
	case *BlankLine:
		b.WriteString("\n")
	case *Comment:
		b.WriteString(indent(depth))
		b.WriteString("<!-- ")
		b.WriteString(v.Lines[0])
		contIndent := strings.Repeat(" ", depth*2+5)
		for _, line := range v.Lines[1:] {
			b.WriteString("\n")
			b.WriteString(contIndent)
			b.WriteString(line)
		}
		b.WriteString(" -->\n")
	case *RunNode:
		b.WriteString(indent(depth))
		printInline(b, v.Items)
		b.WriteString("\n")
	case *Element:
		printElement(b, v, depth)
	}
}

func printElement(b *strings.Builder, e *Element, depth int) {
	b.WriteString(indent(depth))
	writeOpenTag(b, e)
	if htmlVoid[e.Tag] {
		b.WriteString("\n")
		return
	}
	if len(e.Block) > 0 {
		b.WriteString("\n")
		childDepth := depth + 1
		if e.Tag == "html" {
			childDepth = depth
		}
		printNodes(b, e.Block, childDepth)
		b.WriteString(indent(depth))
		b.WriteString("</")
		b.WriteString(e.Tag)
		b.WriteString(">\n")
		return
	}
	printInline(b, e.Inline)
	b.WriteString("</")
	b.WriteString(e.Tag)
	b.WriteString(">\n")
}

func writeOpenTag(b *strings.Builder, e *Element) {
	b.WriteString("<")
	b.WriteString(e.Tag)
	for _, a := range e.Attrs {
		b.WriteString(" ")
		b.WriteString(a.Name)
		if !a.Bare {
			b.WriteString(`="`)
			b.WriteString(attrEscaper.Replace(a.Value))
			b.WriteString(`"`)
		}
	}
	b.WriteString(">")
}

func printInline(b *strings.Builder, items []Item) {
	for _, it := range items {
		switch v := it.(type) {
		case *TextNode:
			b.WriteString(textEscaper.Replace(v.Value))
		case *Element:
			writeOpenTag(b, v)
			if htmlVoid[v.Tag] {
				continue
			}
			printInline(b, v.Inline)
			b.WriteString("</")
			b.WriteString(v.Tag)
			b.WriteString(">")
		}
	}
}
