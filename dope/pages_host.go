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
	"time"
)

type hostMyTournament struct {
	ID        int64
	Title     string
	StartDate string
	EndDate   string
	Dates     string
	IsPublic  bool
}

type hostLandingData struct {
	LoggedIn    bool
	Username    string
	Tournaments []hostMyTournament
	Error       string
}

type hostTournamentDashData struct {
	Tournament  hostMyTournament
	Description string
	RatingID    int64
	Games       []publicTournamentGame
	Error       string
}

var hostLoggedOutTemplate = template.Must(template.New("hostLogin").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Вход для организаторов · Чемпионат</title>
  <link rel="stylesheet" href="/static/styles.css">
</head>
<body class="public">
  <header class="public-top">
    <h1>Организаторы</h1>
  </header>
  <main class="public-main">
    <p>Чтобы создавать чемпионаты и проводить бои, нужно войти.</p>
    <ul class="list">
      <li><a class="list-row" href="/login"><span class="list-row-title">Вход</span></a></li>
      <li><a class="list-row" href="/register"><span class="list-row-title">Регистрация по приглашению</span></a></li>
    </ul>
  </main>
</body>
</html>`))

var hostLoggedInTemplate = template.Must(template.New("hostHome").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Мои чемпионаты · {{.Username}}</title>
  <link rel="stylesheet" href="/static/styles.css">
</head>
<body class="public">
  <header class="public-top">
    <h1>Мои чемпионаты</h1>
    <span class="muted">{{.Username}}</span>
  </header>
  <main class="public-main">
    {{if .Error}}<p class="empty">{{.Error}}</p>{{end}}
    {{if .Tournaments}}
    <ul class="list">
      {{range .Tournaments}}
      <li>
        <a class="list-row" href="/host/tournament/{{.ID}}">
          <span class="list-row-title">{{.Title}}{{if not .IsPublic}} · черновик{{end}}</span>
          {{if .Dates}}<span class="muted">{{.Dates}}</span>{{end}}
        </a>
      </li>
      {{end}}
    </ul>
    {{else}}
    <p class="empty">Чемпионатов пока нет.</p>
    {{end}}

    <section class="section">
      <h2>Создать чемпионат</h2>
      <form method="post" action="/host/tournament" class="card stack" autocomplete="off">
        <label class="field">
          <span>Название</span>
          <input name="title" required>
        </label>
        <label class="field">
          <span>Описание (markdown)</span>
          <textarea name="description" rows="4"></textarea>
        </label>
        <label class="field">
          <span>Дата начала (YYYY-MM-DD)</span>
          <input name="start_date" placeholder="2026-05-15">
        </label>
        <label class="field">
          <span>Дата окончания</span>
          <input name="end_date" placeholder="2026-05-17">
        </label>
        <label class="field">
          <span>rating.chgk.info ID (опционально)</span>
          <input name="rating_id" inputmode="numeric">
        </label>
        <label class="checkbox">
          <input type="checkbox" name="is_public" value="1">
          <span>Публичный</span>
        </label>
        <div class="cluster">
          <button class="btn" type="submit">Создать</button>
        </div>
      </form>
    </section>
  </main>
</body>
</html>`))

var hostTournamentDashTemplate = template.Must(template.New("hostDash").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Tournament.Title}} · ведущий</title>
  <link rel="stylesheet" href="/static/styles.css">
