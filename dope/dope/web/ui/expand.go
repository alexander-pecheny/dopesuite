package ui

import (
	base "pecheny.me/dopeuikit/kit"
)

// expandGameTopbar builds the game pages' header: the game-header-main column
// (breadcrumbs mount, an optional viewer heading, the tabs mount, an optional OD
// progress readout) and the host-actions sync-stack dot. It is self-contained —
// the ids and class variants the game scripts bind to are fixed here, not
// authored per page.
func expandGameTopbar(c *base.ExpandCtx, p *base.Element) []base.Node {
	mainCls := []string{"game-header-main"}
	if base.Flag(p, "viewer") {
		mainCls = append(mainCls, "viewer-header-main")
	}

	kids := []base.Node{
		base.El("nav", []base.Attr{base.ClassAttr("game-breadcrumbs"), base.At("id", "gameBreadcrumbs"), base.At("aria-label", "Навигация")}),
	}
	if h, ok := base.Get(p, "heading"); ok {
		kids = append(kids, base.Inl("h1", nil, &base.TextNode{Value: h}))
	}

	tabsCls := []string{"match-tabs"}
	if base.Flag(p, "ektabs") {
		tabsCls = append(tabsCls, "ek-tabs")
	}
	tabsid, _ := base.Get(p, "tabsid")
	tabsAttrs := []base.Attr{base.ClassAttr(tabsCls...), base.At("id", tabsid), base.At("role", "tablist")}
	if base.Flag(p, "tabshidden") {
		tabsAttrs = append(tabsAttrs, base.BareAt("hidden"))
	}
	kids = append(kids, base.El("nav", tabsAttrs))

	if pid, ok := base.Get(p, "progressid"); ok {
		kids = append(kids, base.El("span", []base.Attr{base.ClassAttr("od-header-progress"), base.At("id", pid)}))
	}

	headerMain := base.El("div", []base.Attr{base.ClassAttr(mainCls...)}, kids...)

	sync := c.Chrome().TopbarSync
	state := "syncing"
	if v, ok := base.Get(p, "syncstate"); ok {
		state = v
	}
	dot := base.El("span", []base.Attr{
		base.ClassAttr(sync.Class), base.At("id", sync.ID), base.At("data-state", state),
		base.At("aria-label", "Синхронизация"), base.At("title", "Синхронизация"),
	})
	actions := base.El("div", []base.Attr{base.ClassAttr("host-actions")},
		base.El("div", []base.Attr{base.ClassAttr("sync-stack")}, dot))

	return one(base.El("header", []base.Attr{base.ClassAttr("host-top", "game-host-top")}, headerMain, actions))
}

// expandPublicTopbar builds the server-rendered pages' header: header.public-top
// with an optional back link (a.public-back "←"), the h1 title, and an optional
// corner user/context link (a.public-user). No sync dot — these pages are static.
func expandPublicTopbar(_ *base.ExpandCtx, p *base.Element) []base.Node {
	title, _ := base.Get(p, "title")
	var kids []base.Node
	if back, ok := base.Get(p, "back"); ok {
		kids = append(kids, base.Inl("a", []base.Attr{base.ClassAttr("public-back"), base.At("href", back)}, &base.TextNode{Value: "←"}))
	}
	kids = append(kids, base.Inl("h1", nil, &base.TextNode{Value: title}))
	if user, ok := base.Get(p, "user"); ok {
		label, _ := base.Get(p, "userlabel")
		kids = append(kids, base.Inl("a", []base.Attr{base.ClassAttr("public-user"), base.At("href", user)}, &base.TextNode{Value: label}))
	}
	return one(base.El("header", []base.Attr{base.ClassAttr("public-top")}, kids...))
}

// inlineLink renders an inline anchor; newtab adds target="_blank" rel="noopener".
// Used for the prose links in the server-rendered pages (register bot/done links).
func inlineLink(c *base.ExpandCtx, p *base.Element) base.Item {
	href, _ := base.Get(p, "href")
	attrs := []base.Attr{base.At("href", href)}
	if base.Flag(p, "newtab") {
		attrs = append(attrs, base.At("target", "_blank"), base.At("rel", "noopener"))
	}
	return &base.Element{Tag: "a", Attrs: attrs, Inline: c.Items(p.Inline)}
}

