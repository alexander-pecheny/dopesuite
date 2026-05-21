package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
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

type hostFestDashData struct {
	Fest            hostMyFest
	Description     string
	Slug            string
	RatingID        int64
	Games           []publicFestGame
	Access          []hostAccessMember
	TeamCount       int
	PlayerCount     int
	NumbersAssigned int
	NumbersAllSet   bool
	CurrentRole     string
	CanManageFest   bool
	CanManageGames  bool
	CanManageAccess bool
	CanDeleteFest   bool
	IsCreator       bool
	Error           string
	AccessError     string
	AccessNotice    string
	ImportError     string
	ImportNotice    string
	RosterError     string
	RosterNotice    string
}

type hostGameSettingsData struct {
	Fest  hostMyFest
	Game  publicFestGame
	Slug  string
	Error string
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

type hostGameCreateData struct {
	Fest         hostMyFest
	Error        string
	SelectedType string
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
    {{if .CanManageFest}}
    <form method="post" action="/host/fest/{{.Fest.Ref}}" class="card stack" autocomplete="off">
      <label class="field">
        <span>Название</span>
        <input name="title" value="{{.Fest.Title}}" required>
      </label>
      <label class="field">
        <span>Описание (markdown)</span>
        <textarea name="description" rows="6">{{.Description}}</textarea>
      </label>
      <label class="field">
        <span>Slug (необязательно; задайте, чтобы получить URL вида /fest/{slug})</span>
        <input name="slug" value="{{.Slug}}" pattern="[a-z0-9-]+" placeholder="my-fest">
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
    {{end}}

    <section class="section">
      <h2>Игры</h2>
      {{if .Games}}
      <ul class="list">
        {{range .Games}}
        <li class="list-action-row">
          <a class="list-row" href="/host/fest/{{$.Fest.Ref}}/game/{{.Ref}}/">
            <span class="list-row-title">{{.Title}}</span>
            {{if .Slug}}<span class="muted">{{.Slug}}</span>{{end}}
          </a>
          {{if $.CanManageGames}}
          <a class="btn" href="/host/fest/{{$.Fest.Ref}}/game/{{.Ref}}/settings">Свойства</a>
          <form method="post" action="/host/fest/{{$.Fest.Ref}}/game/{{.Ref}}/delete" onsubmit="return confirm('Удалить игру? Все результаты этой игры будут потеряны.');">
            <button class="btn danger" type="submit">Удалить</button>
          </form>
          {{end}}
        </li>
        {{end}}
      </ul>
      {{else}}
      <p class="empty">Игр пока нет.</p>
      {{end}}
      {{if .CanManageGames}}
      <div class="cluster">
        <a class="btn" href="/host/fest/{{.Fest.Ref}}/game/new">Добавить игру</a>
      </div>
      {{end}}
    </section>

    {{if .CanManageAccess}}
    <section class="section" id="access">
      <h2>Доступ</h2>
      {{if .AccessError}}<p class="empty">{{.AccessError}}</p>{{end}}
      {{if .AccessNotice}}<p class="muted">{{.AccessNotice}}</p>{{end}}
      <form method="post" action="/host/fest/{{.Fest.Ref}}/access#access" class="card stack" autocomplete="off">
        <div class="table-scroll">
          <table class="data-table access-table">
            <thead><tr><th class="access-name-col">Никнейм</th><th class="access-role-col">Роль</th><th class="access-action-col"></th></tr></thead>
            <tbody>
              {{range .Access}}
              <tr>
                <td class="access-name-cell">{{.Nickname}}</td>
                <td class="access-role-cell">
                  {{if .IsCreator}}
                  <input type="hidden" name="role_{{.UserID}}" value="creator">
                  <span class="access-role-label">creator</span>
                  {{else}}
                  <select name="role_{{.UserID}}" onchange="this.form.requestSubmit ? this.form.requestSubmit() : this.form.submit()">
                    <option value="admin"{{if eq .Role "admin"}} selected{{end}}>admin</option>
                    <option value="host"{{if eq .Role "host"}} selected{{end}}>host</option>
                  </select>
                  {{end}}
                </td>
                <td class="access-action-cell">
                  {{if not .IsCreator}}
                  <button class="btn danger" type="submit" name="delete_{{.UserID}}" value="1">Удалить</button>
                  {{end}}
                </td>
              </tr>
              {{end}}
              <tr>
                <td class="access-name-cell"><input name="new_nickname" placeholder="nickname"></td>
                <td class="access-role-cell">
                  <select name="new_role">
                    <option value="host">host</option>
                    <option value="admin">admin</option>
                  </select>
                </td>
                <td class="access-action-cell"><button class="btn" type="submit" name="add_access" value="1">Добавить</button></td>
              </tr>
            </tbody>
          </table>
        </div>
      </form>
    </section>
    {{end}}

    {{if .CanManageFest}}
    <section class="section">
      <h2>Участники</h2>
      {{if .RosterError}}<p class="empty">{{.RosterError}}</p>{{end}}
      {{if .RosterNotice}}<p class="muted">{{.RosterNotice}}</p>{{end}}
      <ul class="list">
        <li>
          <a class="list-row" href="/host/fest/{{.Fest.Ref}}/teams">
            <span class="list-row-title">Команды</span>
            <span class="muted">{{.TeamCount}}</span>
          </a>
        </li>
        <li>
          <a class="list-row" href="/host/fest/{{.Fest.Ref}}/players">
            <span class="list-row-title">Игроки</span>
            <span class="muted">{{.PlayerCount}}</span>
          </a>
        </li>
        {{if .TeamCount}}
        <li>
          <a class="list-row" href="/host/fest/{{.Fest.Ref}}/numbers">
            <span class="list-row-title">Номера команд</span>
            <span class="muted">{{if .NumbersAllSet}}готово{{else if .NumbersAssigned}}{{.NumbersAssigned}} из {{.TeamCount}}{{else}}не выставлены{{end}}</span>
          </a>
        </li>
        {{end}}
        <li>
          <a class="list-row" href="/host/fest/{{.Fest.Ref}}/rating/import">
            <span class="list-row-title">Загрузить команды и игроков</span>
            <span class="muted">{{if .RatingID}}rating {{.RatingID}}{{else}}нет rating ID{{end}}</span>
          </a>
        </li>
      </ul>
    </section>
    {{end}}

    {{if .CanDeleteFest}}
    <section class="section">
      <h2>Удаление</h2>
      <form method="post" action="/host/fest/{{.Fest.Ref}}/delete" class="card stack" autocomplete="off" onsubmit="return confirm('Удалить турнир? Все игры, команды и результаты будут удалены.');">
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

var hostGameCreateTemplate = template.Must(template.New("hostGameCreate").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Fest.Title}} · новая игра</title>
  <link rel="stylesheet" href="/static/styles.css">
</head>
<body class="public">
  <header class="public-top">
    <a class="public-back" href="/host/fest/{{.Fest.Ref}}">←</a>
    <h1>Добавить игру</h1>
  </header>
  <main class="public-main">
    {{if .Error}}<p class="empty">{{.Error}}</p>{{end}}
    <form method="post" action="/host/fest/{{.Fest.Ref}}/game/new" class="card stack" autocomplete="off" data-game-create-form>
      <fieldset class="field game-type-fieldset">
        <span>Тип игры</span>
        <label class="checkbox">
          <input type="radio" name="game_type" value="od" {{if eq .SelectedType "od"}}checked{{end}}>
          <span>ОД</span>
        </label>
        <label class="checkbox">
          <input type="radio" name="game_type" value="ksi" {{if eq .SelectedType "ksi"}}checked{{end}}>
          <span>КСИ</span>
        </label>
        <label class="checkbox">
          <input type="radio" name="game_type" value="ek" {{if eq .SelectedType "ek"}}checked{{end}}>
          <span>ЭК</span>
        </label>
      </fieldset>

      <section class="stack game-create-settings" data-game-settings="od" {{if ne .SelectedType "od"}}hidden{{end}}>
        <label class="field">
          <span>Количество туров</span>
          <input name="od_tours" inputmode="numeric" value="3">
        </label>
        <label class="field">
          <span>Количество вопросов в туре</span>
          <input name="od_questions" inputmode="numeric" value="15">
        </label>
      </section>

      <section class="stack game-create-settings" data-game-settings="ksi" {{if ne .SelectedType "ksi"}}hidden{{end}}>
        <label class="field">
          <span>Количество тем</span>
          <input name="ksi_themes" inputmode="numeric" value="20">
        </label>
      </section>

      <section class="stack game-create-settings" data-game-settings="ek" {{if ne .SelectedType "ek"}}hidden{{end}}>
        <label class="field">
          <span>JSON-схема</span>
          <textarea name="ek_scheme" rows="14" placeholder='{"slug":"...","title":"...","gameType":"ek","stages":[...]}'></textarea>
        </label>
      </section>

      <div class="cluster" data-game-submit {{if eq .SelectedType ""}}hidden{{end}}>
        <button class="btn" type="submit">Создать</button>
      </div>
    </form>
  </main>
  <script>
    (() => {
      const form = document.querySelector("[data-game-create-form]");
      if (!form) return;
      const sync = () => {
        const selected = form.querySelector('input[name="game_type"]:checked')?.value || "";
        form.querySelectorAll("[data-game-settings]").forEach((section) => {
          section.hidden = section.dataset.gameSettings !== selected;
        });
        const submit = form.querySelector("[data-game-submit]");
        if (submit) submit.hidden = selected === "";
      };
      form.querySelectorAll('input[name="game_type"]').forEach((input) => input.addEventListener("change", sync));
      sync();
    })();
  </script>
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
    <a class="public-back" href="/host/fest/{{.Fest.Ref}}">←</a>
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
    <a class="public-back" href="/host/fest/{{.Fest.Ref}}">←</a>
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
    <a class="public-back" href="/host/fest/{{.Fest.Ref}}">←</a>
    <h1>Импорт участников</h1>
  </header>
  <main class="public-main">
    {{if .Error}}<p class="empty">{{.Error}}</p>{{end}}
    {{if .Notice}}<p class="muted">{{.Notice}}</p>{{end}}
    <section class="section">
      {{if .RatingID}}
      <p class="muted">Источник: rating.chgk.info ID {{.RatingID}}</p>
      <form method="post" action="/host/fest/{{.Fest.Ref}}/rating/import" class="card stack" autocomplete="off">
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

var hostGameSettingsTemplate = template.Must(template.New("hostGameSettings").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Game.Title}} · свойства</title>
  <link rel="stylesheet" href="/static/styles.css">
