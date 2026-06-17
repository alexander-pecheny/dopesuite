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
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>История изменений</title>
  <link rel="preload" href="/static/fonts/noto-sans-400.woff2" as="font" type="font/woff2" crossorigin>
  <link rel="stylesheet" href="/static/styles.css">
  <script src="/static/menu.js"></script>
</head>
<body class="public">
  <header class="public-top">
    <a class="public-back" href="/host/fest/{{.FestID}}">←</a>
    <h1>История изменений</h1>
  </header>
  <main class="public-main">
    <section class="section">
      <p class="muted">История и откат ведутся отдельно по каждой игре.</p>
      {{if .Games}}
      <ul class="list">
        {{range .Games}}
        <li class="list-action-row">
          <a class="list-row" href="/host/fest/{{$.FestID}}/audit/{{.ID}}">
            <span class="list-row-title">{{.Title}}</span>
            {{if .Code}}<span class="muted">{{.Code}}</span>{{end}}
          </a>
        </li>
        {{end}}
      </ul>
      {{else}}
      <p class="empty">В этом фестивале пока нет игр.</p>
      {{end}}
    </section>
  </main>
</body>
</html>`))

func (s *server) renderHostFestAudit(w http.ResponseWriter, r *http.Request, festID int64, errMsg, notice string) {
	rows, err := s.db.QueryContext(r.Context(),
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = festAuditIndexTmpl.Execute(w, map[string]any{"FestID": festID, "Games": games})
}