</head>
<body class="public">
  <header class="public-top">
    <a class="public-back" href="/host">←</a>
    <h1>{{.Tournament.Title}}</h1>
  </header>
  <main class="public-main">
    {{if .Error}}<p class="empty">{{.Error}}</p>{{end}}
    <form method="post" action="/host/tournament/{{.Tournament.ID}}" class="card stack" autocomplete="off">
      <label class="field">
        <span>Название</span>
        <input name="title" value="{{.Tournament.Title}}" required>
      </label>
      <label class="field">
        <span>Описание (markdown)</span>
        <textarea name="description" rows="6">{{.Description}}</textarea>
      </label>
      <label class="field">
        <span>Дата начала</span>
        <input name="start_date" value="{{.Tournament.StartDate}}">
      </label>
      <label class="field">
        <span>Дата окончания</span>
        <input name="end_date" value="{{.Tournament.EndDate}}">
      </label>
      <label class="field">
        <span>rating.chgk.info ID</span>
        <input name="rating_id" value="{{if .RatingID}}{{.RatingID}}{{end}}" inputmode="numeric">
      </label>
      <label class="checkbox">
        <input type="checkbox" name="is_public" value="1"{{if .Tournament.IsPublic}} checked{{end}}>
        <span>Публичный</span>
      </label>
      <div class="cluster">
        <button class="btn" type="submit">Сохранить</button>
      </div>
    </form>

    <section class="section">
      <h2>Игры</h2>
      {{if .Games}}
      <ul class="list">
        {{range .Games}}
        <li class="list-row">
          <a class="text-link" href="/host/tournament/{{$.Tournament.ID}}/game/{{.ID}}/">{{.Title}}</a>
          <span class="muted">{{.Type}}</span>
        </li>
        {{end}}
      </ul>
      {{else}}
      <p class="empty">Игр пока нет.</p>
      {{end}}
      <p class="muted">Создание игры с импортом сетки появится позже. Пока новую игру можно завести через POST /api/import.</p>
    </section>
  </main>
</body>
</html>`))

// /host — landing page.
func (s *server) handleHostLanding(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/host" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		s.renderHostLanding(w, r, "")
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *server) renderHostLanding(w http.ResponseWriter, r *http.Request, errMsg string) {
	user, ok := s.lookupSession(r)
	if !ok {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = hostLoggedOutTemplate.Execute(w, nil)
		return
	}
	tournaments, err := s.loadHostTournaments(r.Context(), user.UserID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	username := ""
	if user.Username.Valid {
		username = user.Username.String
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = hostLoggedInTemplate.Execute(w, hostLandingData{
		LoggedIn:    true,
		Username:    username,
		Tournaments: tournaments,
		Error:       errMsg,
	})
}

// /host/<...> — auth-gated subpaths.
//   - /host/tournament              POST: create tournament
//   - /host/tournament/{id}         GET: dashboard, POST: update
//   - /host/tournament/{id}/game/{gid}/...   serves host.html for the EK match grid
func (s *server) handleHostRouter(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/host/")
	if rest == "" || rest == "/" {
		http.Redirect(w, r, "/host", http.StatusSeeOther)
		return
	}
	user, ok := s.lookupSession(r)
	if !ok {
		http.Redirect(w, r, "/host", http.StatusSeeOther)
		return
	}
	parts := strings.Split(strings.TrimSuffix(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	if parts[0] != "tournament" {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 1 {
		// /host/tournament — only POST (create)
		if r.Method != http.MethodPost {
			http.Redirect(w, r, "/host", http.StatusSeeOther)
			return
		}
		s.handleHostCreateTournament(w, r, user)
		return
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	allowed, err := s.isOrganizer(r.Context(), id, user.UserID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !allowed {
		http.Redirect(w, r, "/host", http.StatusSeeOther)
		return
	}
	if len(parts) == 2 {
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			s.renderHostTournamentDashboard(w, r, id, "")
		case http.MethodPost:
			s.handleHostUpdateTournament(w, r, id)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	// /host/tournament/{id}/game/{gid}[/...] → serve host.html (EK grid).
	if !isHostGameSubPath(parts[2:]) {
		http.NotFound(w, r)
		return
	}
	s.serveHostHTML(w, r)
}

func isHostGameSubPath(parts []string) bool {
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

func (s *server) handleHostCreateTournament(w http.ResponseWriter, r *http.Request, user sessionUser) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(r.Form.Get("title"))
	if title == "" {
		s.renderHostLanding(w, r, "Название обязательно.")
		return
	}
	description := r.Form.Get("description")
	startDate := strings.TrimSpace(r.Form.Get("start_date"))
	endDate := strings.TrimSpace(r.Form.Get("end_date"))
	ratingID := parseOptionalInt64(r.Form.Get("rating_id"))
	isPublic := r.Form.Get("is_public") == "1"

	now := utcNow()
	slug := generateSlug(title, time.Now())
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	tournamentID, err := insertReturningID(r.Context(), tx, `
insert into tournaments(slug, title, description, rating_id, created_by, revision, created_at, updated_at, start_date, end_date, is_public)
values(?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?)`,
		slug, title, description, ratingID, user.UserID, now, now,
		nullableString(startDate), nullableString(endDate), boolToInt(isPublic))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := tx.ExecContext(r.Context(), `
