package main

import (
	"html/template"
	"net/http"
)

// The fest-level "audit" page is now an index of the fest's games, each linking
// to its own per-game edit history + revert (see pages_host_journal.go). Revert
// and history are scoped per game; the old per-fest before/after audit_log view
// has been retired in favour of the forward journal.

type auditGameRow struct {
	ID    int64
	Code  string
	Title string
}

var festAuditIndexTmpl = template.Must(template.New("fest-audit-index").Parse(`<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>История изменений</title><link rel="stylesheet" href="/static/styles.css"></head>
<body><main class="container">
<h1>История изменений</h1>
<p>История и откат теперь ведутся отдельно по каждой игре.</p>
<ul class="list">
{{range .Games}}
  <li><a class="list-row" href="/host/fest/{{$.FestID}}/game/{{.ID}}/journal">{{.Title}} <small>({{.Code}})</small></a></li>
{{else}}
  <li>в этом фестивале пока нет игр</li>
{{end}}
</ul>
<p><a href="/host/fest/{{.FestID}}">← к фестивалю</a></p>
</main></body></html>`))

func (s *server) renderHostFestAudit(w http.ResponseWriter, r *http.Request, festID int64, errMsg, notice string) {
	rows, err := s.db.QueryContext(r.Context(),
		`select id, code, coalesce(title, '') from games where fest_id = ? order by position, id`, festID)
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = festAuditIndexTmpl.Execute(w, map[string]any{"FestID": festID, "Games": games})
}
