package kit

// coreExpanders maps each core primitive to its HTML expansion. App overlays
// override an entry (checkbox/editor in xy) or add new ones (docoverlay …).
var coreExpanders = map[string]ExpandFunc{
	"page":    expandPage,
	"topbar":  expandTopbar,
	"iconbtn": expandIconbtn,
	"iconlink": func(c *ExpandCtx, p *Element) []Node {
		label, _ := Get(p, "label")
		href, _ := Get(p, "href")
		attrs := []Attr{ClassAttr("action-icon"), At("href", href), At("aria-label", label), At("title", tooltip(p, label))}
		attrs = append(attrs, Passthrough(p)...)
		return one(&Element{Tag: "a", Attrs: attrs, Inline: c.Items(p.Inline)})
	},
	"col": func(c *ExpandCtx, p *Element) []Node {
		return one(El("div", RootAttrs(FlexClasses("u-col", p), p), c.Nodes(p.Block)...))
	},
	"row": func(c *ExpandCtx, p *Element) []Node {
		return one(El("div", RootAttrs(FlexClasses("u-row", p), p), c.Nodes(p.Block)...))
	},
	"spacer": func(c *ExpandCtx, p *Element) []Node { return one(El("div", RootAttrs([]string{"u-spacer"}, p))) },
	"section": func(c *ExpandCtx, p *Element) []Node {
		return one(El("section", RootAttrs([]string{"section"}, p), c.Nodes(p.Block)...))
	},
	"text":    func(c *ExpandCtx, p *Element) []Node { return one(Leaf(c, "p", nil, nil, p)) },
	"hint":    func(c *ExpandCtx, p *Element) []Node { return one(Leaf(c, "p", []string{"hint"}, nil, p)) },
	"subhead": func(c *ExpandCtx, p *Element) []Node { return one(Leaf(c, "h2", []string{"subhead"}, nil, p)) },
	"label": func(c *ExpandCtx, p *Element) []Node {
		return one(Leaf(c, "label", []string{"section-label"}, forAttr(p), p))
	},
	"bigcode": func(c *ExpandCtx, p *Element) []Node { return one(Leaf(c, "p", []string{"bigcode"}, nil, p)) },
	"message": func(c *ExpandCtx, p *Element) []Node { return one(Leaf(c, "pre", []string{"message"}, nil, p)) },
	"empty":   func(c *ExpandCtx, p *Element) []Node { return one(Leaf(c, "p", []string{"empty"}, nil, p)) },
	"strong":  func(c *ExpandCtx, p *Element) []Node { return one(inlineStrong(c, p).(*Element)) },
	"code":    func(c *ExpandCtx, p *Element) []Node { return one(inlineCode(c, p).(*Element)) },
	"muted":   func(c *ExpandCtx, p *Element) []Node { return one(inlineMuted(c, p).(*Element)) },
	"form":    expandForm,
	"textfield": func(c *ExpandCtx, p *Element) []Node {
		return one(Input(c, "text", p, "name", "placeholder", "autocomplete", "spellcheck", "autocapitalize", "autocorrect", "value", "maxlength", "minlength", "inputmode", "pattern", "list"))
	},
	"password": func(c *ExpandCtx, p *Element) []Node {
		return one(Input(c, "password", p, "name", "placeholder", "autocomplete", "spellcheck", "autocapitalize", "autocorrect", "value", "maxlength", "minlength", "inputmode", "pattern"))
	},
	"filefield": func(c *ExpandCtx, p *Element) []Node { return one(Input(c, "file", p, "accept")) },
	"hiddenfield": func(c *ExpandCtx, p *Element) []Node {
		attrs := []Attr{At("type", "hidden")}
		attrs = append(attrs, IDAttr(p)...)
		attrs = append(attrs, CopyProps(p, "name", "value")...)
		attrs = append(attrs, Passthrough(p)...)
		return one(El("input", attrs))
	},
	"numfield":    expandNumfield,
	"colorfield":  expandColorfield,
	"sliderrow":   expandSliderrow,
	"checkbox":    expandCheckboxGeneric,
	"radio":       expandRadio,
	"selectfield": expandSelect,
	"option":      expandOption,
	"editor":      expandEditorGeneric,
	"button":      expandButton,
	"field":       expandField,
	"unreaddot":   func(c *ExpandCtx, p *Element) []Node { return one(El("span", unreadAttrs(p))) },
	"modal":       expandModal,
	"dialog":      expandDialog,
	"tabs": func(c *ExpandCtx, p *Element) []Node {
		return one(El("div", RootAttrs([]string{"card-view-tabs"}, p, At("role", "tablist")), c.Nodes(p.Block)...))
	},
	"tab":      expandTab,
	"tabpanel": func(c *ExpandCtx, p *Element) []Node { return one(El("div", tabpanelAttrs(p), c.Nodes(p.Block)...)) },
	"details":  expandDetails,
	"summary":  func(c *ExpandCtx, p *Element) []Node { return one(Leaf(c, "summary", nil, nil, p)) },
	"fieldset": func(c *ExpandCtx, p *Element) []Node {
		return one(El("fieldset", RootAttrs([]string{"field"}, p), c.Nodes(p.Block)...))
	},
	"datalist": func(c *ExpandCtx, p *Element) []Node {
		return one(El("datalist", RootAttrs(nil, p), c.Nodes(p.Block)...))
	},
	"list": func(c *ExpandCtx, p *Element) []Node {
		return one(El("ul", RootAttrs([]string{"list"}, p), c.Nodes(p.Block)...))
	},
	"listrow":   expandListrow,
	"listtitle": func(c *ExpandCtx, p *Element) []Node { return one(Leaf(c, "span", []string{"list-row-title"}, nil, p)) },
	"table":     expandTable,
	"trow":      func(c *ExpandCtx, p *Element) []Node { return one(El("tr", RootAttrs(nil, p), c.Nodes(p.Block)...)) },
	"hcell":     func(c *ExpandCtx, p *Element) []Node { return one(Leaf(c, "th", nil, nil, p)) },
	"cell":      func(c *ExpandCtx, p *Element) []Node { return one(Leaf(c, "td", nil, nil, p)) },
	"mount":     expandMount,
}

