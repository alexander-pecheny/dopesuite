package qyserver

import (
	"context"
	"database/sql"
	"errors"
	"html"
	"io"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"

	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"

	"qy/qy/domain/contest"
	"qy/qy/platform/markdown"
	"qy/qy/storage/store"
	"qy/qy/web/route"
	"qy/qy/web/ui"
)

// The CMS, after studchr's site_v2: every page is a row, edited in the
// browser as Markdown or HTML, and every save is a revision that can be
// opened again. Uploaded files are served at their own path. A page under
// /{year}/ may use that year's rules variables, written {{name}}.

type page struct {
	Path, Title, Format, Body, UpdatedAt string
}

const (
	formatMarkdown = "markdown"
	formatHTML     = "html"
)

func (s *server) loadPage(ctx context.Context, p string) (page, error) {
	pg := page{Path: p}
	err := s.db.QueryRowContext(ctx, `select title, format, body, updated_at from pages where path = ?`, p).
		Scan(&pg.Title, &pg.Format, &pg.Body, &pg.UpdatedAt)
	return pg, err
}

func renderPage(p page, vars map[string]string) string {
	body := markdown.Expand(p.Body, vars)
	if p.Format == formatHTML {
		return body
	}
	out, err := markdown.Render(body)
	if err != nil {
		return "<pre>" + html.EscapeString(err.Error()) + "</pre>"
	}
	return out
}

var pagePathRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*(/[a-z0-9][a-z0-9_.-]*)*$`)

// reservedRoots are the app's own routes, which a page could never be
// reached at.
var reservedRoots = map[string]bool{
	"admin": true, "api": true, "login": true, "nominate": true, "vote": true,
	"final": true, "popular": true, "profile": true, "logout": true, "static": true,
}

func validPagePath(p string) bool {
	if p == "" {
		return true
	}
	return pagePathRe.MatchString(p) && !reservedRoots[strings.SplitN(p, "/", 2)[0]]
}

// yearOf is the contest year a path sits under, or 0.
func yearOf(p string) int {
	first := strings.SplitN(p, "/", 2)[0]
	if len(first) != len("2026") {
		return 0
	}
	y, err := strconv.Atoi(first)
	if err != nil {
		return 0
	}
	return y
}

func (s *server) varsFor(ctx context.Context, p string) (map[string]string, error) {
	y := yearOf(p)
	if y == 0 {
		return nil, nil
	}
	c, err := store.Contest(ctx, s.db, y)
	if errors.Is(err, store.ErrNoContest) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.rulesVars(ctx, c)
}

// ---- the public side ----

// generatedPages are the pages of a year the app builds from its own data,
// unless a page of the same path has been written in the CMS.
const (
	generatedShortlist = "shortlist"
	generatedResults   = "results"
)

func (s *server) handlePublic(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	trimmed := strings.Trim(r.URL.Path, "/")
	ctx := r.Context()
	if served, err := s.serveAsset(w, r, trimmed); served || err != nil {
		return err
	}
	p, err := s.loadPage(ctx, trimmed)
	if errors.Is(err, sql.ErrNoRows) {
		return s.serveGenerated(w, r, sc, trimmed)
	}
	if err != nil {
		return err
	}
	if trimmed != "" && !strings.HasSuffix(r.URL.Path, "/") {
		http.Redirect(w, r, "/"+trimmed+"/", http.StatusMovedPermanently)
		return nil
	}
	vars, err := s.varsFor(ctx, trimmed)
	if err != nil {
		return err
	}
	body := []ui.Item{ui.Richtext(ui.Raw(renderPage(p, vars)))}
	if trimmed == "" {
		banner, err := s.contestBanner(ctx, sc)
		if err != nil {
			return err
		}
		body = append(banner, body...)
	}
	if sc.IsAdmin {
		body = append(body, ui.Row(ui.Button(ui.Href("/admin/pages/edit?path="+trimmed), ui.Ghost, ui.Small(), ui.IconPencil, text(str.Cms.EditThis()))))
	}
	title := markdown.Expand(p.Title, vars)
	return s.ok(w, r, view{title: title, trail: pageTrail(trimmed, title), body: []ui.Item{ui.Section(body...)}})
}

// pageTrail links back through a page's ancestors by their path segments.
func pageTrail(p, title string) []crumb {
	if p == "" {
		return []crumb{{"", title}}
	}
	parts := strings.Split(p, "/")
	var out []crumb
	href := "/"
	for _, part := range parts[:len(parts)-1] {
		href += part + "/"
		out = append(out, crumb{href, part})
	}
	return append(out, crumb{"", title})
}

func (s *server) serveGenerated(w http.ResponseWriter, r *http.Request, sc route.Scope, p string) error {
	y := yearOf(p)
	rest := strings.TrimPrefix(p, strconv.Itoa(y)+"/")
	if y == 0 || (rest != generatedShortlist && rest != generatedResults) {
		return route.NotFound(str.Cms.NotFound())
	}
	c, err := store.Contest(r.Context(), s.db, y)
	if errors.Is(err, store.ErrNoContest) {
		return route.NotFound(str.Cms.NotFound())
	}
	if err != nil {
		return err
	}
	if !strings.HasSuffix(r.URL.Path, "/") {
		http.Redirect(w, r, "/"+p+"/", http.StatusMovedPermanently)
		return nil
	}
	switch {
	case rest == generatedResults && c.ResultsPublishedAt != "":
		return s.serveResults(w, r, c)
	case rest == generatedShortlist && s.shortlistVisible(c):
		return s.serveShortlist(w, r, sc, c)
	}
	return route.NotFound(str.Cms.NotYet())
}

// contestBanner opens the home page with what the current contest asks of a
// visitor today.
func (s *server) contestBanner(ctx context.Context, sc route.Scope) ([]ui.Item, error) {
	c, err := store.Current(ctx, s.db)
	if errors.Is(err, store.ErrNoContest) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	y := strconv.Itoa(c.Year)
	items := []ui.Item{ui.DirCol, ui.SpaceSM, ui.Strong(text(str.Contest.Name(y))), hint(stageName(c.StageAt(s.now())))}
	var buttons []ui.Item
	switch c.StageAt(s.now()) {
	case contest.Before:
		items = append(items, hint(str.Banner.Before(dayText(c.NominationStart))))
	case contest.Nomination:
		items = append(items, hint(str.Banner.Nomination(dayText(c.NominationEnd))))
		buttons = append(buttons, ui.Button(ui.Href("/nominate"), ui.Primary, text(str.Nominate.Page.Title())))
	case contest.Prelim, contest.Factcheck:
		buttons = append(buttons, ui.Button(ui.Href("/vote"), ui.Ghost, text(str.Vote.Page.Title())))
	case contest.Final:
		if c.ShortlistFrozenAt != "" {
			buttons = append(buttons,
				ui.Button(ui.Href("/popular"), ui.Primary, text(str.Final.Popular.Title())),
				ui.Button(ui.Href("/"+y+"/shortlist/"), ui.Ghost, text(str.Results.Shortlist())))
		}
	case contest.Done:
		buttons = append(buttons, ui.Button(ui.Href("/"+y+"/results/"), ui.Primary, text(str.Results.Title())))
	}
	buttons = append(buttons, ui.Button(ui.Href("/"+y+"/rules/"), ui.Ghost, text(str.Banner.Rules())))
	items = append(items, ui.Row(append([]ui.Item{ui.SpaceSM, ui.Wrap()}, buttons...)...))
	return []ui.Item{ui.Card(items...)}, nil
}

func (s *server) serveAsset(w http.ResponseWriter, r *http.Request, p string) (bool, error) {
	var contentType, updatedAt string
	var data []byte
	err := s.db.QueryRowContext(r.Context(), `select content_type, data, updated_at from assets where path = ?`, p).Scan(&contentType, &data, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	etag := `"` + updatedAt + `"`
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return true, nil
	}
	_, _ = w.Write(data)
	return true, nil
}

// ---- the admin side ----

func cmsTrail(steps ...crumb) []crumb {
	return adminTrail(append([]crumb{{"/admin/pages", str.Cms.Pages()}}, steps...)...)
}

func (s *server) handlePageList(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	rows, err := s.db.QueryContext(r.Context(), `select path, title from pages order by path`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var list []ui.Item
	for rows.Next() {
		var p, title string
		if err := rows.Scan(&p, &title); err != nil {
			return err
		}
		list = append(list, ui.Listrow(ui.Href("/admin/pages/edit?path="+p), ui.Listtitle(text(title)), ui.Muted(text("/"+p))))
	}
	if err := rows.Err(); err != nil {
		return err
	}
	body := []ui.Item{ui.Row(ui.Button(ui.Href("/admin/pages/edit?new=1"), ui.Primary, ui.IconPlus, text(str.Cms.NewPage())))}
	if len(list) > 0 {
		body = append(body, ui.List(list...))
	}
	return s.ok(w, r, view{title: str.Cms.Pages(), trail: adminTrail(crumb{"", str.Cms.Pages()}), body: []ui.Item{ui.Section(body...)}})
}

// editorState is what the editor shows: the page as typed, the path and
// version it was loaded at (a save compares them to catch a concurrent edit),
// and whether it is new.
type editorState struct {
	page
	original, base string
	isNew          bool
	notice         string
	preview        bool
}

func (s *server) handlePageEdit(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	q := r.URL.Query()
	if q.Get("new") != "" {
		return s.renderEditor(w, r, http.StatusOK, editorState{page: page{Format: formatMarkdown}, isNew: true})
	}
	p, err := s.loadPage(r.Context(), q.Get("path"))
	if errors.Is(err, sql.ErrNoRows) {
		return s.renderEditor(w, r, http.StatusOK, editorState{page: page{Path: q.Get("path"), Format: formatMarkdown}, isNew: true})
	}
	if err != nil {
		return err
	}
	st := editorState{page: p, original: p.Path, base: p.UpdatedAt}
	if q.Get("done") == doneSaved {
		st.notice = str.Common.Saved()
	}
	return s.renderEditor(w, r, http.StatusOK, st)
}

func (s *server) renderEditor(w http.ResponseWriter, r *http.Request, status int, st editorState) error {
	title := str.Cms.NewPage()
	if !st.isNew {
		title = st.Title
	}
	form := []ui.Item{ui.Method("post"), ui.Action("/admin/pages/save"), ui.DirCol, ui.SpaceSM,
		ui.Hiddenfield(ui.Name("original_path"), ui.Value(st.original)),
		ui.Hiddenfield(ui.Name("base_updated_at"), ui.Value(st.base)),
		ui.Field(ui.Label(str.Cms.Path()), ui.Textfield(ui.Name("path"), ui.Value(st.Path), ui.Placeholder(str.Cms.PathPlaceholder()))),
		ui.Field(ui.Label(str.Cms.Title()), ui.Textfield(ui.Name("title"), ui.Value(st.Title), ui.Required())),
		ui.Field(ui.Label(str.Cms.Format()), ui.Selectfield(ui.Name("format"),
			option(formatMarkdown, str.Cms.Markdown(), st.Format), option(formatHTML, str.Cms.Html(), st.Format))),
		ui.Field(ui.Label(str.Cms.Body()), ui.Editor(ui.Name("body"), ui.Rows("24"), ui.Spellcheck("true"), ui.Text(st.Body))),
		hint(str.Cms.VarsHint("{{year}}, {{dates_nomination}}, {{jury}}")),
		ui.Row(ui.SpaceSM, ui.Wrap(),
			ui.Button(ui.Submit(), ui.Primary, text(str.Common.Save())),
			ui.Button(ui.Submit(), ui.Ghost, ui.Name("preview"), ui.Value("1"), text(str.Cms.Preview()))),
	}
	head := []ui.Item{}
	if st.notice != "" {
		head = append(head, hint(st.notice))
	}
	if !st.isNew {
		head = append(head, ui.Row(ui.SpaceSM, ui.Wrap(),
			ui.Button(ui.Href("/"+st.Path), ui.Ghost, ui.Small(), text(str.Cms.Open())),
			ui.Button(ui.Href("/admin/pages/history?path="+st.Path), ui.Ghost, ui.Small(), text(str.Cms.History()))))
	}
	body := []ui.Item{ui.Section(append(head, ui.Form(form...))...)}
	if st.preview {
		vars, err := s.varsFor(r.Context(), st.Path)
		if err != nil {
			return err
		}
		body = append(body, ui.Section(subhead(str.Cms.Preview()), ui.Card(ui.Richtext(ui.Raw(renderPage(st.page, vars))))))
	}
	if !st.isNew {
		body = append(body, ui.Section(ui.Form(ui.Method("post"), ui.Action("/admin/pages/delete"), ui.Data("confirm", str.Cms.DeleteConfirm()),
			ui.Hiddenfield(ui.Name("path"), ui.Value(st.Path)),
			ui.Button(ui.Submit(), ui.Danger, ui.Small(), ui.IconTrash2, text(str.Cms.Delete())))))
	}
	return s.render(w, r, status, view{title: title, trail: cmsTrail(crumb{"", title}), body: body})
}

func (s *server) handlePageSave(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	if err := parseForm(w, r); err != nil {
		return err
	}
	st := editorState{
		page: page{
			Path:   strings.Trim(strings.TrimSpace(r.FormValue("path")), "/"),
			Title:  strings.TrimSpace(r.FormValue("title")),
			Format: r.FormValue("format"),
			Body:   strings.ReplaceAll(r.FormValue("body"), "\r\n", "\n"),
		},
		original: r.FormValue("original_path"),
		base:     r.FormValue("base_updated_at"),
	}
	st.isNew = st.base == ""
	if r.FormValue("preview") != "" {
		st.preview = true
		return s.renderEditor(w, r, http.StatusOK, st)
	}
	err := s.withWriteTx(r.Context(), "page-save", func(ctx context.Context, tx *sql.Tx) error {
		return s.savePage(ctx, tx, sc.User.UserID, st)
	})
	if err != nil {
		code, msg := errorOf(err)
		if code == http.StatusInternalServerError {
			return err
		}
		// The editor keeps what was typed, with the reason it was refused.
		st.notice = msg
		return s.renderEditor(w, r, code, st)
	}
	return seeOther(w, r, "/admin/pages/edit?done="+doneSaved+"&path="+st.Path)
}

func checkPage(st editorState) error {
	switch {
	case !validPagePath(st.Path):
		return userError(str.Cms.BadPath())
	case st.Title == "":
		return userError(str.Cms.NoTitle())
	case st.Format != formatMarkdown && st.Format != formatHTML:
		return userError(str.Server.Error.BadRequest())
	}
	return nil
}

func (s *server) savePage(ctx context.Context, tx *sql.Tx, author int64, st editorState) error {
	if err := checkPage(st); err != nil {
		return err
	}
	var current string
	err := tx.QueryRowContext(ctx, `select updated_at from pages where path = ?`, st.original).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if st.base != "" && current != st.base {
		return route.Conflict(str.Cms.Conflict())
	}
	if st.isNew || st.original != st.Path {
		var taken int
		if err := tx.QueryRowContext(ctx, `select count(*) from pages where path = ?`, st.Path).Scan(&taken); err != nil {
			return err
		}
		if taken > 0 {
			return route.Conflict(str.Cms.Taken())
		}
	}
	now := s.stamp()
	if !st.isNew && st.original != st.Path {
		if _, err := tx.ExecContext(ctx, `delete from pages where path = ?`, st.original); err != nil {
			return err
		}
		if err := addRevision(ctx, tx, page{Path: st.original, Title: str.Cms.MovedTo("/" + st.Path), Format: st.Format}, true, now, author); err != nil {
			return err
		}
	}
	return writePage(ctx, tx, st.page, now, author)
}

func writePage(ctx context.Context, tx *sql.Tx, p page, now string, author int64) error {
	_, err := tx.ExecContext(ctx, `
