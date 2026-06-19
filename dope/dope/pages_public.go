package dopeserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"dope/dope/gameexport"
	"dope/dope/games"
	"dope/dope/hostpages"
	"dope/dope/markdown"
	"dope/dope/session"
	"dope/dope/store"
	"dope/dope/util"
)

type publicFestSummary struct {
	ID          int64
	Slug        string
	Title       string
	StartDate   string
	EndDate     string
	Dates       string
	Description string
}

func (s publicFestSummary) Ref() string {
	if s.Slug != "" {
		return s.Slug
	}
	return fmt.Sprintf("%d", s.ID)
}

type publicFestDetail struct {
	ID          int64
	Slug        string
	Title       string
	Dates       string
	Description template.HTML
	Games       []hostpages.PublicFestGame
}

func (d publicFestDetail) Ref() string {
	if d.Slug != "" {
		return d.Slug
	}
	return fmt.Sprintf("%d", d.ID)
}

var publicListTemplate = template.Must(template.New("publicList").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Фесты</title>
  <link rel="preload" href="/static/fonts/noto-sans-400.woff2" as="font" type="font/woff2" crossorigin>
  <link rel="stylesheet" href="/static/styles.css">
  <script src="/static/menu.js"></script>
</head>
<body class="public" data-jump-label="Режим ведущего" data-jump-href="/host" data-jump-title="Перейти в режим ведущего">
  <header class="public-top">
    <h1>Фесты</h1>
  </header>
  <main class="public-main">
    {{if .}}
    <ul class="list">
      {{range .}}
      <li>
        <a class="list-row" href="/fest/{{.Ref}}">
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
  <link rel="preload" href="/static/fonts/noto-sans-400.woff2" as="font" type="font/woff2" crossorigin>
  <link rel="stylesheet" href="/static/styles.css">
  <script src="/static/menu.js"></script>
</head>
<body class="public" data-jump-label="Режим ведущего" data-jump-href="/host/fest/{{.Ref}}" data-jump-title="Открыть в режиме ведущего">
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
            {{if .Slug}}<span class="muted">{{.Slug}}</span>{{end}}
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
	// A trailing /static segment forces the static snapshot for this viewer page
	// (the always-on, edge-cacheable handle), independent of load mode.
	forceStatic := false
	if len(parts) > 1 && parts[len(parts)-1] == "static" {
		forceStatic = true
		parts = parts[:len(parts)-1]
	}
	id, err := store.ResolveFestID(r.Context(), s.eng.DB, parts[0])
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if id <= 0 {
		http.NotFound(w, r)
		return
	}
	// /fest/{festRef}/game/{gameRef}.xlsx — XLSX archive download. Read-gated by
	// handleScopedGameExport (authorizeFestRead), so hosts of non-public fests can
	// download too; it deliberately skips the assertFestPublic check below.
	if len(parts) == 3 && parts[1] == "game" && strings.HasSuffix(parts[2], ".xlsx") {
		gameRef := strings.TrimSuffix(parts[2], ".xlsx")
		gameID, err := resolveGameID(r.Context(), s.eng.DB, id, gameRef)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if gameID <= 0 {
			http.NotFound(w, r)
			return
		}
		gameexport.HandleScopedGameExport(s, w, r, id, gameID)
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
		gameID, err := resolveGameID(r.Context(), s.eng.DB, id, parts[2])
		if err == nil && gameID > 0 {
			var gameType string
			if err := s.eng.DB.QueryRowContext(r.Context(), `select game_type from games where id = ? and fest_id = ?`, gameID, id).Scan(&gameType); err == nil {
				scope := festScope{FestID: id, GameID: gameID}
				route := parseHostInitRoute(parts[1:], scope)
				if games.IsChGK(gameType) {
					// OD/SI viewers always render the whole game regardless of
					// sub-route, so collapse to one snapshot cache key.
					route = hostInitRoute{Mode: "grid", FestID: id, GameID: gameID}
				}
				// Serve the static snapshot when forced (/static) or, under load,
				// for cookie-less viewers. Cookie-bearing requests fall through to
				// the live path so editors keep working — but only up to a small
				// concurrency budget, so a flood of forged session cookies can't
				// pierce the shield.
				serveStatic := forceStatic
				if !serveStatic && s.eng.StaticMode.Load() {
					if session.HasCookie(r) {
						if s.eng.LiveFallthrough.Add(1) > liveFallthroughCap {
							serveStatic = true
						}
						defer s.eng.LiveFallthrough.Add(-1)
					} else {
						serveStatic = true
					}
				}
				if serveStatic {
					s.serveStaticSnapshot(w, r, route)
					return
				}
				switch gameType {
				case "od":
					s.serveGameHTMLWithInit(w, r, "static/od.html", scope)
					return
				case "si", "ksi":
					s.serveGameHTMLWithInit(w, r, "static/si.html", scope)
					return
				default:
					s.serveViewerHTMLWithInit(w, r, scope, parts[1:])
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
	if err := s.eng.DB.QueryRowContext(ctx, `select is_public from fests where id = ?`, festID).Scan(&isPublic); err != nil {
		return err
	}
	if isPublic != 1 {
		return sql.ErrNoRows
	}
	return nil
}

// isViewerSubPath validates that a fest-scoped path under /fest/{id}/
// is one of the recognised viewer routes (/game/{gid}/...). The game segment
// can be a numeric id or a slug.
func isViewerSubPath(parts []string) bool {
	if len(parts) < 2 {
		return false
	}
	if parts[0] != "game" || parts[1] == "" {
		return false
	}
	if len(parts) == 2 {
		return true
	}
	switch parts[2] {
	case "venues", "stats":
		return len(parts) == 3
	case "matches", "stage":
		return len(parts) == 4 && parts[3] != ""
	}
	return false
}

func (s *server) loadPublicFestSummaries(ctx context.Context) ([]publicFestSummary, error) {
	if s.eng.DB == nil {
		return nil, nil
	}
	rows, err := s.eng.DB.QueryContext(ctx, `
select id, coalesce(slug, ''), title, coalesce(start_date, ''), coalesce(end_date, '')
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
		if err := rows.Scan(&s.ID, &s.Slug, &s.Title, &s.StartDate, &s.EndDate); err != nil {
			return nil, err
		}
		s.Dates = util.FormatFestDates(s.StartDate, s.EndDate)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (s *server) loadPublicFestDetail(ctx context.Context, id int64) (publicFestDetail, error) {
	if s.eng.DB == nil {
		return publicFestDetail{}, sql.ErrNoRows
	}
	var (
		slug        string
		title       string
		description string
		startDate   sql.NullString
		endDate     sql.NullString
		isPublic    int
	)
	if err := s.eng.DB.QueryRowContext(ctx, `
select coalesce(slug, ''), title, description, start_date, end_date, is_public
from fests where id = ?`, id).Scan(&slug, &title, &description, &startDate, &endDate, &isPublic); err != nil {
		return publicFestDetail{}, err
	}
	if isPublic != 1 {
		return publicFestDetail{}, sql.ErrNoRows
	}
	gameRows, err := hostpages.LoadFestGames(ctx, s.eng.DB, id)
	if err != nil {
		return publicFestDetail{}, err
	}
	festRef := slug
	if festRef == "" {
		festRef = fmt.Sprintf("%d", id)
	}
	publicGames := make([]hostpages.PublicFestGame, len(gameRows))
	for i, g := range gameRows {
		publicGames[i] = hostpages.PublicFestGame{
			ID:    g.ID,
			Slug:  g.Slug,
			Code:  g.Code,
			Title: g.Title,
			Type:  games.Label(g.Type),
			URL:   fmt.Sprintf("/fest/%s/game/%s/", festRef, g.Ref()),
		}
	}
	detail := publicFestDetail{
		ID:          id,
		Slug:        slug,
		Title:       title,
		Dates:       util.FormatFestDates(startDate.String, endDate.String),
		Description: markdown.Render(description),
		Games:       publicGames,
	}
	return detail, nil
}
