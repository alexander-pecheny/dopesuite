package main

import (
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type hostMyFest struct {
	ID        int64
	Slug      string
	Title     string
	StartDate string
	EndDate   string
	Dates     string
	IsPublic  bool
}

// Ref returns the fest slug if set, otherwise the stringified id. Use this
// when building URLs so users see /host/fest/my-fest in preference to
// /host/fest/123.
func (h hostMyFest) Ref() string {
	if h.Slug != "" {
		return h.Slug
	}
	return fmt.Sprintf("%d", h.ID)
}

type hostLandingData struct {
	LoggedIn bool
	Username string
	Fests    []hostMyFest
	Error    string
}

var hostLoggedOutTemplate = template.Must(template.New("hostLogin").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Вход для организаторов · Фест</title>
  <link rel="preload" href="/static/fonts/noto-sans-400.woff2" as="font" type="font/woff2" crossorigin>
  <link rel="stylesheet" href="/static/styles.css">
  <script src="/static/menu.js"></script>
</head>
<body class="public" data-jump-label="Страница зрителя" data-jump-href="/" data-jump-title="Открыть зрительскую страницу">
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
  <link rel="preload" href="/static/fonts/noto-sans-400.woff2" as="font" type="font/woff2" crossorigin>
  <link rel="stylesheet" href="/static/styles.css">
  <script src="/static/menu.js"></script>
</head>
<body class="public" data-jump-label="Страница зрителя" data-jump-href="/" data-jump-title="Открыть зрительскую страницу">
  <header class="public-top">
    <h1>Мои фесты</h1>
  </header>
  <main class="public-main">
    {{if .Error}}<p class="empty">{{.Error}}</p>{{end}}
    {{if .Fests}}
    <ul class="list">
      {{range .Fests}}
      <li>
        <a class="list-row" href="/host/fest/{{.Ref}}">
          <span class="list-row-title">{{.Title}}{{if not .IsPublic}} · непубличный{{end}}</span>
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

type profileData struct {
	HasPassword bool
}

var profileTemplate = template.Must(template.New("profile").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Профиль</title>
  <link rel="preload" href="/static/fonts/noto-sans-400.woff2" as="font" type="font/woff2" crossorigin>
  <link rel="stylesheet" href="/static/styles.css">
  <script src="/static/menu.js"></script>
  <script defer src="/static/profile.js"></script>
</head>
<body class="public">
  <header class="public-top">
    <h1>Профиль</h1>
  </header>
  <main class="public-main">
    <section class="auth-step">
      <p class="auth-hint">{{if .HasPassword}}Сменить пароль{{else}}Установить пароль{{end}}</p>
      <form id="passwordForm" class="auth-form auth-form-stack" autocomplete="off" data-has-password="{{if .HasPassword}}1{{else}}0{{end}}">
        {{if .HasPassword}}
        <input class="input" id="currentPassword" name="current_password" type="password" placeholder="Текущий пароль" autocomplete="current-password" required>
        {{end}}
        <input class="input" id="newPassword" name="new_password" type="password" placeholder="Новый пароль" autocomplete="new-password" minlength="8" required>
        <input class="input" id="confirmPassword" name="confirm_password" type="password" placeholder="Повторите новый пароль" autocomplete="new-password" minlength="8" required>
        <button class="btn" type="submit">{{if .HasPassword}}Сменить пароль{{else}}Установить пароль{{end}}</button>
      </form>
      <pre id="passwordMessage" class="import-message"></pre>
    </section>
    <form method="post" action="/profile/logout">
      <button class="btn" type="submit">Разлогиниться</button>
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
		user, ok := s.lookupSession(r)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		var hash sql.NullString
		if err := s.db.QueryRowContext(r.Context(),
			`select password_hash from users where id = ?`, user.UserID).Scan(&hash); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = profileTemplate.Execute(w, profileData{HasPassword: hash.Valid && hash.String != ""})
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
	// SameSite=Lax on the session cookie is the primary CSRF defense, but
	// also reject cross-origin form submits explicitly so a single browser
	// quirk cannot escalate into delete-fest / assign-host from another tab.
	if !requireSameOriginUnsafe(w, r) {
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
	id, err := resolveFestID(r.Context(), s.db, parts[1])
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
	role, err := s.festUserRole(r.Context(), id, user.UserID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if role == "" {
		http.Redirect(w, r, "/host", http.StatusSeeOther)
		return
	}
	requireManageFest := func() bool {
		if festRoleCanManageFest(role) {
			return true
		}
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	requireCreator := func() bool {
		if festRoleCanDeleteFest(role) {
			return true
		}
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	if len(parts) == 2 {
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			s.renderHostFestDashboard(w, r, id, hostDashMessages{})
		case http.MethodPost:
			if !requireManageFest() {
				return
			}
			s.handleHostUpdateFest(w, r, id)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) == 3 && parts[2] == "teams" {
		if !requireManageFest() {
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.renderHostFestTeams(w, r, id)
		return
	}
	if len(parts) == 3 && parts[2] == "players" {
		if !requireManageFest() {
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.renderHostFestPlayers(w, r, id)
		return
	}
	if len(parts) == 4 && parts[2] == "players" && parts[3] == "overrides" {
		if !requireManageFest() {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleHostAddPlayerOverride(w, r, id)
		return
	}
	if len(parts) == 3 && parts[2] == "import" {
		if !requireManageFest() {
			return
		}
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
	if len(parts) == 3 && parts[2] == "access" {
		if !requireManageFest() {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleHostSaveAccess(w, r, id, user.UserID)
		return
	}
	if len(parts) == 3 && parts[2] == "delete" {
		if !requireCreator() {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleHostDeleteFest(w, r, id, user.UserID)
		return
	}
	if len(parts) == 4 && parts[2] == "game" && parts[3] == "new" {
		if !requireManageFest() {
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			s.renderHostCreateGamePage(w, r, id, "", "")
		case http.MethodPost:
			s.handleHostCreateGame(w, r, id)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) == 5 && parts[2] == "game" && (parts[4] == "delete" || parts[4] == "clear") {
		if !requireManageFest() {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		gameID, err := resolveGameID(r.Context(), s.db, id, parts[3])
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
		if parts[4] == "clear" {
			s.handleHostClearGame(w, r, id, gameID)
		} else {
			s.handleHostDeleteGame(w, r, id, gameID)
		}
		return
	}
	if len(parts) == 5 && parts[2] == "game" && parts[4] == "settings" {
		if !requireManageFest() {
			return
		}
		gameID, err := resolveGameID(r.Context(), s.db, id, parts[3])
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
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			s.renderHostGameSettings(w, r, id, gameID, "")
		case http.MethodPost:
			s.handleHostUpdateGameSettings(w, r, id, gameID)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) == 3 && parts[2] == "numbers" {
		if !requireManageFest() {
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			s.renderHostFestNumbers(w, r, id, "", "", nil)
		case http.MethodPost:
			s.handleHostSaveFestNumbers(w, r, id)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) == 4 && parts[2] == "numbers" && parts[3] == "auto" {
		if !requireManageFest() {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleHostAutoFestNumbers(w, r, id)
		return
	}
	if len(parts) == 4 && parts[2] == "numbers" && parts[3] == "clear" {
		if !requireManageFest() {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleHostClearFestNumbers(w, r, id)
		return
	}
	if len(parts) == 5 && parts[2] == "numbers" && parts[3] == "import" {
		if !requireManageFest() {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		switch parts[4] {
		case "match":
			s.handleHostFestNumbersImportMatch(w, r, id)
		case "apply":
			s.handleHostFestNumbersImportApply(w, r, id)
		default:
			http.NotFound(w, r)
		}
		return
	}
	if len(parts) == 4 && parts[2] == "rating" && parts[3] == "import" {
		if !requireManageFest() {
			return
		}
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
	// /host/fest/{id}/audit              → per-game history index
	// /host/fest/{id}/audit/{gid}        → one game's edit history
	// /host/fest/{id}/audit/{gid}/revert → per-game derived revert (POST)
	if parts[2] == "audit" {
		if !requireManageFest() {
			return
		}
		switch {
		case len(parts) == 3:
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			s.renderHostFestAudit(w, r, id, "", "")
			return
		case len(parts) == 4 || (len(parts) == 5 && parts[4] == "revert"):
			gid, err := strconv.ParseInt(parts[3], 10, 64)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			if len(parts) == 5 { // revert
				if r.Method != http.MethodPost {
					http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
					return
				}
				s.handleGameRevert(w, r, id, gid)
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			s.renderGameJournal(w, r, id, gid, "", "")
			return
		default:
			http.NotFound(w, r)
			return
		}
	}
	// /host/fest/{id}/game/{gid}[/...] → serve host.html / od.html / si.html.
	if !isHostGameSubPath(parts[2:]) {
		http.NotFound(w, r)
		return
	}
	s.serveHostGamePage(w, r, id, parts[2:])
}

func (s *server) serveHostGamePage(w http.ResponseWriter, r *http.Request, festID int64, parts []string) {
	gameID, err := resolveGameID(r.Context(), s.db, festID, parts[1])
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
	var gameType string
	if err := s.db.QueryRowContext(r.Context(), `select game_type from games where id = ? and fest_id = ?`, gameID, festID).Scan(&gameType); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	scope := festScope{FestID: festID, GameID: gameID}
	switch gameType {
	case "od":
		s.serveGameHTMLWithInit(w, r, "static/od.html", scope)
	case "si", "ksi":
		s.serveGameHTMLWithInit(w, r, "static/si.html", scope)
	default:
		s.serveHostHTMLWithInit(w, r, scope, parts)
	}
}

func isHostGameSubPath(parts []string) bool {
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
	case "venues", "seed-import", "stats":
		return len(parts) == 3
	case "matches", "stage":
		return len(parts) == 4 && parts[3] != ""
	}
	return false
}

func parsePositiveFormInt(form url.Values, key, label string, min, max int) (int, error) {
	raw := strings.TrimSpace(form.Get(key))
	value, err := strconv.Atoi(raw)
	if err != nil || value < min || value > max {
		return 0, fmt.Errorf("%s должно быть от %d до %d", label, min, max)
	}
	return value, nil
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