var coreInline = map[string]InlineFunc{
	"strong":    inlineStrong,
	"code":      inlineCode,
	"muted":     inlineMuted,
	"unreaddot": func(c *ExpandCtx, p *Element) Item { return El("span", unreadAttrs(p)) },
}

func inlineStrong(c *ExpandCtx, p *Element) Item {
	return &Element{Tag: "strong", Attrs: RootAttrs(nil, p), Inline: c.Items(p.Inline)}
}
func inlineCode(c *ExpandCtx, p *Element) Item {
	return &Element{Tag: "code", Attrs: RootAttrs(nil, p), Inline: c.Items(p.Inline)}
}
func inlineMuted(c *ExpandCtx, p *Element) Item {
	return &Element{Tag: "span", Attrs: RootAttrs([]string{"muted"}, p), Inline: c.Items(p.Inline)}
}

func expandIconbtn(c *ExpandCtx, p *Element) []Node {
	label, _ := Get(p, "label")
	classes := []string{"action-icon"}
	badgeid, hasBadge := Get(p, "badgeid")
	if hasBadge {
		classes = append(classes, "notif-toggle")
	}
	attrs := []Attr{ClassAttr(classes...)}
	attrs = append(attrs, IDAttr(p)...)
	attrs = append(attrs, At("type", "button"), At("aria-label", label), At("title", tooltip(p, label)))
	attrs = append(attrs, Passthrough(p)...)
	items := c.Items(p.Inline)
	if hasBadge {
		items = append(items, &Element{Tag: "span", Attrs: []Attr{ClassAttr("notif-badge"), At("id", badgeid), BareAt("hidden")}})
	}
	return one(&Element{Tag: "button", Attrs: attrs, Inline: items})
}

func expandForm(c *ExpandCtx, p *Element) []Node {
	dirClass := "u-row"
	if d, _ := Get(p, "dir"); d == "col" {
		dirClass = "u-col"
	}
	classes := []string{dirClass, gapClass(p, "sm")}
	attrs := []Attr{ClassAttr(GrowClasses(classes, p)...)}
	attrs = append(attrs, IDAttr(p)...)
	attrs = append(attrs, CopyProps(p, "autocomplete", "method", "action")...)
	attrs = append(attrs, MetaAttrs(p)...)
	return one(El("form", attrs, c.Nodes(p.Block)...))
}

