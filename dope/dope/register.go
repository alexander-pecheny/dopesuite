package main

import (
	"html/template"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	pendingRegisterCookieName = "pending_register"
	defaultBotName            = "dope_pecheny_bot"
)

func registerBotName() string {
	if v := strings.TrimSpace(os.Getenv("DOPE_BOT_NAME")); v != "" {
		return strings.TrimPrefix(v, "@")
	}
	return defaultBotName
}

type registerStageData struct {
	BotName    string
	Code       string
	InviteCode string
	Username   string
	Error      string
}

var registerTemplates = map[string]*template.Template{
	"invite":   template.Must(template.New("invite").Parse(registerLayout + registerInviteBody)),
	"code":     template.Must(template.New("code").Parse(registerLayout + registerCodeBody)),
	"username": template.Must(template.New("username").Parse(registerLayout + registerUsernameBody)),
	"done":     template.Must(template.New("done").Parse(registerLayout + registerDoneBody)),
}

func (s *server) handleRegisterPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")

	if user, ok := s.lookupSession(r); ok {
		clearPendingRegisterCookie(w)
		if user.Username.Valid {
			http.Redirect(w, r, "/host", http.StatusSeeOther)
			return
		}
		renderRegisterStage(w, "username", registerStageData{})
		return
	}

	cookie, err := r.Cookie(pendingRegisterCookieName)
	if err == nil && cookie.Value != "" {
		code := cookie.Value
		resp, token, ferr := s.finalizeRegister(r.Context(), code)
		if ferr == nil && token != "" {
			setSessionCookie(w, token)
			clearPendingRegisterCookie(w)
			http.Redirect(w, r, "/register", http.StatusSeeOther)
			return
		}
		switch resp.Status {
		case "pending":
			renderRegisterStage(w, "code", registerStageData{Code: code})
			return
		case "expired":
			clearPendingRegisterCookie(w)
			renderRegisterStage(w, "invite", registerStageData{Error: "Срок действия кода истек. Попробуйте еще раз."})
			return
		case "not_found":
			clearPendingRegisterCookie(w)
			renderRegisterStage(w, "invite", registerStageData{Error: "Код не найден. Начните заново."})
			return
		}
	}

	renderRegisterStage(w, "invite", registerStageData{})
}

func (s *server) handleRegisterInviteSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	invite := strings.TrimSpace(strings.ToUpper(r.Form.Get("invite_code")))
	if invite == "" {
		renderRegisterStage(w, "invite", registerStageData{Error: "Введите код приглашения."})
		return
	}
	resp, err := s.startRegister(r.Context(), invite)
	if err != nil {
		renderRegisterStage(w, "invite", registerStageData{InviteCode: invite, Error: registerErrorMessage(err)})
		return
	}
	setPendingRegisterCookie(w, resp.Code)
	http.Redirect(w, r, "/register", http.StatusSeeOther)
}

func (s *server) handleRegisterUsernameSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := s.lookupSession(r)
	if !ok {
		http.Redirect(w, r, "/register", http.StatusSeeOther)
		return
	}
	if user.Username.Valid {
		http.Redirect(w, r, "/register", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(r.Form.Get("username"))
	if !validUsername(username) {
		renderRegisterStage(w, "username", registerStageData{Username: username, Error: "Никнейм должен быть 2–32 символа: латиница, цифры, _ - ."})
		return
	}
	res, err := s.db.ExecContext(r.Context(), `
update users set username = ?, updated_at = ? where id = ? and username is null`,
		username, utcNow(), user.UserID)
	if err != nil {
		if isUniqueViolation(err) {
			renderRegisterStage(w, "username", registerStageData{Username: username, Error: "Этот никнейм уже занят."})
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		http.Redirect(w, r, "/register", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/register", http.StatusSeeOther)
}

func renderRegisterStage(w http.ResponseWriter, stage string, data registerStageData) {
	tmpl, ok := registerTemplates[stage]
	if !ok {
		http.Error(w, "unknown stage", http.StatusInternalServerError)
		return
	}
	if data.BotName == "" {
		data.BotName = registerBotName()
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "page", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func setPendingRegisterCookie(w http.ResponseWriter, code string) {
	http.SetCookie(w, &http.Cookie{
		Name:     pendingRegisterCookieName,
		Value:    code,
		Path:     "/",
		HttpOnly: true,
		Secure:   isProdEnv(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(telegramAuthLifetime / time.Second),
	})
}

func clearPendingRegisterCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     pendingRegisterCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   isProdEnv(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func registerErrorMessage(err error) string {
	switch err.Error() {
	case "invite not found":
		return "Код приглашения не найден."
	case "invite already used":
		return "Этот код уже использован."
	case "invite expired":
		return "Срок действия приглашения истек."
	case "missing invite code":
		return "Введите код приглашения."
	default:
		return err.Error()
	}
}

const registerLayout = `{{define "page"}}<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Регистрация · Фест</title>
  <link rel="stylesheet" href="/static/styles.css">
  {{block "head" .}}{{end}}
</head>
<body class="host import-page">
  <header class="host-top">
    <h1>Регистрация</h1>
    <div class="host-actions">
      <a class="action-icon" href="/login" aria-label="Вход" title="Вход">→</a>
    </div>
  </header>

  <main class="match-main">
    <div class="sheet-frame import-frame">
      {{template "body" .}}
    </div>
  </main>
</body>
</html>
{{end}}`

const registerInviteBody = `{{define "body"}}
<section class="auth-step">
  <p class="auth-hint">Введите код приглашения, который вам прислали.</p>
  <form class="auth-form" method="post" action="/register/invite" autocomplete="off">
    <input class="input" name="invite_code" type="text" placeholder="Код приглашения" spellcheck="false" autocapitalize="characters" value="{{.InviteCode}}" required autofocus>
    <button class="btn" type="submit">Получить код для бота</button>
  </form>
  {{if .Error}}<p class="import-message">{{.Error}}</p>{{end}}
</section>
{{end}}`

const registerCodeBody = `{{define "head"}}<meta http-equiv="refresh" content="2">{{end}}
{{define "body"}}
<section class="auth-step">
  <p class="auth-hint">Отправьте этот код боту <a href="https://t.me/{{.BotName}}" target="_blank" rel="noopener">@{{.BotName}}</a>:</p>
  <p class="code-display">{{.Code}}</p>
  <p class="auth-hint">Код действует одну минуту. Жду подтверждения от бота…</p>
</section>
{{end}}`

const registerUsernameBody = `{{define "body"}}
<section class="auth-step">
  <p class="auth-hint">Готово! Выберите никнейм. Изменить его потом нельзя.</p>
  <form class="auth-form" method="post" action="/register/username" autocomplete="off">
    <input class="input" name="username" type="text" placeholder="username" spellcheck="false" autocapitalize="none" value="{{.Username}}" required autofocus>
    <button class="btn" type="submit">Сохранить</button>
  </form>
  {{if .Error}}<p class="import-message">{{.Error}}</p>{{end}}
</section>
{{end}}`

const registerDoneBody = `{{define "body"}}
<section class="auth-step">
  <p class="auth-hint">Вы вошли. <a href="/host">Перейти к фесту →</a></p>
</section>
{{end}}`
