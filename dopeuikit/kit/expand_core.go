package kit

import (
	"strings"

	kitstrings "pecheny.me/dopeuikit/i18nstrings"
)

// coreExpanders maps each core primitive to its HTML expansion. App overlays
// override an entry (checkbox/editor in xy) or add new ones (docoverlay …).
var coreExpanders = map[string]ExpandFunc{
	"page":    expandPage,
	"topbar":  expandTopbar,
	"crumbs":  ExpandCrumbs,
	"iconbtn": expandIconbtn,
	"iconlink": func(c *ExpandCtx, p *Element) []Node {
		label, _ := Get(p, "label")
		href, _ := Get(p, "href")
		attrs := []Attr{ClassAttr("action-icon"), At("href", href), At("aria-label", label), At("title", tooltip(p, label))}
		attrs = append(attrs, Passthrough(p)...)
		return one(&Element{Tag: "a", Attrs: attrs, Inline: withIcon(p, c.Items(p.Inline))})
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
	"text": func(c *ExpandCtx, p *Element) []Node { return one(Leaf(c, "p", nil, nil, p)) },
	"hint": func(c *ExpandCtx, p *Element) []Node {
		classes := []string{"hint"}
		if k, _ := Get(p, "kind"); k == "danger" {
			classes = append(classes, "hint-danger")
		}
		return one(Leaf(c, "p", classes, nil, p))
	},
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
		// title is what a browser shows when a pattern refuses the value, so a
		// field that constrains its input can say what it takes.
		return one(Input(c, "text", p, "name", "placeholder", "autocomplete", "spellcheck", "autocapitalize", "autocorrect", "value", "maxlength", "minlength", "inputmode", "pattern", "list", "title"))
	},
	"datetimefield": expandDatetimefield,
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
		return one(El("div", RootAttrs([]string{"seg", "seg-grow"}, p, At("role", "tablist")), c.Nodes(p.Block)...))
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
		classes = append(classes, "has-badge")
	}
	attrs := []Attr{ClassAttr(classes...)}
	attrs = append(attrs, IDAttr(p)...)
	attrs = append(attrs, At("type", "button"), At("aria-label", label), At("title", tooltip(p, label)))
	attrs = append(attrs, Passthrough(p)...)
	items := withIcon(p, c.Items(p.Inline))
	if hasBadge {
		items = append(items, &Element{Tag: "span", Attrs: []Attr{ClassAttr("unread-dot", "unread-dot-badge"), At("id", badgeid), BareAt("hidden")}})
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
	case "primary":
		classes = append(classes, "btn-primary")
	case "ghost":
		classes = append(classes, "btn-ghost")
	case "danger":
		classes = append(classes, "btn-danger")
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
		return one(&Element{Tag: "a", Attrs: attrs, Inline: withIcon(p, c.Items(p.Inline))})
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
	return one(&Element{Tag: "button", Attrs: attrs, Inline: withIcon(p, c.Items(p.Inline))})
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
	case "wide":
		innerCls = append(innerCls, "modal-wide")
	}
	var kids []Node
	if title, ok := Get(p, "title"); ok {
		head := []Item{&TextNode{Value: title}}
		if ico := iconItemClass(p, "ico ico-lead"); ico != nil {
			head = []Item{ico, &TextNode{Value: title}}
		}
		kids = append(kids, Inl("h2", []Attr{ClassAttr("appearance-modal-title")}, head...))
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
	classes := []string{"modal-dialog"}
	if Flag(p, "wide") {
		classes = append(classes, "modal-dialog-wide")
	}
	attrs := []Attr{ClassAttr(classes...)}
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
	classes := []string{"seg-btn"}
	if Flag(p, "active") {
		classes = append(classes, "active")
	}
	attrs := []Attr{ClassAttr(classes...)}
	attrs = append(attrs, IDAttr(p)...)
	// A tab whose choices are pages is a link, like a button with an href: it
	// needs no script, and it opens in a new tab like any other.
	if href, ok := Get(p, "href"); ok {
		attrs = append(attrs, At("href", href))
		if Flag(p, "active") {
			attrs = append(attrs, At("aria-current", "page"))
		}
		return one(&Element{Tag: "a", Attrs: attrs, Inline: withIcon(p, c.Items(p.Inline))})
	}
	view, _ := Get(p, "view")
	attrs = append(attrs, At("type", "button"), At("role", "tab"), At("data-view", view))
	return one(&Element{Tag: "button", Attrs: attrs, Inline: withIcon(p, c.Items(p.Inline))})
}