func expandNumfield(c *ExpandCtx, p *Element) []Node {
	base := []string{"input"}
	if Flag(p, "narrow") {
		base = append(base, "lists-move-pos")
	}
	attrs := []Attr{ClassAttr(GrowClasses(base, p)...)}
	attrs = append(attrs, IDAttr(p)...)
	attrs = append(attrs, At("type", "number"))
	attrs = append(attrs, CopyProps(p, "min", "max", "step", "placeholder")...)
	attrs = append(attrs, MetaAttrs(p)...)
	return one(El("input", attrs))
}

func expandColorfield(c *ExpandCtx, p *Element) []Node {
	attrs := []Attr{ClassAttr(GrowClasses([]string{"label-color-input"}, p)...)}
	attrs = append(attrs, IDAttr(p)...)
	attrs = append(attrs, At("type", "color"))
	attrs = append(attrs, CopyProps(p, "value")...)
	attrs = append(attrs, MetaAttrs(p)...)
	return one(El("input", attrs))
}

func expandSliderrow(c *ExpandCtx, p *Element) []Node {
	id, _ := Get(p, "id")
	label, _ := Get(p, "label")
	valueid, _ := Get(p, "valueid")
	head := El("div", []Attr{ClassAttr("sizes-row-head")},
		Inl("label", []Attr{ClassAttr("appearance-row-label"), At("for", id)}, &TextNode{Value: label}),
		El("span", []Attr{ClassAttr("sizes-value"), At("id", valueid)}),
	)
	rangeAttrs := []Attr{At("type", "range"), At("id", id)}
	rangeAttrs = append(rangeAttrs, CopyProps(p, "min", "max", "step")...)
	kids := []Node{head, El("input", rangeAttrs)}
	if hint, ok := Get(p, "hint"); ok {
		kids = append(kids, Inl("p", []Attr{ClassAttr("sizes-hint")}, &TextNode{Value: hint}))
	}
	outer := RootAttrs([]string{"sizes-row"}, p)
	return one(El("div", dropAttr(outer, "id"), kids...))
}

// expandCheckboxGeneric is the core (dope) checkbox: label.checkbox wrapping the
// box and a span. xy overrides this in its overlay to emit .attach-lossless.
func expandCheckboxGeneric(c *ExpandCtx, p *Element) []Node {
	labelAttrs := []Attr{ClassAttr(GrowClasses([]string{"checkbox"}, p)...)}
	labelAttrs = append(labelAttrs, MetaAttrs(p)...)
	boxAttrs := []Attr{At("type", "checkbox")}
	boxAttrs = append(boxAttrs, IDAttr(p)...)
	boxAttrs = append(boxAttrs, CopyFlags(p, "checked")...)
	span := &Element{Tag: "span", Inline: c.Items(p.Inline)}
	return one(&Element{Tag: "label", Attrs: labelAttrs, Block: []Node{El("input", boxAttrs), span}})
}

func expandRadio(c *ExpandCtx, p *Element) []Node {
	labelAttrs := []Attr{ClassAttr(GrowClasses([]string{"checkbox"}, p)...)}
	labelAttrs = append(labelAttrs, MetaAttrs(p)...)
	boxAttrs := []Attr{At("type", "radio")}
	boxAttrs = append(boxAttrs, IDAttr(p)...)
	boxAttrs = append(boxAttrs, CopyProps(p, "name", "value")...)
	boxAttrs = append(boxAttrs, CopyFlags(p, "checked")...)
	span := &Element{Tag: "span", Inline: c.Items(p.Inline)}
	return one(&Element{Tag: "label", Attrs: labelAttrs, Block: []Node{El("input", boxAttrs), span}})
}

func expandSelect(c *ExpandCtx, p *Element) []Node {
	base := []string{"input"}
	if Flag(p, "compact") {
		base = append(base, "card-kind-select")
	}
	attrs := []Attr{ClassAttr(GrowClasses(base, p)...)}
	attrs = append(attrs, IDAttr(p)...)
	attrs = append(attrs, CopyProps(p, "name")...)
	attrs = append(attrs, MetaAttrs(p)...)
	return one(El("select", attrs, c.Nodes(p.Block)...))
}

