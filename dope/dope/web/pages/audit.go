package pages

import (
	"fmt"
	"net/http"

	ui "dope/dope/web/ui"
	dopestrings "dope/i18nstrings"
)

// The fest-level "audit" page is now an index of the fest's games, each linking
// to its own per-game edit history + revert (see journal.go). Revert and history
// are scoped per game; the old per-fest before/after audit_log view has been
// retired in favour of the forward journal.

type auditGameRow struct {
	ID    int64
	Code  string
	Title string
}

// festAuditIndexDoc builds the fest's per-game history index page: a link list of
// the fest's games, each pointing at its own edit history + revert.
func festAuditIndexDoc(base, festTitle string, games []auditGameRow) *ui.Doc {
	sect := []ui.Item{ui.Note(ui.Text(dopestrings.Default.Journal.Index.Note()))}
	if len(games) > 0 {
		rows := make([]ui.Item, 0, len(games))
		for _, g := range games {
			row := []ui.Item{
				ui.Href(fmt.Sprintf("%s/audit/%d", base, g.ID)),
				ui.Listtitle(ui.Text(g.Title)),
			}
			if g.Code != "" {
				row = append(row, ui.Muted(ui.Text(g.Code)))
			}
			rows = append(rows, ui.Listrow(row...))
		}
		sect = append(sect, ui.List(rows...))
	} else {
		sect = append(sect, ui.Empty(ui.Text(dopestrings.Default.Journal.Index.Empty())))
	}
	return &ui.Doc{Nodes: []ui.Node{
		ui.Page(ui.Title(dopestrings.Default.Journal.Index.Title()), ui.PagePublic,
			ui.Publictopbar(Trail(FestCrumbs(base, festTitle), dopestrings.Default.Journal.Index.Title())),
			ui.Section(sect...),
		),
	}}
}

// RenderHostFestAudit renders the fest's per-game history index page.
func (s *Server) RenderHostFestAudit(w http.ResponseWriter, r *http.Request, festID int64, errMsg, notice string) {
	fest, err := s.h.LoadHostFestHeader(r.Context(), festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rows, err := s.h.Engine().DB.QueryContext(r.Context(),
		`select id, code, coalesce(title, code) from games where fest_id = ? order by position, id`, festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var games []auditGameRow
	for rows.Next() {
		var g auditGameRow
		if err := rows.Scan(&g.ID, &g.Code, &g.Title); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		games = append(games, g)
	}
	RenderDoc(w, s.h.Engine().AssetETags, festAuditIndexDoc(fest.HostBase(), fest.Title, games))
}
