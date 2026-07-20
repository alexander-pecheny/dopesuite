package ui

import (
	"strings"

	base "pecheny.me/dopeuikit/kit"
)

// expandCheckbox overrides the core (generic) checkbox with xy's attach-lossless
// flavor and its preview kinds.
func expandCheckbox(c *base.ExpandCtx, p *base.Element) []base.Node {
	classes := []string{"attach-lossless"}
	switch k, _ := base.Get(p, "kind"); k {
	case "preview":
		classes = append(classes, "preview-screen-toggle")
	case "card-preview":
		classes = append(classes, "card-preview-screen")
	}
	labelAttrs := []base.Attr{base.ClassAttr(base.GrowClasses(classes, p)...)}
	labelAttrs = append(labelAttrs, base.MetaAttrs(p)...)
	boxAttrs := []base.Attr{base.At("type", "checkbox")}
	boxAttrs = append(boxAttrs, base.IDAttr(p)...)
	boxAttrs = append(boxAttrs, base.CopyFlags(p, "checked")...)
	items := []base.Item{base.El("input", boxAttrs)}
	items = append(items, c.Items(p.Inline)...)
	return one(base.Inl("label", labelAttrs, items...))
}

// expandEditor overrides the core textarea with xy's card-desc flavor + kinds.
func expandEditor(c *base.ExpandCtx, p *base.Element) []base.Node {
	var classes []string
	switch k, _ := base.Get(p, "kind"); k {
	case "comment":
		classes = []string{"card-desc", "comment-input"}
	case "handouts":
		classes = []string{"card-desc", "handouts-textarea"}
	case "importsrc":
		classes = []string{"input", "handouts-textarea", "import-textarea"}
	default:
		classes = []string{"card-desc"}
	}
	attrs := []base.Attr{base.ClassAttr(base.GrowClasses(classes, p)...)}
	attrs = append(attrs, base.IDAttr(p)...)
	attrs = append(attrs, base.CopyProps(p, "placeholder", "rows", "name", "spellcheck")...)
	attrs = append(attrs, base.CopyFlags(p, "readonly", "required")...)
	attrs = append(attrs, base.MetaAttrs(p)...)
	e := &base.Element{Tag: "textarea", Attrs: attrs}
	if len(p.Inline) > 0 {
		e.Inline = c.Items(p.Inline)
	} else {
		e.Block = c.Nodes(p.Block)
	}
	return one(e)
}

func expandDocoverlay(c *base.ExpandCtx, p *base.Element) []base.Node {
	overlayCls := "card-overlay preview-overlay"
	docCls := "preview-doc"
	if k, _ := base.Get(p, "kind"); k == "detail" {
		overlayCls, docCls = "card-overlay", "card-detail"
	} else {
		switch v, _ := base.Get(p, "variant"); v {
		case "handouts":
			docCls = "preview-doc handouts-doc"
		case "import":
			overlayCls, docCls = "card-overlay import-overlay", "preview-doc import-doc"
		case "feed":
			docCls = "preview-doc feed-doc"
		}
	}
	id, _ := base.Get(p, "id")
	docAttrs := []base.Attr{base.ClassAttr(strings.Fields(docCls)...), base.At("role", "dialog"), base.At("aria-modal", "true")}
	if label, ok := base.Get(p, "label"); ok {
		docAttrs = append(docAttrs, base.At("aria-label", label))
	}
	doc := base.El("div", docAttrs, c.Nodes(p.Block)...)
	return one(base.El("div", []base.Attr{base.ClassAttr(strings.Fields(overlayCls)...), base.At("id", id), base.BareAt("hidden")}, doc))
}

func expandHeadrow(c *base.ExpandCtx, p *base.Element) []base.Node {
	return one(base.El("div", base.RootAttrs([]string{headClass(p, "preview-head", "card-detail-head")}, p), c.Nodes(p.Block)...))
}

func expandHeadactions(c *base.ExpandCtx, p *base.Element) []base.Node {
	return one(base.El("div", base.RootAttrs([]string{headClass(p, "preview-head-actions", "card-head-actions")}, p), c.Nodes(p.Block)...))
}

func expandSplit(c *base.ExpandCtx, p *base.Element) []base.Node {
	return one(base.El("div", base.RootAttrs([]string{"handouts-body"}, p), c.Nodes(p.Block)...))
}

func expandPane(c *base.ExpandCtx, p *base.Element) []base.Node {
	return one(base.El("div", base.RootAttrs([]string{"handouts-pane", paneClass(p)}, p), c.Nodes(p.Block)...))
}

func headClass(p *base.Element, doc, detail string) string {
	if k, _ := base.Get(p, "kind"); k == "detail" {
		return detail
	}
	return doc
}

func paneClass(p *base.Element) string {
	if k, _ := base.Get(p, "kind"); k == "preview" {
		return "handouts-preview"
	}
	return "handouts-src"
}

// xyMounts maps each board mount kind to the empty JS-owned container it renders.
var xyMounts = map[string]base.MountSpec{
	"kanban":            {Tag: "div", Classes: []string{"kanban"}},
	"board-grid":        {Tag: "div", Classes: []string{"board-grid"}},
	"timeline":          {Tag: "div", Classes: []string{"timeline"}},
	"label-picker":      {Tag: "div", Classes: []string{"label-picker"}},
	"label-add-row":     {Tag: "div", Classes: []string{"label-add-row"}},
	"attachments":       {Tag: "div", Classes: []string{"attachments"}},
	"card-fields":       {Tag: "div", Classes: []string{"card-fields"}},
	"preview-body":      {Tag: "div", Classes: []string{"preview-body"}},
	"card-preview-body": {Tag: "div", Classes: []string{"preview-body", "card-preview-body"}},
	"handouts-pdf":      {Tag: "div", Classes: []string{"handouts-pdf"}},
	"import-preview":    {Tag: "div", Classes: []string{"handouts-pane", "import-preview"}},
	"lists-manage-rows": {Tag: "div", Classes: []string{"lists-manage-rows"}},
	"members-list":      {Tag: "div", Classes: []string{"members-list"}},
	"token-list":        {Tag: "ul", Classes: []string{"token-list"}},
	"token-value":       {Tag: "code", Classes: []string{"token-value"}},
	"card-copy-msg":     {Tag: "div", Classes: []string{"card-copy-msg"}},
	"import-count":      {Tag: "span", Classes: []string{"import-count"}},
	"card-title":        {Tag: "h2", Classes: []string{"card-detail-title"}},
	"sizes-preview":     {Tag: "div", Classes: []string{"sizes-preview"}},
	"excerpts":          {Tag: "div", Classes: []string{"excerpts"}},
	"excerpt-count":     {Tag: "span", Classes: []string{"excerpt-count"}},
	"thread":            {Tag: "div", Classes: []string{"thread"}},
	"feed-grid":         {Tag: "div", Classes: []string{"feed-grid"}},
}
