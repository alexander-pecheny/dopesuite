package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type hostMyFest struct {
	ID        int64
	Title     string
	StartDate string
	EndDate   string
	Dates     string
	IsPublic  bool
}

type hostLandingData struct {
	LoggedIn bool
	Username string
	Fests    []hostMyFest
	Error    string
}

type hostFestDashData struct {
	Fest         hostMyFest
	Description  string
	RatingID     int64
	Games        []publicFestGame
	TeamCount    int
	PlayerCount  int
	IsCreator    bool
	Error        string
	ImportError  string
	ImportNotice string
	RosterError  string
	RosterNotice string
}

type hostFestTeam struct {
	RatingID int64
	Name     string
	City     string
	Players  int
}

type hostFestPlayer struct {
	RatingID int64
	Name     string
	Team     string
}

type hostFestRosterData struct {
	Fest    hostMyFest
	Teams   []hostFestTeam
	Players []hostFestPlayer
}

type hostFestImportData struct {
	Fest     hostMyFest
	RatingID int64
	Error    string
	Notice   string
}

var hostLoggedOutTemplate = template.Must(template.New("hostLogin").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Вход для организаторов · Фест</title>
  <link rel="stylesheet" href="/static/styles.css">
</head>
<body class="public">
  <header class="public-top">
    <h1>Организаторы</h1>
  </header>
  <main class="public-main">
    <p>Чтобы создавать фесты и проводить бои, нужно войти.</p>
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
  <title>Мои фесты · {{.Username}}</title>
  <link rel="stylesheet" href="/static/styles.css">
</head>
<body class="public">
  <header class="public-top">
    <h1>Мои фесты</h1>
    <a class="public-user" href="/profile">{{.Username}}</a>
  </header>
  <main class="public-main">
    {{if .Error}}<p class="empty">{{.Error}}</p>{{end}}
    {{if .Fests}}
    <ul class="list">
      {{range .Fests}}
      <li>
        <a class="list-row" href="/host/fest/{{.ID}}">
          <span class="list-row-title">{{.Title}}{{if not .IsPublic}} · черновик{{end}}</span>
          {{if .Dates}}<span class="muted">{{.Dates}}</span>{{end}}
        </a>
      </li>
      {{end}}
    </ul>
    {{else}}
    <p class="empty">Фестов пока нет.</p>
    {{end}}

    <section class="section">
      <details class="disclosure">
        <summary class="btn">Создать фест</summary>
        <form method="post" action="/host/fest" class="card stack" autocomplete="off">
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
      </details>
    </section>
  </main>
</body>
</html>`))

var profileTemplate = template.Must(template.New("profile").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Профиль</title>
  <link rel="stylesheet" href="/static/styles.css">
</head>
<body class="public">
  <header class="public-top">
    <h1>Профиль</h1>
  </header>
  <main class="public-main">
    <form method="post" action="/profile/logout">
      <button class="btn" type="submit">Разлогиниться</button>
    </form>
  </main>
</body>
</html>`))

var hostFestDashTemplate = template.Must(template.New("hostDash").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Fest.Title}} · ведущий</title>
  <link rel="stylesheet" href="/static/styles.css">
