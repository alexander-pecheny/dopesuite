package hostpages

import (
	"html/template"

	"dope/dope/web/pages"
	ui "dope/dope/web/ui"
	dopestrings "dope/i18nstrings"
)

// The two public pages — the fest index at / and a fest's own page at
// /fest/{ref}. They were the last hand-written html/template strings in dope,
// which is why they still carried a bare "←" long after every other page had
// moved to the breadcrumb trail. They are ordinary builder pages now, so the
// header, the fest buckets and the game list all come from the same primitives
// the host tree uses.

// PublicFest is one fest on the index.
type PublicFest struct {
	Ref   string
	Title string
	Dates string
}

// PublicFestGroup is a collapsible bucket (current / future / past).
type PublicFestGroup struct {
	Title string
	Fests []PublicFest
}

// PublicFestDetail is one fest's own page.
type PublicFestDetail struct {
	Ref   string
	Title string
	Dates string
	// Description is markdown already rendered to HTML by the server, so it
	// goes through ui.Raw rather than being escaped as text.
	Description template.HTML
	Games       []PublicFestGame
}

// jumpHostNav are the body data-jump-* attrs menu.js reads to offer the host
// view from a public page.
func jumpHostNav(href, label, title string) []ui.Item {
	return []ui.Item{
		ui.Data("jump-label", label),
		ui.Data("jump-href", href),
		ui.Data("jump-title", title),
	}
}

// PublicIndexDoc builds the public fest list at /. It is home, so its 🏠 crumb
// is the page you are on rather than a link out.
func PublicIndexDoc(groups []PublicFestGroup) *ui.Doc {
	s := dopestrings.Default
	page := []ui.Item{ui.Title(s.Host.Pages.PublicIndexTitle()), ui.PagePublic}
	page = append(page, jumpHostNav("/host", s.Host.Pages.JumpHostLabel(), s.Host.Pages.JumpHostTitleIndex())...)
	page = append(page, ui.Publictopbar(ui.Crumbs(
		ui.Crumb(ui.Home(), ui.IconHouse, ui.Label(s.Host.Pages.HomeCrumbLabel())),
		ui.Crumb(ui.Text(s.Host.Pages.PublicIndexCrumb())),
	)))

	if len(groups) == 0 {
		page = append(page, ui.Empty(ui.Text(s.Host.Pages.PublicIndexEmpty())))
		return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
	}
	for _, g := range groups {
		rows := make([]ui.Item, 0, len(g.Fests))
		for _, f := range g.Fests {
			row := []ui.Item{ui.Href("/fest/" + f.Ref), ui.Listtitle(ui.Text(f.Title))}
			if f.Dates != "" {
				row = append(row, ui.Muted(ui.Text(f.Dates)))
			}
			rows = append(rows, ui.Listrow(row...))
		}
		page = append(page, ui.Festgroup(ui.Open(), ui.Title(g.Title), ui.List(rows...)))
	}
	return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
}

// PublicFestDoc builds a fest's public page: its dates, its description and its
// games. The trail is 🏠 / {fest} — the public tree's home is /, not /host.
func PublicFestDoc(d PublicFestDetail) *ui.Doc {
	s := dopestrings.Default
	page := []ui.Item{ui.Title(d.Title), ui.PagePublic}
	page = append(page, jumpHostNav("/host/fest/"+d.Ref, s.Host.Pages.JumpHostLabel(), s.Host.Pages.JumpHostTitleFest())...)
	page = append(page, ui.Publictopbar(pages.Trail([]ui.Item{pages.HomeCrumb()}, d.Title)))

	if d.Dates != "" {
		page = append(page, ui.Note(ui.Text(d.Dates)))
	}
	if d.Description != "" {
		page = append(page, ui.Richtext(ui.Raw(string(d.Description))))
	}
	if len(d.Games) == 0 {
		page = append(page, ui.Empty(ui.Text(s.Host.Pages.FestGamesEmpty())))
		return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
	}
	rows := make([]ui.Item, 0, len(d.Games))
	for _, g := range d.Games {
		row := []ui.Item{ui.Href(g.URL), ui.Listtitle(ui.Text(g.Title))}
		if g.Slug != "" {
			row = append(row, ui.Muted(ui.Text(g.Slug)))
		}
		rows = append(rows, ui.Listrow(row...))
	}
	page = append(page, ui.Section(ui.Subhead(ui.Text(s.Host.Pages.GamesSubhead())), ui.List(rows...)))
	return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
}