insert into pages(path, title, format, body, updated_at, updated_by) values(?, ?, ?, ?, ?, ?)
on conflict(path) do update set title = excluded.title, format = excluded.format, body = excluded.body,
  updated_at = excluded.updated_at, updated_by = excluded.updated_by`,
		p.Path, p.Title, p.Format, p.Body, now, authorID(author))
	if err != nil {
		return err
	}
	return addRevision(ctx, tx, p, false, now, author)
}

func addRevision(ctx context.Context, tx *sql.Tx, p page, deleted bool, now string, author int64) error {
	_, err := tx.ExecContext(ctx, `
insert into page_revisions(path, title, format, body, deleted, created_at, author_id) values(?, ?, ?, ?, ?, ?, ?)`,
		p.Path, p.Title, p.Format, p.Body, deleted, now, authorID(author))
	return err
}

// authorID is the nullable author column: 0 is a change made from the shell.
func authorID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func (s *server) handlePageDelete(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	p := r.FormValue("path")
	err := s.withWriteTx(r.Context(), "page-delete", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `delete from pages where path = ?`, p)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
		return addRevision(ctx, tx, page{Path: p, Format: formatMarkdown}, true, s.stamp(), sc.User.UserID)
	})
	if err != nil {
		return err
	}
	return seeOther(w, r, "/admin/pages")
}

type revision struct {
	id            int64
	page          page
	deleted       bool
	createdAt, by string
}

func (s *server) revisions(ctx context.Context, p string) ([]revision, error) {
	rows, err := s.db.QueryContext(ctx, `
