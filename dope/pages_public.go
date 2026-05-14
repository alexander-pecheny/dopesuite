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

type publicTournamentSummary struct {
	ID          int64
	Title       string
	StartDate   string
	EndDate     string
	Dates       string
	Description string
}

type publicTournamentGame struct {
	ID    int64
	Code  string
	Title string
	Type  string
	URL   string
}

type publicTournamentDetail struct {
	ID          int64
	Title       string
	Dates       string
	Description template.HTML
	Games       []publicTournamentGame
	RatingURL   string
}

var publicListTemplate = template.Must(template.New("publicList").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Чемпионаты</title>
  <link rel="stylesheet" href="/static/styles.css">
</head>
<body class="public">
  <header class="public-top">
    <h1>Чемпионаты</h1>
  </header>
  <main class="public-main">
    {{if .}}
    <ul class="list">
      {{range .}}
      <li>
        <a class="list-row" href="/tournaments/{{.ID}}">
          <span class="list-row-title">{{.Title}}</span>
          {{if .Dates}}<span class="muted">{{.Dates}}</span>{{end}}
        </a>
      </li>
      {{end}}
    </ul>
    {{else}}
    <p class="empty">Нет публичных чемпионатов.</p>
    {{end}}
  </main>
</body>
</html>`))

var publicTournamentTemplate = template.Must(template.New("publicTournament").Parse(`<!doctype html>
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
    {{if .RatingURL}}<p class="muted"><a class="text-link" href="{{.RatingURL}}" target="_blank" rel="noreferrer">рейтинг ЧГК</a></p>{{end}}
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
    <p class="empty">В этом чемпионате пока нет игр.</p>
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
	summaries, err := s.loadPublicTournamentSummaries(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := publicListTemplate.Execute(w, summaries); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *server) handleTournamentRouter(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/tournaments/")
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
		s.renderPublicTournamentPage(w, r, id)
		return
	}
	if !isViewerSubPath(parts[1:]) {
		http.NotFound(w, r)
		return
	}
	if err := s.assertTournamentPublic(r.Context(), id); err != nil {
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
			if err := s.db.QueryRowContext(r.Context(), `select game_type from games where id = ? and tournament_id = ?`, gameID, id).Scan(&gameType); err == nil {
				switch gameType {
				case "od":
					s.serveAppHTML(w, r, "static/od.html")
					return
				case "si":
					s.serveAppHTML(w, r, "static/si.html")
					return
				}
			}
		}
	}
	s.serveViewerHTML(w, r)
}

func (s *server) renderPublicTournamentPage(w http.ResponseWriter, r *http.Request, id int64) {
	detail, err := s.loadPublicTournamentDetail(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := publicTournamentTemplate.Execute(w, detail); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *server) assertTournamentPublic(ctx context.Context, tournamentID int64) error {
	var isPublic int
	if err := s.db.QueryRowContext(ctx, `select is_public from tournaments where id = ?`, tournamentID).Scan(&isPublic); err != nil {
		return err
	}
	if isPublic != 1 {
		return sql.ErrNoRows
	}
	return nil
}

// isViewerSubPath validates that a tournament-scoped path under /tournaments/{id}/
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

func (s *server) loadPublicTournamentSummaries(ctx context.Context) ([]publicTournamentSummary, error) {
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
select id, title, coalesce(start_date, ''), coalesce(end_date, '')
from tournaments
where is_public = 1
order by case when start_date is null or start_date = '' then 1 else 0 end,
         start_date,
         id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []publicTournamentSummary
	for rows.Next() {
		var s publicTournamentSummary
		if err := rows.Scan(&s.ID, &s.Title, &s.StartDate, &s.EndDate); err != nil {
			return nil, err
		}
		s.Dates = formatTournamentDates(s.StartDate, s.EndDate)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (s *server) loadPublicTournamentDetail(ctx context.Context, id int64) (publicTournamentDetail, error) {
	if s.db == nil {
		return publicTournamentDetail{}, sql.ErrNoRows
	}
	var (
		title       string
		description string
		startDate   sql.NullString
		endDate     sql.NullString
		ratingID    sql.NullInt64
		isPublic    int
	)
	if err := s.db.QueryRowContext(ctx, `
select title, description, start_date, end_date, rating_id, is_public
from tournaments where id = ?`, id).Scan(&title, &description, &startDate, &endDate, &ratingID, &isPublic); err != nil {
		return publicTournamentDetail{}, err
	}
	if isPublic != 1 {
		return publicTournamentDetail{}, sql.ErrNoRows
	}
	games, err := loadTournamentGames(ctx, s.db, id)
	if err != nil {
		return publicTournamentDetail{}, err
	}
	publicGames := make([]publicTournamentGame, len(games))
	for i, g := range games {
		publicGames[i] = publicTournamentGame{
			ID:    g.ID,
			Code:  g.Code,
			Title: g.Title,
			Type:  g.Type,
			URL:   fmt.Sprintf("/tournaments/%d/game/%d/", id, g.ID),
		}
	}
	detail := publicTournamentDetail{
		ID:          id,
		Title:       title,
		Dates:       formatTournamentDates(startDate.String, endDate.String),
		Description: renderMarkdown(description),
		Games:       publicGames,
	}
	if ratingID.Valid && ratingID.Int64 > 0 {
		detail.RatingURL = fmt.Sprintf("https://rating.chgk.info/tournament/%d", ratingID.Int64)
	}
	return detail, nil
}

func formatTournamentDates(start, end string) string {
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

type tournamentGameRow struct {
	ID    int64
	Code  string
	Title string
	Type  string
}

func loadTournamentGames(ctx context.Context, q dbQueryer, tournamentID int64) ([]tournamentGameRow, error) {
	rows, err := q.QueryContext(ctx, `
select id, code, title, game_type
from games where tournament_id = ?
order by position, id`, tournamentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tournamentGameRow
	for rows.Next() {
		var g tournamentGameRow
		if err := rows.Scan(&g.ID, &g.Code, &g.Title, &g.Type); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