</head>
<body class="public">
  <header class="public-top">
    <a class="public-back" href="/host">←</a>
    <h1>{{.Fest.Title}}</h1>
  </header>
  <main class="public-main">
    {{if .Error}}<p class="empty">{{.Error}}</p>{{end}}
    <form method="post" action="/host/fest/{{.Fest.ID}}" class="card stack" autocomplete="off">
      <label class="field">
        <span>Название</span>
        <input name="title" value="{{.Fest.Title}}" required>
      </label>
      <label class="field">
        <span>Описание (markdown)</span>
        <textarea name="description" rows="6">{{.Description}}</textarea>
      </label>
      <label class="field">
        <span>Дата начала</span>
        <input name="start_date" value="{{.Fest.StartDate}}">
      </label>
      <label class="field">
        <span>Дата окончания</span>
        <input name="end_date" value="{{.Fest.EndDate}}">
      </label>
      <label class="field">
        <span>rating.chgk.info ID</span>
        <input name="rating_id" value="{{if .RatingID}}{{.RatingID}}{{end}}" inputmode="numeric">
      </label>
      <label class="checkbox">
        <input type="checkbox" name="is_public" value="1"{{if .Fest.IsPublic}} checked{{end}}>
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
        <li>
          <a class="list-row" href="/host/fest/{{$.Fest.ID}}/game/{{.ID}}/">
            <span class="list-row-title">{{.Title}}</span>
            <span class="muted">{{.Type}}</span>
          </a>
        </li>
        {{end}}
      </ul>
      {{else}}
      <p class="empty">Игр пока нет.</p>
      {{end}}
    </section>

    <section class="section">
      <h2>Участники</h2>
      {{if .RosterError}}<p class="empty">{{.RosterError}}</p>{{end}}
      {{if .RosterNotice}}<p class="muted">{{.RosterNotice}}</p>{{end}}
      <ul class="list">
        <li>
          <a class="list-row" href="/host/fest/{{.Fest.ID}}/teams">
            <span class="list-row-title">Команды</span>
            <span class="muted">{{.TeamCount}}</span>
          </a>
        </li>
        <li>
          <a class="list-row" href="/host/fest/{{.Fest.ID}}/players">
            <span class="list-row-title">Игроки</span>
            <span class="muted">{{.PlayerCount}}</span>
          </a>
        </li>
        <li>
          <a class="list-row" href="/host/fest/{{.Fest.ID}}/rating/import">
            <span class="list-row-title">Загрузить команды и игроков</span>
            <span class="muted">{{if .RatingID}}rating {{.RatingID}}{{else}}нет rating ID{{end}}</span>
          </a>
        </li>
      </ul>
    </section>

    <section class="section">
      <h2>Импорт схемы из JSON</h2>
      {{if .ImportError}}<p class="empty">{{.ImportError}}</p>{{end}}
      {{if .ImportNotice}}<p class="muted">{{.ImportNotice}}</p>{{end}}
      <div class="cluster">
        <a class="btn" href="/host/fest/{{.Fest.ID}}/import">Открыть импорт схемы</a>
      </div>
    </section>

    {{if .IsCreator}}
    <section class="section">
      <h2>Удаление</h2>
      <form method="post" action="/host/fest/{{.Fest.ID}}/delete" class="card stack" autocomplete="off">
        <p class="muted">Удаление убирает фест со всеми играми, командами и результатами.</p>
        <div class="cluster">
          <button class="btn danger" type="submit">Удалить фест</button>
        </div>
      </form>
    </section>
    {{end}}
  </main>
</body>
</html>`))

var hostFestTeamsTemplate = template.Must(template.New("hostTeams").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Fest.Title}} · команды</title>
  <link rel="stylesheet" href="/static/styles.css">
</head>
<body class="public">
  <header class="public-top">
    <a class="public-back" href="/host/fest/{{.Fest.ID}}">←</a>
    <h1>Команды</h1>
  </header>
  <main class="public-main">
    {{if .Teams}}
    <div class="table-scroll">
      <table class="data-table">
        <thead><tr><th>ID</th><th>Команда</th><th>Город</th><th>Игроков</th></tr></thead>
        <tbody>
          {{range .Teams}}
          <tr><td>{{if .RatingID}}{{.RatingID}}{{end}}</td><td>{{.Name}}</td><td>{{.City}}</td><td>{{.Players}}</td></tr>
          {{end}}
        </tbody>
      </table>
    </div>
    {{else}}
    <p class="empty">Команды пока не загружены.</p>
    {{end}}
  </main>
</body>
</html>`))

var hostFestPlayersTemplate = template.Must(template.New("hostPlayers").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Fest.Title}} · игроки</title>
  <link rel="stylesheet" href="/static/styles.css">
</head>
<body class="public">
  <header class="public-top">
    <a class="public-back" href="/host/fest/{{.Fest.ID}}">←</a>
    <h1>Игроки</h1>
  </header>
  <main class="public-main">
    {{if .Players}}
    <div class="table-scroll">
      <table class="data-table">
        <thead><tr><th>ID</th><th>Игрок</th><th>Команда</th></tr></thead>
        <tbody>
          {{range .Players}}
          <tr><td>{{if .RatingID}}{{.RatingID}}{{end}}</td><td>{{.Name}}</td><td>{{.Team}}</td></tr>
          {{end}}
        </tbody>
      </table>
    </div>
    {{else}}
    <p class="empty">Игроки пока не загружены.</p>
    {{end}}
  </main>