select r.id, r.title, r.format, r.body, r.deleted, r.created_at, coalesce(u.telegram_username, u.username, '')
from page_revisions r left join users u on u.id = r.author_id where r.path = ? order by r.id desc`, p)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []revision
	for rows.Next() {
		rv := revision{page: page{Path: p}}
		if err := rows.Scan(&rv.id, &rv.page.Title, &rv.page.Format, &rv.page.Body, &rv.deleted, &rv.createdAt, &rv.by); err != nil {
			return nil, err
		}
		out = append(out, rv)
	}
	return out, rows.Err()
}

func (s *server) handlePageHistory(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	p := r.URL.Query().Get("path")
	revs, err := s.revisions(r.Context(), p)
	if err != nil {
		return err
	}
	selected, _ := idstr.Parse(r.URL.Query().Get("rev"))
	var list []ui.Item
	var detail []ui.Item
	for i, rv := range revs {
		by := rv.by
		if by == "" {
			by = str.Cms.ByShell()
		}
		label := str.Cms.Revision(sinceStamp(rv.createdAt), by)
		if rv.deleted {
			label += str.Cms.Deleted()
		}
		list = append(list, ui.Listrow(ui.Href("/admin/pages/history?path="+p+"&rev="+idstr.Format(rv.id)), ui.Listtitle(text(label))))
		if rv.id != selected {
			continue
		}
		prev := ""
		if i+1 < len(revs) {
			prev = revs[i+1].page.Body
		}
		detail = append(detail, subhead(label), ui.Richtext(ui.Raw(diffHTML(lineDiff(prev, rv.page.Body)))))
		if !rv.deleted {
			detail = append(detail, ui.Form(ui.Method("post"), ui.Action("/admin/pages/restore"),
				ui.Hiddenfield(ui.Name("rev"), ui.Value(idstr.Format(rv.id))),
				ui.Button(ui.Submit(), text(str.Cms.Restore()))))
		}
	}
	body := []ui.Item{ui.Section(hint(str.Cms.HistoryHint()), ui.List(list...))}
	if len(detail) > 0 {
		body = append(body, ui.Section(detail...))
	}
	return s.ok(w, r, view{title: str.Cms.History(), trail: cmsTrail(crumb{"/admin/pages/edit?path=" + p, "/" + p}, crumb{"", str.Cms.History()}), body: body})
}

func diffHTML(lines []diffLine) string {
	var b strings.Builder
	b.WriteString(`<pre class="page-diff">`)
	for _, l := range lines {
		class := map[string]string{"+": "diff-add", "-": "diff-del", "…": "diff-skip"}[l.Op]
		if class == "" {
			class = "diff-same"
		}
		b.WriteString(`<span class="` + class + `">` + html.EscapeString(l.Op+" "+l.Text) + "\n</span>")
	}
	b.WriteString("</pre>")
	return b.String()
}

// handlePageRestore opens an old revision in the editor, unsaved, so the
// admin sees it before it goes live.
func (s *server) handlePageRestore(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	var p page
	err := s.db.QueryRowContext(r.Context(), `select path, title, format, body from page_revisions where id = ? and deleted = 0`, r.FormValue("rev")).
		Scan(&p.Path, &p.Title, &p.Format, &p.Body)
	if errors.Is(err, sql.ErrNoRows) {
		return route.NotFound(str.Cms.NotFound())
	}
	if err != nil {
		return err
	}
	cur, err := s.loadPage(r.Context(), p.Path)
	isNew := errors.Is(err, sql.ErrNoRows)
	if err != nil && !isNew {
		return err
	}
	st := editorState{page: p, original: p.Path, base: cur.UpdatedAt, isNew: isNew, notice: str.Cms.Restored()}
	if isNew {
		st.original = ""
	}
	return s.renderEditor(w, r, http.StatusOK, st)
}

// ---- files ----

const maxUpload = 20 << 20

func (s *server) handleFiles(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	rows, err := s.db.QueryContext(r.Context(), `select path, content_type, length(data) from assets order by path`)
	if err != nil {
		return err
	}
	defer rows.Close()
	tableRows := []ui.Item{ui.Scroll(), ui.Trow(ui.Hcell(text(str.Cms.Path())), ui.Hcell(text(str.Cms.Type())), ui.Hcell(text(str.Cms.Size())), ui.Hcell())}
	n := 0
	for rows.Next() {
		var p, ctype string
		var size int64
		if err := rows.Scan(&p, &ctype, &size); err != nil {
			return err
		}
		n++
		tableRows = append(tableRows, ui.Trow(
			ui.Cell(ui.Button(ui.Href("/"+p), ui.Ghost, ui.Small(), text("/"+p))),
			ui.Cell(text(ctype)), ui.Cell(text(str.Cms.Bytes(idstr.Format(size)))),
			ui.Cell(ui.Form(ui.Method("post"), ui.Action("/admin/files/delete"), ui.Data("confirm", str.Cms.DeleteFileConfirm()),
				ui.Hiddenfield(ui.Name("path"), ui.Value(p)),
				ui.Button(ui.Submit(), ui.Ghost, ui.Small(), text(str.Common.Remove()))))))
	}
	if err := rows.Err(); err != nil {
		return err
	}
	upload := ui.Form(ui.Method("post"), ui.Action("/admin/files/upload"), ui.Enctype("multipart/form-data"), ui.DirCol, ui.SpaceSM,
		ui.Field(ui.Label(str.Cms.Folder()), ui.Textfield(ui.Name("dir"), ui.Placeholder("img"))),
		ui.Field(ui.Label(str.Cms.File()), ui.Filefield(ui.Name("file"), ui.Required())),
		ui.Row(ui.Button(ui.Submit(), ui.Primary, text(str.Cms.Upload()))))
	body := []ui.Item{ui.Section(hint(str.Cms.FilesHint()), upload)}
	if n > 0 {
		body = append(body, ui.Section(ui.Table(tableRows...)))
	}
	return s.ok(w, r, view{title: str.Admin.Site.Files(), trail: adminTrail(crumb{"", str.Admin.Site.Files()}), body: body})
}

func (s *server) handleFileUpload(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	f, hdr, err := r.FormFile("file")
	if err != nil {
		return userError(str.Cms.UploadFailed())
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	name := path.Join(strings.Trim(strings.TrimSpace(r.FormValue("dir")), "/"), path.Base(hdr.Filename))
	if err := s.withWriteTx(r.Context(), "file-upload", func(ctx context.Context, tx *sql.Tx) error {
		return saveAsset(ctx, tx, name, data, s.stamp(), sc.User.UserID)
	}); err != nil {
		return err
	}
	return seeOther(w, r, "/admin/files")
}

func validFileName(name string) bool {
	switch {
	case name == "", name == ".", strings.Contains(name, ".."), strings.ContainsAny(name, " ?#"):
		return false
	}
	return !reservedRoots[strings.SplitN(name, "/", 2)[0]]
}

func saveAsset(ctx context.Context, tx *sql.Tx, name string, data []byte, now string, author int64) error {
	if !validFileName(name) {
		return userError(str.Cms.BadFileName())
	}
	ctype := mime.TypeByExtension(path.Ext(name))
	if ctype == "" {
		ctype = http.DetectContentType(data)
	}
	_, err := tx.ExecContext(ctx, `
insert into assets(path, content_type, data, updated_at, updated_by) values(?, ?, ?, ?, ?)
on conflict(path) do update set content_type = excluded.content_type, data = excluded.data,
  updated_at = excluded.updated_at, updated_by = excluded.updated_by`, name, ctype, data, now, authorID(author))
	return err
}

func (s *server) handleFileDelete(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	err := s.withWriteTx(r.Context(), "file-delete", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `delete from assets where path = ?`, r.FormValue("path"))
		return err
	})
	if err != nil {
		return err
	}
	return seeOther(w, r, "/admin/files")
}