</head>
<body class="public">
  <header class="public-top">
    <a class="public-back" href="/host/fest/{{.Fest.Ref}}">←</a>
    <h1>{{.Game.Title}}</h1>
  </header>
  <main class="public-main">
    {{if .Error}}<p class="empty">{{.Error}}</p>{{end}}
    <form method="post" action="/host/fest/{{.Fest.Ref}}/game/{{.Game.Ref}}/settings" class="card stack" autocomplete="off">
      <label class="field">
        <span>Slug (необязательно, a-z, 0-9, дефис)</span>
        <input name="slug" value="{{.Slug}}" pattern="[a-z0-9-]+">
      </label>
      <div class="cluster">
        <button class="btn" type="submit">Сохранить</button>
      </div>
    </form>
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
    <a class="public-back" href="/host/fest/{{.Fest.Ref}}">←</a>
    <h1>Импорт схемы</h1>
  </header>
  <main class="public-main">
    {{if .Error}}<p class="empty">{{.Error}}</p>{{end}}
    {{if .Notice}}<p class="muted">{{.Notice}}</p>{{end}}
    <form method="post" action="/host/fest/{{.Fest.Ref}}/import" class="card stack" autocomplete="off">
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
	if len(parts) == 5 && parts[2] == "game" && parts[4] == "delete" {
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
		s.handleHostDeleteGame(w, r, id, gameID)
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
	if len(parts) == 5 && parts[2] == "game" && parts[4] == "venues" && !festRoleCanManageFest(role) {
		http.Error(w, "forbidden", http.StatusForbidden)
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
	if parts[0] != "game" || parts[1] == "" {
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
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	festID, err := insertReturningID(r.Context(), tx, `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, start_date, end_date, is_public)
values(?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?)`,
		nil, title, description, ratingID, user.UserID, now, now,
		nullableString(startDate), nullableString(endDate), boolToInt(isPublic))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := tx.ExecContext(r.Context(), `
insert into fest_organizers(fest_id, user_id, role, added_at)
values(?, ?, 'creator', ?)`, festID, user.UserID, now); err != nil {
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
	AccessError  string
	AccessNotice string
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
	slug := strings.TrimSpace(r.Form.Get("slug"))
	var slugValue any
	if slug != "" {
		if err := validateSlug(slug); err != nil {
			s.renderHostFestDashboard(w, r, festID, hostDashMessages{FormError: "Slug: " + err.Error()})
			return
		}
		if taken, err := s.slugTakenByOtherFest(r.Context(), slug, festID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		} else if taken {
			s.renderHostFestDashboard(w, r, festID, hostDashMessages{FormError: "Slug уже занят."})
			return
		}
		slugValue = slug
	}

	if _, err := s.db.ExecContext(r.Context(), `
update fests
set title = ?, slug = ?, description = ?, rating_id = ?, start_date = ?, end_date = ?, is_public = ?, updated_at = ?
where id = ?`,
		title, slugValue, description, ratingID,
		nullableString(startDate), nullableString(endDate), boolToInt(isPublic),
		utcNow(), festID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirectRef := slug
	if redirectRef == "" {
		redirectRef = fmt.Sprintf("%d", festID)
	}
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%s", redirectRef), http.StatusSeeOther)
}

func (s *server) handleHostSaveAccess(w http.ResponseWriter, r *http.Request, festID, actorID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if err := s.saveFestAccess(r.Context(), festID, actorID, r.Form); err != nil {
		s.renderHostFestDashboard(w, r, festID, hostDashMessages{AccessError: err.Error()})
		return
	}
	s.renderHostFestDashboard(w, r, festID, hostDashMessages{AccessNotice: "Доступ сохранён."})
}

func (s *server) slugTakenByOtherFest(ctx context.Context, slug string, festID int64) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `select count(*) from fests where slug = ? and id <> ?`, slug, festID).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *server) festRefOrID(ctx context.Context, festID int64) string {
	var slug string
	if err := s.db.QueryRowContext(ctx, `select coalesce(slug, '') from fests where id = ?`, festID).Scan(&slug); err == nil && slug != "" {
		return slug
	}
	return fmt.Sprintf("%d", festID)
}

func (s *server) gameRefOrID(ctx context.Context, gameID int64) string {
	var slug string
	if err := s.db.QueryRowContext(ctx, `select coalesce(slug, '') from games where id = ?`, gameID).Scan(&slug); err == nil && slug != "" {
		return slug
	}
	return fmt.Sprintf("%d", gameID)
}

func (s *server) renderHostGameSettings(w http.ResponseWriter, r *http.Request, festID, gameID int64, errMsg string) {
	fest, err := s.loadHostFestHeader(r.Context(), festID)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var (
		code     string
		title    string
		gameType string
		slug     sql.NullString
	)
	if err := s.db.QueryRowContext(r.Context(), `
select code, title, game_type, slug from games where id = ? and fest_id = ?`, gameID, festID).Scan(&code, &title, &gameType, &slug); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = hostGameSettingsTemplate.Execute(w, hostGameSettingsData{
		Fest: fest,
		Game: publicFestGame{
			ID:    gameID,
			Slug:  slug.String,
			Code:  code,
			Title: title,
			Type:  gameTypeLabel(gameType),
		},
		Slug:  slug.String,
		Error: errMsg,
	})
}

func (s *server) handleHostUpdateGameSettings(w http.ResponseWriter, r *http.Request, festID, gameID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	slug := strings.TrimSpace(r.Form.Get("slug"))
	var slugValue any
	if slug != "" {
		if err := validateSlug(slug); err != nil {
			s.renderHostGameSettings(w, r, festID, gameID, "Slug: "+err.Error())
			return
		}
		var count int
		if err := s.db.QueryRowContext(r.Context(), `
select count(*) from games where fest_id = ? and slug = ? and id <> ?`, festID, slug, gameID).Scan(&count); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if count > 0 {
			s.renderHostGameSettings(w, r, festID, gameID, "Slug уже занят в этом фесте.")
			return
		}
		slugValue = slug
	}
	if _, err := s.db.ExecContext(r.Context(), `
update games set slug = ?, updated_at = ? where id = ? and fest_id = ?`,
		slugValue, utcNow(), gameID, festID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	gameRef := slug
	if gameRef == "" {
		gameRef = fmt.Sprintf("%d", gameID)
	}
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%s/game/%s/settings", s.festRefOrID(r.Context(), festID), gameRef), http.StatusSeeOther)
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

func (s *server) handleHostDeleteGame(w http.ResponseWriter, r *http.Request, festID, gameID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	var title string
	if err := tx.QueryRowContext(r.Context(), `
select title from games where id = ? and fest_id = ?`, gameID, festID).Scan(&title); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := tx.ExecContext(r.Context(), `delete from games where id = ? and fest_id = ?`, gameID, festID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var nextGameID sql.NullInt64
	var nextMatchCode sql.NullString
	if err := tx.QueryRowContext(r.Context(), `
select g.id, coalesce((
  select m.code from matches m where m.game_id = g.id order by m.position, m.id limit 1
), '')
from games g
where g.fest_id = ?
order by g.position, g.id
limit 1`, festID).Scan(&nextGameID, &nextMatchCode); err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := bumpFestRevisionTx(r.Context(), tx, festID, "game:delete", mustJSON(map[string]any{
		"gameID": gameID,
		"title":  title,
	})); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if s.festID == festID && s.activeGameID == gameID {
		if nextGameID.Valid {
			s.activeGameID = nextGameID.Int64
			s.activeMatchCode = nextMatchCode.String
		} else {
			s.activeGameID = 0
			s.activeMatchCode = ""
		}
	}
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%s", s.festRefOrID(r.Context(), festID)), http.StatusSeeOther)
}

func (s *server) renderHostCreateGamePage(w http.ResponseWriter, r *http.Request, festID int64, errMsg string, selectedType string) {
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
	_ = hostGameCreateTemplate.Execute(w, hostGameCreateData{Fest: fest, Error: errMsg, SelectedType: selectedType})
}

func (s *server) handleHostCreateGame(w http.ResponseWriter, r *http.Request, festID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	gameType := strings.TrimSpace(r.Form.Get("game_type"))
	gameID, err := s.createHostGame(r.Context(), festID, gameType, r.Form)
	if err != nil {
		s.renderHostCreateGamePage(w, r, festID, err.Error(), gameType)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%s/game/%s/", s.festRefOrID(r.Context(), festID), s.gameRefOrID(r.Context(), gameID)), http.StatusSeeOther)
}

func (s *server) createHostGame(ctx context.Context, festID int64, gameType string, form url.Values) (int64, error) {
	if s.db == nil {
		return 0, errors.New("sqlite is not enabled")
	}
	gameType = strings.TrimSpace(gameType)
	if gameType != "od" && gameType != "ksi" && gameType != "ek" {
		return 0, errors.New("выберите тип игры")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRowContext(ctx, `select count(*) from fests where id = ?`, festID).Scan(&exists); err != nil {
		return 0, err
	}
	if exists == 0 {
		return 0, sql.ErrNoRows
	}

	var gameID int64
	switch gameType {
	case "od":
		tours, err := parsePositiveFormInt(form, "od_tours", "Количество туров", 1, 20)
		if err != nil {
			return 0, err
		}
		questions, err := parsePositiveFormInt(form, "od_questions", "Количество вопросов в туре", 1, 100)
		if err != nil {
			return 0, err
		}
		gameID, err = createODGameTx(ctx, tx, festID, tours, questions)
		if err != nil {
			return 0, err
		}
	case "ksi":
		themes, err := parsePositiveFormInt(form, "ksi_themes", "Количество тем", 1, 100)
		if err != nil {
			return 0, err
		}
		gameID, err = createKSIGameTx(ctx, tx, festID, themes)
		if err != nil {
			return 0, err
		}
	case "ek":
		raw := strings.TrimSpace(form.Get("ek_scheme"))
		if raw == "" {
			return 0, errors.New("Вставьте JSON-схему ЭК")
		}
		var scheme festScheme
		if err := json.Unmarshal([]byte(raw), &scheme); err != nil {
			return 0, fmt.Errorf("Не удалось разобрать JSON: %w", err)
		}
		gameID, err = createEKGameTx(ctx, tx, festID, scheme)
		if err != nil {
			return 0, err
		}
	}

	if _, err := bumpFestRevisionTx(ctx, tx, festID, "game:create", mustJSON(map[string]any{
		"gameID":   gameID,
		"gameType": gameType,
	})); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return gameID, nil
}

func parsePositiveFormInt(form url.Values, key, label string, min, max int) (int, error) {
	raw := strings.TrimSpace(form.Get(key))
	value, err := strconv.Atoi(raw)
	if err != nil || value < min || value > max {
		return 0, fmt.Errorf("%s должно быть от %d до %d", label, min, max)
	}
	return value, nil
}

type gameIdentity struct {
	Code     string
	Title    string
	Position int
}

func nextGameIdentityTx(ctx context.Context, tx *sql.Tx, festID int64, gameType, titleBase string) (gameIdentity, error) {
	var position int
	if err := tx.QueryRowContext(ctx, `select coalesce(max(position), 0) + 1 from games where fest_id = ?`, festID).Scan(&position); err != nil {
		return gameIdentity{}, err
	}
	var typeCount int
	if err := tx.QueryRowContext(ctx, `select count(*) from games where fest_id = ? and game_type = ?`, festID, gameType).Scan(&typeCount); err != nil {
		return gameIdentity{}, err
	}
	title := titleBase
	if typeCount > 0 && gameType != "ek" {
		title = fmt.Sprintf("%s %d", titleBase, typeCount+1)
	}
	for suffix := position; ; suffix++ {
		code := fmt.Sprintf("%s-%d", gameType, suffix)
		var existing int
		if err := tx.QueryRowContext(ctx, `select count(*) from games where fest_id = ? and code = ?`, festID, code).Scan(&existing); err != nil {
			return gameIdentity{}, err
		}
		if existing == 0 {
			return gameIdentity{Code: code, Title: title, Position: position}, nil
		}
	}
}

func createODGameTx(ctx context.Context, tx *sql.Tx, festID int64, tours, questions int) (int64, error) {
	identity, err := nextGameIdentityTx(ctx, tx, festID, "od", "ОД")
	if err != nil {
		return 0, err
	}
	tourComp := make([]int, tours)
	for i := range tourComp {
		tourComp[i] = questions
	}
	totalQuestions := tours * questions
	entries := make([][]int, totalQuestions)
	for i := range entries {
		entries[i] = []int{}
	}
	schemeJSON := []byte(mustJSON(map[string]any{
		"schemaVersion": 2,
		"slug":          identity.Code,
		"title":         identity.Title,
		"gameType":      "od",
		"tourComp":      tourComp,
		"nTeams":        0,
		"teams":         []chgkTeamJSON{},
	}))
	stateJSON := []byte(mustJSON(map[string]any{
		"teams":     []chgkTeamJSON{},
		"entries":   entries,
		"completed": make([]bool, totalQuestions),
	}))
	teams, err := loadFestRosterImportTeamsTx(ctx, tx, festID)
	if err != nil {
		return 0, err
	}
	if len(teams) > 0 {
		schemeJSON, err = applyRosterToChGKScheme(string(schemeJSON), teams)
		if err != nil {
			return 0, err
		}
		stateJSON, err = applyRosterToChGKState(string(stateJSON), teams, nil)
		if err != nil {
			return 0, err
		}
	}
	return insertJSONGameTx(ctx, tx, festID, identity, "od", schemeJSON, stateJSON)
}

func createKSIGameTx(ctx context.Context, tx *sql.Tx, festID int64, themesCount int) (int64, error) {
	identity, err := nextGameIdentityTx(ctx, tx, festID, "ksi", "КСИ")
	if err != nil {
		return 0, err
	}
	themes := make([]map[string]any, themesCount)
	for i := range themes {
		themes[i] = map[string]any{"answers": [][]string{}}
	}
	schemeJSON := []byte(mustJSON(map[string]any{
		"schemaVersion": 2,
		"slug":          identity.Code,
		"title":         identity.Title,
		"gameType":      "ksi",
		"participants":  []string{},
		"themes":        themesCount,
	}))
	stateJSON := []byte(mustJSON(map[string]any{
		"participants": []string{},
		"themes":       themes,
		"finished":     false,
	}))
	teams, err := loadFestRosterImportTeamsTx(ctx, tx, festID)
	if err != nil {
		return 0, err
	}
	if len(teams) > 0 {
		schemeJSON, err = applyRosterToKSIScheme(string(schemeJSON), teams)
		if err != nil {
			return 0, err
		}
		stateJSON, err = applyRosterToKSIState(string(stateJSON), teams, themesCount)
		if err != nil {
			return 0, err
		}
	}
	return insertJSONGameTx(ctx, tx, festID, identity, "ksi", schemeJSON, stateJSON)
}

func insertJSONGameTx(ctx context.Context, tx *sql.Tx, festID int64, identity gameIdentity, gameType string, schemeJSON, stateJSON []byte) (int64, error) {
	now := utcNow()
	schemeID, err := insertReturningID(ctx, tx, `
insert into schemes(slug, title, version, schema_json, created_at)
values(?, ?, 2, ?, ?)`, uniqueSchemeSlug(identity.Code), identity.Title, string(schemeJSON), now)
	if err != nil {
		return 0, err
	}
	return insertReturningID(ctx, tx, `
insert into games(fest_id, code, title, game_type, position, scheme_id, scheme_json, state_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(?, ?, ?, ?, ?, ?, ?, ?, 'active', 'fest', 'fest', 1, ?, ?)`,
		festID, identity.Code, identity.Title, gameType, identity.Position, schemeID, string(schemeJSON), string(stateJSON), now, now)
}

func createEKGameTx(ctx context.Context, tx *sql.Tx, festID int64, scheme festScheme) (int64, error) {
	if scheme.GameType == "" {
		scheme.GameType = defaultGameType
	}
	if scheme.GameType != defaultGameType {
		return 0, errors.New("для ЭК нужна JSON-схема с gameType \"ek\"")
	}
	if err := validateScheme(scheme); err != nil {
		return 0, err
	}
	if len(scheme.Teams) > 0 {
		return 0, errors.New("команды загружаются только из rating.chgk.info; уберите teams из JSON-схемы")
	}
	schemaJSON, err := json.Marshal(scheme)
	if err != nil {
		return 0, err
	}
	title := strings.TrimSpace(scheme.Title)
	if title == "" {
		title = "ЭК"
	}
	identity, err := nextGameIdentityTx(ctx, tx, festID, "ek", title)
	if err != nil {
		return 0, err
	}
	identity.Title = title

	now := utcNow()
	schemeID, err := insertReturningID(ctx, tx, `
insert into schemes(slug, title, version, schema_json, created_at)
values(?, ?, ?, ?, ?)`, uniqueSchemeSlug(scheme.Slug), title, maxInt(scheme.SchemaVersion, 2), string(schemaJSON), now)
	if err != nil {
		return 0, err
	}
	gameID, err := insertReturningID(ctx, tx, `
insert into games(fest_id, code, title, game_type, position, scheme_id, scheme_json, state_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(?, ?, ?, ?, ?, ?, ?, '{}', 'pending', 'fest', 'fest', 1, ?, ?)`,
		festID, identity.Code, title, defaultGameType, identity.Position, schemeID, string(schemaJSON), now, now)
	if err != nil {
		return 0, err
	}

	venueIDs := make(map[int]int64, len(scheme.Venues))
	for _, venue := range scheme.Venues {
		venueID, err := upsertVenueTx(ctx, tx, festID, venue, now)
		if err != nil {
			return 0, err
		}
		venueIDs[venue.Number] = venueID
	}

	for stageIndex, stage := range scheme.Stages {
		position := stage.Position
		if position == 0 {
			position = stageIndex + 1
		}
		configJSON := stageConfigJSON(stage)
		stageType := stage.StageType
		if stageType == "" {
			stageType = "matches"
		}
		stageID, err := insertReturningID(ctx, tx, `
insert into stages(fest_id, game_id, code, title, stage_type, position, status, config_json)
values(?, ?, ?, ?, ?, ?, 'pending', ?)`, festID, gameID, stage.Code, stage.Title, stageType, position, configJSON)
		if err != nil {
			return 0, err
		}
		if stageType != "matches" {
			continue
		}
		for matchIndex, match := range stage.Matches {
			participantCount := match.ParticipantCount
			if participantCount == 0 {
				participantCount = len(match.Slots)
			}
			var venueID any
			if id, ok := venueIDs[match.Venue]; ok {
				venueID = id
			}
			matchID, err := insertReturningID(ctx, tx, `
insert into matches(fest_id, game_id, stage_id, code, title, position, participant_count, venue_id, status, revision)
values(?, ?, ?, ?, ?, ?, ?, ?, 'pending', 1)`, festID, gameID, stageID, match.Code, match.Title, matchIndex+1, participantCount, venueID)
			if err != nil {
				return 0, err
			}
			for slotIndex, slot := range match.Slots {
				sourceType, sourceRef := slotSource(slot)
				if _, err := tx.ExecContext(ctx, `
insert into match_slots(match_id, slot_index, source_type, source_ref_json, team_id, locked)
values(?, ?, ?, ?, null, 0)`, matchID, slotIndex, sourceType, sourceRef); err != nil {
					return 0, err
				}
			}
		}
	}
	return gameID, nil
}

func upsertVenueTx(ctx context.Context, tx *sql.Tx, festID int64, venue schemeVenue, now string) (int64, error) {
	if _, err := tx.ExecContext(ctx, `
insert into venues(fest_id, number, title, created_at, updated_at)
values(?, ?, ?, ?, ?)
on conflict(fest_id, number) do update set title = excluded.title, updated_at = excluded.updated_at`,
		festID, venue.Number, venue.Title, now, now); err != nil {
		return 0, err
	}
	var id int64
	err := tx.QueryRowContext(ctx, `select id from venues where fest_id = ? and number = ?`, festID, venue.Number).Scan(&id)
	return id, err
}

func uniqueSchemeSlug(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "game"
	}
	return fmt.Sprintf("%s-%d", base, time.Now().UnixNano())
}

func loadFestRosterImportTeamsTx(ctx context.Context, q dbQueryer, festID int64) ([]festRosterImportTeam, error) {
	rows, err := q.QueryContext(ctx, `
select coalesce(rating_id, 0), name, city, coalesce(number, 0)
from fest_teams
where fest_id = ? and deleted = 0
order by position, id`, festID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var teams []festRosterImportTeam
	for rows.Next() {
		var team festRosterImportTeam
		if err := rows.Scan(&team.RatingID, &team.Name, &team.City, &team.Number); err != nil {
			return nil, err
		}
		teams = append(teams, team)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sortedFestRosterImportTeams(teams), nil
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
		slug        string
		description string
		startDate   sql.NullString
		endDate     sql.NullString
		ratingID    sql.NullInt64
		isPublic    int
	)
	if err := s.db.QueryRowContext(r.Context(), `
select title, coalesce(slug, ''), description, start_date, end_date, rating_id, is_public
from fests where id = ?`, festID).Scan(&title, &slug, &description, &startDate, &endDate, &ratingID, &isPublic); err != nil {
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
	festRef := slug
	if festRef == "" {
		festRef = fmt.Sprintf("%d", festID)
	}
	hostGames := make([]publicFestGame, len(games))
	for i, g := range games {
		hostGames[i] = publicFestGame{
			ID:    g.ID,
			Slug:  g.Slug,
			Code:  g.Code,
			Title: g.Title,
			Type:  gameTypeLabel(g.Type),
			URL:   fmt.Sprintf("/host/fest/%s/game/%s/", festRef, g.Ref()),
		}
	}
	teamCount, playerCount, err := s.loadHostFestRosterCounts(r.Context(), festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var numbersAssigned int
	if err := s.db.QueryRowContext(r.Context(), `
select coalesce(sum(case when number is not null then 1 else 0 end), 0)
from fest_teams where fest_id = ? and deleted = 0`, festID).Scan(&numbersAssigned); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	currentRole := ""
	if user, ok := s.lookupSession(r); ok {
		currentRole, err = s.festUserRole(r.Context(), festID, user.UserID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	canManageFest := festRoleCanManageFest(currentRole)
	canManageAccess := festRoleCanManageAccess(currentRole)
	canDeleteFest := festRoleCanDeleteFest(currentRole)
	canManageGames := canManageFest
	var access []hostAccessMember
	if canManageAccess {
		access, err = s.loadFestAccessMembers(r.Context(), festID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	data := hostFestDashData{
		Fest: hostMyFest{
			ID:        festID,
			Slug:      slug,
			Title:     title,
			StartDate: startDate.String,
			EndDate:   endDate.String,
			Dates:     formatFestDates(startDate.String, endDate.String),
			IsPublic:  isPublic == 1,
		},
		Description:     description,
		Slug:            slug,
		Games:           hostGames,
		Access:          access,
		TeamCount:       teamCount,
		PlayerCount:     playerCount,
		NumbersAssigned: numbersAssigned,
		NumbersAllSet:   teamCount > 0 && numbersAssigned == teamCount,
		CurrentRole:     currentRole,
		CanManageFest:   canManageFest,
		CanManageGames:  canManageGames,
		CanManageAccess: canManageAccess,
		CanDeleteFest:   canDeleteFest,
		IsCreator:       canDeleteFest,
		Error:           msgs.FormError,
		AccessError:     msgs.AccessError,
		AccessNotice:    msgs.AccessNotice,
		ImportError:     msgs.ImportError,
		ImportNotice:    msgs.ImportNotice,
		RosterError:     msgs.RosterError,
		RosterNotice:    msgs.RosterNotice,
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
select id, coalesce(slug, ''), title, coalesce(start_date, ''), coalesce(end_date, ''), is_public
from fests where id = ?`, festID).Scan(&t.ID, &t.Slug, &t.Title, &t.StartDate, &t.EndDate, &pub); err != nil {
		return hostMyFest{}, err
	}
	t.IsPublic = pub == 1
	t.Dates = formatFestDates(t.StartDate, t.EndDate)
	return t, nil
}

func (s *server) loadHostFestRosterCounts(ctx context.Context, festID int64) (int, int, error) {
	var teamCount, playerCount int
	if err := s.db.QueryRowContext(ctx, `select count(*) from fest_teams where fest_id = ? and deleted = 0`, festID).Scan(&teamCount); err != nil {
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
where tt.fest_id = ? and tt.deleted = 0
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
	sort.SliceStable(teams, func(i, j int) bool {
		if cmp := compareAlpha(teams[i].Name, teams[j].Name); cmp != 0 {
			return cmp < 0
		}
		if cmp := compareAlpha(teams[i].City, teams[j].City); cmp != 0 {
			return cmp < 0
		}
		return teams[i].RatingID < teams[j].RatingID
	})
	return teams, nil
}

func (s *server) loadHostFestPlayers(ctx context.Context, festID int64) ([]hostFestPlayer, error) {
	playerRows, err := s.db.QueryContext(ctx, `
select coalesce(p.rating_id, 0), p.first_name, p.last_name, tt.name
from fest_team_players ttp
join fest_players p on p.id = ttp.player_id
join fest_teams tt on tt.id = ttp.team_id
where tt.fest_id = ? and tt.deleted = 0
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
	if err := playerRows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(players, func(i, j int) bool {
		if cmp := compareAlpha(players[i].Team, players[j].Team); cmp != 0 {
			return cmp < 0
		}
		if cmp := compareAlpha(players[i].Name, players[j].Name); cmp != 0 {
			return cmp < 0
		}
		return players[i].RatingID < players[j].RatingID
	})
	return players, nil
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
select t.id, coalesce(t.slug, ''), t.title, coalesce(t.start_date, ''), coalesce(t.end_date, ''), t.is_public
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
		if err := rows.Scan(&t.ID, &t.Slug, &t.Title, &t.StartDate, &t.EndDate, &pub); err != nil {
			return nil, err
		}
		t.IsPublic = pub == 1
		t.Dates = formatFestDates(t.StartDate, t.EndDate)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *server) isOrganizer(ctx context.Context, festID, userID int64) (bool, error) {
	role, err := s.festUserRole(ctx, festID, userID)
	if err != nil {
		return false, err
	}
	return role != "", nil
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
