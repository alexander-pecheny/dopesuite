package pages

import (
	"net/http"
	"os"
	"strings"
	"time"

	"dope/dope/platform/session"
	"dope/dope/platform/util"
	ui "dope/dope/web/ui"
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

func (s *Server) HandleRegisterPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")

	if user, ok := s.h.LookupSession(r); ok {
		clearPendingRegisterCookie(w)
		if user.Username.Valid {
			http.Redirect(w, r, "/host", http.StatusSeeOther)
			return
		}
		s.renderRegisterStage(w, "username", registerStageData{})
		return
	}

	cookie, err := r.Cookie(pendingRegisterCookieName)
	if err == nil && cookie.Value != "" {
		code := cookie.Value
		resp, token, ferr := s.h.FinalizeRegister(r.Context(), code)
		if ferr == nil && token != "" {
			session.SetCookie(w, token)
			clearPendingRegisterCookie(w)
			http.Redirect(w, r, "/register", http.StatusSeeOther)
			return
		}
		switch resp.Status {
		case "pending":
			s.renderRegisterStage(w, "code", registerStageData{Code: code})
			return
		case "expired":
			clearPendingRegisterCookie(w)
			s.renderRegisterStage(w, "invite", registerStageData{Error: "Срок действия кода истек. Попробуйте еще раз."})
			return
		case "not_found":
			clearPendingRegisterCookie(w)
			s.renderRegisterStage(w, "invite", registerStageData{Error: "Код не найден. Начните заново."})
			return
		}
	}

	s.renderRegisterStage(w, "invite", registerStageData{})
}

func (s *Server) HandleRegisterInviteSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.h.RequireSameOrigin(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	invite := strings.TrimSpace(strings.ToUpper(r.Form.Get("invite_code")))
	if invite == "" {
		s.renderRegisterStage(w, "invite", registerStageData{Error: "Введите код приглашения."})
		return
	}
	resp, err := s.h.StartRegister(r.Context(), invite)
	if err != nil {
		s.renderRegisterStage(w, "invite", registerStageData{InviteCode: invite, Error: registerErrorMessage(err)})
		return
	}
	setPendingRegisterCookie(w, resp.Code)
	http.Redirect(w, r, "/register", http.StatusSeeOther)
}

func (s *Server) HandleRegisterUsernameSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.h.RequireSameOrigin(w, r) {
		return
	}
	user, ok := s.h.LookupSession(r)
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
	if !util.ValidUsername(username) {
		s.renderRegisterStage(w, "username", registerStageData{Username: username, Error: "Никнейм должен быть 2–32 символа: латиница, цифры, _ - ."})
		return
	}
	res, err := s.h.WriteExec(r.Context(), `
update users set username = ?, updated_at = ? where id = ? and username is null`,
		username, util.UtcNow(), user.UserID)
	if err != nil {
		if util.IsUniqueViolation(err) {
			s.renderRegisterStage(w, "username", registerStageData{Username: username, Error: "Этот никнейм уже занят."})
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

func (s *Server) renderRegisterStage(w http.ResponseWriter, stage string, data registerStageData) {
	if data.BotName == "" {
		data.BotName = registerBotName()
	}
	doc := registerDoc(stage, data)
	if doc == nil {
		http.Error(w, "unknown stage", http.StatusInternalServerError)
		return
	}
	RenderDoc(w, s.h.Engine().AssetETags, doc)
}

// registerDoc builds the register page for one of the four flow stages. The shell
// is the sheet page kind (matching the login page); the header carries a →-to-login
// link and no sync dot. The code stage adds the 2s meta-refresh that polls for the
// bot's confirmation.
func registerDoc(stage string, data registerStageData) *ui.Doc {
	page := []ui.Item{
		ui.Title("Регистрация · Фест"), ui.PageSheet,
		ui.Topbar(ui.Title("Регистрация"), ui.Nosync(),
			ui.Iconlink(ui.Href("/login"), ui.Label("Вход"), ui.Text("→")),
		),
	}
	switch stage {
	case "invite":
		page = append(page, registerInviteSection(data))
	case "code":
		page = append([]ui.Item{ui.Refresh("2")}, page...)
		page = append(page, registerCodeSection(data))
	case "username":
		page = append(page, registerUsernameSection(data))
	case "done":
		page = append(page, registerDoneSection())
	default:
		return nil
	}
	return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
}

func registerErrorItems(msg string) []ui.Item {
	if msg == "" {
		return nil
	}
	return []ui.Item{ui.Empty(ui.Text(msg))}
}

func registerInviteSection(data registerStageData) *ui.Element {
	items := []ui.Item{
		ui.Hint(ui.Text("Введите код приглашения, который вам прислали.")),
		ui.Form(ui.Method("post"), ui.Action("/register/invite"), ui.Autocomplete("off"),
			ui.Textfield(ui.Name("invite_code"), ui.Placeholder("Код приглашения"), ui.Spellcheck("false"),
				ui.Autocapitalize("characters"), ui.Value(data.InviteCode), ui.Required(), ui.Autofocus(), ui.Grow()),
			ui.Button(ui.Submit(), ui.Text("Получить код для бота")),
		),
	}
	items = append(items, registerErrorItems(data.Error)...)
	return ui.Section(items...)
}

func registerCodeSection(data registerStageData) *ui.Element {
	return ui.Section(
		ui.Hint(ui.Inline(
			ui.Text("Отправьте этот код боту "),
			ui.Link(ui.Href("https://t.me/"+data.BotName), ui.Newtab(), ui.Text("@"+data.BotName)),
			ui.Text(":"),
		)),
		ui.Codedisplay(ui.Text(data.Code)),
		ui.Hint(ui.Text("Код действует одну минуту. Жду подтверждения от бота…")),
	)
}

func registerUsernameSection(data registerStageData) *ui.Element {
	items := []ui.Item{
		ui.Hint(ui.Text("Готово! Выберите никнейм. Изменить его потом нельзя.")),
		ui.Form(ui.Method("post"), ui.Action("/register/username"), ui.Autocomplete("off"),
			ui.Textfield(ui.Name("username"), ui.Placeholder("username"), ui.Spellcheck("false"),
				ui.Autocapitalize("none"), ui.Value(data.Username), ui.Required(), ui.Autofocus(), ui.Grow()),
			ui.Button(ui.Submit(), ui.Text("Сохранить")),
		),
	}
	items = append(items, registerErrorItems(data.Error)...)
	return ui.Section(items...)
}

func registerDoneSection() *ui.Element {
	return ui.Section(
		ui.Hint(ui.Inline(
			ui.Text("Вы вошли. "),
			ui.Link(ui.Href("/host"), ui.Text("Перейти к фесту →")),
		)),
	)
}

func setPendingRegisterCookie(w http.ResponseWriter, code string) {
	http.SetCookie(w, &http.Cookie{
		Name:     pendingRegisterCookieName,
		Value:    code,
		Path:     "/",
		HttpOnly: true,
		Secure:   session.IsProdEnv(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(session.TelegramAuthLifetime / time.Second),
	})
}

func clearPendingRegisterCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     pendingRegisterCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   session.IsProdEnv(),
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
