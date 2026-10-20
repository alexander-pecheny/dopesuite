package qyserver

import (
	"net/http"
	"strings"

	"qy/qy/web/ui"

	qystrings "qy/i18nstrings"
)

// Every page qy serves but the login is built here with the kit's typed
// builder: a topbar with the breadcrumb trail, then the page's sections, in
// the "wide" frame that reads at a column's measure.

var str = qystrings.Default

// crumb is one step of the trail. The last step is the page itself and is
// shown as text.
type crumb struct{ href, text string }

func trailOf(steps []crumb) ui.Item {
	items := []ui.Item{ui.Crumb(ui.Home(), ui.Href("/"), ui.IconHouse, ui.Label(str.Common.Home()))}
	for i, c := range steps {
		if i == len(steps)-1 || c.href == "" {
			items = append(items, ui.Crumb(ui.Text(c.text)))
			continue
		}
		items = append(items, ui.Crumb(ui.Href(c.href), ui.Text(c.text)))
	}
	return ui.Crumbs(items...)
}

// view is one page: its title, its trail and its body.
type view struct {
	title string
	trail []crumb
	body  []ui.Item
}

func (s *server) render(w http.ResponseWriter, r *http.Request, status int, v view) error {
	// forms.js is small and every page has a form or might: one bundle for all.
	page := []ui.Item{ui.Title(pageTitle(v.title)), ui.PageWide, ui.Scripts("dist/forms.js")}
	page = append(page, ui.Topbar(ui.Nosync(), trailOf(v.trail)))
	page = append(page, v.body...)
	body, err := ui.Render(&ui.Doc{Nodes: []ui.Node{ui.Page(page...)}})
	if err != nil {
		return err
	}
	s.writePage(w, r, status, body)
	return nil
}

// pageTitle names the site after the page, unless the page's own title
// already starts with it.
func pageTitle(title string) string {
	if strings.HasPrefix(title, str.Common.SiteName()) {
		return title
	}
	return str.Common.TitleSuffix(title, str.Common.SiteName())
}

func (s *server) ok(w http.ResponseWriter, r *http.Request, v view) error {
	return s.render(w, r, http.StatusOK, v)
}

// writePageError is the pages' error writer: the same words the API would
// send, on a page with the way home.
func (s *server) writePageError(w http.ResponseWriter, r *http.Request, err error) {
	if err == nil {
		return
	}
	code, msg := errorOf(err)
	logDropped("error page", s.render(w, r, code, view{
		title: str.Common.ErrorTitle(),
		trail: []crumb{{"", str.Common.ErrorTitle()}},
		body: []ui.Item{ui.Section(
			ui.Hint(ui.HintDanger, ui.Text(msg)),
			ui.Row(ui.Button(ui.Href("/"), ui.Text(str.Common.ToHome()))),
		)},
	}))
}

// seeOther ends a successful form post: back to a page, which shows what
// happened when done names one of the notices.
func seeOther(w http.ResponseWriter, r *http.Request, target string) error {
	http.Redirect(w, r, target, http.StatusSeeOther)
	return nil
}

// Notices a redirect can name in ?done=; a page shows the matching line.
const (
	doneSaved = "saved"
)

func noticeOf(r *http.Request) []ui.Item {
	if r.URL.Query().Get("done") == doneSaved {
		return []ui.Item{ui.Hint(ui.Text(str.Common.Saved()))}
	}
	return nil
}

func hint(text string) ui.Item       { return ui.Hint(ui.Text(text)) }
func dangerHint(text string) ui.Item { return ui.Hint(ui.HintDanger, ui.Text(text)) }
func subhead(text string) ui.Item    { return ui.Subhead(ui.Text(text)) }
func text(t string) ui.Item          { return ui.Text(t) }

// headRow is a table's header row.
func headRow(labels ...string) ui.Item {
	cells := make([]ui.Item, len(labels))
	for i, l := range labels {
		cells[i] = ui.Hcell(text(l))
	}
	return ui.Trow(cells...)
}
