package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"html/template"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"
)

// adminUsername gates the /admin tooling. Defaults to "pecheny"; override with
// DOPE_ADMIN_USER for other deployments or tests.
func adminUsername() string {
	if v := strings.TrimSpace(os.Getenv("DOPE_ADMIN_USER")); v != "" {
		return v
	}
	return "pecheny"
}

// requireAdmin resolves the session and confirms it belongs to the configured
// admin. On failure it writes the response itself — a redirect to /login when
// logged out, otherwise a 404 so the page's existence isn't revealed to
// authenticated non-admins — and returns false.
func (s *server) requireAdmin(w http.ResponseWriter, r *http.Request) (sessionUser, bool) {
	user, ok := s.lookupSession(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return sessionUser{}, false
	}
	if !user.Username.Valid || user.Username.String != adminUsername() {
		http.NotFound(w, r)
		return sessionUser{}, false
	}
	return user, true
}

// generatedPasswordAlphabet omits look-alike characters (0/O, 1/l/I) so the
// one-time passwords can be read aloud or retyped without ambiguity.
const generatedPasswordAlphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ23456789"
const generatedPasswordLen = 12

func newRandomPassword() (string, error) {
	buf := make([]byte, generatedPasswordLen)
	max := big.NewInt(int64(len(generatedPasswordAlphabet)))
	for i := range buf {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		buf[i] = generatedPasswordAlphabet[n.Int64()]
	}
	return string(buf), nil
}

var adminIndexTemplate = template.Must(template.New("adminIndex").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Админка</title>
  <link rel="preload" href="/static/fonts/noto-sans-400.woff2" as="font" type="font/woff2" crossorigin>
  <link rel="stylesheet" href="/static/styles.css">
  <script src="/static/menu.js"></script>
</head>
<body class="public">
  <header class="public-top">
    <h1>Админка</h1>
  </header>
  <main class="public-main">
    <ul class="list">
      <li><a class="list-row" href="/admin/create_users"><span class="list-row-title">Создать пользователей</span></a></li>
      <li><a class="list-row" href="/admin/users"><span class="list-row-title">Пользователи</span></a></li>
    </ul>
  </main>
</body>
</html>`))

type adminCreatedUser struct {
	Username string
	Password string
}

type adminUserError struct {
	Username string
	Reason   string
}

type adminCreateUsersData struct {
	Submitted bool
	Created   []adminCreatedUser
	Skipped   []string
	Errors    []adminUserError
}

// Copyable returns the created credentials as tab-separated lines, ready to
// paste into a message to hand out to each new user.
func (d adminCreateUsersData) Copyable() string {
	var b strings.Builder
	for _, u := range d.Created {
		b.WriteString(u.Username)
		b.WriteString("\t")
		b.WriteString(u.Password)
		b.WriteString("\n")
	}
	return b.String()
}

var adminCreateUsersTemplate = template.Must(template.New("adminCreateUsers").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Создать пользователей · Админка</title>
  <link rel="preload" href="/static/fonts/noto-sans-400.woff2" as="font" type="font/woff2" crossorigin>
  <link rel="stylesheet" href="/static/styles.css">
  <script src="/static/menu.js"></script>
</head>
<body class="public">
  <header class="public-top">
    <h1>Создать пользователей</h1>
    <a class="public-user" href="/admin">Админка</a>
  </header>
  <main class="public-main">
    {{if .Submitted}}
    {{if .Created}}
    <section class="section">
      <p class="auth-hint">Пароли показаны один раз. Скопируйте и разошлите — пользователи сменят их сами.</p>
      <table class="data-table">
        <thead><tr><th>Логин</th><th>Пароль</th></tr></thead>
        <tbody>
          {{range .Created}}<tr><td>{{.Username}}</td><td><code>{{.Password}}</code></td></tr>{{end}}
        </tbody>
      </table>
      <label class="field">
        <span>Для копирования (логин ⇥ пароль)</span>
        <textarea rows="{{len .Created}}" readonly onclick="this.select()">{{.Copyable}}</textarea>
      </label>
    </section>
    {{end}}
    {{if .Skipped}}
    <section class="section">
      <p class="empty">Уже существуют (пропущены): {{range $i, $u := .Skipped}}{{if $i}}, {{end}}{{$u}}{{end}}</p>
    </section>
    {{end}}
    {{if .Errors}}
    <section class="section">
      <p class="empty">Ошибки:</p>
      <ul class="list">
        {{range .Errors}}<li class="list-row"><span class="list-row-title">{{.Username}}</span><span class="muted">{{.Reason}}</span></li>{{end}}
      </ul>
    </section>
    {{end}}
    {{if not .Created}}{{if not .Skipped}}{{if not .Errors}}
    <p class="empty">Не указано ни одного логина.</p>
    {{end}}{{end}}{{end}}
    {{end}}

    <section class="section">
      <form method="post" action="/admin/create_users" class="card stack" autocomplete="off">
        <label class="field">
          <span>Логины (по одному в строке)</span>
          <textarea name="usernames" rows="10" placeholder="anton&#10;anya_a&#10;dasha" required></textarea>
        </label>
        <div class="cluster">
          <button class="btn" type="submit">Создать</button>
        </div>
      </form>
    </section>
  </main>
</body>
</html>`))