func expandLink(c *base.ExpandCtx, p *base.Element) []base.Node {
	return one(inlineLink(c, p).(*base.Element))
}

// expandCodeDisplay renders a prominent code line (the login telegram code):
// <p class="code-display">…</p>. It carries an id so the login JS can fill it in.
func expandCodeDisplay(c *base.ExpandCtx, p *base.Element) []base.Node {
	attrs := []base.Attr{base.ClassAttr("code-display")}
	attrs = append(attrs, base.IDAttr(p)...)
	return one(&base.Element{Tag: "p", Attrs: attrs, Inline: c.Items(p.Inline)})
}

// expandNote renders a muted notice paragraph: <p class="muted">…</p> (the
// server-rendered pages' informational lines). It carries id + universal meta so
// a toggled note (the numbers-edit help line) can be found and hidden by its JS.
func expandNote(c *base.ExpandCtx, p *base.Element) []base.Node {
	attrs := []base.Attr{base.ClassAttr("muted")}
	attrs = append(attrs, base.IDAttr(p)...)
	attrs = append(attrs, base.MetaAttrs(p)...)
	return one(&base.Element{Tag: "p", Attrs: attrs, Inline: c.Items(p.Inline)})
}

// expandSummary is dope's <summary>: the core plain summary plus a btn flag that
// styles it as a button (the create-fest disclosure on the host home).
func expandSummary(c *base.ExpandCtx, p *base.Element) []base.Node {
	var classes []string
	if base.Flag(p, "btn") {
		classes = []string{"btn"}
	}
	return one(base.Leaf(c, "summary", classes, nil, p))
}

// expandPalette wraps sticker swatches in the radiogroup box with its "Цвет" label.
func expandPalette(c *base.ExpandCtx, p *base.Element) []base.Node {
	kids := []base.Node{base.Inl("span", nil, &base.TextNode{Value: "Цвет"})}
	kids = append(kids, c.Nodes(p.Block)...)
	attrs := []base.Attr{base.ClassAttr("sticker-palette"), base.At("role", "radiogroup"), base.At("aria-label", "Цвет")}
	return one(base.El("div", attrs, kids...))
}

// expandSwatchradio renders one sticker-colour radio. The colour comes from a
// closed palette enum, emitted as data-color; styles.css maps each value to its
// --swatch var, so the dot carries no inline style (keeps the CSP script/style
// strict) and no page authors raw CSS.
func expandSwatchradio(c *base.ExpandCtx, p *base.Element) []base.Node {
	name, _ := base.Get(p, "name")
	value, _ := base.Get(p, "value")
	color, _ := base.Get(p, "color")
	labelAttrs := []base.Attr{base.ClassAttr("swatch")}
	if title, ok := base.Get(p, "title"); ok {
		labelAttrs = append(labelAttrs, base.At("title", title))
	}
	boxAttrs := []base.Attr{base.At("type", "radio"), base.At("name", name), base.At("value", value)}
	boxAttrs = append(boxAttrs, base.CopyFlags(p, "checked")...)
	dot := base.El("span", []base.Attr{base.ClassAttr("swatch-dot"), base.At("data-color", color)})
	return one(base.El("label", labelAttrs, base.El("input", boxAttrs), dot))
}

// expandNumbersform is the fest-numbers editing form: a column carrying the
// number-form class the .number-form(.editing) descendant selectors key on.
func expandNumbersform(c *base.ExpandCtx, p *base.Element) []base.Node {
	attrs := []base.Attr{base.ClassAttr("u-col", "u-gap-md", "number-form")}
	attrs = append(attrs, base.IDAttr(p)...)
	attrs = append(attrs, base.At("method", "post"))
	attrs = append(attrs, base.CopyProps(p, "action")...)
	attrs = append(attrs, base.At("autocomplete", "off"))
	attrs = append(attrs, base.MetaAttrs(p)...)
	return one(base.El("form", attrs, c.Nodes(p.Block)...))
}

func expandNumberlist(c *base.ExpandCtx, p *base.Element) []base.Node {
	return one(base.El("ol", []base.Attr{base.ClassAttr("number-list")}, c.Nodes(p.Block)...))
}