</body>
</html>`))

var hostFestRatingImportTemplate = template.Must(template.New("hostRatingImport").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Fest.Title}} · импорт участников</title>
  <link rel="stylesheet" href="/static/styles.css">
</head>
<body class="public">
  <header class="public-top">
    <a class="public-back" href="/host/fest/{{.Fest.ID}}">←</a>
    <h1>Импорт участников</h1>
  </header>
  <main class="public-main">
    {{if .Error}}<p class="empty">{{.Error}}</p>{{end}}
    {{if .Notice}}<p class="muted">{{.Notice}}</p>{{end}}
    <section class="section">
      {{if .RatingID}}
      <p class="muted">Источник: rating.chgk.info ID {{.RatingID}}</p>
      <form method="post" action="/host/fest/{{.Fest.ID}}/rating/import" class="card stack" autocomplete="off">
        <p class="muted">Импорт заменит списки команд и игроков феста и обновит список команд в играх ЧГК и КСИ.</p>
        <div class="cluster">
          <button class="btn" type="submit">Загрузить команды и игроков</button>
        </div>
      </form>
      {{else}}
      <p class="empty">Сначала сохраните rating.chgk.info ID в свойствах феста.</p>
      {{end}}
    </section>
  </main>
</body>
</html>`))

var hostFestSchemeImportTemplate = template.Must(template.New("hostSchemeImport").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Fest.Title}} · импорт схемы</title>
  <link rel="stylesheet" href="/static/styles.css">
</head>
<body class="public">
  <header class="public-top">
    <a class="public-back" href="/host/fest/{{.Fest.ID}}">←</a>
    <h1>Импорт схемы</h1>
  </header>
  <main class="public-main">
    {{if .Error}}<p class="empty">{{.Error}}</p>{{end}}
    {{if .Notice}}<p class="muted">{{.Notice}}</p>{{end}}
    <form method="post" action="/host/fest/{{.Fest.ID}}/import" class="card stack" autocomplete="off">
      <p class="muted">Импорт пересоздаёт игру феста из JSON-схемы. Существующие игры этого феста будут заменены.</p>
      <label class="field">
        <span>JSON-схема</span>
        <textarea name="scheme" rows="14" placeholder='{"slug":"...","title":"...","gameType":"ek","stages":[...]}'></textarea>
      </label>
      <div class="cluster">
        <button class="btn" type="submit">Импортировать</button>
      </div>
    </form>
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
	fests, err := s.loadHostFests(r.Context(), user.UserID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	username := ""
	if user.Username.Valid {
		username = user.Username.String
	}
	if username == "" {
		username = "Профиль"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = hostLoggedInTemplate.Execute(w, hostLandingData{
		LoggedIn: true,
		Username: username,
		Fests:    fests,
		Error:    errMsg,
	})
}

func (s *server) handleProfilePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/profile" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		if _, ok := s.lookupSession(r); !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = profileTemplate.Execute(w, nil)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *server) handleProfileLogout(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/profile/logout" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !requireSameOriginUnsafe(w, r) {
		return
	}
	s.logoutSession(r)
	clearSessionCookie(w)
	http.Redirect(w, r, "/host", http.StatusSeeOther)
}

// /host/<...> — auth-gated subpaths.
//   - /host/fest              POST: create fest
//   - /host/fest/{id}         GET: dashboard, POST: update
//   - /host/fest/{id}/game/{gid}/...   serves host.html for the EK match grid
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
	if parts[0] != "fest" {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 1 {
		// /host/fest — only POST (create)
		if r.Method != http.MethodPost {
			http.Redirect(w, r, "/host", http.StatusSeeOther)
			return
		}
		s.handleHostCreateFest(w, r, user)
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
			s.renderHostFestDashboard(w, r, id, hostDashMessages{})
		case http.MethodPost:
			s.handleHostUpdateFest(w, r, id)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) == 3 && parts[2] == "teams" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.renderHostFestTeams(w, r, id)
		return
	}
	if len(parts) == 3 && parts[2] == "players" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.renderHostFestPlayers(w, r, id)
		return
	}
	if len(parts) == 3 && parts[2] == "import" {
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			s.renderHostSchemeImportPage(w, r, id, "", "")
		case http.MethodPost:
			s.handleHostImportScheme(w, r, id)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) == 3 && parts[2] == "delete" {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleHostDeleteFest(w, r, id, user.UserID)
		return
	}
	if len(parts) == 4 && parts[2] == "rating" && parts[3] == "import" {
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			s.renderHostRatingImportPage(w, r, id, "", "")
		case http.MethodPost:
			s.handleHostImportRatingRoster(w, r, id)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	// /host/fest/{id}/game/{gid}[/...] → serve host.html / od.html / si.html.
	if !isHostGameSubPath(parts[2:]) {
		http.NotFound(w, r)
		return
	}
	s.serveHostGamePage(w, r, id, parts[2:])
}