func expandOption(c *ExpandCtx, p *Element) []Node {
	v, _ := Get(p, "value")
	attrs := []Attr{At("value", v)}
	attrs = append(attrs, IDAttr(p)...)
	attrs = append(attrs, CopyFlags(p, "selected")...)
	attrs = append(attrs, MetaAttrs(p)...)
	return one(&Element{Tag: "option", Attrs: attrs, Inline: c.Items(p.Inline)})
}

// expandEditorGeneric is the core textarea. xy overrides it to emit .card-desc
// and its kinds.
func expandEditorGeneric(c *ExpandCtx, p *Element) []Node {
	attrs := []Attr{ClassAttr(GrowClasses([]string{"input"}, p)...)}
	attrs = append(attrs, IDAttr(p)...)
	attrs = append(attrs, CopyProps(p, "placeholder", "rows", "name", "spellcheck")...)
	attrs = append(attrs, CopyFlags(p, "readonly", "required")...)
	attrs = append(attrs, MetaAttrs(p)...)
	return one(textareaBody(c, attrs, p))
}

func textareaBody(c *ExpandCtx, attrs []Attr, p *Element) *Element {
	e := &Element{Tag: "textarea", Attrs: attrs}
	if len(p.Inline) > 0 {
		e.Inline = c.Items(p.Inline)
	} else {
		e.Block = c.Nodes(p.Block)
	}
	return e
}

func expandButton(c *ExpandCtx, p *Element) []Node {
	classes := []string{"btn"}
	switch k, _ := Get(p, "kind"); k {
	case "ghost":
		classes = append(classes, "btn-ghost")
	case "danger":
		classes = append(classes, "btn-danger")
	case "secondary":
		classes = append(classes, "btn-secondary")
	}
	if Flag(p, "small") {
		classes = append(classes, "btn-small")
	}
	classes = GrowClasses(classes, p)

	href, hasHref := Get(p, "href")
	if hasHref || Flag(p, "download") {
		attrs := []Attr{ClassAttr(classes...)}
		attrs = append(attrs, IDAttr(p)...)
		if hasHref {
			attrs = append(attrs, At("href", href))
		}
		attrs = append(attrs, CopyFlags(p, "download")...)
		attrs = append(attrs, MetaAttrs(p)...)
		return one(&Element{Tag: "a", Attrs: attrs, Inline: c.Items(p.Inline)})
	}
	typ := "button"
	if Flag(p, "submit") {
		typ = "submit"
	}
	attrs := []Attr{ClassAttr(classes...)}
	attrs = append(attrs, IDAttr(p)...)
	attrs = append(attrs, At("type", typ))
	attrs = append(attrs, CopyProps(p, "name", "value", "formaction")...)
	attrs = append(attrs, CopyFlags(p, "formnovalidate", "disabled")...)
	attrs = append(attrs, MetaAttrs(p)...)
	return one(&Element{Tag: "button", Attrs: attrs, Inline: c.Items(p.Inline)})
}

func expandField(c *ExpandCtx, p *Element) []Node {
	label, _ := Get(p, "label")
	attrs := RootAttrs([]string{"field"}, p)
	kids := []Node{Inl("span", nil, &TextNode{Value: label})}
	kids = append(kids, c.Nodes(p.Block)...)
	return one(El("label", attrs, kids...))
}

func unreadAttrs(p *Element) []Attr {
	attrs := []Attr{ClassAttr(GrowClasses([]string{"unread-dot"}, p)...)}
	attrs = append(attrs, IDAttr(p)...)
	attrs = append(attrs, BareAt("hidden"))
	if v, ok := Get(p, "title"); ok {
		attrs = append(attrs, At("title", v))
	}
	attrs = append(attrs, Passthrough(p)...)
	return attrs
}