// expandNumberrow renders one team's number row: the (initially read-only) number
// input plus the team label and the hidden team_label/team_id fields the save
// handler reads back, all keyed by the row index.
func expandNumberrow(c *base.ExpandCtx, p *base.Element) []base.Node {
	idx, _ := base.Get(p, "index")
	num, _ := base.Get(p, "num")
	teamlabel, _ := base.Get(p, "teamlabel")
	teamid, _ := base.Get(p, "teamid")
	numInput := base.El("input", []base.Attr{
		base.ClassAttr("number-row-num"), base.At("type", "text"),
		base.At("inputmode", "numeric"), base.At("maxlength", "4"),
		base.At("name", "num_"+idx), base.At("value", num), base.BareAt("readonly"),
	})
	team := base.Inl("span", []base.Attr{base.ClassAttr("number-row-team")}, &base.TextNode{Value: teamlabel})
	hiddenLabel := base.El("input", []base.Attr{base.At("type", "hidden"), base.At("name", "team_label_"+idx), base.At("value", teamlabel)})
	hiddenID := base.El("input", []base.Attr{base.At("type", "hidden"), base.At("name", "team_id_"+idx), base.At("value", teamid)})
	return one(base.El("li", []base.Attr{base.ClassAttr("number-row")}, numInput, team, hiddenLabel, hiddenID))
}

// expandFestgroup is the fest-list collapsible bucket (Текущие/Будущие/Прошедшие)
// shared by the public index and the host landing: details.fest-group with the
// chevroned summary.fest-group-title.
func expandFestgroup(c *base.ExpandCtx, p *base.Element) []base.Node {
	title, _ := base.Get(p, "title")
	attrs := []base.Attr{base.ClassAttr("fest-group")}
	attrs = append(attrs, base.CopyFlags(p, "open")...)
	summary := base.Inl("summary", []base.Attr{base.ClassAttr("fest-group-title")}, &base.TextNode{Value: title})
	kids := append([]base.Node{summary}, c.Nodes(p.Block)...)
	return one(base.El("details", attrs, kids...))
}

// expandPickgroup is a labelled <fieldset> for a group of radios/checkboxes (game
// type on create, game picker on the roster override dialogs).
func expandPickgroup(c *base.ExpandCtx, p *base.Element) []base.Node {
	label, _ := base.Get(p, "label")
	kids := []base.Node{base.Inl("span", nil, &base.TextNode{Value: label})}
	kids = append(kids, c.Nodes(p.Block)...)
	return one(base.El("fieldset", []base.Attr{base.ClassAttr("field", "game-type-fieldset")}, kids...))
}

// expandActionlist / expandActionrow / expandRowlink build a list whose rows pair
// a growing link with trailing action controls (the dashboard games list).
func expandActionlist(c *base.ExpandCtx, p *base.Element) []base.Node {
	return one(base.El("ul", []base.Attr{base.ClassAttr("list")}, c.Nodes(p.Block)...))
}

func expandActionrow(c *base.ExpandCtx, p *base.Element) []base.Node {
	return one(base.El("li", []base.Attr{base.ClassAttr("list-action-row")}, c.Nodes(p.Block)...))
}

func expandRowlink(c *base.ExpandCtx, p *base.Element) []base.Node {
	href, _ := base.Get(p, "href")
	return one(base.El("a", []base.Attr{base.ClassAttr("list-row"), base.At("href", href)}, c.Nodes(p.Block)...))
}

// expandCheckbox is dope's checkbox: the core generic box plus name/value on the
// input, so server-rendered forms (create-fest "Публичный", the roster game
// pickers) submit real values. label.checkbox > input[type=checkbox] + span.
func expandCheckbox(c *base.ExpandCtx, p *base.Element) []base.Node {
	labelAttrs := []base.Attr{base.ClassAttr(base.GrowClasses([]string{"checkbox"}, p)...)}
	labelAttrs = append(labelAttrs, base.MetaAttrs(p)...)
	boxAttrs := []base.Attr{base.At("type", "checkbox")}
	boxAttrs = append(boxAttrs, base.IDAttr(p)...)
	boxAttrs = append(boxAttrs, base.CopyProps(p, "name", "value")...)
	boxAttrs = append(boxAttrs, base.CopyFlags(p, "checked")...)
	span := &base.Element{Tag: "span", Inline: c.Items(p.Inline)}
	return one(&base.Element{Tag: "label", Attrs: labelAttrs, Block: []base.Node{base.El("input", boxAttrs), span}})
}