type adminUserRow struct {
	ID        int64
	Username  string
	Telegram  string
	IsSystem  bool
	CreatedAt string
}

type adminUsersData struct {
	Users []adminUserRow
}

var adminUsersTemplate = template.Must(template.New("adminUsers").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Пользователи · Админка</title>
  <link rel="preload" href="/static/fonts/noto-sans-400.woff2" as="font" type="font/woff2" crossorigin>
  <link rel="stylesheet" href="/static/styles.css">
  <script src="/static/menu.js"></script>
</head>
<body class="public">
  <header class="public-top">
    <h1>Пользователи</h1>
    <a class="public-user" href="/admin">Админка</a>
  </header>
  <main class="public-main">
    {{if .Users}}
    <section class="section">
      <table class="data-table">
        <thead><tr><th>ID</th><th>Логин</th><th>Telegram</th><th>Создан</th></tr></thead>
        <tbody>
          {{range .Users}}<tr><td>{{.ID}}</td><td>{{.Username}}{{if .IsSystem}} <span class="muted">(система)</span>{{end}}</td><td>{{.Telegram}}</td><td>{{.CreatedAt}}</td></tr>{{end}}
        </tbody>
      </table>
    </section>
    {{else}}
    <p class="empty">Пользователей нет.</p>
    {{end}}
  </main>
</body>
</html>`))

// /admin/users — lists all users with their creation timestamp.
func (s *server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin/users" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	users, err := s.loadAdminUsers(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = adminUsersTemplate.Execute(w, adminUsersData{Users: users})
}

func (s *server) loadAdminUsers(ctx context.Context) ([]adminUserRow, error) {
	return collectRows(ctx, s.db, `
select id, coalesce(username, ''), coalesce(telegram_username, ''), is_system, created_at
from users
order by created_at desc, id desc`, nil, func(rows *sql.Rows) (adminUserRow, error) {
		var u adminUserRow
		var isSystem int
		if err := rows.Scan(&u.ID, &u.Username, &u.Telegram, &isSystem, &u.CreatedAt); err != nil {
			return u, err
		}
		u.IsSystem = isSystem == 1
		if t, err := time.Parse(time.RFC3339, u.CreatedAt); err == nil {
			u.CreatedAt = t.Format("2006-01-02 15:04")
		}
		return u, nil
	})
}

// /admin — landing page with links to admin tools.
func (s *server) handleAdminLanding(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = adminIndexTemplate.Execute(w, nil)
}

// /admin/create_users — GET shows the form; POST bulk-creates users with random
// one-time passwords and renders them once.
func (s *server) handleAdminCreateUsers(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin/create_users" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		if _, ok := s.requireAdmin(w, r); !ok {
			return
		}
		s.renderAdminCreateUsers(w, adminCreateUsersData{})
	case http.MethodPost:
		if _, ok := s.requireAdmin(w, r); !ok {
			return
		}
		if !requireSameOriginUnsafe(w, r) {
			return
		}
		s.handleAdminCreateUsersSubmit(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *server) renderAdminCreateUsers(w http.ResponseWriter, data adminCreateUsersData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = adminCreateUsersTemplate.Execute(w, data)
}

func (s *server) handleAdminCreateUsersSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	usernames := parseUsernameLines(r.PostForm.Get("usernames"))

	data := adminCreateUsersData{Submitted: true}

	ctx := r.Context()
	tx, err := s.beginWriteTx(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	for _, name := range usernames {
		if !validUsername(name) {
			data.Errors = append(data.Errors, adminUserError{Username: name, Reason: "недопустимый логин"})
			continue
		}
		var existing int64
		err := tx.QueryRowContext(ctx, `select id from users where username = ?`, name).Scan(&existing)
		if err == nil {
			data.Skipped = append(data.Skipped, name)
			continue
		} else if !errors.Is(err, sql.ErrNoRows) {
			data.Errors = append(data.Errors, adminUserError{Username: name, Reason: err.Error()})
			continue
		}
		password, err := newRandomPassword()
		if err != nil {
			data.Errors = append(data.Errors, adminUserError{Username: name, Reason: err.Error()})
			continue
		}
		hash, err := hashPassword(password)
		if err != nil {
			data.Errors = append(data.Errors, adminUserError{Username: name, Reason: err.Error()})
			continue
		}
		now := utcNow()
		if _, err := tx.ExecContext(ctx, `
insert into users(telegram_user_id, telegram_username, username, password_hash, password_salt, is_system, created_at, updated_at)
values(null, null, ?, ?, null, 0, ?, ?)`, name, hash, now, now); err != nil {
			if isUniqueViolation(err) {
				data.Skipped = append(data.Skipped, name)
				continue
			}
			data.Errors = append(data.Errors, adminUserError{Username: name, Reason: err.Error()})
			continue
		}
		data.Created = append(data.Created, adminCreatedUser{Username: name, Password: password})
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderAdminCreateUsers(w, data)
}

// parseUsernameLines splits the textarea input into trimmed, de-duplicated
// usernames, preserving first-seen order and dropping blank lines.
func parseUsernameLines(raw string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}