func (s *server) serveHostGamePage(w http.ResponseWriter, r *http.Request, festID int64, parts []string) {
	gameID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || gameID <= 0 {
		http.NotFound(w, r)
		return
	}
	var gameType string
	if err := s.db.QueryRowContext(r.Context(), `select game_type from games where id = ? and fest_id = ?`, gameID, festID).Scan(&gameType); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	switch gameType {
	case "od":
		s.serveAppHTML(w, r, "static/od.html")
	case "si", "ksi":
		s.serveAppHTML(w, r, "static/si.html")
	default:
		s.serveHostHTML(w, r)
	}
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

func (s *server) handleHostCreateFest(w http.ResponseWriter, r *http.Request, user sessionUser) {
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
	festID, err := insertReturningID(r.Context(), tx, `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, start_date, end_date, is_public)
values(?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?)`,
		slug, title, description, ratingID, user.UserID, now, now,
		nullableString(startDate), nullableString(endDate), boolToInt(isPublic))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := tx.ExecContext(r.Context(), `
insert into fest_organizers(fest_id, user_id, added_at)
values(?, ?, ?)`, festID, user.UserID, now); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%d", festID), http.StatusSeeOther)
}

type hostDashMessages struct {
	FormError    string
	ImportError  string
	ImportNotice string
	RosterError  string
	RosterNotice string
}

func (s *server) handleHostUpdateFest(w http.ResponseWriter, r *http.Request, festID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(r.Form.Get("title"))
	if title == "" {
		s.renderHostFestDashboard(w, r, festID, hostDashMessages{FormError: "Название обязательно."})
		return
	}
	description := r.Form.Get("description")
	startDate := strings.TrimSpace(r.Form.Get("start_date"))
	endDate := strings.TrimSpace(r.Form.Get("end_date"))
	ratingID := parseOptionalInt64(r.Form.Get("rating_id"))
	isPublic := r.Form.Get("is_public") == "1"

	if _, err := s.db.ExecContext(r.Context(), `
update fests
set title = ?, description = ?, rating_id = ?, start_date = ?, end_date = ?, is_public = ?, updated_at = ?
where id = ?`,
		title, description, ratingID,
		nullableString(startDate), nullableString(endDate), boolToInt(isPublic),
		utcNow(), festID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%d", festID), http.StatusSeeOther)
}

func (s *server) handleHostImportScheme(w http.ResponseWriter, r *http.Request, festID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	raw := strings.TrimSpace(r.Form.Get("scheme"))
	if raw == "" {
		s.renderHostSchemeImportPage(w, r, festID, "Вставьте JSON схемы.", "")
		return
	}
	var scheme festScheme
	if err := json.Unmarshal([]byte(raw), &scheme); err != nil {
		s.renderHostSchemeImportPage(w, r, festID, "Не удалось разобрать JSON: "+err.Error(), "")
		return
	}
	if err := s.importSchemeIntoFest(r.Context(), festID, scheme); err != nil {
		s.renderHostSchemeImportPage(w, r, festID, err.Error(), "")
		return
	}
	s.renderHostSchemeImportPage(w, r, festID, "", "Импорт выполнен.")
}

func (s *server) handleHostImportRatingRoster(w http.ResponseWriter, r *http.Request, festID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	ratingID, err := s.loadFestRatingID(r.Context(), festID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ratingID <= 0 {
		s.renderHostRatingImportPage(w, r, festID, "Сначала сохраните rating.chgk.info ID в свойствах феста.", "")
		return
	}
	result, err := s.fetchAndImportRatingRoster(r.Context(), festID, ratingID)
	if err != nil {
		s.renderHostRatingImportPage(w, r, festID, err.Error(), "")
		return
	}
	msg := fmt.Sprintf("Загружено команд: %d, игроков: %d. Обновлено игр ЧГК: %d, КСИ: %d.", result.TeamCount, result.PlayerCount, result.ODGameCount, result.KSIGameCount)
	s.renderHostRatingImportPage(w, r, festID, "", msg)
}

func (s *server) handleHostDeleteFest(w http.ResponseWriter, r *http.Request, festID, userID int64) {
	creator, err := s.isFestCreator(r.Context(), festID, userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !creator {
		http.Error(w, "only fest creator can delete fest", http.StatusForbidden)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.db.ExecContext(r.Context(), `delete from fests where id = ? and created_by = ?`, festID, userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		http.NotFound(w, r)
		return
	}
	if s.festID == festID {
		s.festID = 0
		s.activeGameID = 0
		s.activeMatchCode = ""
	}
	http.Redirect(w, r, "/host", http.StatusSeeOther)
}

func (s *server) renderHostFestTeams(w http.ResponseWriter, r *http.Request, festID int64) {
	fest, err := s.loadHostFestHeader(r.Context(), festID)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	teams, err := s.loadHostFestTeams(r.Context(), festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = hostFestTeamsTemplate.Execute(w, hostFestRosterData{Fest: fest, Teams: teams})
}

func (s *server) renderHostFestPlayers(w http.ResponseWriter, r *http.Request, festID int64) {
	fest, err := s.loadHostFestHeader(r.Context(), festID)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	players, err := s.loadHostFestPlayers(r.Context(), festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = hostFestPlayersTemplate.Execute(w, hostFestRosterData{Fest: fest, Players: players})
}

func (s *server) renderHostRatingImportPage(w http.ResponseWriter, r *http.Request, festID int64, errMsg, notice string) {
	fest, err := s.loadHostFestHeader(r.Context(), festID)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ratingID, err := s.loadFestRatingID(r.Context(), festID)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = hostFestRatingImportTemplate.Execute(w, hostFestImportData{
		Fest:     fest,
		RatingID: ratingID,
		Error:    errMsg,
		Notice:   notice,
	})
}

func (s *server) renderHostSchemeImportPage(w http.ResponseWriter, r *http.Request, festID int64, errMsg, notice string) {
	fest, err := s.loadHostFestHeader(r.Context(), festID)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = hostFestSchemeImportTemplate.Execute(w, hostFestImportData{
		Fest:   fest,
		Error:  errMsg,
		Notice: notice,
	})
}

func (s *server) renderHostFestDashboard(w http.ResponseWriter, r *http.Request, festID int64, msgs hostDashMessages) {
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
from fests where id = ?`, festID).Scan(&title, &description, &startDate, &endDate, &ratingID, &isPublic); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	games, err := loadFestGames(r.Context(), s.db, festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	hostGames := make([]publicFestGame, len(games))
	for i, g := range games {
		hostGames[i] = publicFestGame{
			ID:    g.ID,
			Code:  g.Code,
			Title: g.Title,
			Type:  gameTypeLabel(g.Type),
			URL:   fmt.Sprintf("/host/fest/%d/game/%d/", festID, g.ID),
		}
	}
	teamCount, playerCount, err := s.loadHostFestRosterCounts(r.Context(), festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	isCreator := false
	if user, ok := s.lookupSession(r); ok {
		isCreator, err = s.isFestCreator(r.Context(), festID, user.UserID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	data := hostFestDashData{
		Fest: hostMyFest{
			ID:        festID,
			Title:     title,
			StartDate: startDate.String,
			EndDate:   endDate.String,
			Dates:     formatFestDates(startDate.String, endDate.String),
			IsPublic:  isPublic == 1,
		},
		Description:  description,
		Games:        hostGames,
		TeamCount:    teamCount,
		PlayerCount:  playerCount,
		IsCreator:    isCreator,
		Error:        msgs.FormError,
		ImportError:  msgs.ImportError,
		ImportNotice: msgs.ImportNotice,
		RosterError:  msgs.RosterError,
		RosterNotice: msgs.RosterNotice,
	}
	if ratingID.Valid {
		data.RatingID = ratingID.Int64
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = hostFestDashTemplate.Execute(w, data)
}

func (s *server) loadHostFestHeader(ctx context.Context, festID int64) (hostMyFest, error) {
	var t hostMyFest
	var pub int
	if err := s.db.QueryRowContext(ctx, `
select id, title, coalesce(start_date, ''), coalesce(end_date, ''), is_public
from fests where id = ?`, festID).Scan(&t.ID, &t.Title, &t.StartDate, &t.EndDate, &pub); err != nil {
		return hostMyFest{}, err
	}
	t.IsPublic = pub == 1
	t.Dates = formatFestDates(t.StartDate, t.EndDate)
	return t, nil
}

func (s *server) loadHostFestRosterCounts(ctx context.Context, festID int64) (int, int, error) {
	var teamCount, playerCount int
	if err := s.db.QueryRowContext(ctx, `select count(*) from fest_teams where fest_id = ?`, festID).Scan(&teamCount); err != nil {
		return 0, 0, err
	}
	if err := s.db.QueryRowContext(ctx, `select count(*) from fest_players where fest_id = ?`, festID).Scan(&playerCount); err != nil {
		return 0, 0, err
	}
	return teamCount, playerCount, nil
}

func (s *server) loadHostFestTeams(ctx context.Context, festID int64) ([]hostFestTeam, error) {
	teamRows, err := s.db.QueryContext(ctx, `
select coalesce(tt.rating_id, 0), tt.name, tt.city, count(ttp.player_id)
from fest_teams tt
left join fest_team_players ttp on ttp.team_id = tt.id
where tt.fest_id = ?
group by tt.id
order by tt.position, tt.id`, festID)
	if err != nil {
		return nil, err
	}
	defer teamRows.Close()
	var teams []hostFestTeam
	for teamRows.Next() {
		var team hostFestTeam
		if err := teamRows.Scan(&team.RatingID, &team.Name, &team.City, &team.Players); err != nil {
			return nil, err
		}
		teams = append(teams, team)
	}
	if err := teamRows.Err(); err != nil {
		return nil, err
	}
	return teams, nil
}

func (s *server) loadHostFestPlayers(ctx context.Context, festID int64) ([]hostFestPlayer, error) {
	playerRows, err := s.db.QueryContext(ctx, `
select coalesce(p.rating_id, 0), p.first_name, p.last_name, tt.name
from fest_team_players ttp
join fest_players p on p.id = ttp.player_id
join fest_teams tt on tt.id = ttp.team_id
where tt.fest_id = ?
order by tt.position, tt.id, ttp.roster_order, p.id`, festID)
	if err != nil {
		return nil, err
	}
	defer playerRows.Close()
	var players []hostFestPlayer
	for playerRows.Next() {
		var firstName, lastName, teamName string
		var ratingID int64
		if err := playerRows.Scan(&ratingID, &firstName, &lastName, &teamName); err != nil {
			return nil, err
		}
		players = append(players, hostFestPlayer{
			RatingID: ratingID,
			Name:     joinPlayerName(firstName, lastName),
			Team:     teamName,
		})
	}
	return players, playerRows.Err()
}

func (s *server) loadFestRatingID(ctx context.Context, festID int64) (int64, error) {
	var ratingID sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `select rating_id from fests where id = ?`, festID).Scan(&ratingID); err != nil {
		return 0, err
	}
	if !ratingID.Valid {
		return 0, nil
	}
	return ratingID.Int64, nil
}

func (s *server) loadHostFests(ctx context.Context, userID int64) ([]hostMyFest, error) {
	rows, err := s.db.QueryContext(ctx, `
select t.id, t.title, coalesce(t.start_date, ''), coalesce(t.end_date, ''), t.is_public
from fests t
join fest_organizers o on o.fest_id = t.id
where o.user_id = ?
order by case when t.start_date is null or t.start_date = '' then 1 else 0 end,
         t.start_date desc,
         t.id desc`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []hostMyFest
	for rows.Next() {
		var t hostMyFest
		var pub int
		if err := rows.Scan(&t.ID, &t.Title, &t.StartDate, &t.EndDate, &pub); err != nil {
			return nil, err
		}
		t.IsPublic = pub == 1
		t.Dates = formatFestDates(t.StartDate, t.EndDate)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *server) isOrganizer(ctx context.Context, festID, userID int64) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
select count(*) from fest_organizers where fest_id = ? and user_id = ?`,
		festID, userID).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *server) isFestCreator(ctx context.Context, festID, userID int64) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
select count(*) from fests where id = ? and created_by = ?`, festID, userID).Scan(&n)
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
		stem = "fest"
	}
	return fmt.Sprintf("%s-%d", stem, now.Unix())
}
