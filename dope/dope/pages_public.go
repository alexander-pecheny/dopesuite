package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
)

type publicFestSummary struct {
	ID          int64
	Title       string
	StartDate   string
	EndDate     string
	Dates       string
	Description string
}

type publicFestGame struct {
	ID    int64
	Code  string
	Title string
	Type  string
	URL   string
}

type publicFestDetail struct {
	ID          int64
	Title       string
	Dates       string
	Description template.HTML
	Games       []publicFestGame
}

var publicListTemplate = template.Must(template.New("publicList").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Фесты</title>
  <link rel="stylesheet" href="/static/styles.css">
</head>
<body class="public">
  <header class="public-top">
    <h1>Фесты</h1>
  </header>
  <main class="public-main">
    {{if .}}
    <ul class="list">
      {{range .}}
      <li>
        <a class="list-row" href="/fest/{{.ID}}">
          <span class="list-row-title">{{.Title}}</span>
          {{if .Dates}}<span class="muted">{{.Dates}}</span>{{end}}
        </a>
      </li>
      {{end}}
    </ul>
    {{else}}
    <p class="empty">Нет публичных фестов.</p>
    {{end}}
  </main>
</body>
</html>`))

var publicFestTemplate = template.Must(template.New("publicFest").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Title}}</title>
  <link rel="stylesheet" href="/static/styles.css">
</head>
<body class="public">
  <header class="public-top">
    <a class="public-back" href="/">←</a>
    <h1>{{.Title}}</h1>
  </header>
  <main class="public-main">
    {{if .Dates}}<p class="muted">{{.Dates}}</p>{{end}}
    {{if .Description}}<section class="public-description">{{.Description}}</section>{{end}}
    {{if .Games}}
    <section class="section">
      <h2>Игры</h2>
      <ul class="list">
        {{range .Games}}
        <li>
          <a class="list-row" href="{{.URL}}">
            <span class="list-row-title">{{.Title}}</span>
            <span class="muted">{{.Type}}</span>
          </a>
        </li>
        {{end}}
      </ul>
    </section>
    {{else}}
    <p class="empty">В этом фесте пока нет игр.</p>
    {{end}}
  </main>
</body>
</html>`))

func (s *server) handlePublicIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	summaries, err := s.loadPublicFestSummaries(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := publicListTemplate.Execute(w, summaries); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *server) handleFestRouter(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/fest/")
	if rest == r.URL.Path {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(strings.TrimSuffix(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 1 {
		s.renderPublicFestPage(w, r, id)
		return
	}
	if !isViewerSubPath(parts[1:]) {
		http.NotFound(w, r)
		return
	}
	if err := s.assertFestPublic(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Public viewer pages mirror host pages: OD/SI get their own viewer.
	if len(parts) >= 3 {
		gameID, err := strconv.ParseInt(parts[2], 10, 64)
		if err == nil && gameID > 0 {
			var gameType string
			if err := s.db.QueryRowContext(r.Context(), `select game_type from games where id = ? and fest_id = ?`, gameID, id).Scan(&gameType); err == nil {
				switch gameType {
				case "od":
					s.serveAppHTML(w, r, "static/od.html")
					return
				case "si", "ksi":
					s.serveAppHTML(w, r, "static/si.html")
					return
				}
			}
		}
	}
	s.serveViewerHTML(w, r)
}

func (s *server) renderPublicFestPage(w http.ResponseWriter, r *http.Request, id int64) {
	detail, err := s.loadPublicFestDetail(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := publicFestTemplate.Execute(w, detail); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *server) assertFestPublic(ctx context.Context, festID int64) error {
	var isPublic int
	if err := s.db.QueryRowContext(ctx, `select is_public from fests where id = ?`, festID).Scan(&isPublic); err != nil {
		return err
	}
	if isPublic != 1 {
		return sql.ErrNoRows
	}
	return nil
}

// isViewerSubPath validates that a fest-scoped path under /fest/{id}/
// is one of the recognised viewer routes (/game/{gid}/...). Only known shapes
// pass; anything else 404s.
func isViewerSubPath(parts []string) bool {
	if len(parts) < 2 {
		return false
	}
	if parts[0] != "game" {
		return false
	}
	if _, err := strconv.ParseInt(parts[1], 10, 64); err != nil {
		return false
	}
	if len(parts) == 2 {
		return true
	}
	switch parts[2] {
	case "venues":
		return len(parts) == 3
	case "matches", "stage":
		return len(parts) == 4 && parts[3] != ""
	}
	return false
}

func (s *server) loadPublicFestSummaries(ctx context.Context) ([]publicFestSummary, error) {
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
select id, title, coalesce(start_date, ''), coalesce(end_date, '')
from fests
where is_public = 1
order by case when start_date is null or start_date = '' then 1 else 0 end,
         start_date,
         id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []publicFestSummary
	for rows.Next() {
		var s publicFestSummary
		if err := rows.Scan(&s.ID, &s.Title, &s.StartDate, &s.EndDate); err != nil {
			return nil, err
		}
		s.Dates = formatFestDates(s.StartDate, s.EndDate)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (s *server) loadPublicFestDetail(ctx context.Context, id int64) (publicFestDetail, error) {
	if s.db == nil {
		return publicFestDetail{}, sql.ErrNoRows
	}
	var (
		title       string
		description string
		startDate   sql.NullString
		endDate     sql.NullString
		isPublic    int
	)
	if err := s.db.QueryRowContext(ctx, `
select title, description, start_date, end_date, is_public
from fests where id = ?`, id).Scan(&title, &description, &startDate, &endDate, &isPublic); err != nil {
		return publicFestDetail{}, err
	}
	if isPublic != 1 {
		return publicFestDetail{}, sql.ErrNoRows
	}
	games, err := loadFestGames(ctx, s.db, id)
	if err != nil {
		return publicFestDetail{}, err
	}
	publicGames := make([]publicFestGame, len(games))
	for i, g := range games {
		publicGames[i] = publicFestGame{
			ID:    g.ID,
			Code:  g.Code,
			Title: g.Title,
			Type:  gameTypeLabel(g.Type),
			URL:   fmt.Sprintf("/fest/%d/game/%d/", id, g.ID),
		}
	}
	detail := publicFestDetail{
		ID:          id,
		Title:       title,
		Dates:       formatFestDates(startDate.String, endDate.String),
		Description: renderMarkdown(description),
		Games:       publicGames,
	}
	return detail, nil
}

func gameTypeLabel(gameType string) string {
	switch gameType {
	case "od":
		return "ЧГК"
	case "si":
		return "СИ"
	case "ksi":
		return "КСИ"
	case "ek":
		return "ЭК"
	default:
		return gameType
	}
}

func formatFestDates(start, end string) string {
	start = strings.TrimSpace(start)
	end = strings.TrimSpace(end)
	switch {
	case start == "" && end == "":
		return ""
	case start != "" && end != "" && start != end:
		return start + " — " + end
	case start != "":
		return start
	default:
		return end
	}
}

type festGameRow struct {
	ID    int64
	Code  string
	Title string
	Type  string
}

func loadFestGames(ctx context.Context, q dbQueryer, festID int64) ([]festGameRow, error) {
	rows, err := q.QueryContext(ctx, `
select id, code, title, game_type
from games where fest_id = ?
order by position, id`, festID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []festGameRow
	for rows.Next() {
		var g festGameRow
		if err := rows.Scan(&g.ID, &g.Code, &g.Title, &g.Type); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
