package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"html/template"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"xy/internal/session"
)

// adminUsername gates the /admin tooling. Defaults to "pecheny"; override with
// XY_ADMIN_USER for other deployments or tests. (Ported from dope.)
func adminUsername() string {
	if v := strings.TrimSpace(os.Getenv("XY_ADMIN_USER")); v != "" {
		return v
	}
	return "pecheny"
}

// requireAdmin resolves the session and confirms it belongs to the configured
// admin. On failure it writes the response itself — a redirect to /login when
// logged out, otherwise a 404 so the page's existence isn't revealed to
// authenticated non-admins — and returns false.
func (s *server) requireAdmin(w http.ResponseWriter, r *http.Request) (session.User, bool) {
	user, ok := s.lookupSession(w, r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return session.User{}, false
	}
	if !user.Username.Valid || user.Username.String != adminUsername() {
		http.NotFound(w, r)
		return session.User{}, false
	}
	return user, true
}

// sameOrigin guards state-changing admin POSTs: a present Origin header must
// match the request host. (The session cookie is SameSite=Lax, so a cross-site
// POST wouldn't carry it anyway; this is defense in depth.)
func sameOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Host == r.Host
}

// adminUsernameRe allows the same shape the rest of the app uses for logins:
// letters, digits, and ._- (length is checked separately).
var adminUsernameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func validNewUsername(name string) bool {
	return len(name) >= 3 && len(name) <= 64 && adminUsernameRe.MatchString(name)
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
        <textarea rows="{{len .Created}}" readonly>{{.Copyable}}</textarea>
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

// HandleAdminLanding serves /admin — a landing page linking to the admin tools.
func (s *server) HandleAdminLanding(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	s.renderAdminPage(w, adminIndexTemplate, nil)
}

// HandleAdminCreateUsers serves /admin/create_users: GET shows the form, POST
// bulk-creates username+password accounts with random one-time passwords and
// renders them once. (Ported from dope, adapted to xy's users schema.)
func (s *server) HandleAdminCreateUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		s.renderAdminPage(w, adminCreateUsersTemplate, adminCreateUsersData{})
	case http.MethodPost:
		if !sameOrigin(r) {
			http.Error(w, "bad origin", http.StatusForbidden)
			return
		}
		s.handleAdminCreateUsersSubmit(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// renderAdminPage executes an admin template with asset-ref versioning + the
// app's strict CSP (the admin pages only load same-origin styles.css/menu.js).
func (s *server) renderAdminPage(w http.ResponseWriter, tmpl *template.Template, data any) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	body := s.versionAssetRefs(buf.Bytes())
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(body)
}

func (s *server) handleAdminCreateUsersSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	usernames := parseUsernameLines(r.PostForm.Get("usernames"))
	data := adminCreateUsersData{Submitted: true}

	now := time.Now()
	err := s.withWriteTx(r.Context(), "admin-create-users", func(ctx context.Context, tx *sql.Tx) error {
		for _, name := range usernames {
			if !validNewUsername(name) {
				data.Errors = append(data.Errors, adminUserError{Username: name, Reason: "недопустимый логин"})
				continue
			}
			var existing int64
			err := tx.QueryRowContext(ctx, `select id from users where username = ?`, name).Scan(&existing)
			if err == nil {
				data.Skipped = append(data.Skipped, name)
				continue
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			password, err := newRandomPassword()
			if err != nil {
				return err
			}
			hash, err := hashPassword(password)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
insert into users(username, password_hash, created_at, updated_at) values(?, ?, ?, ?)`,
				name, hash, rfc3339(now), rfc3339(now)); err != nil {
				if strings.Contains(err.Error(), "UNIQUE") {
					data.Skipped = append(data.Skipped, name)
					continue
				}
				return err
			}
			data.Created = append(data.Created, adminCreatedUser{Username: name, Password: password})
		}
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderAdminPage(w, adminCreateUsersTemplate, data)
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