func expandModal(c *ExpandCtx, p *Element) []Node {
	id, _ := Get(p, "id")
	label, _ := Get(p, "label")
	innerCls := []string{"appearance-modal"}
	switch v, _ := Get(p, "variant"); v {
	case "lists":
		innerCls = append(innerCls, "lists-manage")
	case "sizes":
		innerCls = append(innerCls, "sizes-modal")
	}
	var kids []Node
	if title, ok := Get(p, "title"); ok {
		kids = append(kids, Inl("h2", []Attr{ClassAttr("appearance-modal-title")}, &TextNode{Value: title}))
	}
	kids = append(kids, c.Nodes(p.Block)...)
	if done, ok := Get(p, "done"); ok {
		attrs := []Attr{ClassAttr("appearance-modal-done")}
		if did, ok := Get(p, "doneid"); ok {
			attrs = append(attrs, At("id", did))
		}
		attrs = append(attrs, At("type", "button"))
		kids = append(kids, &Element{Tag: "button", Attrs: attrs, Inline: []Item{&TextNode{Value: done}}})
	}
	inner := El("div", []Attr{ClassAttr(innerCls...), At("role", "dialog"), At("aria-modal", "true"), At("aria-label", label)}, kids...)
	return one(El("div", []Attr{ClassAttr("appearance-modal-overlay"), At("id", id), BareAt("hidden")}, inner))
}

func expandDialog(c *ExpandCtx, p *Element) []Node {
	attrs := []Attr{ClassAttr("modal-dialog")}
	attrs = append(attrs, IDAttr(p)...)
	attrs = append(attrs, CopyFlags(p, "open")...)
	attrs = append(attrs, MetaAttrs(p)...)
	return one(El("dialog", attrs, c.Nodes(p.Block)...))
}

func expandDetails(c *ExpandCtx, p *Element) []Node {
	attrs := []Attr{ClassAttr(GrowClasses([]string{"disclosure"}, p)...)}
	attrs = append(attrs, IDAttr(p)...)
	attrs = append(attrs, CopyFlags(p, "open")...)
	attrs = append(attrs, MetaAttrs(p)...)
	return one(El("details", attrs, c.Nodes(p.Block)...))
}

func expandTab(c *ExpandCtx, p *Element) []Node {
	view, _ := Get(p, "view")
	attrs := []Attr{ClassAttr("card-view-tab")}
	attrs = append(attrs, IDAttr(p)...)
	attrs = append(attrs, At("type", "button"), At("role", "tab"), At("data-view", view))
	return one(&Element{Tag: "button", Attrs: attrs, Inline: c.Items(p.Inline)})
}

func tabpanelAttrs(p *Element) []Attr {
	attrs := []Attr{ClassAttr("card-view")}
	attrs = append(attrs, IDAttr(p)...)
	attrs = append(attrs, BareAt("hidden"))
	attrs = append(attrs, Passthrough(p)...)
	return attrs
}

func expandListrow(c *ExpandCtx, p *Element) []Node {
	if href, ok := Get(p, "href"); ok {
		a := El("a", []Attr{ClassAttr("list-row"), At("href", href)}, c.Nodes(p.Block)...)
		return one(El("li", RootAttrs(nil, dropAttrPrim(p, "href")), a))
	}
	return one(El("li", RootAttrs([]string{"list-row"}, p), c.Nodes(p.Block)...))
}

func expandTable(c *ExpandCtx, p *Element) []Node {
	var headRows, bodyRows []Node
	for _, ch := range p.Block {
		row, ok := ch.(*Element)
		if !ok || row.Tag != "trow" {
			continue
		}
		tr := c.Expand(row)
		if allHeaderCells(row) {
			headRows = append(headRows, tr...)
		} else {
			bodyRows = append(bodyRows, tr...)
		}
	}
	var kids []Node
	if len(headRows) > 0 {
		kids = append(kids, El("thead", nil, headRows...))
	}
	if len(bodyRows) > 0 {
		kids = append(kids, El("tbody", nil, bodyRows...))
	}
	table := El("table", RootAttrs([]string{"data-table"}, p), kids...)
	if Flag(p, "scroll") {
		return one(El("div", []Attr{ClassAttr("table-scroll")}, table))
	}
	return one(table)
}

func allHeaderCells(row *Element) bool {
	saw := false
	for _, ch := range row.Block {
		if e, ok := ch.(*Element); ok {
			if e.Tag != "hcell" {
				return false
			}
			saw = true
		}
	}
	return saw
}

func expandMount(c *ExpandCtx, p *Element) []Node {
	kind, _ := Get(p, "kind")
	if m, ok := c.Mount(kind); ok {
		return one(El(m.Tag, RootAttrs(append([]string(nil), m.Classes...), p), c.Nodes(p.Block)...))
	}
	return one(El("div", RootAttrs([]string{kind}, p), c.Nodes(p.Block)...))
}