func tabpanelAttrs(p *Element) []Attr {
	attrs := []Attr{ClassAttr("tabpanel")}
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
	var labels []string
	for _, ch := range p.Block {
		row, ok := ch.(*Element)
		if !ok || row.Tag != "trow" {
			continue
		}
		tr := c.Expand(row)
		if allHeaderCells(row) {
			if labels == nil {
				labels = headerLabels(c, row)
			}
			headRows = append(headRows, tr...)
		} else {
			labelCells(tr, labels)
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

// headerLabels is what each column is called, so a body cell can carry its own
// column's name. On a phone the table stops being a grid and each row becomes a
// block of «label: value» lines — a table that scrolls sideways on a 393px
// screen is a table nobody reads the right-hand half of.
func headerLabels(c *ExpandCtx, row *Element) []string {
	var out []string
	for _, ch := range row.Block {
		cell, ok := ch.(*Element)
		if !ok || cell.Tag != "hcell" {
			continue
		}
		out = append(out, strings.TrimSpace(cellText(cell)))
	}
	return out
}

// cellText is a header cell's words, which is all a label needs: a header that
// holds anything but text has no name to lend its column.
func cellText(cell *Element) string {
	var b strings.Builder
	for _, item := range cell.Inline {
		if t, ok := item.(*TextNode); ok {
			b.WriteString(t.Value)
		}
	}
	return b.String()
}

// labelCells stamps a rendered body row's cells with their column names.
func labelCells(rows []Node, labels []string) {
	if len(labels) == 0 {
		return
	}
	for _, node := range rows {
		tr, ok := node.(*Element)
		if !ok || tr.Tag != "tr" {
			continue
		}
		i := 0
		for _, kid := range tr.Block {
			cell, ok := kid.(*Element)
			if !ok || cell.Tag != "td" {
				continue
			}
			if i < len(labels) && labels[i] != "" {
				cell.Attrs = append(cell.Attrs, At("data-label", labels[i]))
			}
			i++
		}
	}
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

// expandDatetimefield is a date and time a person may type, paste OR pick. The
// posted value is the text input: a bare datetime-local is segmented, so
// pasting «2026-09-04 19:00» into one does nothing. The calendar beside it is
// the kit's own — Monday-first in every browser, opened by the button or a
// click in the field (assets/ts/datetime.ts) — because the native picker
// follows the browser locale and cannot be told otherwise. A field may carry
// data-datetime-tz: the zone its value is written in, spelled out under the
// grid.
func expandDatetimefield(c *ExpandCtx, p *Element) []Node {
	text := Input(c, "text", p, "name", "placeholder", "value")
	addClass(text, "u-grow")
	text.Attrs = append(text.Attrs, At("autocomplete", "off"), BareAt("data-datetime-text"))
	text.Attrs = append(text.Attrs, CopyFlags(p, "required")...)
	button := El("button", []Attr{
		ClassAttr("btn", "btn-ghost", "btn-small"), At("type", "button"),
		At("aria-label", kitstrings.Default.Datetime.Calendar.Label()), BareAt("data-datetime-open"),
	}, calendarGlyph())
	return one(El("span", []Attr{
		ClassAttr("datetime-field", "u-row", "u-gap-xs", "u-align-center"), BareAt("data-datetime-field"),
	}, text, button))
}

// addClass appends to an element's class attribute, for a control a helper
// already built.
func addClass(el *Element, class string) {
	for i, attr := range el.Attrs {
		if attr.Name == "class" {
			el.Attrs[i].Value += " " + class
			return
		}
	}
	el.Attrs = append([]Attr{ClassAttr(class)}, el.Attrs...)
}