insert into tournament_organizers(tournament_id, user_id, added_at)
values(?, ?, ?)`, tournamentID, user.UserID, now); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/host/tournament/%d", tournamentID), http.StatusSeeOther)
}

func (s *server) handleHostUpdateTournament(w http.ResponseWriter, r *http.Request, tournamentID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(r.Form.Get("title"))
	if title == "" {
		s.renderHostTournamentDashboard(w, r, tournamentID, "Название обязательно.")
		return
	}
	description := r.Form.Get("description")
	startDate := strings.TrimSpace(r.Form.Get("start_date"))
	endDate := strings.TrimSpace(r.Form.Get("end_date"))
	ratingID := parseOptionalInt64(r.Form.Get("rating_id"))
	isPublic := r.Form.Get("is_public") == "1"

	if _, err := s.db.ExecContext(r.Context(), `
update tournaments
set title = ?, description = ?, rating_id = ?, start_date = ?, end_date = ?, is_public = ?, updated_at = ?
where id = ?`,
		title, description, ratingID,
		nullableString(startDate), nullableString(endDate), boolToInt(isPublic),
		utcNow(), tournamentID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/host/tournament/%d", tournamentID), http.StatusSeeOther)
}

func (s *server) renderHostTournamentDashboard(w http.ResponseWriter, r *http.Request, tournamentID int64, errMsg string) {
	var (
		title       string
		description string
		startDate   sql.NullString
		endDate     sql.NullString
		ratingID    sql.NullInt64
		isPublic    int
	)
	if err := s.db.QueryRowContext(r.Context(), `
select title, description, start_date, end_date, rating_id, is_public
from tournaments where id = ?`, tournamentID).Scan(&title, &description, &startDate, &endDate, &ratingID, &isPublic); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	games, err := loadTournamentGames(r.Context(), s.db, tournamentID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	hostGames := make([]publicTournamentGame, len(games))
	for i, g := range games {
		hostGames[i] = publicTournamentGame{
			ID:    g.ID,
			Code:  g.Code,
			Title: g.Title,
			Type:  g.Type,
			URL:   fmt.Sprintf("/host/tournament/%d/game/%d/", tournamentID, g.ID),
		}
	}
	data := hostTournamentDashData{
		Tournament: hostMyTournament{
			ID:        tournamentID,
			Title:     title,
			StartDate: startDate.String,
			EndDate:   endDate.String,
			Dates:     formatTournamentDates(startDate.String, endDate.String),
			IsPublic:  isPublic == 1,
		},
		Description: description,
		Games:       hostGames,
		Error:       errMsg,
	}
	if ratingID.Valid {
		data.RatingID = ratingID.Int64
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = hostTournamentDashTemplate.Execute(w, data)
}

func (s *server) loadHostTournaments(ctx context.Context, userID int64) ([]hostMyTournament, error) {
	rows, err := s.db.QueryContext(ctx, `
select t.id, t.title, coalesce(t.start_date, ''), coalesce(t.end_date, ''), t.is_public
from tournaments t
join tournament_organizers o on o.tournament_id = t.id
where o.user_id = ?
order by case when t.start_date is null or t.start_date = '' then 1 else 0 end,
         t.start_date desc,
         t.id desc`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []hostMyTournament
	for rows.Next() {
		var t hostMyTournament
		var pub int
		if err := rows.Scan(&t.ID, &t.Title, &t.StartDate, &t.EndDate, &pub); err != nil {
			return nil, err
		}
		t.IsPublic = pub == 1
		t.Dates = formatTournamentDates(t.StartDate, t.EndDate)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *server) isOrganizer(ctx context.Context, tournamentID, userID int64) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
select count(*) from tournament_organizers where tournament_id = ? and user_id = ?`,
		tournamentID, userID).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func parseOptionalInt64(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil
	}
	return v
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func generateSlug(title string, now time.Time) string {
	slug := strings.ToLower(strings.TrimSpace(title))
	out := make([]rune, 0, len(slug))
	for _, r := range slug {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, r)
		case r == ' ' || r == '-' || r == '_':
			if len(out) > 0 && out[len(out)-1] != '-' {
				out = append(out, '-')
			}
		}
	}
	stem := strings.Trim(string(out), "-")
	if stem == "" {
		stem = "tournament"
	}
	return fmt.Sprintf("%s-%d", stem, now.Unix())
}
